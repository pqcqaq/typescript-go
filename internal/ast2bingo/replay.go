package ast2bingo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
)

type (
	ModuleID          = frontendwire.ModuleID
	ModuleSnapshot    = frontendwire.ModuleSnapshot
	NodeID            = frontendwire.NodeID
	NodeSnapshot      = frontendwire.NodeSnapshot
	OriginID          = frontendwire.OriginID
	ProgramSnapshot   = frontendwire.ProgramSnapshot
	SignatureID       = frontendwire.SignatureID
	SignatureSnapshot = frontendwire.SignatureSnapshot
	SymbolID          = frontendwire.SymbolID
	SymbolSnapshot    = frontendwire.SymbolSnapshot
	TypeID            = frontendwire.TypeID
	TypeSnapshot      = frontendwire.TypeSnapshot
)

// SnapshotReplaySchemaVersion is the persisted checker-free replay contract.
const SnapshotReplaySchemaVersion uint32 = 3

// LoweringEvent is a checker-free, canonical trace consumed by the first HIR
// vertical slice. Node and Origin make every event traceable to source.
type LoweringEvent struct {
	Kind     string           `json:"kind"`
	Node     NodeID           `json:"node"`
	Origin   OriginID         `json:"origin"`
	Type     TypeID           `json:"type,omitempty"`
	Operator string           `json:"operator,omitempty"`
	Callee   bingo.FunctionID `json:"callee,omitempty"`
	Inputs   []NodeID         `json:"inputs,omitempty"`
}

// SnapshotReplayResult is the complete output of snapshot-only lowering.
type SnapshotReplayResult struct {
	SchemaVersion         uint32                      `json:"schemaVersion"`
	FrontendSnapshotHash  string                      `json:"frontendSnapshotHash"`
	CompilerBuildIdentity bingo.CompilerBuildIdentity `json:"compilerBuildIdentity"`
	Events                []LoweringEvent             `json:"events"`
	HIR                   bingo.HIRModule             `json:"hir"`
	ContentHash           string                      `json:"contentHash"`
}

// CanonicalBytes verifies the replay identity before returning the stable
// wire representation emitted by the standalone replay command.
func (r SnapshotReplayResult) CanonicalBytes() ([]byte, error) {
	if r.SchemaVersion != SnapshotReplaySchemaVersion {
		return nil, fmt.Errorf("unsupported replay schema %d", r.SchemaVersion)
	}
	if r.FrontendSnapshotHash == "" || r.FrontendSnapshotHash != r.HIR.Provenance.FrontendSnapshotHash {
		return nil, fmt.Errorf("replay frontend snapshot hash does not match HIR provenance")
	}
	if err := bingo.ValidateCompilerBuildIdentity(r.CompilerBuildIdentity); err != nil {
		return nil, fmt.Errorf("invalid replay compiler build identity: %w", err)
	}
	if r.CompilerBuildIdentity != r.HIR.Provenance.CompilerBuildIdentity {
		return nil, fmt.Errorf("replay compiler build identity does not match HIR provenance")
	}
	if err := verifyCanonicalPrimitiveHIR(r.HIR); err != nil {
		return nil, fmt.Errorf("verify replay HIR: %w", err)
	}
	withoutHash := r
	withoutHash.ContentHash = ""
	encoded, err := json.Marshal(withoutHash)
	if err != nil {
		return nil, err
	}
	if want := hashBytes(encoded); r.ContentHash != want {
		return nil, fmt.Errorf("replay content hash mismatch: got %s, want %s", r.ContentHash, want)
	}
	return json.Marshal(r)
}

// snapshotLowererReadinessHandler validates the syntax contract needed by a
// lowerer. The handler is deliberately checker-free: it can only inspect the
// immutable DTO and the snapshot's node table.
type snapshotLowererReadinessHandler func(NodeSnapshot, map[NodeID]NodeSnapshot) error

type snapshotLowererReadinessDefinition struct {
	Kind                  string
	PayloadTag            string
	SnapshotSchemaVersion uint32
	RequiredFacts         []string
	Handle                snapshotLowererReadinessHandler
}

type snapshotSemanticFactIndexes struct {
	Nodes      map[NodeID]NodeSnapshot
	Types      map[TypeID]TypeSnapshot
	Symbols    map[SymbolID]SymbolSnapshot
	Signatures map[SignatureID]SignatureSnapshot
	Modules    map[ModuleID]ModuleSnapshot
}

type snapshotSemanticFactValidator func(NodeSnapshot, snapshotSemanticFactIndexes) error

const (
	snapshotFactFunctionContract = "function-contract"
	snapshotFactModule           = "module"
	snapshotFactSymbol           = "symbol"
	snapshotFactType             = "type"
)

var snapshotSemanticFactRegistry = map[string]snapshotSemanticFactValidator{
	snapshotFactFunctionContract: validateFunctionContractFact,
	snapshotFactModule:           validateModuleFact,
	snapshotFactSymbol:           validateSymbolFact,
	snapshotFactType:             validateTypeFact,
}

const (
	snapshotKindBinaryExpression    = "KindBinaryExpression"
	snapshotKindBlock               = "KindBlock"
	snapshotKindBooleanKeyword      = "KindBooleanKeyword"
	snapshotKindCallExpression      = "KindCallExpression"
	snapshotKindEndOfFile           = "KindEndOfFile"
	snapshotKindEqualsToken         = "KindEqualsToken"
	snapshotKindExportKeyword       = "KindExportKeyword"
	snapshotKindExpressionStatement = "KindExpressionStatement"
	snapshotKindFunctionDeclaration = "KindFunctionDeclaration"
	snapshotKindIdentifier          = "KindIdentifier"
	snapshotKindIfStatement         = "KindIfStatement"
	snapshotKindNumberKeyword       = "KindNumberKeyword"
	snapshotKindParameter           = "KindParameter"
	snapshotKindPlusToken           = "KindPlusToken"
	snapshotKindReturnStatement     = "KindReturnStatement"
	snapshotKindSourceFile          = "KindSourceFile"
	snapshotKindVariableDeclaration = "KindVariableDeclaration"
	snapshotKindVariableList        = "KindVariableDeclarationList"
	snapshotKindVariableStatement   = "KindVariableStatement"
)

const (
	snapshotNodeFlagUsing     uint32 = 1 << 2
	snapshotKnownNodeFlagMask uint32 = 1<<29 - 1
	snapshotModifierExport    uint32 = 1 << 5
)

// snapshotLowererReadinessRegistry is the executable support boundary for the
// first replay slice. Keep this list sorted by Kind; validation below makes an
// accidental duplicate or nil handler fail closed at package use sites.
var snapshotLowererReadinessRegistry = []snapshotLowererReadinessDefinition{
	{Kind: snapshotKindBinaryExpression, PayloadTag: snapshotKindBinaryExpression, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule, snapshotFactType}, Handle: validateBinaryExpressionLowerer},
	{Kind: snapshotKindBlock, PayloadTag: snapshotKindBlock, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateContainerLowerer},
	{Kind: snapshotKindBooleanKeyword, PayloadTag: snapshotKindBooleanKeyword, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateContainerLowerer},
	{Kind: snapshotKindCallExpression, PayloadTag: snapshotKindCallExpression, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule, snapshotFactType}, Handle: validateCallExpressionLowerer},
	{Kind: snapshotKindEndOfFile, PayloadTag: snapshotKindEndOfFile, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateContainerLowerer},
	{Kind: snapshotKindEqualsToken, PayloadTag: snapshotKindEqualsToken, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateContainerLowerer},
	{Kind: snapshotKindExportKeyword, PayloadTag: snapshotKindExportKeyword, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateContainerLowerer},
	{Kind: snapshotKindExpressionStatement, PayloadTag: snapshotKindExpressionStatement, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateExpressionStatementLowerer},
	{Kind: snapshotKindFunctionDeclaration, PayloadTag: snapshotKindFunctionDeclaration, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactFunctionContract, snapshotFactModule, snapshotFactSymbol}, Handle: validateFunctionLowerer},
	{Kind: snapshotKindIdentifier, PayloadTag: snapshotKindIdentifier, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule, snapshotFactSymbol, snapshotFactType}, Handle: validateIdentifierLowerer},
	{Kind: snapshotKindIfStatement, PayloadTag: snapshotKindIfStatement, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateIfLowerer},
	{Kind: snapshotKindNumberKeyword, PayloadTag: snapshotKindNumberKeyword, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateContainerLowerer},
	{Kind: snapshotKindParameter, PayloadTag: snapshotKindParameter, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule, snapshotFactSymbol, snapshotFactType}, Handle: validateParameterLowerer},
	{Kind: snapshotKindPlusToken, PayloadTag: snapshotKindPlusToken, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateContainerLowerer},
	{Kind: snapshotKindReturnStatement, PayloadTag: snapshotKindReturnStatement, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateReturnLowerer},
	{Kind: snapshotKindSourceFile, PayloadTag: snapshotKindSourceFile, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateContainerLowerer},
	{Kind: snapshotKindVariableDeclaration, PayloadTag: snapshotKindVariableDeclaration, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule, snapshotFactSymbol, snapshotFactType}, Handle: validateVariableDeclarationLowerer},
	{Kind: snapshotKindVariableList, PayloadTag: snapshotKindVariableList, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateVariableListLowerer},
	{Kind: snapshotKindVariableStatement, PayloadTag: snapshotKindVariableStatement, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateVariableStatementLowerer},
}

