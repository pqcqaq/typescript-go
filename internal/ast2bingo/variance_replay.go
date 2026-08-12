package ast2bingo

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
)

const VarianceReplaySchemaVersion uint32 = 3

type VarianceReplayResult struct {
	SchemaVersion         uint32                      `json:"schemaVersion"`
	FrontendSnapshotHash  string                      `json:"frontendSnapshotHash"`
	CompilerBuildIdentity bingo.CompilerBuildIdentity `json:"compilerBuildIdentity"`
	Contracts             []bingo.VarianceContract    `json:"contracts"`
	Graph                 bingo.VarianceGraphContract `json:"graph"`
	RelationGraph         bingo.TypeRelationGraph     `json:"relationGraph"`
	ContentHash           string                      `json:"contentHash"`
}

func (result VarianceReplayResult) CanonicalBytes() ([]byte, error) {
	if result.SchemaVersion != VarianceReplaySchemaVersion || result.FrontendSnapshotHash == "" || len(result.Contracts) == 0 {
		return nil, fmt.Errorf("invalid OBJ-004 variance replay identity")
	}
	if err := bingo.ValidateCompilerBuildIdentity(result.CompilerBuildIdentity); err != nil {
		return nil, err
	}
	for _, contract := range result.Contracts {
		if err := bingo.VerifyCanonicalVarianceContract(contract); err != nil {
			return nil, fmt.Errorf("invalid variance contract: %w", err)
		}
	}
	if err := bingo.VerifyCanonicalVarianceGraph(result.Graph); err != nil {
		return nil, fmt.Errorf("invalid variance graph: %w", err)
	}
	if err := bingo.VerifyCanonicalTypeRelationGraph(result.RelationGraph); err != nil {
		return nil, fmt.Errorf("invalid type relation graph: %w", err)
	}
	if !sameVarianceContracts(result.Contracts, result.Graph.Contracts) {
		return nil, fmt.Errorf("variance replay contracts do not match graph contracts")
	}
	withoutHash := result
	withoutHash.ContentHash = ""
	encoded, err := json.Marshal(withoutHash)
	if err != nil {
		return nil, err
	}
	if hashBytes(encoded) != result.ContentHash {
		return nil, fmt.Errorf("OBJ-004 variance replay content hash mismatch")
	}
	return json.Marshal(result)
}

func sameVarianceContracts(left, right []bingo.VarianceContract) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].DeclarationKey != right[i].DeclarationKey || left[i].ContentHash != right[i].ContentHash {
			return false
		}
	}
	return true
}

func ReplayVarianceFrontendSnapshot(data []byte, identity bingo.CompilerBuildIdentity) (VarianceReplayResult, error) {
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		return VarianceReplayResult{}, err
	}
	return ReplayVarianceSnapshot(frontend.Program, identity)
}

func ReplayVarianceSnapshot(snapshot ProgramSnapshot, identity bingo.CompilerBuildIdentity) (VarianceReplayResult, error) {
	if err := frontendwire.ValidateProgramSnapshot(snapshot); err != nil {
		return VarianceReplayResult{}, fmt.Errorf("validate variance frontend snapshot: %w", err)
	}
	if err := validateCompilerIdentityForSnapshot(identity, snapshot); err != nil {
		return VarianceReplayResult{}, err
	}
	indexes := indexPrimitiveSnapshot(snapshot)
	contracts, candidates, err := lowerVarianceContracts(indexes)
	if err != nil {
		return VarianceReplayResult{}, err
	}
	edges, err := lowerVarianceDependencyEdges(candidates, indexes)
	if err != nil {
		return VarianceReplayResult{}, err
	}
	graph, err := bingo.BuildVarianceGraph(contracts, edges)
	if err != nil {
		return VarianceReplayResult{}, fmt.Errorf("build variance graph: %w", err)
	}
	relationGraph, err := lowerTypeRelationGraph(candidates, indexes)
	if err != nil {
		return VarianceReplayResult{}, err
	}
	result := VarianceReplayResult{SchemaVersion: VarianceReplaySchemaVersion, FrontendSnapshotHash: snapshot.ContentHash, CompilerBuildIdentity: identity, Contracts: contracts, Graph: graph, RelationGraph: relationGraph}
	encoded, err := json.Marshal(result)
	if err != nil {
		return VarianceReplayResult{}, err
	}
	result.ContentHash = hashBytes(encoded)
	return result, nil
}

