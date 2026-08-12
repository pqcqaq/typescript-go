package ast2bingo

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/microsoft/typescript-go/internal/bingo"
)

type ClassAccessReplay struct {
	Contract  bingo.ClassAccessContract
	Execution bingo.ClassAccessExecutionContract
	Requests  []bingo.ClassAccessRequest
	Decisions []bingo.ClassAccessDecision
}

type classAccessMemberCandidate struct {
	member bingo.ClassAccessMember
	start  int
}

// LowerClassAccessReplay consumes snapshot-owned nominal class, visibility,
// private identity, lexical owner, receiver type, and selected member facts.
func LowerClassAccessReplay(snapshot ProgramSnapshot) (ClassAccessReplay, error) {
	indexes := indexPrimitiveSnapshot(snapshot)
	classNodes := make([]NodeSnapshot, 0, 2)
	for _, node := range snapshot.Nodes {
		if node.Kind == "KindClassDeclaration" {
			classNodes = append(classNodes, node)
		}
	}
	slices.SortFunc(classNodes, func(left, right NodeSnapshot) int { return cmp.Compare(left.Span.Start, right.Span.Start) })
	if len(classNodes) != 2 {
		return ClassAccessReplay{}, fmt.Errorf("OBJ-003b access fixture requires two declaration-ordered classes")
	}

	classes := make([]bingo.ClassAccessClass, 0, len(classNodes))
	classBySymbol := make(map[SymbolID]uint32, len(classNodes))
	classByNode := make(map[NodeID]uint32, len(classNodes))
	classByType := make(map[TypeID]uint32, len(classNodes))
	classTypes := make(map[uint32]TypeSnapshot, len(classNodes))
	for index, node := range classNodes {
		symbol, ok := symbolWithValueDeclaration(indexes, node.ID)
		typ, typeOK := indexes.Types[node.NarrowedType]
		if !ok || !typeOK || typ.Symbol != symbol.ID {
			return ClassAccessReplay{}, fmt.Errorf("OBJ-003b class %d identity is not snapshot-bound", index+1)
		}
		id := uint32(index + 1)
		baseID := uint32(0)
		if len(typ.BaseTypes) > 1 {
			return ClassAccessReplay{}, fmt.Errorf("OBJ-003b class %d has unsupported base arity", id)
		}
		if len(typ.BaseTypes) == 1 {
			baseID = classByType[typ.BaseTypes[0]]
			if baseID == 0 {
				return ClassAccessReplay{}, fmt.Errorf("OBJ-003b class %d base is not declaration ordered", id)
			}
		}
		classes = append(classes, bingo.ClassAccessClass{ID: id, SymbolKey: string(symbol.ID), InstanceTypeKey: typ.CanonicalHash, BaseClassID: baseID})
		classBySymbol[symbol.ID], classByNode[node.ID], classByType[node.NarrowedType], classTypes[id] = id, id, id, typ
	}
	if indexes.Symbols[classTypes[1].Symbol].Name != "Vault" || indexes.Symbols[classTypes[2].Symbol].Name != "DerivedVault" || classes[2-1].BaseClassID != 1 {
		return ClassAccessReplay{}, fmt.Errorf("OBJ-003b access fixture nominal hierarchy mismatch")
	}

	candidates := make([]classAccessMemberCandidate, 0, 4)
	factBySymbol := make(map[SymbolID]struct {
		visibility      bingo.ClassMemberVisibility
		privateIdentity string
	})
	for _, class := range classes {
		typ := classTypes[class.ID]
		for _, fact := range typ.PropertyFacts {
			symbol := indexes.Symbols[fact.Symbol]
			if classBySymbol[symbol.Parent] != class.ID {
				continue
			}
			declaration := indexes.Nodes[symbol.ValueDeclaration]
			visibility := bingo.ClassMemberVisibility(fact.Visibility)
			memberKind := bingo.ClassAccessField
			if declaration.Kind == "KindMethodDeclaration" {
				memberKind = bingo.ClassAccessMethod
			} else if declaration.Kind != "KindPropertyDeclaration" {
				return ClassAccessReplay{}, fmt.Errorf("OBJ-003b member %q has unsupported declaration kind %s", symbol.Name, declaration.Kind)
			}
			if visibility == bingo.ClassMemberPrivate && fact.PrivateIdentity != string(fact.Symbol) {
				return ClassAccessReplay{}, fmt.Errorf("OBJ-003b private identity is not the declaration symbol")
			}
			factBySymbol[fact.Symbol] = struct {
				visibility      bingo.ClassMemberVisibility
				privateIdentity string
			}{visibility: visibility, privateIdentity: fact.PrivateIdentity}
			candidates = append(candidates, classAccessMemberCandidate{
				member: bingo.ClassAccessMember{OwnerClassID: class.ID, SymbolKey: string(fact.Symbol), Name: symbol.Name, Kind: memberKind, Visibility: visibility, PrivateIdentity: fact.PrivateIdentity},
				start:  declaration.Span.Start,
			})
		}
	}
	slices.SortFunc(candidates, func(left, right classAccessMemberCandidate) int { return cmp.Compare(left.start, right.start) })
	members := make([]bingo.ClassAccessMember, len(candidates))
	memberBySymbol := make(map[SymbolID]uint32, len(candidates))
	for index, candidate := range candidates {
		candidate.member.ID = uint32(index + 1)
		members[index] = candidate.member
		memberBySymbol[SymbolID(candidate.member.SymbolKey)] = candidate.member.ID
	}
	if err := verifyClassAccessFixtureMembers(members); err != nil {
		return ClassAccessReplay{}, err
	}
	if err := verifyClassAccessSourceExecution(snapshot, indexes, classNodes, classes, members); err != nil {
		return ClassAccessReplay{}, err
	}
	contract := bingo.ClassAccessContract{SchemaVersion: bingo.ClassAccessContractSchemaVersion, Classes: classes, Members: members}
	_, hash, err := bingo.CanonicalClassAccessContract(contract)
	if err != nil {
		return ClassAccessReplay{}, err
	}
	contract.ContentHash = hash

	accesses := make([]NodeSnapshot, 0, 4)
	for _, node := range snapshot.Nodes {
		if node.Kind == "KindPropertyAccessExpression" && memberBySymbol[node.Symbol] != 0 {
			accesses = append(accesses, node)
		}
	}
	slices.SortFunc(accesses, func(left, right NodeSnapshot) int { return cmp.Compare(left.Span.Start, right.Span.Start) })
	if len(accesses) != 4 {
		return ClassAccessReplay{}, fmt.Errorf("OBJ-003b access fixture requires four selected member accesses")
	}
	requests := make([]bingo.ClassAccessRequest, 0, len(accesses))
	decisions := make([]bingo.ClassAccessDecision, 0, len(accesses))
	for _, access := range accesses {
		if len(access.Children) != 2 || indexes.Nodes[access.Children[1]].Symbol != access.Symbol {
			return ClassAccessReplay{}, fmt.Errorf("OBJ-003b property access does not preserve selected member identity")
		}
		receiver := indexes.Nodes[access.Children[0]]
		receiverClassID := classByType[receiver.NarrowedType]
		if receiverClassID == 0 {
			return ClassAccessReplay{}, fmt.Errorf("OBJ-003b property receiver is not a known nominal class")
		}
		accessingClassID := lexicalClassID(access, indexes.Nodes, classByNode)
		fact := factBySymbol[access.Symbol]
		request := bingo.ClassAccessRequest{AccessingClassID: accessingClassID, ReceiverClassID: receiverClassID, MemberID: memberBySymbol[access.Symbol], PrivateIdentity: fact.privateIdentity}
		decision, err := bingo.PlanClassMemberAccess(contract, request)
		if err != nil {
			return ClassAccessReplay{}, err
		}
		if !decision.Allowed {
			return ClassAccessReplay{}, fmt.Errorf("OBJ-003b snapshot access rejected: %s", decision.Reason)
		}
		requests, decisions = append(requests, request), append(decisions, decision)
	}
	execution, err := bingo.NewClassAccessExecutionContract(contract)
	if err != nil {
		return ClassAccessReplay{}, err
	}
	return ClassAccessReplay{Contract: contract, Execution: execution, Requests: requests, Decisions: decisions}, nil
}