func validateSnapshotLowererReadinessRegistry(registry []snapshotLowererReadinessDefinition) error {
	if len(registry) == 0 {
		return fmt.Errorf("registry is empty")
	}
	for index, definition := range registry {
		if strings.TrimSpace(definition.Kind) == "" {
			return fmt.Errorf("lowerer %d has an empty Kind", index)
		}
		if index > 0 && registry[index-1].Kind >= definition.Kind {
			return fmt.Errorf("lowerer Kinds are not sorted and unique at %q", definition.Kind)
		}
		if definition.PayloadTag == "" {
			return fmt.Errorf("lowerer for Kind %q has no payload tag", definition.Kind)
		}
		if definition.SnapshotSchemaVersion != frontendwire.SnapshotSchemaVersion {
			return fmt.Errorf("lowerer for Kind %q declares snapshot schema %d, want %d", definition.Kind, definition.SnapshotSchemaVersion, frontendwire.SnapshotSchemaVersion)
		}
		if definition.Handle == nil {
			return fmt.Errorf("lowerer for Kind %q is unbound", definition.Kind)
		}
		for factIndex, fact := range definition.RequiredFacts {
			if _, ok := snapshotSemanticFactRegistry[fact]; !ok {
				return fmt.Errorf("lowerer for Kind %q requires unbound semantic fact %q", definition.Kind, fact)
			}
			if factIndex != 0 && definition.RequiredFacts[factIndex-1] >= fact {
				return fmt.Errorf("lowerer for Kind %q semantic facts are not sorted and unique at %q", definition.Kind, fact)
			}
		}
	}
	return nil
}

func lookupSnapshotLowererReadiness(kind string) (snapshotLowererReadinessDefinition, bool) {
	index, ok := slices.BinarySearchFunc(snapshotLowererReadinessRegistry, kind, func(definition snapshotLowererReadinessDefinition, kind string) int {
		return strings.Compare(definition.Kind, kind)
	})
	if !ok {
		return snapshotLowererReadinessDefinition{}, false
	}
	return snapshotLowererReadinessRegistry[index], true
}

func preflightSnapshotLowerers(snapshot ProgramSnapshot, indexes snapshotSemanticFactIndexes) error {
	if err := validateSnapshotLowererReadinessRegistry(snapshotLowererReadinessRegistry); err != nil {
		return fmt.Errorf("snapshot lowerer readiness registry: %w", err)
	}
	for _, node := range snapshot.Nodes {
		definition, ok := lookupSnapshotLowererReadiness(node.Kind)
		if !ok {
			return fmt.Errorf("lowerer for Kind %q is not bound", node.Kind)
		}
		if definition.SnapshotSchemaVersion != snapshot.SchemaVersion {
			return fmt.Errorf("lowerer for Kind %q requires snapshot schema %d, got %d", node.Kind, definition.SnapshotSchemaVersion, snapshot.SchemaVersion)
		}
		if node.SyntaxPayload.Tag != definition.PayloadTag {
			return fmt.Errorf("lowerer for Kind %q requires payload tag %q, got %q", node.Kind, definition.PayloadTag, node.SyntaxPayload.Tag)
		}
		for _, fact := range definition.RequiredFacts {
			if err := snapshotSemanticFactRegistry[fact](node, indexes); err != nil {
				return fmt.Errorf("lowerer readiness for node %q (%s) required fact %q: %w", node.ID, node.Kind, fact, err)
			}
		}
		if err := definition.Handle(node, indexes.Nodes); err != nil {
			return fmt.Errorf("lowerer readiness for node %q (%s): %w", node.ID, node.Kind, err)
		}
	}
	return nil
}

// validatePrimitiveSubsetContract re-establishes the checker-free subset
// decision at the serialized lowering boundary. Snapshot hashes prove content
// identity, not that NodeFlags, modifiers, or ignored type alternatives were
// accepted by the source gate, so the first slice validates every such fact
// before it can mint a source plan or HIR artifact.
func validatePrimitiveSubsetContract(snapshot ProgramSnapshot, indexes snapshotSemanticFactIndexes) error {
	for _, node := range snapshot.Nodes {
		if node.NodeFlags&snapshotNodeFlagUsing != 0 || node.NodeFlags & ^snapshotKnownNodeFlagMask != 0 {
			return fmt.Errorf("primitive subset contract rejects node %q (%s) flags %#x", node.ID, node.Kind, node.NodeFlags)
		}
		wantModifiers := uint32(0)
		if node.Kind == snapshotKindFunctionDeclaration && node.ModifierBits == snapshotModifierExport {
			wantModifiers = snapshotModifierExport
		}
		if node.ModifierBits != wantModifiers {
			return fmt.Errorf(
				"primitive subset contract rejects node %q (%s) modifiers %#x, want %#x",
				node.ID,
				node.Kind,
				node.ModifierBits,
				wantModifiers,
			)
		}

		seenRoots := make(map[TypeID]struct{}, 3)
		for _, root := range []TypeID{node.NarrowedType, node.DeclaredType, node.ContextualType} {
			if root == 0 {
				continue
			}
			if _, duplicate := seenRoots[root]; duplicate {
				continue
			}
			seenRoots[root] = struct{}{}
			if err := validatePrimitiveTypeClosure(root, indexes, make(map[TypeID]struct{})); err != nil {
				return fmt.Errorf("primitive subset contract rejects node %q (%s) type %d: %w", node.ID, node.Kind, root, err)
			}
		}
	}
	return nil
}

func validatePrimitiveTypeClosure(id TypeID, indexes snapshotSemanticFactIndexes, visited map[TypeID]struct{}) error {
	if id == 0 {
		return nil
	}
	if _, seen := visited[id]; seen {
		return nil
	}
	visited[id] = struct{}{}
	record, ok := indexes.Types[id]
	if !ok {
		return fmt.Errorf("missing type")
	}
	kind := strings.ToLower(strings.TrimSpace(record.Kind))
	switch kind {
	case "any", "dynamic", "intrinsic:any":
		return fmt.Errorf("type closure contains any")
	case "unknown", "intrinsic:unknown":
		return fmt.Errorf("type closure contains unknown")
	}
	if reason := strings.TrimSpace(record.NotLowerableReason); reason != "" {
		return fmt.Errorf("type is not lowerable: %s", reason)
	}

	children := make([]TypeID, 0, len(record.ElementTypes)+len(record.TypeArguments)+len(record.BaseTypes)+len(record.IndexInfos)*2)
	children = append(children, record.ElementTypes...)
	children = append(children, record.TypeArguments...)
	children = append(children, record.BaseTypes...)
	for _, index := range record.IndexInfos {
		children = append(children, index.KeyType, index.ValueType)
	}
	for _, propertyID := range record.Properties {
		property, ok := indexes.Symbols[propertyID]
		if ok && len(property.Declarations) != 0 {
			children = append(children, property.Type)
		}
	}
	for _, signatureID := range slices.Concat(record.CallSignatures, record.ConstructSignatures) {
		signature, ok := indexes.Signatures[signatureID]
		if !ok || signature.Declaration == "" {
			continue
		}
		children = append(children, signature.ReturnType, signature.Predicate.Type)
		children = append(children, signature.InstantiatedTypeArguments...)
		for _, parameterID := range signature.Parameters {
			if parameter, ok := indexes.Symbols[parameterID]; ok {
				children = append(children, parameter.Type)
			}
		}
	}
	for _, child := range children {
		if err := validatePrimitiveTypeClosure(child, indexes, visited); err != nil {
			return err
		}
	}
	return nil
}