func lowerTypeRelationGraph(candidates []varianceCandidate, indexes snapshotSemanticFactIndexes) (bingo.TypeRelationGraph, error) {
	declarationBySymbol := make(map[SymbolID]string, len(candidates))
	for _, candidate := range candidates {
		declarationBySymbol[candidate.symbol.ID] = candidate.typeRecord.CanonicalHash
	}
	nodes := make([]bingo.TypeRelationNode, 0, len(indexes.Types))
	edges := make([]bingo.TypeRelationEdge, 0)
	bySymbol := make(map[SymbolID][]TypeSnapshot)
	for _, record := range indexes.Types {
		if record.CanonicalHash == "" {
			return bingo.TypeRelationGraph{}, fmt.Errorf("type %d has no canonical hash", record.ID)
		}
		declaration := declarationBySymbol[record.Symbol]
		if declaration == "" {
			declaration = record.CanonicalHash
		}
		arguments := make([]string, len(record.TypeArguments))
		for i, argument := range record.TypeArguments {
			child, ok := indexes.Types[argument]
			if !ok || child.CanonicalHash == "" {
				return bingo.TypeRelationGraph{}, fmt.Errorf("type %d has invalid argument", record.ID)
			}
			arguments[i] = child.CanonicalHash
		}
		nodes = append(nodes, bingo.TypeRelationNode{TypeKey: record.CanonicalHash, DeclarationKey: declaration, ArgumentKeys: arguments})
		if record.Symbol != "" {
			bySymbol[record.Symbol] = append(bySymbol[record.Symbol], record)
		}
		for i, base := range record.BaseTypes {
			baseRecord, ok := indexes.Types[base]
			if !ok || baseRecord.CanonicalHash == "" {
				return bingo.TypeRelationGraph{}, fmt.Errorf("type %d has invalid base", record.ID)
			}
			edges = append(edges, bingo.TypeRelationEdge{SubTypeKey: record.CanonicalHash, SuperTypeKey: baseRecord.CanonicalHash, Path: record.DebugText + ".base[" + strconv.Itoa(i) + "]"})
		}
	}
	for symbol, records := range bySymbol {
		for i := 0; i < len(records); i++ {
			for j := i + 1; j < len(records); j++ {
				if records[i].CanonicalHash == records[j].CanonicalHash || !sameSnapshotTypeArguments(records[i], records[j], indexes) {
					continue
				}
				path := "symbol-equivalence:" + string(symbol)
				edges = append(edges,
					bingo.TypeRelationEdge{SubTypeKey: records[i].CanonicalHash, SuperTypeKey: records[j].CanonicalHash, Path: path},
					bingo.TypeRelationEdge{SubTypeKey: records[j].CanonicalHash, SuperTypeKey: records[i].CanonicalHash, Path: path})
			}
		}
	}
	return bingo.BuildTypeRelationGraph(nodes, edges)
}

func sameSnapshotTypeArguments(left, right TypeSnapshot, indexes snapshotSemanticFactIndexes) bool {
	if len(left.TypeArguments) != len(right.TypeArguments) {
		return false
	}
	for i := range left.TypeArguments {
		leftType, leftOK := indexes.Types[left.TypeArguments[i]]
		rightType, rightOK := indexes.Types[right.TypeArguments[i]]
		if !leftOK || !rightOK || leftType.CanonicalHash != rightType.CanonicalHash {
			return false
		}
	}
	return true
}

type varianceCandidate struct {
	typeRecord    TypeSnapshot
	node          NodeSnapshot
	symbol        SymbolSnapshot
	parameters    []bingo.VarianceParameter
	contractIndex uint32
}

