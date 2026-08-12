package ast2bingo

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"

	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
)

const VERT010ReplaySchemaVersion uint32 = 1

// VERT010ReplayResult is the checker-free snapshot-to-object-HIR artifact.
type VERT010ReplayResult struct {
	SchemaVersion         uint32                      `json:"schemaVersion"`
	FrontendSnapshotHash  string                      `json:"frontendSnapshotHash"`
	CompilerBuildIdentity bingo.CompilerBuildIdentity `json:"compilerBuildIdentity"`
	Events                []LoweringEvent             `json:"events"`
	HIR                   bingo.HIRModule             `json:"hir"`
	ContentHash           string                      `json:"contentHash"`
}

// CanonicalBytes verifies the complete object replay before serialization.
func (result VERT010ReplayResult) CanonicalBytes() ([]byte, error) {
	if result.SchemaVersion != VERT010ReplaySchemaVersion || result.FrontendSnapshotHash == "" || result.FrontendSnapshotHash != result.HIR.Provenance.FrontendSnapshotHash {
		return nil, fmt.Errorf("invalid VERT-010 replay identity")
	}
	if err := bingo.ValidateCompilerBuildIdentity(result.CompilerBuildIdentity); err != nil || result.CompilerBuildIdentity != result.HIR.Provenance.CompilerBuildIdentity {
		return nil, fmt.Errorf("invalid VERT-010 compiler identity")
	}
	if err := bingo.VerifyCanonicalVERT010ObjectHIR(result.HIR); err != nil {
		return nil, err
	}
	withoutHash := result
	withoutHash.ContentHash = ""
	encoded, err := json.Marshal(withoutHash)
	if err != nil {
		return nil, err
	}
	if want := hashBytes(encoded); result.ContentHash != want {
		return nil, fmt.Errorf("VERT-010 replay content hash mismatch: got %q, want %q", result.ContentHash, want)
	}
	return json.Marshal(result)
}

// ReplayVERT010FrontendSnapshot strictly decodes and lowers the first owned
// object slice without retaining AST or checker state.
func ReplayVERT010FrontendSnapshot(data []byte, identity bingo.CompilerBuildIdentity) (VERT010ReplayResult, error) {
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		return VERT010ReplayResult{}, err
	}
	return ReplayVERT010Snapshot(frontend.Program, identity)
}

// ReplayVERT010Snapshot lowers the exact objectAlias source contract to HIR v9.
func ReplayVERT010Snapshot(snapshot ProgramSnapshot, identity bingo.CompilerBuildIdentity) (VERT010ReplayResult, error) {
	if err := validateCompilerIdentityForSnapshot(identity, snapshot); err != nil {
		return VERT010ReplayResult{}, err
	}
	plan, err := buildPrimitiveSourceTypePlan(snapshot)
	if err != nil {
		return VERT010ReplayResult{}, err
	}
	if len(plan.Functions) != 1 {
		return VERT010ReplayResult{}, fmt.Errorf("VERT-010 requires one function")
	}
	indexes := indexPrimitiveSnapshot(snapshot)
	functionNode := indexes.Nodes[plan.Functions[0]]
	function, objectType, events, err := lowerVERT010ObjectAlias(functionNode, indexes)
	if err != nil {
		return VERT010ReplayResult{}, err
	}
	requirements := bingo.VERT010LogicalCapabilities()
	requirementsDigest, err := bingo.LogicalCapabilityRequirementsDigest(requirements)
	if err != nil {
		return VERT010ReplayResult{}, err
	}
	module := bingo.HIRModule{
		SchemaVersion:                 bingo.VERT010HIRSchemaVersion,
		Provenance:                    primitiveHIRProvenance(snapshot, identity, requirementsDigest),
		LogicalCapabilityRequirements: requirements,
		ObjectTypes:                   []bingo.HIRObjectType{objectType},
		Functions:                     []bingo.HIRFunction{function},
	}
	_, hirHash, err := bingo.CanonicalVERT010ObjectHIR(module)
	if err != nil {
		return VERT010ReplayResult{}, err
	}
	module.ContentHash = hirHash
	result := VERT010ReplayResult{SchemaVersion: VERT010ReplaySchemaVersion, FrontendSnapshotHash: snapshot.ContentHash, CompilerBuildIdentity: identity, Events: events, HIR: module}
	encoded, err := json.Marshal(result)
	if err != nil {
		return VERT010ReplayResult{}, err
	}
	result.ContentHash = hashBytes(encoded)
	return result, nil
}