func validateModuleFact(node NodeSnapshot, indexes snapshotSemanticFactIndexes) error {
	if node.Module == "" {
		return fmt.Errorf("node has no owning module")
	}
	if _, ok := indexes.Modules[node.Module]; !ok {
		return fmt.Errorf("node references missing owning module %q", node.Module)
	}
	return nil
}

func validateTypeFact(node NodeSnapshot, indexes snapshotSemanticFactIndexes) error {
	typeID := nodeTypeID(node)
	if typeID == 0 {
		return fmt.Errorf("node has no resolved type")
	}
	if _, ok := indexes.Types[typeID]; !ok {
		return fmt.Errorf("node references missing resolved type %d", typeID)
	}
	return nil
}

func validateSymbolFact(node NodeSnapshot, indexes snapshotSemanticFactIndexes) error {
	ids := nodeAndNamedChildSymbolIDs(node, "name", indexes.Nodes)
	for _, id := range ids {
		if _, ok := indexes.Symbols[id]; ok {
			return nil
		}
	}
	return fmt.Errorf("node has no resolved symbol")
}

func validateFunctionContractFact(node NodeSnapshot, indexes snapshotSemanticFactIndexes) error {
	if !node.CaptureComplete {
		return fmt.Errorf("function has no complete capture proof")
	}
	if len(node.CaptureSet) != len(node.CaptureBindings) {
		return fmt.Errorf("primitive function capture proof is incomplete")
	}
	for index, binding := range node.CaptureBindings {
		if binding.Symbol != node.CaptureSet[index] || binding.Kind != "binding" || binding.Access != "read" {
			return fmt.Errorf("primitive function capture %d is not a direct-call binding", index)
		}
		symbol, ok := indexes.Symbols[binding.Symbol]
		if !ok || symbol.ValueDeclaration == "" {
			return fmt.Errorf("primitive function capture %d has no resolved declaration", index)
		}
		declaration, ok := indexes.Nodes[symbol.ValueDeclaration]
		if !ok || declaration.Kind != snapshotKindFunctionDeclaration || declaration.Module != node.Module {
			return fmt.Errorf("primitive function capture %d is not a same-module function", index)
		}
	}

	candidates := make(map[SignatureID]SignatureSnapshot)
	for _, signature := range indexes.Signatures {
		if signature.Declaration == node.ID && signature.EffectProof.Implementation == node.ID {
			candidates[signature.ID] = signature
		}
	}
	if len(candidates) != 1 {
		return fmt.Errorf("primitive function requires exactly one implementation signature, got %d", len(candidates))
	}
	var signature SignatureSnapshot
	for _, candidate := range candidates {
		signature = candidate
	}
	if signature.EffectProof.Kind != "body-resolved" || !signature.EffectProof.Complete || !slices.Equal(signature.Effects, []string{"pure"}) {
		return fmt.Errorf("primitive function signature %d has incomplete or impure effect proof %v", signature.ID, signature.Effects)
	}

	parameters := namedChildren(node, "parameter[")
	if len(parameters) != len(signature.ParameterFacts) {
		return fmt.Errorf("function parameter count %d does not match signature count %d", len(parameters), len(signature.ParameterFacts))
	}
	if signature.HasRest || signature.MinArgumentCount != len(signature.ParameterFacts) {
		return fmt.Errorf("primitive function does not support optional or rest parameters")
	}
	for _, parameterID := range parameters {
		parameter, ok := indexes.Nodes[parameterID]
		if !ok {
			return fmt.Errorf("function references missing parameter %q", parameterID)
		}
		if childByRole(parameter, "initializer") != "" || childByRole(parameter, "dotDotDotToken") != "" || childByRole(parameter, "questionToken") != "" {
			return fmt.Errorf("primitive function does not support parameter defaults, optional markers, or rest parameters")
		}
	}
	return nil
}

func validateContainerLowerer(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) error {
	switch node.Kind {
	case snapshotKindBlock:
		statements := namedChildren(node, "statement[")
		if len(statements) == 0 || len(statements) > 3 || len(node.NamedChildren) != len(statements) || len(node.Children) != len(statements) {
			return fmt.Errorf("primitive block requires one to three statements")
		}
		for _, statement := range statements {
			child, ok := nodes[statement]
			if !ok || child.Parent != node.ID || (child.Kind != snapshotKindReturnStatement && child.Kind != snapshotKindIfStatement && child.Kind != snapshotKindVariableStatement && child.Kind != snapshotKindExpressionStatement) {
				return fmt.Errorf("primitive block statement %q is not supported", statement)
			}
		}
	case snapshotKindSourceFile:
		statements := namedChildren(node, "statement[")
		eof := namedChildren(node, "child[")
		if (len(statements) != 1 && len(statements) != 2) || len(eof) != 1 || len(node.NamedChildren) != len(statements)+1 || len(node.Children) != len(statements)+1 {
			return fmt.Errorf("primitive source file requires one or two functions and one EOF token")
		}
		for _, statement := range statements {
			if _, err := requireChildKind(node, statement, snapshotKindFunctionDeclaration, nodes); err != nil {
				return err
			}
		}
		if _, err := requireChildKind(node, eof[0], snapshotKindEndOfFile, nodes); err != nil {
			return err
		}
	case snapshotKindBooleanKeyword, snapshotKindEndOfFile, snapshotKindEqualsToken, snapshotKindExportKeyword, snapshotKindNumberKeyword, snapshotKindPlusToken:
		if len(node.NamedChildren) != 0 || len(node.Children) != 0 {
			return fmt.Errorf("primitive token Kind %q cannot have children", node.Kind)
		}
	default:
		return fmt.Errorf("Kind %q is not a primitive container", node.Kind)
	}
	return nil
}

func validateFunctionLowerer(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) error {
	if len(node.NamedChildren) < 5 || len(node.NamedChildren) > 7 || len(node.Children) != len(node.NamedChildren) {
		return fmt.Errorf("primitive function requires an optional export, name, two or three parameters, number return type, and body")
	}
	if _, err := requireRoleKind(node, "name", snapshotKindIdentifier, nodes); err != nil {
		return err
	}
	if _, err := requireRoleKind(node, "returnType", snapshotKindNumberKeyword, nodes); err != nil {
		return err
	}
	if _, err := requireRoleKind(node, "body", snapshotKindBlock, nodes); err != nil {
		return err
	}
	parameters := namedChildren(node, "parameter[")
	if len(parameters) < 2 || len(parameters) > 3 {
		return fmt.Errorf("primitive function requires two or three parameters")
	}
	for _, parameter := range parameters {
		if _, err := requireChildKind(node, parameter, snapshotKindParameter, nodes); err != nil {
			return err
		}
	}
	modifiers := namedChildren(node, "modifier[")
	if len(modifiers) > 1 {
		return fmt.Errorf("primitive function permits at most one export modifier")
	}
	if len(modifiers) == 1 {
		if _, err := requireChildKind(node, modifiers[0], snapshotKindExportKeyword, nodes); err != nil {
			return err
		}
	}
	return nil
}

func validateIdentifierLowerer(node NodeSnapshot, _ map[NodeID]NodeSnapshot) error {
	if strings.TrimSpace(node.SyntaxPayload.Text) == "" {
		return fmt.Errorf("identifier has no source text")
	}
	if len(node.NamedChildren) != 0 || len(node.Children) != 0 {
		return fmt.Errorf("identifier cannot have children")
	}
	if len(nodeSymbolIDs(node)) == 0 {
		return fmt.Errorf("identifier has no symbol or resolved symbol")
	}
	return nil
}

