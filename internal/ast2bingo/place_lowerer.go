package ast2bingo

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"

	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
)

const VERT011ReplaySchemaVersion uint32 = 1

// VERT011ReplayResult is the checker-free snapshot-to-property-place HIR artifact.
type VERT011ReplayResult struct {
	SchemaVersion         uint32                      `json:"schemaVersion"`
	FrontendSnapshotHash  string                      `json:"frontendSnapshotHash"`
	CompilerBuildIdentity bingo.CompilerBuildIdentity `json:"compilerBuildIdentity"`
	Events                []LoweringEvent             `json:"events"`
	HIR                   bingo.HIRModule             `json:"hir"`
	ContentHash           string                      `json:"contentHash"`
}

func (result VERT011ReplayResult) CanonicalBytes() ([]byte, error) {
	if result.SchemaVersion != VERT011ReplaySchemaVersion || result.FrontendSnapshotHash == "" || result.FrontendSnapshotHash != result.HIR.Provenance.FrontendSnapshotHash || result.CompilerBuildIdentity != result.HIR.Provenance.CompilerBuildIdentity {
		return nil, fmt.Errorf("invalid VERT-011 replay identity")
	}
	if err := bingo.ValidateCompilerBuildIdentity(result.CompilerBuildIdentity); err != nil {
		return nil, err
	}
	if err := bingo.VerifyCanonicalVERT011PlaceHIR(result.HIR); err != nil {
		return nil, err
	}
	withoutHash := result
	withoutHash.ContentHash = ""
	encoded, err := json.Marshal(withoutHash)
	if err != nil {
		return nil, err
	}
	if result.ContentHash != hashBytes(encoded) {
		return nil, fmt.Errorf("VERT-011 replay content hash mismatch")
	}
	return json.Marshal(result)
}

func ReplayVERT011FrontendSnapshot(data []byte, identity bingo.CompilerBuildIdentity) (VERT011ReplayResult, error) {
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		return VERT011ReplayResult{}, err
	}
	return ReplayVERT011Snapshot(frontend.Program, identity)
}

// ReplayVERT011Snapshot lowers exactly propertyNullishAssign to HIR v10.
func ReplayVERT011Snapshot(snapshot ProgramSnapshot, identity bingo.CompilerBuildIdentity) (VERT011ReplayResult, error) {
	if err := validateCompilerIdentityForSnapshot(identity, snapshot); err != nil {
		return VERT011ReplayResult{}, err
	}
	plan, err := buildPrimitiveSourceTypePlan(snapshot)
	if err != nil {
		return VERT011ReplayResult{}, err
	}
	if len(plan.Functions) != 1 {
		return VERT011ReplayResult{}, fmt.Errorf("VERT-011 requires one function")
	}
	indexes := indexPrimitiveSnapshot(snapshot)
	function, ok := indexes.Nodes[plan.Functions[0]]
	if !ok {
		return VERT011ReplayResult{}, fmt.Errorf("VERT-011 function is missing")
	}
	hirFunction, places, events, err := lowerVERT011PropertyNullishAssign(function, indexes)
	if err != nil {
		return VERT011ReplayResult{}, err
	}
	requirements := bingo.VERT010LogicalCapabilities()
	digest, err := bingo.LogicalCapabilityRequirementsDigest(requirements)
	if err != nil {
		return VERT011ReplayResult{}, err
	}
	module := bingo.HIRModule{SchemaVersion: bingo.VERT011HIRSchemaVersion, Provenance: primitiveHIRProvenance(snapshot, identity, digest), LogicalCapabilityRequirements: requirements, PlaceRefs: &places, Functions: []bingo.HIRFunction{hirFunction}}
	_, hash, err := bingo.CanonicalVERT011PlaceHIR(module)
	if err != nil {
		return VERT011ReplayResult{}, err
	}
	module.ContentHash = hash
	result := VERT011ReplayResult{SchemaVersion: VERT011ReplaySchemaVersion, FrontendSnapshotHash: snapshot.ContentHash, CompilerBuildIdentity: identity, Events: events, HIR: module}
	encoded, err := json.Marshal(result)
	if err != nil {
		return VERT011ReplayResult{}, err
	}
	result.ContentHash = hashBytes(encoded)
	return result, nil
}