type vert010Source struct {
	ObjectDeclaration NodeSnapshot
	ObjectLiteral     NodeSnapshot
	Shorthand         NodeSnapshot
	AliasDeclaration  NodeSnapshot
	AliasInitializer  NodeSnapshot
	Assignment        NodeSnapshot
	StoreAccess       NodeSnapshot
	Add               NodeSnapshot
	LoadAlias         NodeSnapshot
	One               NodeSnapshot
	Return            NodeSnapshot
	LoadOriginal      NodeSnapshot
}

func lowerVERT010ObjectAlias(functionNode NodeSnapshot, indexes snapshotSemanticFactIndexes) (bingo.HIRFunction, bingo.HIRObjectType, []LoweringEvent, error) {
	source, err := findVERT010Source(functionNode, indexes.Nodes)
	if err != nil {
		return bingo.HIRFunction{}, bingo.HIRObjectType{}, nil, err
	}
	return lowerVERT010ObjectAliasSource(functionNode, source, indexes)
}

func lowerVERT010ObjectAliasSource(functionNode NodeSnapshot, source vert010Source, indexes snapshotSemanticFactIndexes) (bingo.HIRFunction, bingo.HIRObjectType, []LoweringEvent, error) {
	if childText(functionNode, "name", indexes.Nodes) != "objectAlias" || functionNode.ModifierBits != snapshotModifierExport {
		return bingo.HIRFunction{}, bingo.HIRObjectType{}, nil, fmt.Errorf("VERT-010 requires exported objectAlias")
	}
	parameters := namedChildren(functionNode, "parameter[")
	if len(parameters) != 1 {
		return bingo.HIRFunction{}, bingo.HIRObjectType{}, nil, fmt.Errorf("VERT-010 requires one parameter")
	}
	parameter := indexes.Nodes[parameters[0]]
	if childText(parameter, "name", indexes.Nodes) != "value" {
		return bingo.HIRFunction{}, bingo.HIRObjectType{}, nil, fmt.Errorf("VERT-010 parameter must be value")
	}
	parameterType, err := bingoType(nodeTypeID(parameter), indexes.Types)
	if err != nil || parameterType != bingo.TypeNumber {
		return bingo.HIRFunction{}, bingo.HIRObjectType{}, nil, fmt.Errorf("VERT-010 parameter is not number")
	}
	returnTypeID, ok := resolveFunctionReturnType(functionNode, indexes.Nodes, indexes.Symbols, indexes.Types, indexes.Signatures)
	returnType, returnErr := bingoType(returnTypeID, indexes.Types)
	if !ok || returnErr != nil || returnType != bingo.TypeNumber {
		return bingo.HIRFunction{}, bingo.HIRObjectType{}, nil, fmt.Errorf("VERT-010 return type is not number")
	}
	objectRecord, ok := indexes.Types[nodeTypeID(source.ObjectDeclaration)]
	if !ok {
		return bingo.HIRFunction{}, bingo.HIRObjectType{}, nil, fmt.Errorf("VERT-010 object type is missing")
	}
	matched, err := validateVERT010ObjectTypeClosure(objectRecord, indexes)
	if err != nil || !matched {
		return bingo.HIRFunction{}, bingo.HIRObjectType{}, nil, fmt.Errorf("VERT-010 object type: %w", err)
	}
	propertyFact := objectRecord.PropertyFacts[0]
	propertySymbol := indexes.Symbols[propertyFact.Symbol]
	valueType := indexes.Types[propertyFact.ReadType]
	semantic := bingo.ObjectSemanticContract{
		SchemaVersion: bingo.ObjectSemanticContractSchemaVersion,
		TypeKey:       objectRecord.CanonicalHash,
		Identity:      bingo.ObjectIdentityReference,
		Equality:      bingo.ObjectEqualityReference,
		Properties: []bingo.ObjectPropertyContract{{
			Key: "value", Kind: bingo.ObjectPropertyData, ReadTypeKey: valueType.CanonicalHash, WriteTypeKey: valueType.CanonicalHash, Visibility: "public",
		}},
	}
	_, semanticHash, err := bingo.CanonicalObjectSemanticContract(semantic)
	if err != nil {
		return bingo.HIRFunction{}, bingo.HIRObjectType{}, nil, err
	}
	objectType := bingo.HIRObjectType{TypeKey: objectRecord.CanonicalHash, SemanticContractHash: semanticHash, Properties: []bingo.HIRObjectProperty{{Key: "value", SymbolKey: string(propertySymbol.ID), SourceTypeKey: valueType.CanonicalHash, Type: bingo.TypeNumber, Mutable: true, Required: true}}}
	key, propertyKey := objectType.TypeKey, objectType.Properties[0].SymbolKey
	empty := []bingo.RuntimeCapabilityID{}
	operations := []bingo.HIROp{
		{ID: 2, Kind: "object.alloc", Type: bingo.TypeObject, Effect: bingo.EffectAllocate, ObjectTypeKey: key, LogicalCapabilityRequirements: []bingo.RuntimeCapabilityID{"rt.gc.alloc"}, Origin: originOf(source.ObjectLiteral)},
		{ID: 3, Kind: "object.field.init", Type: bingo.TypeObject, Operands: []bingo.ValueID{2, 1}, Effect: bingo.EffectWrite, ObjectTypeKey: key, PropertySymbolKey: propertyKey, LogicalCapabilityRequirements: empty, Origin: originOf(source.Shorthand)},
		{ID: 4, Kind: "object.alias", Type: bingo.TypeObject, Operands: []bingo.ValueID{3}, Effect: bingo.EffectPure, ObjectTypeKey: key, LogicalCapabilityRequirements: empty, Origin: originOf(source.AliasInitializer)},
		{ID: 5, Kind: "object.field.load", Type: bingo.TypeNumber, Operands: []bingo.ValueID{4}, Effect: bingo.EffectRead, ObjectTypeKey: key, PropertySymbolKey: propertyKey, LogicalCapabilityRequirements: empty, Origin: originOf(source.LoadAlias)},
		{ID: 6, Kind: "number.constant", Type: bingo.TypeNumber, NumberBits: "3ff0000000000000", Effect: bingo.EffectPure, LogicalCapabilityRequirements: empty, Origin: originOf(source.One)},
		{ID: 7, Kind: "binary", Type: bingo.TypeNumber, Operands: []bingo.ValueID{5, 6}, Operator: "+", Effect: bingo.EffectPure, LogicalCapabilityRequirements: empty, Origin: originOf(source.Add)},
		{ID: 8, Kind: "object.field.store", Type: bingo.TypeObject, Operands: []bingo.ValueID{4, 7}, Effect: bingo.EffectWrite, ObjectTypeKey: key, PropertySymbolKey: propertyKey, LogicalCapabilityRequirements: empty, Origin: originOf(source.Assignment)},
		{ID: 9, Kind: "object.field.load", Type: bingo.TypeNumber, Operands: []bingo.ValueID{3}, Effect: bingo.EffectRead, ObjectTypeKey: key, PropertySymbolKey: propertyKey, LogicalCapabilityRequirements: empty, Origin: originOf(source.LoadOriginal)},
	}
	function := bingo.HIRFunction{ID: 1, Name: "objectAlias", Exported: true, Parameters: []bingo.HIRParameter{{Name: "value", Value: 1, Type: bingo.TypeNumber, Origin: originOf(parameter)}}, Blocks: []bingo.HIRBlock{{ID: 1, Operations: operations, Terminator: bingo.HIRTerminator{Kind: "return", Value: 9, Origin: originOf(source.Return)}}}, ReturnType: bingo.TypeNumber, Origin: originOf(functionNode)}
	events := []LoweringEvent{
		{Kind: "function.begin", Node: functionNode.ID, Origin: functionNode.Origin},
		{Kind: "parameter", Node: parameter.ID, Origin: parameter.Origin, Type: nodeTypeID(parameter)},
		{Kind: "object.alloc", Node: source.ObjectLiteral.ID, Origin: source.ObjectLiteral.Origin, Type: nodeTypeID(source.ObjectLiteral)},
		{Kind: "object.field.init", Node: source.Shorthand.ID, Origin: source.Shorthand.Origin, Type: propertyFact.ReadType},
		{Kind: "object.alias", Node: source.AliasInitializer.ID, Origin: source.AliasInitializer.Origin, Type: nodeTypeID(source.AliasInitializer)},
		{Kind: "object.field.load", Node: source.LoadAlias.ID, Origin: source.LoadAlias.Origin, Type: propertyFact.ReadType},
		{Kind: "literal.number", Node: source.One.ID, Origin: source.One.Origin, Type: nodeTypeID(source.One)},
		{Kind: "binary.add", Node: source.Add.ID, Origin: source.Add.Origin, Type: nodeTypeID(source.Add), Operator: "+"},
		{Kind: "object.field.store", Node: source.Assignment.ID, Origin: source.Assignment.Origin, Type: propertyFact.WriteType},
		{Kind: "object.field.load", Node: source.LoadOriginal.ID, Origin: source.LoadOriginal.Origin, Type: propertyFact.ReadType},
		{Kind: "return", Node: source.Return.ID, Origin: source.Return.Origin, Type: returnTypeID},
		{Kind: "function.end", Node: functionNode.ID, Origin: functionNode.Origin},
	}
	return function, objectType, events, nil
}