func validateParameterLowerer(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) error {
	if len(node.NamedChildren) != 2 || len(node.Children) != 2 {
		return fmt.Errorf("primitive parameter requires exactly an identifier and number annotation")
	}
	if _, err := requireRoleKind(node, "name", snapshotKindIdentifier, nodes); err != nil {
		return err
	}
	typeNodeID := childByRole(node, "type")
	typeNode, ok := nodes[typeNodeID]
	if !ok || typeNode.Parent != node.ID || (typeNode.Kind != snapshotKindNumberKeyword && typeNode.Kind != snapshotKindBooleanKeyword) {
		return fmt.Errorf("primitive parameter type must be number or boolean")
	}
	if node.DeclaredType == 0 && node.NarrowedType == 0 && node.ContextualType == 0 {
		return fmt.Errorf("parameter has no type reference")
	}
	return nil
}

func validateReturnLowerer(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) error {
	if len(node.NamedChildren) != 1 || len(node.Children) != 1 {
		return fmt.Errorf("primitive return requires exactly one expression")
	}
	expressionID := childByRole(node, "expression")
	expression, ok := nodes[expressionID]
	if !ok || expression.Parent != node.ID || (expression.Kind != snapshotKindBinaryExpression && expression.Kind != snapshotKindIdentifier) {
		return fmt.Errorf("primitive return expression must be an identifier or binary expression")
	}
	return nil
}

func validateIfLowerer(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) error {
	if len(node.NamedChildren) != 2 || len(node.Children) != 2 {
		return fmt.Errorf("primitive if requires condition and then block")
	}
	if _, err := requireRoleKind(node, "child[0]", snapshotKindIdentifier, nodes); err != nil {
		return err
	}
	if _, err := requireRoleKind(node, "child[1]", snapshotKindBlock, nodes); err != nil {
		return err
	}
	return nil
}

func validateBinaryExpressionLowerer(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) error {
	if len(node.NamedChildren) != 3 || len(node.Children) != 3 {
		return fmt.Errorf("primitive binary expression requires exactly left, operator, and right children")
	}
	if _, err := requireRoleKind(node, "left", snapshotKindIdentifier, nodes); err != nil {
		return err
	}
	switch node.SyntaxPayload.Operator {
	case snapshotKindPlusToken:
		if _, err := requireRoleKind(node, "operator", snapshotKindPlusToken, nodes); err != nil {
			return err
		}
		if _, err := requireRoleKind(node, "right", snapshotKindIdentifier, nodes); err != nil {
			return err
		}
	case snapshotKindEqualsToken:
		if _, err := requireRoleKind(node, "operator", snapshotKindEqualsToken, nodes); err != nil {
			return err
		}
		if _, err := requireRoleKind(node, "right", snapshotKindBinaryExpression, nodes); err != nil {
			return err
		}
	default:
		return fmt.Errorf("operator %q is outside primitive replay", node.SyntaxPayload.Operator)
	}
	return nil
}

func validateCallExpressionLowerer(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) error {
	if node.SelectedSignature == 0 || node.SelectedOverloadOrdinal == 0 || len(node.NamedChildren) != 3 || len(node.Children) != 3 {
		return fmt.Errorf("primitive direct call requires one resolved callee and two arguments")
	}
	if _, err := requireRoleKind(node, "callee", snapshotKindIdentifier, nodes); err != nil {
		return err
	}
	for _, role := range []string{"argument[0]", "argument[1]"} {
		if _, err := requireRoleKind(node, role, snapshotKindIdentifier, nodes); err != nil {
			return err
		}
	}
	return nil
}

func validateExpressionStatementLowerer(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) error {
	if len(node.NamedChildren) != 1 || len(node.Children) != 1 {
		return fmt.Errorf("primitive expression statement requires one expression")
	}
	expression, err := requireRoleKind(node, "expression", snapshotKindBinaryExpression, nodes)
	if err != nil {
		return err
	}
	if expression.SyntaxPayload.Operator != snapshotKindEqualsToken {
		return fmt.Errorf("primitive expression statement requires assignment")
	}
	return nil
}

func validateVariableStatementLowerer(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) error {
	if len(node.NamedChildren) != 1 || len(node.Children) != 1 {
		return fmt.Errorf("primitive variable statement requires one declaration list")
	}
	_, err := requireRoleKind(node, "declarationList", snapshotKindVariableList, nodes)
	return err
}

func validateVariableListLowerer(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) error {
	if node.NodeFlags != 1 || len(node.NamedChildren) != 1 || len(node.Children) != 1 {
		return fmt.Errorf("primitive variable list requires exactly one let declaration")
	}
	_, err := requireRoleKind(node, "declaration[0]", snapshotKindVariableDeclaration, nodes)
	return err
}

func validateVariableDeclarationLowerer(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) error {
	if len(node.NamedChildren) != 2 || len(node.Children) != 2 {
		return fmt.Errorf("primitive variable declaration requires identifier and initializer")
	}
	if _, err := requireRoleKind(node, "name", snapshotKindIdentifier, nodes); err != nil {
		return err
	}
	_, err := requireRoleKind(node, "initializer", snapshotKindCallExpression, nodes)
	return err
}

func requireRoleKind(parent NodeSnapshot, role, kind string, nodes map[NodeID]NodeSnapshot) (NodeSnapshot, error) {
	child := childByRole(parent, role)
	if child == "" {
		return NodeSnapshot{}, fmt.Errorf("Kind %q requires child role %q", parent.Kind, role)
	}
	return requireChildKind(parent, child, kind, nodes)
}

func requireChildKind(parent NodeSnapshot, childID NodeID, kind string, nodes map[NodeID]NodeSnapshot) (NodeSnapshot, error) {
	child, ok := nodes[childID]
	if !ok {
		return NodeSnapshot{}, fmt.Errorf("Kind %q references missing child %q", parent.Kind, childID)
	}
	if child.Parent != parent.ID {
		return NodeSnapshot{}, fmt.Errorf("Kind %q child %q has parent %q", parent.Kind, childID, child.Parent)
	}
	if child.Kind != kind || child.SyntaxPayload.Tag != kind {
		return NodeSnapshot{}, fmt.Errorf("Kind %q child %q is %q, want %q", parent.Kind, childID, child.Kind, kind)
	}
	return child, nil
}

// ReplaySerializedSnapshot proves that the consumer side needs only the
// serialized DTO. It does not call any TypeScript or checker API.
func ReplaySerializedSnapshot(data []byte, identity bingo.CompilerBuildIdentity) (SnapshotReplayResult, error) {
	snapshot, err := frontendwire.DecodeProgramSnapshot(data)
	if err != nil {
		return SnapshotReplayResult{}, err
	}
	return ReplaySnapshot(*snapshot, identity)
}

// ReplayFrontendSnapshot consumes the target-independent wrapper emitted by
// the CLI and lowers only its nested serialized ProgramSnapshot.
func ReplayFrontendSnapshot(data []byte, identity bingo.CompilerBuildIdentity) (SnapshotReplayResult, error) {
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		return SnapshotReplayResult{}, err
	}
	return ReplaySnapshot(frontend.Program, identity)
}

// ReplaySnapshot lowers the supported primitive function shape (including
// add(number, number)) to canonical events and a verified HIR module.
func ReplaySnapshot(snapshot ProgramSnapshot, identity bingo.CompilerBuildIdentity) (SnapshotReplayResult, error) {
	artifact, _, err := executePrimitiveHIRPasses(context.Background(), snapshot, identity)
	if err != nil {
		return SnapshotReplayResult{}, err
	}
	result := SnapshotReplayResult{
		SchemaVersion:         SnapshotReplaySchemaVersion,
		FrontendSnapshotHash:  artifact.FrontendSnapshotHash,
		CompilerBuildIdentity: artifact.CompilerBuildIdentity,
		Events:                artifact.Events,
		HIR:                   artifact.HIR,
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return SnapshotReplayResult{}, err
	}
	result.ContentHash = hashBytes(encoded)
	return result, nil
}

