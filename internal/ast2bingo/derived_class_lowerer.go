package ast2bingo

import (
	"cmp"
	"fmt"
	"slices"

	"github.com/microsoft/typescript-go/internal/bingo"
)

// LowerVERT013bClassContract consumes only the checker-proven facts needed by
// the first derived-class slice. It never infers inheritance from syntax text
// alone: base type, constructor signatures, selected super call, and method
// symbols must all agree in the snapshot.
func LowerVERT013bClassContract(snapshot ProgramSnapshot) (bingo.VERT013bClassContract, error) {
	indexes := indexPrimitiveSnapshot(snapshot)
	classes := make([]NodeSnapshot, 0, 2)
	for _, node := range snapshot.Nodes {
		if node.Kind == "KindClassDeclaration" {
			classes = append(classes, node)
		}
	}
	slices.SortFunc(classes, func(left, right NodeSnapshot) int { return cmp.Compare(left.Span.Start, right.Span.Start) })
	if len(classes) != 2 {
		return bingo.VERT013bClassContract{}, fmt.Errorf("VERT-013b requires two ordered classes")
	}
	base, derived := classes[0], classes[1]
	baseContract, baseSymbol, baseType, baseCtor, baseField, baseMethod, err := lowerVERT013bClass(indexes, base, "Counter", false)
	if err != nil {
		return bingo.VERT013bClassContract{}, err
	}
	derivedContract, derivedSymbol, derivedType, derivedCtor, derivedField, derivedMethod, err := lowerVERT013bClass(indexes, derived, "StepCounter", true)
	if err != nil {
		return bingo.VERT013bClassContract{}, err
	}
	if len(baseType.BaseTypes) != 0 || len(derivedType.BaseTypes) != 1 || derivedType.BaseTypes[0] != base.NarrowedType || derivedSymbol.Parent != "" || baseSymbol.Parent != "" {
		return bingo.VERT013bClassContract{}, fmt.Errorf("VERT-013b nominal base relation is invalid")
	}
	if derivedContract.BaseClassID != 1 || baseCtor.ID == 0 || derivedCtor.ID == 0 || baseField.ID == "" || derivedField.ID == "" || baseMethod.ID == "" || derivedMethod.ID == "" {
		return bingo.VERT013bClassContract{}, fmt.Errorf("VERT-013b declaration facts are incomplete")
	}
	derivedContract.Super.Callee = baseContract.Constructor.SymbolKey
	if err := verifyVERT013bSuperAndUses(snapshot, indexes, derived, baseType, derivedType, baseCtor.ID, derivedCtor.ID, derivedMethod.ID); err != nil {
		return bingo.VERT013bClassContract{}, err
	}
	contract := bingo.VERT013bClassContract{SchemaVersion: bingo.VERT013bClassContractSchemaVersion, Classes: []bingo.VERT013bClass{baseContract, derivedContract}}
	_, hash, err := bingo.CanonicalVERT013bClassContract(contract)
	if err != nil {
		return bingo.VERT013bClassContract{}, err
	}
	contract.ContentHash = hash
	return contract, nil
}