func verifyClassAccessSourceExecution(snapshot ProgramSnapshot, indexes snapshotSemanticFactIndexes, classNodes []NodeSnapshot, classes []bingo.ClassAccessClass, members []bingo.ClassAccessMember) error {
	// This access slice relies on the default constructors and declaration-time
	// field initializers. Bind those source facts explicitly instead of allowing
	// a rehashed snapshot to silently change the executable program underneath
	// otherwise valid access proofs.
	wantChildren := [][]string{
		{"KindIdentifier", "KindPropertyDeclaration", "KindPropertyDeclaration", "KindMethodDeclaration"},
		{"KindIdentifier", "KindHeritageClause", "KindMethodDeclaration"},
	}
	for classIndex, classNode := range classNodes {
		if len(classNode.Children) != len(wantChildren[classIndex]) {
			return fmt.Errorf("OBJ-003b class %d source shape mismatch", classIndex+1)
		}
		for childIndex, childID := range classNode.Children {
			child, ok := indexes.Nodes[childID]
			if !ok || child.Parent != classNode.ID || child.Kind != wantChildren[classIndex][childIndex] {
				return fmt.Errorf("OBJ-003b class %d child %d mismatch", classIndex+1, childIndex+1)
			}
		}
	}
	for index, text := range []string{"1", "2"} {
		declaration := indexes.Nodes[classNodes[0].Children[index+1]]
		if len(declaration.Children) == 0 {
			return fmt.Errorf("OBJ-003b field %d initializer missing", index+1)
		}
		initializer, ok := indexes.Nodes[declaration.Children[len(declaration.Children)-1]]
		if !ok || initializer.Parent != declaration.ID || initializer.Kind != "KindNumericLiteral" || initializer.SyntaxPayload.Text != text || bingoTypeMust(initializer.NarrowedType, indexes.Types) != bingo.TypeNumber {
			return fmt.Errorf("OBJ-003b field %d initializer mismatch", index+1)
		}
	}

	memberSymbols := make(map[string]SymbolID, len(members))
	memberSignatures := make(map[string]SignatureID, 2)
	for _, member := range members {
		memberSymbols[member.Name] = SymbolID(member.SymbolKey)
		if member.Kind == bingo.ClassAccessMethod {
			symbol := indexes.Symbols[SymbolID(member.SymbolKey)]
			signature, ok := signatureForDeclaration(indexes, symbol.ValueDeclaration)
			expectedParameterType := classNodes[member.OwnerClassID-1].NarrowedType
			if !ok || signature.CallingConventionClass != "call" || signature.MinArgumentCount != 1 || len(signature.ParameterFacts) != 1 || signature.HasRest || signature.ParameterFacts[0].Type != expectedParameterType || bingoTypeMust(signature.ReturnType, indexes.Types) != bingo.TypeNumber || !slices.Equal(signature.Effects, []string{"read"}) {
				return fmt.Errorf("OBJ-003b method %q signature mismatch", member.Name)
			}
			memberSignatures[member.Name] = signature.ID
		}
	}

	newCount := 0
	callCounts := map[string]int{"readSecret": 0, "readValue": 0}
	var receiverSymbol SymbolID
	for _, node := range snapshot.Nodes {
		switch node.Kind {
		case "KindNewExpression":
			if len(node.Children) != 1 || node.NarrowedType != classNodes[1].NarrowedType {
				return fmt.Errorf("OBJ-003b allocation shape mismatch")
			}
			callee := indexes.Nodes[node.Children[0]]
			signature, ok := indexes.Signatures[node.SelectedSignature]
			if !ok || callee.Symbol != SymbolID(classes[1].SymbolKey) || signature.CallingConventionClass != "construct" || signature.Declaration != "" || signature.ReturnType != classNodes[1].NarrowedType || signature.MinArgumentCount != 0 || len(signature.ParameterFacts) != 0 || signature.HasRest {
				return fmt.Errorf("OBJ-003b default derived construction mismatch")
			}
			declaration := indexes.Nodes[node.Parent]
			if declaration.Kind != "KindVariableDeclaration" || len(declaration.Children) != 2 || declaration.Children[1] != node.ID {
				return fmt.Errorf("OBJ-003b allocation is not the fixture receiver initializer")
			}
			receiver := indexes.Nodes[declaration.Children[0]]
			if receiver.Kind != "KindIdentifier" || receiver.Symbol == "" || receiver.NarrowedType != classNodes[1].NarrowedType {
				return fmt.Errorf("OBJ-003b allocated receiver binding mismatch")
			}
			receiverSymbol = receiver.Symbol
			newCount++
		case "KindCallExpression":
			for _, name := range []string{"readSecret", "readValue"} {
				if node.SelectedSignature != memberSignatures[name] {
					continue
				}
				if len(node.Children) != 2 {
					return fmt.Errorf("OBJ-003b %s call shape mismatch", name)
				}
				callee, argument := indexes.Nodes[node.Children[0]], indexes.Nodes[node.Children[1]]
				if callee.Kind != "KindPropertyAccessExpression" || callee.Symbol != memberSymbols[name] || len(callee.Children) != 2 {
					return fmt.Errorf("OBJ-003b %s selected callee mismatch", name)
				}
				callReceiver := indexes.Nodes[callee.Children[0]]
				if callReceiver.Symbol == "" || callReceiver.Symbol != argument.Symbol || callReceiver.NarrowedType != classNodes[1].NarrowedType || argument.NarrowedType != classNodes[1].NarrowedType {
					return fmt.Errorf("OBJ-003b %s receiver/argument identity mismatch", name)
				}
				callCounts[name]++
			}
		}
	}
	if newCount != 1 || receiverSymbol == "" || callCounts["readSecret"] != 1 || callCounts["readValue"] != 1 {
		return fmt.Errorf("OBJ-003b requires one allocation and one call to each public method")
	}
	for _, node := range snapshot.Nodes {
		if node.Kind == "KindCallExpression" && (node.SelectedSignature == memberSignatures["readSecret"] || node.SelectedSignature == memberSignatures["readValue"]) {
			callee := indexes.Nodes[node.Children[0]]
			if indexes.Nodes[callee.Children[0]].Symbol != receiverSymbol || indexes.Nodes[node.Children[1]].Symbol != receiverSymbol {
				return fmt.Errorf("OBJ-003b exported calls do not use the allocated receiver")
			}
		}
	}
	return nil
}