func replayFunction(id int, functionNode NodeSnapshot, nodes map[NodeID]NodeSnapshot, types map[TypeID]TypeSnapshot, symbols map[SymbolID]SymbolSnapshot, signatures map[SignatureID]SignatureSnapshot, functionIDs map[NodeID]bingo.FunctionID) (bingo.HIRFunction, []LoweringEvent, error) {
	function := bingo.HIRFunction{ID: bingo.FunctionID(id), Name: "anonymous", ReturnType: bingo.TypeVoid, Origin: originOf(functionNode)}
	events := []LoweringEvent{{Kind: "function.begin", Node: functionNode.ID, Origin: functionNode.Origin}}
	parameters := namedChildren(functionNode, "parameter[")
	parameterValues := make(map[SymbolID]bingo.ValueID, len(parameters))
	parameterTypes := make(map[bingo.ValueID]bingo.TypeKind, len(parameters))
	for index, parameterID := range parameters {
		parameter, ok := nodes[parameterID]
		if !ok {
			return bingo.HIRFunction{}, nil, fmt.Errorf("function %s references missing parameter node %s", functionNode.ID, parameterID)
		}
		name := childText(parameter, "name", nodes)
		if name == "" {
			name = fmt.Sprintf("arg%d", index)
		}
		parameterTypeID := nodeTypeID(parameter)
		parameterType, err := bingoType(parameterTypeID, types)
		if err != nil {
			return bingo.HIRFunction{}, nil, fmt.Errorf("function %s parameter %s: %w", functionNode.ID, name, err)
		}
		value := bingo.ValueID(index + 1)
		function.Parameters = append(function.Parameters, bingo.HIRParameter{Name: name, Value: value, Type: parameterType, Origin: originOf(parameter)})
		parameterTypes[value] = parameterType
		for _, symbol := range parameterSymbolIDs(parameter, nodes) {
			parameterValues[symbol] = value
		}
		events = append(events, LoweringEvent{Kind: "parameter", Node: parameter.ID, Origin: parameter.Origin, Type: parameterTypeID})
	}
	if nameID := childByRole(functionNode, "name"); nameID != "" {
		if name := nodes[nameID].SyntaxPayload.Text; name != "" {
			function.Name = name
		}
	}
	bodyID := childByRole(functionNode, "body")
	if bodyID == "" {
		return bingo.HIRFunction{}, nil, fmt.Errorf("function %s has no body", functionNode.ID)
	}
	if localCall, ok := findPrimitiveLocalCall(bodyID, nodes); ok {
		return replayLocalCallFunction(function, events, localCall, parameterValues, parameterTypes, functionNode, nodes, types, symbols, signatures, functionIDs)
	}
	choose, isChoose, err := findPrimitiveChoose(bodyID, nodes)
	if err != nil {
		return bingo.HIRFunction{}, nil, fmt.Errorf("function %s: %w", functionNode.ID, err)
	}
	if isChoose {
		return replayChooseFunction(function, events, choose, parameterValues, parameterTypes, functionNode, nodes, types, symbols, signatures)
	}
	returnNode, binaryNode, err := findPrimitiveReturn(bodyID, nodes)
	if err != nil {
		return bingo.HIRFunction{}, nil, fmt.Errorf("function %s: %w", functionNode.ID, err)
	}
	block := bingo.HIRBlock{ID: 1}
	nextValue := bingo.ValueID(len(function.Parameters) + 1)
	if binaryNode.SyntaxPayload.Operator != snapshotKindPlusToken {
		return bingo.HIRFunction{}, nil, fmt.Errorf("function %s operator %q is outside primitive replay", functionNode.ID, binaryNode.SyntaxPayload.Operator)
	}
	inputs := namedChildren(binaryNode, "left")
	inputs = append(inputs, namedChildren(binaryNode, "right")...)
	if len(inputs) != 2 {
		return bingo.HIRFunction{}, nil, fmt.Errorf("binary node %s has %d operands", binaryNode.ID, len(inputs))
	}
	operands := make([]bingo.ValueID, 0, 2)
	binaryTypeID := nodeTypeID(binaryNode)
	binaryType, err := bingoType(binaryTypeID, types)
	if err != nil {
		return bingo.HIRFunction{}, nil, fmt.Errorf("binary node %s result type: %w", binaryNode.ID, err)
	}
	if binaryType != bingo.TypeNumber {
		return bingo.HIRFunction{}, nil, fmt.Errorf("binary node %s type %q is outside number-only replay", binaryNode.ID, binaryType)
	}
	for _, inputID := range inputs {
		input, ok := nodes[inputID]
		if !ok {
			return bingo.HIRFunction{}, nil, fmt.Errorf("binary node %s references missing operand %s", binaryNode.ID, inputID)
		}
		value, found := parameterValue(input, parameterValues)
		if !found {
			return bingo.HIRFunction{}, nil, fmt.Errorf("binary node %s input %s is not a parameter", binaryNode.ID, inputID)
		}
		operandTypeID := nodeTypeID(input)
		operandType, typeErr := bingoType(operandTypeID, types)
		if typeErr != nil {
			return bingo.HIRFunction{}, nil, fmt.Errorf("binary node %s input %s type: %w", binaryNode.ID, inputID, typeErr)
		}
		if operandType != binaryType {
			return bingo.HIRFunction{}, nil, fmt.Errorf("binary node %s input %s type %q disagrees with result type %q", binaryNode.ID, inputID, operandType, binaryType)
		}
		if parameterType, ok := parameterTypes[value]; !ok || parameterType != operandType {
			return bingo.HIRFunction{}, nil, fmt.Errorf("binary node %s input %s type %q disagrees with parameter value %d type %q", binaryNode.ID, inputID, operandType, value, parameterType)
		}
		operands = append(operands, value)
	}
	block.Operations = append(block.Operations, bingo.HIROp{
		ID:                            nextValue,
		Kind:                          "binary",
		Type:                          binaryType,
		Operands:                      operands,
		Operator:                      "+",
		Effect:                        bingo.EffectPure,
		LogicalCapabilityRequirements: make([]bingo.RuntimeCapabilityID, 0),
		Origin:                        originOf(binaryNode),
	})
	events = append(events, LoweringEvent{Kind: "binary.add", Node: binaryNode.ID, Origin: binaryNode.Origin, Type: binaryTypeID, Operator: "+", Inputs: inputs})
	returnType, ok := resolveFunctionReturnType(functionNode, nodes, symbols, types, signatures)
	if !ok {
		returnType = annotatedReturnType(functionNode, nodes)
	}
	if returnType == 0 {
		return bingo.HIRFunction{}, nil, fmt.Errorf("function %s has no resolved return type", functionNode.ID)
	}
	functionReturnType, err := bingoType(returnType, types)
	if err != nil {
		return bingo.HIRFunction{}, nil, fmt.Errorf("function %s return type: %w", functionNode.ID, err)
	}
	if len(block.Operations) == 0 || block.Operations[len(block.Operations)-1].Type != functionReturnType {
		return bingo.HIRFunction{}, nil, fmt.Errorf("function %s return type %q disagrees with binary result", functionNode.ID, functionReturnType)
	}
	block.Terminator = bingo.HIRTerminator{Kind: "return", Value: nextValue, Origin: originOf(returnNode)}
	function.ReturnType = functionReturnType
	function.Blocks = []bingo.HIRBlock{block}
	events = append(events, LoweringEvent{Kind: "return", Node: returnNode.ID, Origin: returnNode.Origin, Type: returnType})
	events = append(events, LoweringEvent{Kind: "function.end", Node: functionNode.ID, Origin: functionNode.Origin})
	return function, events, nil
}

type primitiveLocalCallSource struct {
	Declaration NodeSnapshot
	LocalName   NodeSnapshot
	Call        NodeSnapshot
	Callee      NodeSnapshot
	Arguments   []NodeSnapshot
	Assignment  NodeSnapshot
	AssignLeft  NodeSnapshot
	Add         NodeSnapshot
	AddInputs   []NodeSnapshot
	Return      NodeSnapshot
	ReturnValue NodeSnapshot
}