func lowerVarianceContracts(indexes snapshotSemanticFactIndexes) ([]bingo.VarianceContract, []varianceCandidate, error) {
	candidates := make([]varianceCandidate, 0)
	for _, record := range indexes.Types {
		if record.Kind != "object" || len(record.TypeArguments) == 0 || record.Symbol == "" {
			continue
		}
		symbol, ok := indexes.Symbols[record.Symbol]
		if !ok || len(symbol.Declarations) != 1 {
			continue
		}
		node, ok := indexes.Nodes[symbol.Declarations[0]]
		if !ok || node.Kind != "KindInterfaceDeclaration" {
			continue
		}
		validDeclaration := true
		for _, typeID := range record.TypeArguments {
			parameterType, exists := indexes.Types[typeID]
			if !exists || parameterType.Symbol == "" || len(indexes.Symbols[parameterType.Symbol].Declarations) != 1 || indexes.Symbols[parameterType.Symbol].Declarations[0] == "" || indexes.Nodes[indexes.Symbols[parameterType.Symbol].Declarations[0]].Parent != node.ID {
				validDeclaration = false
				break
			}
		}
		if !validDeclaration {
			continue
		}
		candidates = append(candidates, varianceCandidate{typeRecord: record, node: node, symbol: symbol})
	}
	slices.SortFunc(candidates, func(left, right varianceCandidate) int {
		return strings.Compare(left.typeRecord.CanonicalHash, right.typeRecord.CanonicalHash)
	})
	contracts := make([]bingo.VarianceContract, 0, len(candidates))
	for _, item := range candidates {
		parameters := make([]bingo.VarianceParameter, len(item.typeRecord.TypeArguments))
		for index, typeID := range item.typeRecord.TypeArguments {
			parameterType, ok := indexes.Types[typeID]
			if !ok || parameterType.Kind != "typeParameter" || parameterType.Symbol == "" {
				return nil, nil, fmt.Errorf("%s has non-canonical type parameter %d", item.symbol.Name, index+1)
			}
			parameterSymbol, ok := indexes.Symbols[parameterType.Symbol]
			if !ok || len(parameterSymbol.Declarations) != 1 {
				return nil, nil, fmt.Errorf("%s parameter %d has no unique declaration", item.symbol.Name, index+1)
			}
			parameterNode, ok := indexes.Nodes[parameterSymbol.Declarations[0]]
			if !ok || parameterNode.Parent != item.node.ID || parameterNode.Kind != "KindTypeParameter" {
				return nil, nil, fmt.Errorf("%s parameter %d is not declared by the interface", item.symbol.Name, index+1)
			}
			parameters[index] = bingo.VarianceParameter{ID: uint32(index + 1), Name: parameterSymbol.Name, Annotation: varianceAnnotation(parameterNode.ModifierBits), TsgoHint: varianceHint(item.typeRecord.Variance, index)}
		}
		item.parameters = parameters
		item.contractIndex = uint32(len(contracts) + 1)
		candidates[len(contracts)] = item
		occurrences, err := lowerVarianceOccurrences(item.symbol.Name, item.typeRecord, parameters, indexes, candidates)
		if err != nil {
			return nil, nil, err
		}
		contract, err := bingo.BuildVarianceContract(item.typeRecord.CanonicalHash, parameters, occurrences)
		if err != nil {
			return nil, nil, fmt.Errorf("%s variance: %w", item.symbol.Name, err)
		}
		contracts = append(contracts, contract)
	}
	if len(contracts) == 0 {
		return nil, nil, fmt.Errorf("variance snapshot has no supported generic interface")
	}
	return contracts, candidates, nil
}

func lowerVarianceOccurrences(declaration string, record TypeSnapshot, parameters []bingo.VarianceParameter, indexes snapshotSemanticFactIndexes, candidates []varianceCandidate) ([]bingo.VarianceOccurrence, error) {
	parameterByType := make(map[TypeID]uint32, len(record.TypeArguments))
	for index, typeID := range record.TypeArguments {
		parameterByType[typeID] = uint32(index + 1)
	}
	occurrences := make([]bingo.VarianceOccurrence, 0)
	order := uint32(0)
	for _, property := range record.PropertyFacts {
		order++
		propertySymbol, ok := indexes.Symbols[property.Symbol]
		if !ok || len(propertySymbol.Declarations) != 1 {
			return nil, fmt.Errorf("%s property has no unique declaration", declaration)
		}
		propertyNode, ok := indexes.Nodes[propertySymbol.Declarations[0]]
		if !ok {
			return nil, fmt.Errorf("%s property declaration is missing", declaration)
		}
		path := declaration + "." + propertySymbol.Name
		if propertyNode.Kind == "KindMethodSignature" {
			methodType, ok := indexes.Types[property.ReadType]
			if !ok || len(methodType.CallSignatures) != 1 {
				return nil, fmt.Errorf("%s method %s has no unique call signature", declaration, propertySymbol.Name)
			}
			signature, ok := indexes.Signatures[methodType.CallSignatures[0]]
			if !ok {
				return nil, fmt.Errorf("%s method %s signature is missing", declaration, propertySymbol.Name)
			}
			for parameterIndex, parameter := range signature.ParameterFacts {
				if parameterID, ok := parameterByType[parameter.Type]; ok {
					occurrences = append(occurrences, bingo.VarianceOccurrence{ID: uint32(len(occurrences) + 1), ParameterID: parameterID, Kind: bingo.VarianceFunctionParameter, SourceOrder: order, Path: path + ".parameter[" + strconv.Itoa(parameterIndex) + "]"})
				}
			}
			if parameterID, ok := parameterByType[signature.ReturnType]; ok {
				occurrences = append(occurrences, bingo.VarianceOccurrence{ID: uint32(len(occurrences) + 1), ParameterID: parameterID, Kind: bingo.VarianceFunctionReturn, SourceOrder: order, Path: path + ".return"})
			}
			continue
		}
		if property.ReadType != 0 {
			if parameterID, ok := parameterByType[property.ReadType]; ok {
				kind := bingo.VarianceReadonlyProperty
				if property.WriteType != 0 && !property.Readonly {
					kind = bingo.VarianceWritableProperty
				}
				occurrences = append(occurrences, bingo.VarianceOccurrence{ID: uint32(len(occurrences) + 1), ParameterID: parameterID, Kind: kind, SourceOrder: order, Path: path})
			} else if typeContainsVarianceParameter(property.ReadType, parameterByType, indexes, map[TypeID]bool{}) && !typeHasVarianceDependency(property.ReadType, parameterByType, indexes, candidates, map[TypeID]bool{}) {
				occurrences = append(occurrences, bingo.VarianceOccurrence{ID: uint32(len(occurrences) + 1), ParameterID: firstContainedParameter(property.ReadType, parameterByType, indexes), Kind: bingo.VarianceResidual, SourceOrder: order, Path: path + ".residual"})
			}
		}
	}
	slices.SortFunc(occurrences, func(left, right bingo.VarianceOccurrence) int {
		if left.ParameterID != right.ParameterID {
			return int(left.ParameterID) - int(right.ParameterID)
		}
		if left.SourceOrder != right.SourceOrder {
			return int(left.SourceOrder) - int(right.SourceOrder)
		}
		return strings.Compare(left.Path, right.Path)
	})
	for index := range occurrences {
		occurrences[index].ID = uint32(index + 1)
	}
	return occurrences, nil
}