func findVERT010Source(functionNode NodeSnapshot, nodes map[NodeID]NodeSnapshot) (vert010Source, error) {
	body, ok := nodes[childByRole(functionNode, "body")]
	if !ok || body.Kind != snapshotKindBlock {
		return vert010Source{}, fmt.Errorf("VERT-010 body is not a block")
	}
	statements := namedChildren(body, "statement[")
	if len(statements) != 4 {
		return vert010Source{}, fmt.Errorf("VERT-010 requires four statements")
	}
	objectDecl, objectInit, err := vert010Variable(statements[0], nodes)
	if err != nil || objectInit.Kind != snapshotKindObjectLiteralExpression || childText(objectDecl, "name", nodes) != "object" {
		return vert010Source{}, fmt.Errorf("VERT-010 object declaration is invalid")
	}
	shorthand, err := requireRoleKind(objectInit, "child[0]", snapshotKindShorthandPropertyAssignment, nodes)
	if err != nil || childText(shorthand, "name", nodes) != "value" {
		return vert010Source{}, fmt.Errorf("VERT-010 object property is invalid")
	}
	aliasDecl, aliasInit, err := vert010Variable(statements[1], nodes)
	if err != nil || aliasInit.Kind != snapshotKindIdentifier || childText(aliasDecl, "name", nodes) != "alias" || aliasInit.SyntaxPayload.Text != "object" {
		return vert010Source{}, fmt.Errorf("VERT-010 alias declaration is invalid")
	}
	expressionStatement, ok := nodes[statements[2]]
	if !ok || expressionStatement.Kind != snapshotKindExpressionStatement {
		return vert010Source{}, fmt.Errorf("VERT-010 assignment statement is invalid")
	}
	assignment, err := requireRoleKind(expressionStatement, "expression", snapshotKindBinaryExpression, nodes)
	if err != nil || assignment.SyntaxPayload.Operator != snapshotKindEqualsToken {
		return vert010Source{}, fmt.Errorf("VERT-010 assignment is invalid")
	}
	storeAccess, err := requireRoleKind(assignment, "left", snapshotKindPropertyAccessExpression, nodes)
	if err != nil || !vert010Access(storeAccess, "alias", nodes) {
		return vert010Source{}, fmt.Errorf("VERT-010 store receiver is invalid")
	}
	add, err := requireRoleKind(assignment, "right", snapshotKindBinaryExpression, nodes)
	if err != nil || add.SyntaxPayload.Operator != snapshotKindPlusToken {
		return vert010Source{}, fmt.Errorf("VERT-010 increment is invalid")
	}
	loadAlias, err := requireRoleKind(add, "left", snapshotKindPropertyAccessExpression, nodes)
	if err != nil || !vert010Access(loadAlias, "alias", nodes) {
		return vert010Source{}, fmt.Errorf("VERT-010 alias load is invalid")
	}
	one, err := requireRoleKind(add, "right", snapshotKindNumericLiteral, nodes)
	if err != nil || one.Constant.Kind != "number" || one.Constant.Number != 1 || one.Constant.Text != "1" || math.Float64bits(one.Constant.Number) != 0x3ff0000000000000 {
		return vert010Source{}, fmt.Errorf("VERT-010 increment constant is invalid")
	}
	returnNode, ok := nodes[statements[3]]
	if !ok || returnNode.Kind != snapshotKindReturnStatement {
		return vert010Source{}, fmt.Errorf("VERT-010 return is invalid")
	}
	loadOriginal, err := requireRoleKind(returnNode, "expression", snapshotKindPropertyAccessExpression, nodes)
	if err != nil || !vert010Access(loadOriginal, "object", nodes) {
		return vert010Source{}, fmt.Errorf("VERT-010 return receiver is invalid")
	}
	propertySymbols := []SymbolID{storeAccess.Symbol, loadAlias.Symbol, loadOriginal.Symbol}
	if propertySymbols[0] == "" || !slices.Equal(propertySymbols, []SymbolID{propertySymbols[0], propertySymbols[0], propertySymbols[0]}) {
		return vert010Source{}, fmt.Errorf("VERT-010 property symbol identity mismatch")
	}
	return vert010Source{objectDecl, objectInit, shorthand, aliasDecl, aliasInit, assignment, storeAccess, add, loadAlias, one, returnNode, loadOriginal}, nil
}