func findPrimitiveLocalCall(bodyID NodeID, nodes map[NodeID]NodeSnapshot) (primitiveLocalCallSource, bool) {
	body, ok := nodes[bodyID]
	if !ok || body.Kind != snapshotKindBlock {
		return primitiveLocalCallSource{}, false
	}
	statements := namedChildren(body, "statement[")
	if len(statements) != 3 {
		return primitiveLocalCallSource{}, false
	}
	variableStatement, ok := nodes[statements[0]]
	if !ok || variableStatement.Kind != snapshotKindVariableStatement {
		return primitiveLocalCallSource{}, false
	}
	list, ok := nodes[childByRole(variableStatement, "declarationList")]
	if !ok || list.Kind != snapshotKindVariableList || list.NodeFlags != 1 {
		return primitiveLocalCallSource{}, false
	}
	declarations := namedChildren(list, "declaration[")
	if len(declarations) != 1 {
		return primitiveLocalCallSource{}, false
	}
	declaration, ok := nodes[declarations[0]]
	if !ok || declaration.Kind != snapshotKindVariableDeclaration {
		return primitiveLocalCallSource{}, false
	}
	localName, ok := nodes[childByRole(declaration, "name")]
	if !ok || localName.Kind != snapshotKindIdentifier {
		return primitiveLocalCallSource{}, false
	}
	call, ok := nodes[childByRole(declaration, "initializer")]
	if !ok || call.Kind != snapshotKindCallExpression {
		return primitiveLocalCallSource{}, false
	}
	callee, ok := nodes[childByRole(call, "callee")]
	if !ok || callee.Kind != snapshotKindIdentifier {
		return primitiveLocalCallSource{}, false
	}
	argumentIDs := namedChildren(call, "argument[")
	if len(argumentIDs) != 2 {
		return primitiveLocalCallSource{}, false
	}
	arguments := make([]NodeSnapshot, 2)
	for index, argumentID := range argumentIDs {
		argument, exists := nodes[argumentID]
		if !exists || argument.Kind != snapshotKindIdentifier {
			return primitiveLocalCallSource{}, false
		}
		arguments[index] = argument
	}
	expressionStatement, ok := nodes[statements[1]]
	if !ok || expressionStatement.Kind != snapshotKindExpressionStatement {
		return primitiveLocalCallSource{}, false
	}
	assignment, ok := nodes[childByRole(expressionStatement, "expression")]
	if !ok || assignment.Kind != snapshotKindBinaryExpression || assignment.SyntaxPayload.Operator != snapshotKindEqualsToken {
		return primitiveLocalCallSource{}, false
	}
	assignLeft, ok := nodes[childByRole(assignment, "left")]
	if !ok || assignLeft.Kind != snapshotKindIdentifier {
		return primitiveLocalCallSource{}, false
	}
	add, ok := nodes[childByRole(assignment, "right")]
	if !ok || add.Kind != snapshotKindBinaryExpression || add.SyntaxPayload.Operator != snapshotKindPlusToken {
		return primitiveLocalCallSource{}, false
	}
	addInputIDs := []NodeID{childByRole(add, "left"), childByRole(add, "right")}
	addInputs := make([]NodeSnapshot, 2)
	for index, inputID := range addInputIDs {
		input, exists := nodes[inputID]
		if !exists || input.Kind != snapshotKindIdentifier {
			return primitiveLocalCallSource{}, false
		}
		addInputs[index] = input
	}
	returnNode, ok := nodes[statements[2]]
	if !ok || returnNode.Kind != snapshotKindReturnStatement {
		return primitiveLocalCallSource{}, false
	}
	returnValue, ok := nodes[childByRole(returnNode, "expression")]
	if !ok || returnValue.Kind != snapshotKindIdentifier {
		return primitiveLocalCallSource{}, false
	}
	return primitiveLocalCallSource{Declaration: declaration, LocalName: localName, Call: call, Callee: callee, Arguments: arguments, Assignment: assignment, AssignLeft: assignLeft, Add: add, AddInputs: addInputs, Return: returnNode, ReturnValue: returnValue}, true
}

func replayLocalCallFunction(
	function bingo.HIRFunction,
	events []LoweringEvent,
	source primitiveLocalCallSource,
	parameterValues map[SymbolID]bingo.ValueID,
	parameterTypes map[bingo.ValueID]bingo.TypeKind,
	functionNode NodeSnapshot,
	nodes map[NodeID]NodeSnapshot,
	types map[TypeID]TypeSnapshot,
	symbols map[SymbolID]SymbolSnapshot,
	signatures map[SignatureID]SignatureSnapshot,
	functionIDs map[NodeID]bingo.FunctionID,
) (bingo.HIRFunction, []LoweringEvent, error) {
	if source.Call.SelectedSignature == 0 {
		return bingo.HIRFunction{}, nil, fmt.Errorf("direct call %s has no selected signature", source.Call.ID)
	}
	signature, ok := signatures[source.Call.SelectedSignature]
	if !ok || signature.Declaration == "" || len(signature.ParameterFacts) != len(source.Arguments) || signature.ReturnType == 0 {
		return bingo.HIRFunction{}, nil, fmt.Errorf("direct call %s has an invalid selected signature", source.Call.ID)
	}
	calleeID, ok := functionIDs[signature.Declaration]
	if !ok || calleeID >= function.ID {
		return bingo.HIRFunction{}, nil, fmt.Errorf("direct call %s does not target an earlier source function", source.Call.ID)
	}
	calleeBound := false
	for _, symbolID := range nodeSymbolIDs(source.Callee) {
		if symbol, exists := symbols[symbolID]; exists && symbol.ValueDeclaration == signature.Declaration {
			calleeBound = true
		}
	}
	if !calleeBound {
		return bingo.HIRFunction{}, nil, fmt.Errorf("direct call %s callee symbol does not match selected signature", source.Call.ID)
	}
	operands := make([]bingo.ValueID, len(source.Arguments))
	argumentNodes := make([]NodeID, len(source.Arguments))
	for index, argument := range source.Arguments {
		value, found := parameterValue(argument, parameterValues)
		argumentType, typeErr := bingoType(nodeTypeID(argument), types)
		factType, factErr := bingoType(signature.ParameterFacts[index].Type, types)
		if !found || typeErr != nil || factErr != nil || argumentType != factType || parameterTypes[value] != argumentType {
			return bingo.HIRFunction{}, nil, fmt.Errorf("direct call %s argument %d is not a matching parameter", source.Call.ID, index)
		}
		operands[index] = value
		argumentNodes[index] = argument.ID
	}
	callTypeID := nodeTypeID(source.Call)
	callType, err := bingoType(callTypeID, types)
	if err != nil {
		return bingo.HIRFunction{}, nil, fmt.Errorf("direct call %s result type: %w", source.Call.ID, err)
	}
	signatureReturn, err := bingoType(signature.ReturnType, types)
	if err != nil || signatureReturn != callType {
		return bingo.HIRFunction{}, nil, fmt.Errorf("direct call %s result disagrees with selected signature", source.Call.ID)
	}
	nextValue := bingo.ValueID(len(function.Parameters) + 1)
	operations := []bingo.HIROp{{
		ID: nextValue, Kind: "call", Type: callType, Operands: operands, Callee: calleeID,
		Effect: bingo.EffectCall, LogicalCapabilityRequirements: []bingo.RuntimeCapabilityID{}, Origin: originOf(source.Call),
	}}
	localValues := make(map[SymbolID]bingo.ValueID)
	for _, symbolID := range nodeSymbolIDs(source.LocalName) {
		localValues[symbolID] = nextValue
	}
	events = append(events,
		LoweringEvent{Kind: "call.direct", Node: source.Call.ID, Origin: source.Call.Origin, Type: callTypeID, Callee: calleeID, Inputs: argumentNodes},
		LoweringEvent{Kind: "local.bind", Node: source.Declaration.ID, Origin: source.Declaration.Origin, Type: nodeTypeID(source.Declaration), Inputs: []NodeID{source.Call.ID}},
	)
	resolveValue := func(node NodeSnapshot) (bingo.ValueID, bool) {
		if value, found := parameterValue(node, localValues); found {
			return value, true
		}
		return parameterValue(node, parameterValues)
	}
	left, leftOK := resolveValue(source.AddInputs[0])
	right, rightOK := resolveValue(source.AddInputs[1])
	addTypeID := nodeTypeID(source.Add)
	addType, addErr := bingoType(addTypeID, types)
	if !leftOK || !rightOK || addErr != nil || addType != bingo.TypeNumber {
		return bingo.HIRFunction{}, nil, fmt.Errorf("local assignment add operands are not canonical numbers")
	}
	nextValue++
	operations = append(operations, bingo.HIROp{ID: nextValue, Kind: "binary", Type: addType, Operands: []bingo.ValueID{left, right}, Operator: "+", Effect: bingo.EffectPure, LogicalCapabilityRequirements: []bingo.RuntimeCapabilityID{}, Origin: originOf(source.Add)})
	assigned, assignedOK := parameterValue(source.AssignLeft, localValues)
	if !assignedOK || assigned != operations[0].ID {
		return bingo.HIRFunction{}, nil, fmt.Errorf("assignment target is not the bound local")
	}
	for _, symbolID := range nodeSymbolIDs(source.AssignLeft) {
		localValues[symbolID] = nextValue
	}
	events = append(events,
		LoweringEvent{Kind: "binary.add", Node: source.Add.ID, Origin: source.Add.Origin, Type: addTypeID, Operator: "+", Inputs: []NodeID{source.AddInputs[0].ID, source.AddInputs[1].ID}},
		LoweringEvent{Kind: "local.assign", Node: source.Assignment.ID, Origin: source.Assignment.Origin, Type: nodeTypeID(source.Assignment), Inputs: []NodeID{source.AssignLeft.ID, source.Add.ID}},
	)
	returnValue, ok := parameterValue(source.ReturnValue, localValues)
	if !ok || returnValue != nextValue {
		return bingo.HIRFunction{}, nil, fmt.Errorf("return does not read the assigned local")
	}
	returnTypeID, ok := resolveFunctionReturnType(functionNode, nodes, symbols, types, signatures)
	if !ok {
		returnTypeID = annotatedReturnType(functionNode, nodes)
	}
	returnType, err := bingoType(returnTypeID, types)
	if err != nil || returnType != addType {
		return bingo.HIRFunction{}, nil, fmt.Errorf("function %s return type disagrees with assigned local", functionNode.ID)
	}
	function.ReturnType = returnType
	function.Blocks = []bingo.HIRBlock{{ID: 1, Operations: operations, Terminator: bingo.HIRTerminator{Kind: "return", Value: returnValue, Origin: originOf(source.Return)}}}
	events = append(events,
		LoweringEvent{Kind: "return", Node: source.Return.ID, Origin: source.Return.Origin, Type: nodeTypeID(source.ReturnValue), Inputs: []NodeID{source.ReturnValue.ID}},
		LoweringEvent{Kind: "function.end", Node: functionNode.ID, Origin: functionNode.Origin},
	)
	return function, events, nil
}