func lexicalClassID(node NodeSnapshot, nodes map[NodeID]NodeSnapshot, classes map[NodeID]uint32) uint32 {
	for parent := node.Parent; parent != ""; parent = nodes[parent].Parent {
		if classID := classes[parent]; classID != 0 {
			return classID
		}
	}
	return 0
}

func verifyClassAccessFixtureMembers(members []bingo.ClassAccessMember) error {
	if len(members) != 4 {
		return fmt.Errorf("OBJ-003b access fixture requires four owned members")
	}
	want := []struct {
		owner      uint32
		name       string
		visibility bingo.ClassMemberVisibility
	}{
		{1, "secret", bingo.ClassMemberPrivate},
		{1, "value", bingo.ClassMemberProtected},
		{1, "readSecret", bingo.ClassMemberPublic},
		{2, "readValue", bingo.ClassMemberPublic},
	}
	for index, expected := range want {
		member := members[index]
		wantKind := bingo.ClassAccessField
		if index >= 2 {
			wantKind = bingo.ClassAccessMethod
		}
		if member.OwnerClassID != expected.owner || member.Name != expected.name || member.Kind != wantKind || member.Visibility != expected.visibility {
			return fmt.Errorf("OBJ-003b access fixture member %d mismatch", index+1)
		}
	}
	return nil
}