func lowerVERT011PropertyNullishAssign(function NodeSnapshot, indexes snapshotSemanticFactIndexes) (bingo.HIRFunction, bingo.PlaceRefContract, []LoweringEvent, error) {
	bad := func(format string, args ...any) (bingo.HIRFunction, bingo.PlaceRefContract, []LoweringEvent, error) {
		return bingo.HIRFunction{}, bingo.PlaceRefContract{}, nil, fmt.Errorf(format, args...)
	}
	if function.Kind != snapshotKindFunctionDeclaration || function.ModifierBits != snapshotModifierExport || childText(function, "name", indexes.Nodes) != "propertyNullishAssign" {
		return bad("VERT-011 requires exported propertyNullishAssign")
	}
	parameters := namedChildren(function, "parameter[")
	if len(parameters) != 1 {
		return bad("VERT-011 requires one parameter")
	}
	parameter := indexes.Nodes[parameters[0]]
	if childText(parameter, "name", indexes.Nodes) != "value" {
		return bad("VERT-011 parameter must be value")
	}
	parameterType, err := bingoType(nodeTypeID(parameter), indexes.Types)
	if err != nil || parameterType != bingo.TypeNullableNumber {
		return bad("VERT-011 value type is not nullable number")
	}
	returnTypeID, returnOK := resolveFunctionReturnType(function, indexes.Nodes, indexes.Symbols, indexes.Types, indexes.Signatures)
	returnType, err := bingoType(returnTypeID, indexes.Types)
	if !returnOK || err != nil || returnType != bingo.TypeNumber {
		return bad("VERT-011 return type is not number")
	}
	body, err := requireRoleKind(function, "body", snapshotKindBlock, indexes.Nodes)
	if err != nil {
		return bad("VERT-011 body: %w", err)
	}
	statements := namedChildren(body, "statement[")
	if len(statements) != 3 {
		return bad("VERT-011 requires three statements")
	}
	objectDecl, objectLiteral, err := vert011Variable(statements[0], indexes.Nodes, "object")
	if err != nil || objectLiteral.Kind != snapshotKindObjectLiteralExpression {
		return bad("VERT-011 object declaration is invalid")
	}
	keyDecl, assertion, err := vert011Variable(statements[1], indexes.Nodes, "key")
	if err != nil || assertion.Kind != snapshotKindAsExpression {
		return bad("VERT-011 key declaration is invalid")
	}
	if err := validateVERT011AsExpressionLowerer(assertion, indexes.Nodes); err != nil {
		return bad("VERT-011 key: %w", err)
	}
	returnNode := indexes.Nodes[statements[2]]
	parenthesized, err := requireRoleKind(returnNode, "expression", snapshotKindParenthesizedExpression, indexes.Nodes)
	if returnNode.Kind != snapshotKindReturnStatement || err != nil {
		return bad("VERT-011 return is invalid")
	}
	assignment, err := requireRoleKind(parenthesized, "expression", snapshotKindBinaryExpression, indexes.Nodes)
	if err != nil || assignment.SyntaxPayload.Operator != snapshotKindQuestionQuestionEqualsToken {
		return bad("VERT-011 assignment is invalid")
	}
	access, err := requireRoleKind(assignment, "left", snapshotKindElementAccessExpression, indexes.Nodes)
	if err != nil || validateVERT011ElementAccessLowerer(access, indexes.Nodes) != nil {
		return bad("VERT-011 place access is invalid")
	}
	rhs, err := requireRoleKind(assignment, "right", snapshotKindNumericLiteral, indexes.Nodes)
	if err != nil || rhs.Constant.Kind != "number" || rhs.Constant.Text != "1" || rhs.Constant.Number != 1 || math.Float64bits(rhs.Constant.Number) != 0x3ff0000000000000 {
		return bad("VERT-011 RHS is not canonical one")
	}
	objectRecord, ok := indexes.Types[nodeTypeID(objectDecl)]
	if !ok {
		return bad("VERT-011 object type is missing")
	}
	if len(objectRecord.Properties) != 2 || len(objectRecord.PropertyFacts) != 2 {
		return bad("VERT-011 object property facts are incomplete")
	}
	var backing, result frontendwire.PropertySnapshot
	var backingSymbol, resultSymbol frontendwire.SymbolSnapshot
	for _, fact := range objectRecord.PropertyFacts {
		symbol, exists := indexes.Symbols[fact.Symbol]
		if !exists {
			return bad("VERT-011 property symbol is missing")
		}
		switch symbol.Name {
		case "backing":
			backing, backingSymbol = fact, symbol
		case "result":
			result, resultSymbol = fact, symbol
		default:
			return bad("VERT-011 unexpected property %q", symbol.Name)
		}
	}
	if backing.Symbol == "" || result.Symbol == "" || backing.Optional || backing.Readonly || !backing.HasGetter || !backing.HasSetter || result.Optional || result.Readonly || !result.HasGetter || !result.HasSetter || backing.Visibility != "public" || result.Visibility != "public" || backing.ReadType != backing.WriteType || result.ReadType != backing.ReadType {
		return bad("VERT-011 property facts are invalid")
	}
	readType, readErr := bingoType(backing.ReadType, indexes.Types)
	writeType, writeErr := bingoType(result.WriteType, indexes.Types)
	if readErr != nil || writeErr != nil || readType != bingo.TypeNullableNumber || writeType != bingo.TypeNumber {
		return bad("VERT-011 property types are invalid")
	}
	property, getter, setter, err := vert011ObjectMembers(objectLiteral, indexes.Nodes)
	if err != nil {
		return bad("VERT-011 members: %w", err)
	}
	if !sameNodeIDSet(resultSymbol.Declarations, getter.ID, setter.ID) || !slices.Equal(backingSymbol.Declarations, []NodeID{property.ID}) {
		return bad("VERT-011 property declarations do not bind symbols")
	}
	if !vert011GetterBody(getter, indexes.Nodes) || !vert011SetterBody(setter, indexes.Nodes) {
		return bad("VERT-011 accessor bodies are invalid")
	}
	if childText(access, "child[0]", indexes.Nodes) != "object" || childText(access, "child[1]", indexes.Nodes) != "key" {
		return bad("VERT-011 receiver/key identities are invalid")
	}
	readHash, writeHash := indexes.Types[backing.ReadType].CanonicalHash, indexes.Types[result.WriteType].CanonicalHash
	semantic := bingo.ObjectSemanticContract{SchemaVersion: bingo.ObjectSemanticContractSchemaVersion, TypeKey: objectRecord.CanonicalHash, Identity: bingo.ObjectIdentityReference, Equality: bingo.ObjectEqualityReference, Properties: []bingo.ObjectPropertyContract{{Key: "backing", Kind: bingo.ObjectPropertyData, ReadTypeKey: readHash, WriteTypeKey: readHash, Visibility: "public"}, {Key: "result", Kind: bingo.ObjectPropertyAccessor, ReadTypeKey: readHash, WriteTypeKey: writeHash, Visibility: "public"}}}
	_, semanticHash, err := bingo.CanonicalObjectSemanticContract(semantic)
	if err != nil {
		return bad("VERT-011 semantic contract: %w", err)
	}
	semantic.ContentHash = semanticHash
	places := bingo.PlaceRefContract{SchemaVersion: bingo.PlaceRefSchemaVersion, ObjectContracts: []bingo.ObjectSemanticContract{semantic}, EvaluationOrder: []bingo.ValueID{3, 4}, Places: []bingo.PropertyPlaceRef{{ID: 1, Receiver: 3, Key: 4, AccessSyntax: bingo.PlaceAccessComputed, AccessPlan: bingo.PlaceAccessAccessor, ObjectTypeKey: objectRecord.CanonicalHash, PropertyKey: "result", PropertySymbolKey: string(result.Symbol), ReadTypeKey: readHash, WriteTypeKey: writeHash, ReadType: readType, WriteType: writeType, Mutability: bingo.PlaceMutable, Required: true, GetterSymbolKey: string(getter.ID), SetterSymbolKey: string(setter.ID), BackingPropertyKey: "backing", BackingPropertySymbolKey: string(backing.Symbol), LoadEffects: []bingo.Effect{bingo.EffectCall, bingo.EffectRead, bingo.EffectThrow}, StoreEffects: []bingo.Effect{bingo.EffectCall, bingo.EffectThrow, bingo.EffectWrite}, Origin: originOf(access)}}}
	_, placeHash, err := bingo.CanonicalPlaceRefContract(places)
	if err != nil {
		return bad("VERT-011 place contract: %w", err)
	}
	places.ContentHash = placeHash
	empty := []bingo.RuntimeCapabilityID{}
	operations := []bingo.HIROp{{ID: 2, Kind: "object.alloc", Type: bingo.TypeObject, Effect: bingo.EffectAllocate, Effects: []bingo.Effect{bingo.EffectAllocate}, ObjectTypeKey: objectRecord.CanonicalHash, LogicalCapabilityRequirements: []bingo.RuntimeCapabilityID{"rt.gc.alloc"}, Origin: originOf(objectLiteral)}, {ID: 3, Kind: "object.field.init", Type: bingo.TypeObject, Operands: []bingo.ValueID{2, 1}, Effect: bingo.EffectWrite, Effects: []bingo.Effect{bingo.EffectWrite}, ObjectTypeKey: objectRecord.CanonicalHash, PropertySymbolKey: string(backing.Symbol), LogicalCapabilityRequirements: empty, Origin: originOf(property)}, {ID: 4, Kind: "evaluate.key", Type: bingo.TypeString, Effect: bingo.EffectPure, Effects: []bingo.Effect{bingo.EffectPure}, LogicalCapabilityRequirements: empty, Origin: originOf(assertion)}, {ID: 5, Kind: "place.make", Type: bingo.TypeVoid, Operands: []bingo.ValueID{3, 4}, PlaceID: 1, Effect: bingo.EffectPure, Effects: []bingo.Effect{bingo.EffectPure}, LogicalCapabilityRequirements: empty, Origin: originOf(access)}, {ID: 6, Kind: "place.load", Type: readType, PlaceID: 1, Effect: bingo.EffectCall, Effects: places.Places[0].LoadEffects, LogicalCapabilityRequirements: empty, Origin: originOf(access)}, {ID: 7, Kind: "is_nullish", Type: bingo.TypeBoolean, Operands: []bingo.ValueID{6}, Effect: bingo.EffectPure, Effects: []bingo.Effect{bingo.EffectPure}, LogicalCapabilityRequirements: empty, Origin: originOf(assignment)}}
	blocks := []bingo.HIRBlock{{ID: 1, Operations: operations, Terminator: bingo.HIRTerminator{Kind: "condbranch", Value: 7, Successors: []bingo.BlockID{2, 3}, Origin: originOf(assignment)}}, {ID: 2, Operations: []bingo.HIROp{{ID: 8, Kind: "number.constant", Type: bingo.TypeNumber, NumberBits: "3ff0000000000000", Effect: bingo.EffectPure, Effects: []bingo.Effect{bingo.EffectPure}, LogicalCapabilityRequirements: empty, Origin: originOf(rhs)}, {ID: 9, Kind: "place.store", Type: bingo.TypeNumber, Operands: []bingo.ValueID{8}, PlaceID: 1, Effect: bingo.EffectCall, Effects: places.Places[0].StoreEffects, LogicalCapabilityRequirements: empty, Origin: originOf(assignment)}}, Terminator: bingo.HIRTerminator{Kind: "branch", Successors: []bingo.BlockID{4}, Origin: originOf(assignment)}}, {ID: 3, Operations: []bingo.HIROp{{ID: 10, Kind: "unwrap_nullable", Type: bingo.TypeNumber, Operands: []bingo.ValueID{6}, Effect: bingo.EffectPure, Effects: []bingo.Effect{bingo.EffectPure}, LogicalCapabilityRequirements: empty, Origin: originOf(assignment)}}, Terminator: bingo.HIRTerminator{Kind: "branch", Successors: []bingo.BlockID{4}, Origin: originOf(assignment)}}, {ID: 4, Operations: []bingo.HIROp{{ID: 11, Kind: "phi", Type: bingo.TypeNumber, Operands: []bingo.ValueID{9, 10}, IncomingBlocks: []bingo.BlockID{2, 3}, Effect: bingo.EffectPure, Effects: []bingo.Effect{bingo.EffectPure}, LogicalCapabilityRequirements: empty, Origin: originOf(returnNode)}}, Terminator: bingo.HIRTerminator{Kind: "return", Value: 11, Origin: originOf(returnNode)}}}
	hir := bingo.HIRFunction{ID: 1, Name: "propertyNullishAssign", Exported: true, Parameters: []bingo.HIRParameter{{Name: "value", Value: 1, Type: parameterType, Origin: originOf(parameter)}}, Blocks: blocks, ReturnType: returnType, Origin: originOf(function)}
	events := []LoweringEvent{{Kind: "function.begin", Node: function.ID, Origin: function.Origin}, {Kind: "parameter", Node: parameter.ID, Origin: parameter.Origin, Type: nodeTypeID(parameter)}, {Kind: "object.alloc", Node: objectLiteral.ID, Origin: objectLiteral.Origin, Type: nodeTypeID(objectLiteral)}, {Kind: "object.field.init", Node: property.ID, Origin: property.Origin, Type: backing.ReadType}, {Kind: "evaluate.key", Node: keyDecl.ID, Origin: keyDecl.Origin, Type: nodeTypeID(assertion)}, {Kind: "place.load", Node: access.ID, Origin: access.Origin, Type: backing.ReadType}, {Kind: "place.store", Node: assignment.ID, Origin: assignment.Origin, Type: result.WriteType}, {Kind: "return", Node: returnNode.ID, Origin: returnNode.Origin, Type: returnTypeID}, {Kind: "function.end", Node: function.ID, Origin: function.Origin}}
	return hir, places, events, nil
}