type primitiveChooseSource struct {
	IfNode     NodeSnapshot
	Condition  NodeSnapshot
	ThenReturn NodeSnapshot
	ThenValue  NodeSnapshot
	ElseReturn NodeSnapshot
	ElseValue  NodeSnapshot
}

func findPrimitiveChoose(bodyID NodeID, nodes map[NodeID]NodeSnapshot) (primitiveChooseSource, bool, error) {
	body, ok := nodes[bodyID]
	if !ok || body.Kind != snapshotKindBlock {
		return primitiveChooseSource{}, false, fmt.Errorf("body is not a block")
	}
	statements := namedChildren(body, "statement[")
	if len(statements) != 2 {
		return primitiveChooseSource{}, false, nil
	}
	firstStatement, ok := nodes[statements[0]]
	if !ok || firstStatement.Kind != snapshotKindIfStatement {
		return primitiveChooseSource{}, false, nil
	}
	secondStatement, ok := nodes[statements[1]]
	if !ok || secondStatement.Kind != snapshotKindReturnStatement {
		return primitiveChooseSource{}, false, nil
	}
	ifNode, err := requireChildKind(body, statements[0], snapshotKindIfStatement, nodes)
	if err != nil {
		return primitiveChooseSource{}, false, err
	}
	elseReturn, err := requireChildKind(body, statements[1], snapshotKindReturnStatement, nodes)
	if err != nil {
		return primitiveChooseSource{}, false, err
	}
	condition, err := requireRoleKind(ifNode, "child[0]", snapshotKindIdentifier, nodes)
	if err != nil {
		return primitiveChooseSource{}, false, err
	}
	thenBlock, err := requireRoleKind(ifNode, "child[1]", snapshotKindBlock, nodes)
	if err != nil {
		return primitiveChooseSource{}, false, err
	}
	thenStatements := namedChildren(thenBlock, "statement[")
	if len(thenStatements) != 1 {
		return primitiveChooseSource{}, false, fmt.Errorf("primitive if block requires exactly one return")
	}
	thenReturn, err := requireChildKind(thenBlock, thenStatements[0], snapshotKindReturnStatement, nodes)
	if err != nil {
		return primitiveChooseSource{}, false, err
	}
	thenValue, err := requireRoleKind(thenReturn, "expression", snapshotKindIdentifier, nodes)
	if err != nil {
		return primitiveChooseSource{}, false, err
	}
	elseValue, err := requireRoleKind(elseReturn, "expression", snapshotKindIdentifier, nodes)
	if err != nil {
		return primitiveChooseSource{}, false, err
	}
	return primitiveChooseSource{IfNode: ifNode, Condition: condition, ThenReturn: thenReturn, ThenValue: thenValue, ElseReturn: elseReturn, ElseValue: elseValue}, true, nil
}

func replayChooseFunction(
	function bingo.HIRFunction,
	events []LoweringEvent,
	choose primitiveChooseSource,
	parameterValues map[SymbolID]bingo.ValueID,
	parameterTypes map[bingo.ValueID]bingo.TypeKind,
	functionNode NodeSnapshot,
	nodes map[NodeID]NodeSnapshot,
	types map[TypeID]TypeSnapshot,
	symbols map[SymbolID]SymbolSnapshot,
	signatures map[SignatureID]SignatureSnapshot,
) (bingo.HIRFunction, []LoweringEvent, error) {
	conditionValue, ok := parameterValue(choose.Condition, parameterValues)
	if !ok {
		return bingo.HIRFunction{}, nil, fmt.Errorf("if condition %s is not a parameter", choose.Condition.ID)
	}
	conditionTypeID := nodeTypeID(choose.Condition)
	conditionType, err := bingoType(conditionTypeID, types)
	if err != nil || conditionType != bingo.TypeBoolean || parameterTypes[conditionValue] != bingo.TypeBoolean {
		return bingo.HIRFunction{}, nil, fmt.Errorf("if condition %s is not canonical boolean", choose.Condition.ID)
	}
	thenValue, ok := parameterValue(choose.ThenValue, parameterValues)
	if !ok {
		return bingo.HIRFunction{}, nil, fmt.Errorf("then return %s is not a parameter", choose.ThenValue.ID)
	}
	elseValue, ok := parameterValue(choose.ElseValue, parameterValues)
	if !ok {
		return bingo.HIRFunction{}, nil, fmt.Errorf("fallthrough return %s is not a parameter", choose.ElseValue.ID)
	}
	for _, value := range []struct {
		node  NodeSnapshot
		value bingo.ValueID
	}{{choose.ThenValue, thenValue}, {choose.ElseValue, elseValue}} {
		typeID := nodeTypeID(value.node)
		typ, typeErr := bingoType(typeID, types)
		if typeErr != nil || typ != bingo.TypeNumber || parameterTypes[value.value] != bingo.TypeNumber {
			return bingo.HIRFunction{}, nil, fmt.Errorf("return value %s is not canonical number", value.node.ID)
		}
	}
	returnTypeID, ok := resolveFunctionReturnType(functionNode, nodes, symbols, types, signatures)
	if !ok {
		returnTypeID = annotatedReturnType(functionNode, nodes)
	}
	returnType, err := bingoType(returnTypeID, types)
	if err != nil || returnType != bingo.TypeNumber {
		return bingo.HIRFunction{}, nil, fmt.Errorf("function %s return type is not canonical number", functionNode.ID)
	}
	function.ReturnType = returnType
	function.Blocks = []bingo.HIRBlock{
		{ID: 1, Operations: []bingo.HIROp{}, Terminator: bingo.HIRTerminator{Kind: "condbranch", Value: conditionValue, Successors: []bingo.BlockID{2, 3}, Origin: originOf(choose.IfNode)}},
		{ID: 2, Operations: []bingo.HIROp{}, Terminator: bingo.HIRTerminator{Kind: "return", Value: thenValue, Origin: originOf(choose.ThenReturn)}},
		{ID: 3, Operations: []bingo.HIROp{}, Terminator: bingo.HIRTerminator{Kind: "return", Value: elseValue, Origin: originOf(choose.ElseReturn)}},
	}
	events = append(events,
		LoweringEvent{Kind: "if.condition", Node: choose.IfNode.ID, Origin: choose.IfNode.Origin, Type: conditionTypeID, Inputs: []NodeID{choose.Condition.ID}},
		LoweringEvent{Kind: "return", Node: choose.ThenReturn.ID, Origin: choose.ThenReturn.Origin, Type: nodeTypeID(choose.ThenValue), Inputs: []NodeID{choose.ThenValue.ID}},
		LoweringEvent{Kind: "return", Node: choose.ElseReturn.ID, Origin: choose.ElseReturn.Origin, Type: nodeTypeID(choose.ElseValue), Inputs: []NodeID{choose.ElseValue.ID}},
		LoweringEvent{Kind: "function.end", Node: functionNode.ID, Origin: functionNode.Origin},
	)
	return function, events, nil
}