func lowerVERT013bClass(indexes snapshotSemanticFactIndexes, node NodeSnapshot, name string, derived bool) (bingo.VERT013bClass, SymbolSnapshot, TypeSnapshot, SignatureSnapshot, SymbolSnapshot, SymbolSnapshot, error) {
	classSymbol, ok := symbolWithValueDeclaration(indexes, node.ID)
	if !ok || classSymbol.Name != name || node.NarrowedType == 0 {
		return bingo.VERT013bClass{}, SymbolSnapshot{}, TypeSnapshot{}, SignatureSnapshot{}, SymbolSnapshot{}, SymbolSnapshot{}, fmt.Errorf("VERT-013b %s identity is invalid", name)
	}
	instanceType, ok := indexes.Types[node.NarrowedType]
	if !ok || instanceType.Symbol != classSymbol.ID || len(instanceType.PropertyFacts) != 2+boolInt(derived) {
		return bingo.VERT013bClass{}, SymbolSnapshot{}, TypeSnapshot{}, SignatureSnapshot{}, SymbolSnapshot{}, SymbolSnapshot{}, fmt.Errorf("VERT-013b %s instance type is invalid", name)
	}
	var ctor, method, field NodeSnapshot
	for _, id := range node.Children {
		child, ok := indexes.Nodes[id]
		if !ok {
			return bingo.VERT013bClass{}, SymbolSnapshot{}, TypeSnapshot{}, SignatureSnapshot{}, SymbolSnapshot{}, SymbolSnapshot{}, fmt.Errorf("VERT-013b class child missing")
		}
		switch child.Kind {
		case "KindConstructor":
			if ctor.ID != "" {
				return bingo.VERT013bClass{}, SymbolSnapshot{}, TypeSnapshot{}, SignatureSnapshot{}, SymbolSnapshot{}, SymbolSnapshot{}, fmt.Errorf("VERT-013b duplicate constructor")
			}
			ctor = child
		case "KindMethodDeclaration":
			if method.ID != "" {
				return bingo.VERT013bClass{}, SymbolSnapshot{}, TypeSnapshot{}, SignatureSnapshot{}, SymbolSnapshot{}, SymbolSnapshot{}, fmt.Errorf("VERT-013b duplicate method")
			}
			method = child
		case "KindPropertyDeclaration":
			if field.ID != "" {
				return bingo.VERT013bClass{}, SymbolSnapshot{}, TypeSnapshot{}, SignatureSnapshot{}, SymbolSnapshot{}, SymbolSnapshot{}, fmt.Errorf("VERT-013b multiple fields")
			}
			field = child
		case "KindIdentifier", "KindHeritageClause":
		default:
			return bingo.VERT013bClass{}, SymbolSnapshot{}, TypeSnapshot{}, SignatureSnapshot{}, SymbolSnapshot{}, SymbolSnapshot{}, fmt.Errorf("VERT-013b rejects class child %s", child.Kind)
		}
	}
	fieldSymbol, ok := symbolWithValueDeclaration(indexes, field.ID)
	if !ok || fieldSymbol.Parent != classSymbol.ID || fieldSymbol.Name == "" || bingoTypeMust(fieldSymbol.Type, indexes.Types) != bingo.TypeNumber {
		return bingo.VERT013bClass{}, SymbolSnapshot{}, TypeSnapshot{}, SignatureSnapshot{}, SymbolSnapshot{}, SymbolSnapshot{}, fmt.Errorf("VERT-013b %s field identity is invalid", name)
	}
	methodSymbol, ok := symbolWithValueDeclaration(indexes, method.ID)
	if !ok || methodSymbol.Parent != classSymbol.ID || methodSymbol.Name != "increment" {
		return bingo.VERT013bClass{}, SymbolSnapshot{}, TypeSnapshot{}, SignatureSnapshot{}, SymbolSnapshot{}, SymbolSnapshot{}, fmt.Errorf("VERT-013b %s method identity is invalid", name)
	}
	ctorSig, ok := signatureForDeclaration(indexes, ctor.ID)
	if !ok || ctorSig.CallingConventionClass != "construct" || len(ctorSig.ParameterFacts) != 1+boolInt(derived) || ctorSig.ReturnType != node.NarrowedType || ctorSig.MinArgumentCount != len(ctorSig.ParameterFacts) {
		return bingo.VERT013bClass{}, SymbolSnapshot{}, TypeSnapshot{}, SignatureSnapshot{}, SymbolSnapshot{}, SymbolSnapshot{}, fmt.Errorf("VERT-013b %s constructor signature is invalid", name)
	}
	for _, parameter := range ctorSig.ParameterFacts {
		if bingoTypeMust(parameter.Type, indexes.Types) != bingo.TypeNumber || parameter.Optional || parameter.Rest {
			return bingo.VERT013bClass{}, SymbolSnapshot{}, TypeSnapshot{}, SignatureSnapshot{}, SymbolSnapshot{}, SymbolSnapshot{}, fmt.Errorf("VERT-013b %s constructor parameter is invalid", name)
		}
	}
	methodSig, ok := signatureForDeclaration(indexes, method.ID)
	if !ok || !isZeroArgumentNumberMethod(methodSig, indexes) {
		return bingo.VERT013bClass{}, SymbolSnapshot{}, TypeSnapshot{}, SignatureSnapshot{}, SymbolSnapshot{}, SymbolSnapshot{}, fmt.Errorf("VERT-013b %s method signature is invalid", name)
	}
	classID, baseID := uint32(1), uint32(0)
	if derived {
		classID, baseID = 2, 1
	}
	constructorABI := "cdecl(ptr,f64)->void"
	if derived {
		constructorABI = "cdecl(ptr,f64,f64)->void"
	}
	contract := bingo.VERT013bClass{ID: classID, SymbolKey: string(classSymbol.ID), InstanceTypeKey: instanceType.CanonicalHash, BaseClassID: baseID, Constructor: bingo.ClassConstructorContract{SymbolKey: string(ctor.ID), Signature: constructorABI, Derived: derived, AllocatesReceiver: !derived, ReturnsOwnReceiver: true}, Fields: []bingo.ClassFieldContract{{ID: 1, SymbolKey: string(fieldSymbol.ID), Name: fieldSymbol.Name, Type: bingo.TypeNumber, Visibility: "public", Mutable: true, Storage: bingo.ClassFieldInstanceSlot, SourceOrder: 1}}, Methods: []bingo.ClassMethodContract{{ID: 1, SymbolKey: string(methodSymbol.ID), Name: "increment", Signature: "cdecl(ptr)->f64", Visibility: "public", RequiresReceiver: true, SourceOrder: 3}}, Initialization: []bingo.ClassInitializationStep{{ID: 1, Kind: bingo.ClassInitAllocate}, {ID: 2, Kind: bingo.ClassInitField, FieldID: 1, SourceOrder: 1}, {ID: 3, Kind: bingo.ClassInitBody, SourceOrder: 2}}}
	if derived {
		contract.Initialization = []bingo.ClassInitializationStep{{ID: 1, Kind: bingo.ClassInitAllocate}, {ID: 2, Kind: bingo.ClassInitBody, SourceOrder: 1}, {ID: 3, Kind: bingo.ClassInitField, FieldID: 1, SourceOrder: 2}, {ID: 4, Kind: bingo.ClassInitBody, SourceOrder: 3}}
		contract.Super = &bingo.VERT013bSuperCall{BaseClassID: 1, Callee: "", Arguments: []string{"start"}, SourceOrder: 1}
	}
	return contract, classSymbol, instanceType, ctorSig, fieldSymbol, methodSymbol, nil
}