func lowerVarianceDependencyEdges(candidates []varianceCandidate, indexes snapshotSemanticFactIndexes) ([]bingo.VarianceDependencyEdge, error) {
	bySymbol := make(map[SymbolID]varianceCandidate, len(candidates))
	nodeID := make(map[[2]uint32]uint32)
	for i, candidate := range candidates {
		bySymbol[candidate.symbol.ID] = candidate
		for p := range candidate.parameters {
			nodeID[[2]uint32{uint32(i + 1), uint32(p + 1)}] = uint32(len(nodeID) + 1)
		}
	}
	var edges []bingo.VarianceDependencyEdge
	for ownerIndex, candidate := range candidates {
		params := make(map[TypeID]uint32, len(candidate.typeRecord.TypeArguments))
		for i, id := range candidate.typeRecord.TypeArguments {
			params[id] = uint32(i + 1)
		}
		var add func(TypeID, bingo.VarianceTransform, string, map[TypeID]bool)
		add = func(typeID TypeID, transform bingo.VarianceTransform, path string, seen map[TypeID]bool) {
			record, ok := indexes.Types[typeID]
			if !ok || seen[typeID] {
				return
			}
			seen[typeID] = true
			if target, ok := bySymbol[record.Symbol]; ok && len(record.TypeArguments) == len(target.parameters) {
				for i, arg := range record.TypeArguments {
					if p := params[arg]; p != 0 {
						edges = append(edges, bingo.VarianceDependencyEdge{OwnerNodeID: nodeID[[2]uint32{uint32(ownerIndex + 1), p}], DependencyNodeID: nodeID[[2]uint32{target.contractIndex, uint32(i + 1)}], Transform: transform, Path: path})
					}
				}
			}
			for i, child := range slices.Concat(record.ElementTypes, record.TypeArguments) {
				add(child, transform, path+".type["+strconv.Itoa(i)+"]", seen)
			}
		}
		for _, property := range candidate.typeRecord.PropertyFacts {
			symbol := indexes.Symbols[property.Symbol]
			path := candidate.symbol.Name + "." + symbol.Name
			if len(symbol.Declarations) == 1 && indexes.Nodes[symbol.Declarations[0]].Kind == "KindMethodSignature" {
				method := indexes.Types[property.ReadType]
				if len(method.CallSignatures) != 1 {
					return nil, fmt.Errorf("%s has no unique call signature", path)
				}
				signature, ok := indexes.Signatures[method.CallSignatures[0]]
				if !ok {
					return nil, fmt.Errorf("%s signature is missing", path)
				}
				for i, parameter := range signature.ParameterFacts {
					add(parameter.Type, bingo.VarianceTransformNegative, path+".parameter["+strconv.Itoa(i)+"]", map[TypeID]bool{})
				}
				add(signature.ReturnType, bingo.VarianceTransformPositive, path+".return", map[TypeID]bool{})
				continue
			}
			transform := bingo.VarianceTransformPositive
			if property.WriteType != 0 && !property.Readonly {
				transform = bingo.VarianceTransformBoth
			}
			add(property.ReadType, transform, path, map[TypeID]bool{})
		}
	}
	slices.SortFunc(edges, func(a, b bingo.VarianceDependencyEdge) int {
		if a.OwnerNodeID != b.OwnerNodeID {
			return int(a.OwnerNodeID - b.OwnerNodeID)
		}
		if a.DependencyNodeID != b.DependencyNodeID {
			return int(a.DependencyNodeID - b.DependencyNodeID)
		}
		return strings.Compare(a.Path, b.Path)
	})
	for i := range edges {
		edges[i].ID = uint32(i + 1)
	}
	return edges, nil
}