func vert010Variable(statementID NodeID, nodes map[NodeID]NodeSnapshot) (NodeSnapshot, NodeSnapshot, error) {
	statement, ok := nodes[statementID]
	if !ok || statement.Kind != snapshotKindVariableStatement {
		return NodeSnapshot{}, NodeSnapshot{}, fmt.Errorf("not a variable statement")
	}
	list, err := requireRoleKind(statement, "declarationList", snapshotKindVariableList, nodes)
	if err != nil || list.NodeFlags != 2 {
		return NodeSnapshot{}, NodeSnapshot{}, fmt.Errorf("VERT-010 variable must be const")
	}
	declaration, err := requireRoleKind(list, "declaration[0]", snapshotKindVariableDeclaration, nodes)
	if err != nil {
		return NodeSnapshot{}, NodeSnapshot{}, err
	}
	initializerID := childByRole(declaration, "initializer")
	initializer, ok := nodes[initializerID]
	if !ok || initializer.Parent != declaration.ID {
		return NodeSnapshot{}, NodeSnapshot{}, fmt.Errorf("variable initializer is missing")
	}
	return declaration, initializer, nil
}

func vert010Access(access NodeSnapshot, receiverName string, nodes map[NodeID]NodeSnapshot) bool {
	receiver, err := requireRoleKind(access, "child[0]", snapshotKindIdentifier, nodes)
	if err != nil || receiver.SyntaxPayload.Text != receiverName {
		return false
	}
	name, err := requireRoleKind(access, "child[1]", snapshotKindIdentifier, nodes)
	return err == nil && name.SyntaxPayload.Text == "value"
}