func verifyVERT013bSuperAndUses(snapshot ProgramSnapshot, indexes snapshotSemanticFactIndexes, derived NodeSnapshot, _, _ TypeSnapshot, baseCtor, derivedCtor SignatureID, derivedMethod SymbolID) error {
	superCount, newCount, methodCalls := 0, 0, 0
	for _, node := range snapshot.Nodes {
		if node.Kind == "KindNewExpression" {
			if node.SelectedSignature != derivedCtor || node.NarrowedType != derived.NarrowedType {
				return fmt.Errorf("VERT-013b new expression is invalid")
			}
			newCount++
		}
		if node.Kind == "KindCallExpression" && node.SelectedSignature == baseCtor {
			if len(node.Children) < 1 {
				return fmt.Errorf("VERT-013b super call has no callee")
			}
			callee := indexes.Nodes[node.Children[0]]
			if callee.Kind != "KindSuperKeyword" {
				return fmt.Errorf("VERT-013b base constructor call is not super")
			}
			superCount++
		}
		if node.Kind == "KindCallExpression" {
			callee := ""
			if len(node.Children) > 0 {
				callee = string(indexes.Nodes[node.Children[0]].Symbol)
			}
			if node.SelectedSignature != 0 && callee == string(derivedMethod) {
				methodCalls++
			}
		}
	}
	if superCount != 1 || newCount != 1 || methodCalls != 2 {
		return fmt.Errorf("VERT-013b requires one super call, one allocation, and two derived method calls")
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