func sameNodeIDSet(values []NodeID, want ...NodeID) bool {
	if len(values) != len(want) {
		return false
	}
	seen := make(map[NodeID]struct{}, len(values))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	if len(seen) != len(want) {
		return false
	}
	for _, value := range want {
		if _, ok := seen[value]; !ok {
			return false
		}
	}
	return true
}

func vert011Variable(statement NodeID, nodes map[NodeID]NodeSnapshot, name string) (NodeSnapshot, NodeSnapshot, error) {
	declaration, initializer, err := vert010Variable(statement, nodes)
	if err != nil || childText(declaration, "name", nodes) != name {
		return NodeSnapshot{}, NodeSnapshot{}, fmt.Errorf("invalid %s declaration", name)
	}
	return declaration, initializer, nil
}
func vert011ObjectMembers(object NodeSnapshot, nodes map[NodeID]NodeSnapshot) (NodeSnapshot, NodeSnapshot, NodeSnapshot, error) {
	property, e := requireRoleKind(object, "child[0]", snapshotKindPropertyAssignment, nodes)
	if e != nil {
		return NodeSnapshot{}, NodeSnapshot{}, NodeSnapshot{}, e
	}
	getter, e := requireRoleKind(object, "child[1]", snapshotKindGetAccessor, nodes)
	if e != nil {
		return NodeSnapshot{}, NodeSnapshot{}, NodeSnapshot{}, e
	}
	setter, e := requireRoleKind(object, "child[2]", snapshotKindSetAccessor, nodes)
	if e != nil {
		return NodeSnapshot{}, NodeSnapshot{}, NodeSnapshot{}, e
	}
	if validateVERT011PropertyAssignmentLowerer(property, nodes) != nil || validateVERT011GetAccessorLowerer(getter, nodes) != nil || validateVERT011SetAccessorLowerer(setter, nodes) != nil {
		return NodeSnapshot{}, NodeSnapshot{}, NodeSnapshot{}, fmt.Errorf("invalid object members")
	}
	return property, getter, setter, nil
}
func vert011GetterBody(getter NodeSnapshot, nodes map[NodeID]NodeSnapshot) bool {
	body, e := requireRoleKind(getter, "body", snapshotKindBlock, nodes)
	if e != nil {
		return false
	}
	statements := namedChildren(body, "statement[")
	if len(statements) != 1 {
		return false
	}
	ret := nodes[statements[0]]
	access, e := requireRoleKind(ret, "expression", snapshotKindPropertyAccessExpression, nodes)
	return ret.Kind == snapshotKindReturnStatement && e == nil && vert011BackingAccess(access, nodes)
}
func vert011SetterBody(setter NodeSnapshot, nodes map[NodeID]NodeSnapshot) bool {
	body, e := requireRoleKind(setter, "body", snapshotKindBlock, nodes)
	if e != nil {
		return false
	}
	statements := namedChildren(body, "statement[")
	if len(statements) != 1 {
		return false
	}
	statement := nodes[statements[0]]
	assignment, e := requireRoleKind(statement, "expression", snapshotKindBinaryExpression, nodes)
	if statement.Kind != snapshotKindExpressionStatement || e != nil || assignment.SyntaxPayload.Operator != snapshotKindEqualsToken {
		return false
	}
	left, e := requireRoleKind(assignment, "left", snapshotKindPropertyAccessExpression, nodes)
	return e == nil && vert011BackingAccess(left, nodes) && childText(assignment, "right", nodes) == "next"
}