func typeHasVarianceDependency(typeID TypeID, parameters map[TypeID]uint32, indexes snapshotSemanticFactIndexes, candidates []varianceCandidate, seen map[TypeID]bool) bool {
	if parameters[typeID] != 0 || seen[typeID] {
		return false
	}
	seen[typeID] = true
	record, ok := indexes.Types[typeID]
	if !ok {
		return false
	}
	for _, candidate := range candidates {
		if record.Symbol == candidate.symbol.ID {
			for _, argument := range record.TypeArguments {
				if typeContainsVarianceParameter(argument, parameters, indexes, map[TypeID]bool{}) {
					return true
				}
			}
		}
	}
	for _, child := range slices.Concat(record.ElementTypes, record.TypeArguments) {
		if typeHasVarianceDependency(child, parameters, indexes, candidates, seen) {
			return true
		}
	}
	return false
}

func typeContainsVarianceParameter(typeID TypeID, parameters map[TypeID]uint32, indexes snapshotSemanticFactIndexes, seen map[TypeID]bool) bool {
	if parameters[typeID] != 0 {
		return true
	}
	if seen[typeID] {
		return false
	}
	seen[typeID] = true
	record, ok := indexes.Types[typeID]
	if !ok {
		return false
	}
	for _, child := range slices.Concat(record.ElementTypes, record.TypeArguments) {
		if typeContainsVarianceParameter(child, parameters, indexes, seen) {
			return true
		}
	}
	for _, property := range record.PropertyFacts {
		if typeContainsVarianceParameter(property.ReadType, parameters, indexes, seen) || typeContainsVarianceParameter(property.WriteType, parameters, indexes, seen) {
			return true
		}
	}
	return false
}

func firstContainedParameter(typeID TypeID, parameters map[TypeID]uint32, indexes snapshotSemanticFactIndexes) uint32 {
	if id := parameters[typeID]; id != 0 {
		return id
	}
	if record, ok := indexes.Types[typeID]; ok {
		for _, child := range slices.Concat(record.ElementTypes, record.TypeArguments) {
			if id := firstContainedParameter(child, parameters, indexes); id != 0 {
				return id
			}
		}
	}
	return 0
}

func varianceAnnotation(bits uint32) bingo.VarianceAnnotation {
	const (
		modifierIn  = 1 << 13
		modifierOut = 1 << 14
	)
	switch {
	case bits&modifierIn != 0 && bits&modifierOut != 0:
		return bingo.VarianceAnnotationInOut
	case bits&modifierIn != 0:
		return bingo.VarianceAnnotationIn
	case bits&modifierOut != 0:
		return bingo.VarianceAnnotationOut
	default:
		return bingo.VarianceAnnotationNone
	}
}

func varianceHint(raw string, index int) bingo.VarianceHint {
	parts := strings.Split(raw, ",")
	if index >= len(parts) {
		return bingo.VarianceHintIndependent
	}
	value := strings.TrimSpace(parts[index])
	if strings.Contains(value, "unmeasurable") {
		return bingo.VarianceHintUnmeasurable
	}
	if strings.Contains(value, "unreliable") {
		return bingo.VarianceHintUnreliable
	}
	value = strings.Trim(value, "[]")
	switch value {
	case "out", "covariant":
		return bingo.VarianceHintCovariant
	case "in", "contravariant":
		return bingo.VarianceHintContravariant
	case "in out", "invariant":
		return bingo.VarianceHintInvariant
	case "bivariant":
		return bingo.VarianceHintBivariant
	default:
		return bingo.VarianceHintIndependent
	}
}