func findPrimitiveReturn(bodyID NodeID, nodes map[NodeID]NodeSnapshot) (NodeSnapshot, NodeSnapshot, error) {
	body, ok := nodes[bodyID]
	if !ok || body.Kind != snapshotKindBlock {
		return NodeSnapshot{}, NodeSnapshot{}, fmt.Errorf("body is not a block")
	}
	statements := namedChildren(body, "statement[")
	if len(statements) != 1 {
		return NodeSnapshot{}, NodeSnapshot{}, fmt.Errorf("body has %d statements; primitive replay requires exactly one", len(statements))
	}
	returnNode, err := requireChildKind(body, statements[0], snapshotKindReturnStatement, nodes)
	if err != nil {
		return NodeSnapshot{}, NodeSnapshot{}, err
	}
	expressionID := childByRole(returnNode, "expression")
	if expressionID == "" {
		return NodeSnapshot{}, NodeSnapshot{}, fmt.Errorf("return statement has no expression")
	}
	expression, ok := nodes[expressionID]
	if !ok || expression.SyntaxPayload.Tag != snapshotKindBinaryExpression {
		return NodeSnapshot{}, NodeSnapshot{}, fmt.Errorf("return expression is not a binary expression")
	}
	return returnNode, expression, nil
}

func nodeTypeID(node NodeSnapshot) TypeID {
	if node.NarrowedType != 0 {
		return node.NarrowedType
	}
	if node.DeclaredType != 0 {
		return node.DeclaredType
	}
	return node.ContextualType
}

func nodeSymbolIDs(node NodeSnapshot) []SymbolID {
	result := make([]SymbolID, 0, 2)
	seen := make(map[SymbolID]struct{}, 2)
	for _, symbol := range []SymbolID{node.Symbol, node.ResolvedSymbol} {
		if symbol == "" {
			continue
		}
		if _, duplicate := seen[symbol]; duplicate {
			continue
		}
		seen[symbol] = struct{}{}
		result = append(result, symbol)
	}
	return result
}

func parameterSymbolIDs(parameter NodeSnapshot, nodes map[NodeID]NodeSnapshot) []SymbolID {
	return nodeAndNamedChildSymbolIDs(parameter, "name", nodes)
}

func nodeAndNamedChildSymbolIDs(node NodeSnapshot, role string, nodes map[NodeID]NodeSnapshot) []SymbolID {
	result := nodeSymbolIDs(node)
	childID := childByRole(node, role)
	if childID == "" {
		return result
	}
	child, ok := nodes[childID]
	if !ok {
		return result
	}
	seen := make(map[SymbolID]struct{}, len(result)+2)
	for _, symbol := range result {
		seen[symbol] = struct{}{}
	}
	for _, symbol := range nodeSymbolIDs(child) {
		if _, duplicate := seen[symbol]; duplicate {
			continue
		}
		seen[symbol] = struct{}{}
		result = append(result, symbol)
	}
	return result
}

func parameterValue(node NodeSnapshot, values map[SymbolID]bingo.ValueID) (bingo.ValueID, bool) {
	for _, symbol := range nodeSymbolIDs(node) {
		if value, ok := values[symbol]; ok {
			return value, true
		}
	}
	return 0, false
}

func resolveFunctionReturnType(functionNode NodeSnapshot, nodes map[NodeID]NodeSnapshot, symbols map[SymbolID]SymbolSnapshot, types map[TypeID]TypeSnapshot, signatures map[SignatureID]SignatureSnapshot) (TypeID, bool) {
	for _, symbolID := range nodeAndNamedChildSymbolIDs(functionNode, "name", nodes) {
		symbol, ok := symbols[symbolID]
		if !ok || symbol.Type == 0 {
			continue
		}
		typ, ok := types[symbol.Type]
		if !ok {
			continue
		}
		for _, signatureID := range slices.Concat(typ.CallSignatures, typ.ConstructSignatures) {
			signature, ok := signatures[signatureID]
			if !ok || signature.Declaration != functionNode.ID || signature.ReturnType == 0 {
				continue
			}
			return signature.ReturnType, true
		}
	}
	return 0, false
}

func annotatedReturnType(functionNode NodeSnapshot, nodes map[NodeID]NodeSnapshot) TypeID {
	returnTypeID := childByRole(functionNode, "returnType")
	if returnTypeID == "" {
		return 0
	}
	return nodeTypeID(nodes[returnTypeID])
}

func namedChildren(node NodeSnapshot, prefix string) []NodeID {
	result := make([]NodeID, 0)
	for _, child := range node.NamedChildren {
		if strings.HasPrefix(child.Role, prefix) || child.Role == prefix {
			result = append(result, child.Node)
		}
	}
	return result
}

func childByRole(node NodeSnapshot, role string) NodeID {
	for _, child := range node.NamedChildren {
		if child.Role == role {
			return child.Node
		}
	}
	return ""
}

func childText(node NodeSnapshot, role string, nodes map[NodeID]NodeSnapshot) string {
	child := childByRole(node, role)
	if child == "" {
		return ""
	}
	return nodes[child].SyntaxPayload.Text
}

func bingoType(id TypeID, types map[TypeID]TypeSnapshot) (bingo.TypeKind, error) {
	typ, ok := types[id]
	if !ok {
		return "", fmt.Errorf("missing type %d", id)
	}
	if typ.Kind == "intrinsic" && typ.Flags == 64 && typ.ObjectFlags == 0 && typ.TypePayload.Tag == "intrinsic" && typ.TypePayload.Scalar == "intrinsic|64|0|||intrinsic:number" {
		return bingo.TypeNumber, nil
	}
	if typ.Kind == "union" && typ.TypePayload.Tag == "union" && len(typ.ElementTypes) == 2 {
		seen := map[string]bool{}
		for _, elementID := range typ.ElementTypes {
			element, ok := types[elementID]
			if !ok || element.Kind != "literal" || element.Flags != 8192 || element.ObjectFlags != 0 || element.TypePayload.Tag != "literal" {
				return "", fmt.Errorf("type %d is not the canonical boolean union", id)
			}
			seen[element.TypePayload.Scalar] = true
		}
		if seen["literal|8192|0|||literal:bool:false"] && seen["literal|8192|0|||literal:bool:true"] {
			return bingo.TypeBoolean, nil
		}
	}
	return "", fmt.Errorf("type %d (%s) is not a canonical primitive type", id, typ.Kind)
}

func originOf(node NodeSnapshot) bingo.Origin {
	return bingo.Origin{File: string(node.Span.File), Start: node.Span.Start, End: node.Span.End}
}

func hashBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}
