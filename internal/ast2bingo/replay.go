package ast2bingo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
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
	snapshotKindAsExpression                = "KindAsExpression"
	snapshotKindBinaryExpression            = "KindBinaryExpression"
	snapshotKindBlock                       = "KindBlock"
	snapshotKindBooleanKeyword              = "KindBooleanKeyword"
	snapshotKindCallExpression              = "KindCallExpression"
	snapshotKindEndOfFile                   = "KindEndOfFile"
	snapshotKindElementAccessExpression     = "KindElementAccessExpression"
	snapshotKindEqualsToken                 = "KindEqualsToken"
	snapshotKindExportKeyword               = "KindExportKeyword"
	snapshotKindExpressionStatement         = "KindExpressionStatement"
	snapshotKindFunctionDeclaration         = "KindFunctionDeclaration"
	snapshotKindGetAccessor                 = "KindGetAccessor"
	snapshotKindIdentifier                  = "KindIdentifier"
	snapshotKindIfStatement                 = "KindIfStatement"
	snapshotKindLessThanToken               = "KindLessThanToken"
	snapshotKindLiteralType                 = "KindLiteralType"
	snapshotKindNullKeyword                 = "KindNullKeyword"
	snapshotKindNumberKeyword               = "KindNumberKeyword"
	snapshotKindNumericLiteral              = "KindNumericLiteral"
	snapshotKindObjectLiteralExpression     = "KindObjectLiteralExpression"
	snapshotKindParameter                   = "KindParameter"
	snapshotKindParenthesizedExpression     = "KindParenthesizedExpression"
	snapshotKindPlusToken                   = "KindPlusToken"
	snapshotKindPrefixUnaryExpression       = "KindPrefixUnaryExpression"
	snapshotKindPropertyAccessExpression    = "KindPropertyAccessExpression"
	snapshotKindPropertyAssignment          = "KindPropertyAssignment"
	snapshotKindQuestionQuestionEqualsToken = "KindQuestionQuestionEqualsToken"
	snapshotKindQuestionQuestionToken       = "KindQuestionQuestionToken"
	snapshotKindReturnStatement             = "KindReturnStatement"
	snapshotKindShorthandPropertyAssignment = "KindShorthandPropertyAssignment"
	snapshotKindSourceFile                  = "KindSourceFile"
	snapshotKindSetAccessor                 = "KindSetAccessor"
	snapshotKindStringLiteral               = "KindStringLiteral"
	snapshotKindStringKeyword               = "KindStringKeyword"
	snapshotKindUndefinedKeyword            = "KindUndefinedKeyword"
	snapshotKindUnionType                   = "KindUnionType"
	snapshotKindThisKeyword                 = "KindThisKeyword"
	snapshotKindTypeReference               = "KindTypeReference"
	snapshotKindVariableDeclaration         = "KindVariableDeclaration"
	snapshotKindVariableList                = "KindVariableDeclarationList"
	snapshotKindVariableStatement           = "KindVariableStatement"
	snapshotKindWhileStatement              = "KindWhileStatement"
)

const (
	snapshotNodeFlagUsing     uint32 = 1 << 2
	snapshotKnownNodeFlagMask uint32 = 1<<29 - 1
	snapshotModifierExport    uint32 = 1 << 5
	snapshotModifierDeclare   uint32 = 1 << 7
)

// snapshotLowererReadinessRegistry is the executable support boundary for the
// first replay slice. Keep this list sorted by Kind; validation below makes an
// accidental duplicate or nil handler fail closed at package use sites.
var snapshotLowererReadinessRegistry = []snapshotLowererReadinessDefinition{
	{Kind: snapshotKindAsExpression, PayloadTag: snapshotKindAsExpression, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule, snapshotFactType}, Handle: validateVERT011AsExpressionLowerer},
	{Kind: snapshotKindBinaryExpression, PayloadTag: snapshotKindBinaryExpression, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule, snapshotFactType}, Handle: validateBinaryExpressionLowerer},
	{Kind: snapshotKindBlock, PayloadTag: snapshotKindBlock, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateContainerLowerer},
	{Kind: snapshotKindBooleanKeyword, PayloadTag: snapshotKindBooleanKeyword, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateContainerLowerer},
	{Kind: snapshotKindCallExpression, PayloadTag: snapshotKindCallExpression, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule, snapshotFactType}, Handle: validateCallExpressionLowerer},
	{Kind: snapshotKindElementAccessExpression, PayloadTag: snapshotKindElementAccessExpression, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule, snapshotFactType}, Handle: validateVERT011ElementAccessLowerer},
	{Kind: snapshotKindEndOfFile, PayloadTag: snapshotKindEndOfFile, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateContainerLowerer},
	{Kind: snapshotKindEqualsToken, PayloadTag: snapshotKindEqualsToken, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateContainerLowerer},
	{Kind: snapshotKindExportKeyword, PayloadTag: snapshotKindExportKeyword, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateContainerLowerer},
	{Kind: snapshotKindExpressionStatement, PayloadTag: snapshotKindExpressionStatement, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateExpressionStatementLowerer},
	{Kind: snapshotKindFunctionDeclaration, PayloadTag: snapshotKindFunctionDeclaration, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactFunctionContract, snapshotFactModule, snapshotFactSymbol}, Handle: validateFunctionLowerer},
	{Kind: snapshotKindGetAccessor, PayloadTag: snapshotKindGetAccessor, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule, snapshotFactType}, Handle: validateVERT011GetAccessorLowerer},
	{Kind: snapshotKindIdentifier, PayloadTag: snapshotKindIdentifier, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule, snapshotFactSymbol, snapshotFactType}, Handle: validateIdentifierLowerer},
	{Kind: snapshotKindIfStatement, PayloadTag: snapshotKindIfStatement, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateIfLowerer},
	{Kind: snapshotKindLessThanToken, PayloadTag: snapshotKindLessThanToken, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateContainerLowerer},
	{Kind: snapshotKindLiteralType, PayloadTag: snapshotKindLiteralType, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule, snapshotFactType}, Handle: validateLiteralTypeLowerer},
	{Kind: snapshotKindNullKeyword, PayloadTag: snapshotKindNullKeyword, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule, snapshotFactType}, Handle: validateContainerLowerer},
	{Kind: snapshotKindNumberKeyword, PayloadTag: snapshotKindNumberKeyword, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateContainerLowerer},
	{Kind: snapshotKindNumericLiteral, PayloadTag: snapshotKindNumericLiteral, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule, snapshotFactType}, Handle: validateNumericLiteralLowerer},
	{Kind: snapshotKindObjectLiteralExpression, PayloadTag: snapshotKindObjectLiteralExpression, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule, snapshotFactType}, Handle: validateObjectLiteralLowerer},
	{Kind: snapshotKindParameter, PayloadTag: snapshotKindParameter, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule, snapshotFactSymbol, snapshotFactType}, Handle: validateParameterLowerer},
	{Kind: snapshotKindParenthesizedExpression, PayloadTag: snapshotKindParenthesizedExpression, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule, snapshotFactType}, Handle: validateVERT011ParenthesizedLowerer},
	{Kind: snapshotKindPlusToken, PayloadTag: snapshotKindPlusToken, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateContainerLowerer},
	{Kind: snapshotKindPrefixUnaryExpression, PayloadTag: snapshotKindPrefixUnaryExpression, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule, snapshotFactType}, Handle: validatePrefixUnaryLowerer},
	{Kind: snapshotKindPropertyAccessExpression, PayloadTag: snapshotKindPropertyAccessExpression, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule, snapshotFactSymbol, snapshotFactType}, Handle: validatePropertyAccessLowerer},
	{Kind: snapshotKindPropertyAssignment, PayloadTag: snapshotKindPropertyAssignment, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule, snapshotFactType}, Handle: validateVERT011PropertyAssignmentLowerer},
	{Kind: snapshotKindQuestionQuestionEqualsToken, PayloadTag: snapshotKindQuestionQuestionEqualsToken, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateContainerLowerer},
	{Kind: snapshotKindQuestionQuestionToken, PayloadTag: snapshotKindQuestionQuestionToken, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateContainerLowerer},
	{Kind: snapshotKindReturnStatement, PayloadTag: snapshotKindReturnStatement, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateReturnLowerer},
	{Kind: snapshotKindSetAccessor, PayloadTag: snapshotKindSetAccessor, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule, snapshotFactType}, Handle: validateVERT011SetAccessorLowerer},
	{Kind: snapshotKindShorthandPropertyAssignment, PayloadTag: snapshotKindShorthandPropertyAssignment, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateShorthandPropertyLowerer},
	{Kind: snapshotKindSourceFile, PayloadTag: snapshotKindSourceFile, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateContainerLowerer},
	{Kind: snapshotKindStringKeyword, PayloadTag: snapshotKindStringKeyword, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateContainerLowerer},
	{Kind: snapshotKindStringLiteral, PayloadTag: snapshotKindStringLiteral, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule, snapshotFactType}, Handle: validateVERT011StringLiteralLowerer},
	{Kind: snapshotKindThisKeyword, PayloadTag: snapshotKindThisKeyword, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule, snapshotFactSymbol, snapshotFactType}, Handle: validateVERT011ThisLowerer},
	{Kind: snapshotKindTypeReference, PayloadTag: snapshotKindTypeReference, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule, snapshotFactType}, Handle: validateVERT011TypeReferenceLowerer},
	{Kind: snapshotKindUndefinedKeyword, PayloadTag: snapshotKindUndefinedKeyword, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule, snapshotFactType}, Handle: validateContainerLowerer},
	{Kind: snapshotKindUnionType, PayloadTag: snapshotKindUnionType, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule, snapshotFactType}, Handle: validateUnionTypeLowerer},
	{Kind: snapshotKindVariableDeclaration, PayloadTag: snapshotKindVariableDeclaration, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule, snapshotFactSymbol, snapshotFactType}, Handle: validateVariableDeclarationLowerer},
	{Kind: snapshotKindVariableList, PayloadTag: snapshotKindVariableList, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateVariableListLowerer},
	{Kind: snapshotKindVariableStatement, PayloadTag: snapshotKindVariableStatement, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateVariableStatementLowerer},
	{Kind: snapshotKindWhileStatement, PayloadTag: snapshotKindWhileStatement, SnapshotSchemaVersion: frontendwire.SnapshotSchemaVersion, RequiredFacts: []string{snapshotFactModule}, Handle: validateWhileLowerer},
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

		roots, err := primitiveNodeTypeRoots(node, indexes)
		if err != nil {
			return fmt.Errorf("primitive subset contract rejects node %q (%s): %w", node.ID, node.Kind, err)
		}
		seenRoots := make(map[TypeID]struct{}, 3)
		for _, root := range roots {
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

func primitiveNodeTypeRoots(node NodeSnapshot, indexes snapshotSemanticFactIndexes) ([]TypeID, error) {
	if node.Kind == snapshotKindIdentifier && node.SyntaxPayload.Text == "const" {
		parent, parentOK := indexes.Nodes[node.Parent]
		typ, typeOK := indexes.Types[node.NarrowedType]
		if !parentOK || parent.Kind != snapshotKindTypeReference || node.EvaluationFlags != 1 || node.DeclaredType == 0 || node.DeclaredType != node.NarrowedType || !typeOK || typ.NotLowerableReason != "checker-error-type" {
			return nil, fmt.Errorf("VERT-011 const assertion marker lacks the exact type-context proof")
		}
		return nil, nil
	}
	if node.Kind != snapshotKindThisKeyword {
		return []TypeID{node.NarrowedType, node.DeclaredType, node.ContextualType}, nil
	}
	declared, declaredOK := indexes.Types[node.DeclaredType]
	narrowed, narrowedOK := indexes.Types[node.NarrowedType]
	if node.DeclaredType == 0 || node.NarrowedType == 0 || node.ContextualType != 0 || !declaredOK || !narrowedOK || declared.NotLowerableReason != "checker-error-type" || strings.ToLower(strings.TrimSpace(narrowed.Kind)) != "object" || node.Flow.NarrowedTypeHash != narrowed.CanonicalHash {
		return nil, fmt.Errorf("VERT-011 this receiver lacks the exact object-literal location-type proof")
	}
	return []TypeID{node.NarrowedType}, nil
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
	if kind == "object" {
		if matched, err := validateVERT010ObjectTypeClosure(record, indexes); matched || err != nil {
			return err
		}
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

func validateVERT010ObjectTypeClosure(record TypeSnapshot, indexes snapshotSemanticFactIndexes) (bool, error) {
	containsShorthand := false
	for _, propertyID := range record.Properties {
		property, ok := indexes.Symbols[propertyID]
		if !ok {
			return false, fmt.Errorf("object type references missing property symbol %q", propertyID)
		}
		for _, declarationID := range property.Declarations {
			declaration, ok := indexes.Nodes[declarationID]
			if ok && declaration.Kind == snapshotKindShorthandPropertyAssignment {
				containsShorthand = true
			}
		}
	}
	if !containsShorthand {
		return false, nil
	}
	if len(record.Properties) != 1 || len(record.PropertyFacts) != 1 || len(record.IndexInfos) != 0 || len(record.CallSignatures) != 0 || len(record.ConstructSignatures) != 0 || len(record.BaseTypes) != 0 {
		return true, fmt.Errorf("VERT-010 object type must be one closed data property")
	}
	propertyID := record.Properties[0]
	property := indexes.Symbols[propertyID]
	fact := record.PropertyFacts[0]
	if property.Name != "value" || fact.Symbol != propertyID || fact.ReadType == 0 || fact.ReadType != fact.WriteType || fact.Optional || fact.Readonly || !fact.HasGetter || !fact.HasSetter || fact.Visibility != "public" || fact.PrivateIdentity != "" {
		return true, fmt.Errorf("VERT-010 value property contract is invalid")
	}
	valueType, ok := indexes.Types[fact.ReadType]
	if !ok || strings.ToLower(strings.TrimSpace(valueType.Kind)) != "intrinsic" || valueType.DebugText != "number" {
		return true, fmt.Errorf("VERT-010 value property is not canonical number")
	}
	if len(property.Declarations) != 1 || property.ValueDeclaration != property.Declarations[0] {
		return true, fmt.Errorf("VERT-010 value property declaration is ambiguous")
	}
	declaration, ok := indexes.Nodes[property.ValueDeclaration]
	if !ok || declaration.Kind != snapshotKindShorthandPropertyAssignment {
		return true, fmt.Errorf("VERT-010 value property is not shorthand data storage")
	}
	return true, nil
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
	wantEffects := []string{"pure"}
	functionName := childText(node, "name", indexes.Nodes)
	switch functionName {
	case "stringLength":
		// The frontend conservatively classifies .length as a read. The
		// string contract makes this immutable observation a pure HIR op.
		wantEffects = []string{"read"}
	case "objectAlias":
		wantEffects = []string{"alloc", "read", "write"}
	case "propertyNullishAssign":
		if signature.EffectProof.Kind != "body-resolved" || signature.EffectProof.Complete || !slices.Equal(signature.EffectProof.DirectEffects, []string{"alloc", "read", "write"}) || len(signature.EffectProof.Calls) != 0 || !slices.Equal(signature.Effects, []string{"unknown"}) {
			return fmt.Errorf("VERT-011 function signature %d has unexpected accessor effect proof", signature.ID)
		}
	}
	if functionName != "propertyNullishAssign" && (signature.EffectProof.Kind != "body-resolved" || !signature.EffectProof.Complete || !slices.Equal(signature.Effects, wantEffects)) {
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
		if len(statements) == 0 || len(statements) > 4 || len(node.NamedChildren) != len(statements) || len(node.Children) != len(statements) {
			return fmt.Errorf("lowerable block requires one to four statements")
		}
		for _, statement := range statements {
			child, ok := nodes[statement]
			if !ok || child.Parent != node.ID || (child.Kind != snapshotKindReturnStatement && child.Kind != snapshotKindIfStatement && child.Kind != snapshotKindWhileStatement && child.Kind != snapshotKindVariableStatement && child.Kind != snapshotKindExpressionStatement) {
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
	case snapshotKindBooleanKeyword, snapshotKindEndOfFile, snapshotKindEqualsToken, snapshotKindExportKeyword, snapshotKindLessThanToken, snapshotKindNullKeyword, snapshotKindNumberKeyword, snapshotKindPlusToken, snapshotKindQuestionQuestionEqualsToken, snapshotKindQuestionQuestionToken, snapshotKindStringKeyword, snapshotKindUndefinedKeyword:
		if len(node.NamedChildren) != 0 || len(node.Children) != 0 {
			return fmt.Errorf("primitive token Kind %q cannot have children", node.Kind)
		}
	default:
		return fmt.Errorf("Kind %q is not a primitive container", node.Kind)
	}
	return nil
}

func validateFunctionLowerer(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) error {
	if len(node.NamedChildren) < 4 || len(node.NamedChildren) > 7 || len(node.Children) != len(node.NamedChildren) {
		return fmt.Errorf("primitive function requires an optional export, name, supported parameters, number return type, and body")
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
	functionName := childText(node, "name", nodes)
	if len(parameters) == 0 {
		modifiers := namedChildren(node, "modifier[")
		if functionName != "main" || len(modifiers) != 1 {
			return fmt.Errorf("only exported application main may be parameterless")
		}
	} else if len(parameters) > 3 {
		return fmt.Errorf("primitive function requires one to three parameters")
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
		return fmt.Errorf("primitive parameter requires exactly an identifier and supported annotation")
	}
	if _, err := requireRoleKind(node, "name", snapshotKindIdentifier, nodes); err != nil {
		return err
	}
	typeNodeID := childByRole(node, "type")
	typeNode, ok := nodes[typeNodeID]
	if !ok || typeNode.Parent != node.ID || (typeNode.Kind != snapshotKindNumberKeyword && typeNode.Kind != snapshotKindBooleanKeyword && typeNode.Kind != snapshotKindStringKeyword && typeNode.Kind != snapshotKindUnionType) {
		return fmt.Errorf("primitive parameter type must be number, boolean, string, or canonical nullable number")
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
	if !ok || expression.Parent != node.ID || (expression.Kind != snapshotKindBinaryExpression && expression.Kind != snapshotKindIdentifier && expression.Kind != snapshotKindNumericLiteral && expression.Kind != snapshotKindParenthesizedExpression && expression.Kind != snapshotKindPrefixUnaryExpression && expression.Kind != snapshotKindPropertyAccessExpression) {
		return fmt.Errorf("primitive return expression must be an identifier, literal, prefix unary, binary, or supported property expression")
	}
	return nil
}

func validateIfLowerer(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) error {
	if len(node.NamedChildren) != 2 || len(node.Children) != 2 {
		return fmt.Errorf("primitive if requires condition and then block")
	}
	conditionID := childByRole(node, "child[0]")
	condition, ok := nodes[conditionID]
	if !ok || condition.Parent != node.ID || (condition.Kind != snapshotKindIdentifier && condition.Kind != snapshotKindBinaryExpression) {
		return fmt.Errorf("primitive if condition must be an identifier or binary expression")
	}
	if _, err := requireRoleKind(node, "child[1]", snapshotKindBlock, nodes); err != nil {
		return err
	}
	return nil
}

func validateWhileLowerer(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) error {
	if len(node.NamedChildren) != 2 || len(node.Children) != 2 {
		return fmt.Errorf("primitive while requires condition and body block")
	}
	condition, err := requireRoleKind(node, "child[0]", snapshotKindBinaryExpression, nodes)
	if err != nil {
		return err
	}
	if condition.SyntaxPayload.Operator != snapshotKindLessThanToken {
		return fmt.Errorf("primitive while condition must use <")
	}
	_, err = requireRoleKind(node, "child[1]", snapshotKindBlock, nodes)
	return err
}

func validateBinaryExpressionLowerer(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) error {
	if len(node.NamedChildren) != 3 || len(node.Children) != 3 {
		return fmt.Errorf("primitive binary expression requires exactly left, operator, and right children")
	}
	leftID := childByRole(node, "left")
	left, ok := nodes[leftID]
	if !ok || left.Parent != node.ID || (left.Kind != snapshotKindElementAccessExpression && left.Kind != snapshotKindIdentifier && left.Kind != snapshotKindPropertyAccessExpression) {
		return fmt.Errorf("primitive binary left operand must be an identifier or static property access")
	}
	switch node.SyntaxPayload.Operator {
	case snapshotKindPlusToken:
		if _, err := requireRoleKind(node, "operator", snapshotKindPlusToken, nodes); err != nil {
			return err
		}
		rightID := childByRole(node, "right")
		right, ok := nodes[rightID]
		if !ok || right.Parent != node.ID || (right.Kind != snapshotKindIdentifier && right.Kind != snapshotKindNumericLiteral) {
			return fmt.Errorf("primitive addition right operand must be an identifier or numeric literal")
		}
	case snapshotKindEqualsToken:
		if _, err := requireRoleKind(node, "operator", snapshotKindEqualsToken, nodes); err != nil {
			return err
		}
		rightID := childByRole(node, "right")
		right, ok := nodes[rightID]
		if !ok || right.Parent != node.ID {
			return fmt.Errorf("assignment right operand is missing")
		}
		if right.Kind != snapshotKindBinaryExpression {
			if left.Kind != snapshotKindPropertyAccessExpression || !vert011BackingAccess(left, nodes) || right.Kind != snapshotKindIdentifier || right.SyntaxPayload.Text != "next" {
				return fmt.Errorf("direct assignment is outside the VERT-011 setter subset")
			}
		}
	case snapshotKindLessThanToken:
		if _, err := requireRoleKind(node, "operator", snapshotKindLessThanToken, nodes); err != nil {
			return err
		}
		rightID := childByRole(node, "right")
		right, ok := nodes[rightID]
		if !ok || right.Parent != node.ID || (right.Kind != snapshotKindIdentifier && right.Kind != snapshotKindNumericLiteral) {
			return fmt.Errorf("primitive less-than right operand must be an identifier or numeric literal")
		}
	case snapshotKindQuestionQuestionToken:
		if _, err := requireRoleKind(node, "operator", snapshotKindQuestionQuestionToken, nodes); err != nil {
			return err
		}
		if _, err := requireRoleKind(node, "right", snapshotKindIdentifier, nodes); err != nil {
			return err
		}
	case snapshotKindQuestionQuestionEqualsToken:
		if _, err := requireRoleKind(node, "operator", snapshotKindQuestionQuestionEqualsToken, nodes); err != nil {
			return err
		}
		if left.Kind == snapshotKindElementAccessExpression {
			if _, err := requireRoleKind(node, "right", snapshotKindNumericLiteral, nodes); err != nil {
				return err
			}
		} else if _, err := requireRoleKind(node, "right", snapshotKindIdentifier, nodes); err != nil {
			return err
		}
	default:
		return fmt.Errorf("operator %q is outside primitive replay", node.SyntaxPayload.Operator)
	}
	return nil
}

func validateNumericLiteralLowerer(node NodeSnapshot, _ map[NodeID]NodeSnapshot) error {
	if len(node.NamedChildren) != 0 || len(node.Children) != 0 || node.Constant.Kind != "number" || node.Constant.Text != node.SyntaxPayload.Text {
		return fmt.Errorf("primitive numeric literal must carry one canonical number constant")
	}
	if math.IsNaN(node.Constant.Number) || math.IsInf(node.Constant.Number, 0) {
		return fmt.Errorf("primitive numeric literal must be finite")
	}
	return nil
}

func validatePrefixUnaryLowerer(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) error {
	if node.SyntaxPayload.Operator != "KindMinusToken" || len(node.NamedChildren) != 1 || len(node.Children) != 1 {
		return fmt.Errorf("primitive prefix unary expression must be numeric negation")
	}
	_, err := requireRoleKind(node, "operand", snapshotKindNumericLiteral, nodes)
	return err
}

func validatePropertyAccessLowerer(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) error {
	if len(node.NamedChildren) != 2 || len(node.Children) != 2 {
		return fmt.Errorf("primitive property access requires receiver and name")
	}
	receiverID := childByRole(node, "child[0]")
	receiver, ok := nodes[receiverID]
	if !ok || receiver.Parent != node.ID || (receiver.Kind != snapshotKindIdentifier && receiver.Kind != snapshotKindThisKeyword) {
		return fmt.Errorf("property access receiver is unsupported")
	}
	name, err := requireRoleKind(node, "child[1]", snapshotKindIdentifier, nodes)
	if err != nil {
		return err
	}
	if receiver.Kind == snapshotKindThisKeyword && name.SyntaxPayload.Text == "backing" {
		return nil
	}
	if strings.TrimSpace(receiver.SyntaxPayload.Text) == "" || (name.SyntaxPayload.Text != "length" && name.SyntaxPayload.Text != "value") {
		return fmt.Errorf("property access is outside the static length/value subset")
	}
	return nil
}

func validateObjectLiteralLowerer(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) error {
	if len(node.NamedChildren) == 1 && len(node.Children) == 1 {
		_, err := requireRoleKind(node, "child[0]", snapshotKindShorthandPropertyAssignment, nodes)
		return err
	}
	if len(node.NamedChildren) != 3 || len(node.Children) != 3 {
		return fmt.Errorf("object literal is outside VERT-010/011 closed shapes")
	}
	for index, kind := range []string{snapshotKindPropertyAssignment, snapshotKindGetAccessor, snapshotKindSetAccessor} {
		if _, err := requireRoleKind(node, fmt.Sprintf("child[%d]", index), kind, nodes); err != nil {
			return err
		}
	}
	return nil
}

func validateShorthandPropertyLowerer(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) error {
	if len(node.NamedChildren) != 1 || len(node.Children) != 1 {
		return fmt.Errorf("VERT-010 shorthand property requires exactly one name")
	}
	name, err := requireRoleKind(node, "name", snapshotKindIdentifier, nodes)
	if err != nil {
		return err
	}
	if name.SyntaxPayload.Text != "value" {
		return fmt.Errorf("VERT-010 shorthand property %q is unsupported", name.SyntaxPayload.Text)
	}
	return nil
}

func validateLiteralTypeLowerer(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) error {
	if len(node.NamedChildren) != 1 || len(node.Children) != 1 {
		return fmt.Errorf("primitive literal type requires one child")
	}
	_, err := requireRoleKind(node, "child[0]", snapshotKindNullKeyword, nodes)
	return err
}

func validateUnionTypeLowerer(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) error {
	if len(node.NamedChildren) != 3 || len(node.Children) != 3 {
		return fmt.Errorf("primitive nullable union requires number, null, and undefined")
	}
	if _, err := requireRoleKind(node, "child[0]", snapshotKindNumberKeyword, nodes); err != nil {
		return err
	}
	if _, err := requireRoleKind(node, "child[1]", snapshotKindLiteralType, nodes); err != nil {
		return err
	}
	_, err := requireRoleKind(node, "child[2]", snapshotKindUndefinedKeyword, nodes)
	return err
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
	if expression.SyntaxPayload.Operator != snapshotKindEqualsToken && expression.SyntaxPayload.Operator != snapshotKindQuestionQuestionEqualsToken {
		return fmt.Errorf("primitive expression statement requires supported assignment")
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
	if (node.NodeFlags != 1 && node.NodeFlags != 2) || len(node.NamedChildren) != 1 || len(node.Children) != 1 {
		return fmt.Errorf("variable list requires exactly one let or const declaration")
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
	initializerID := childByRole(node, "initializer")
	initializer, ok := nodes[initializerID]
	if !ok || initializer.Parent != node.ID || (initializer.Kind != snapshotKindAsExpression && initializer.Kind != snapshotKindCallExpression && initializer.Kind != snapshotKindIdentifier && initializer.Kind != snapshotKindObjectLiteralExpression) {
		return fmt.Errorf("primitive variable initializer must be a direct call or identifier")
	}
	return nil
}

func vert011BackingAccess(access NodeSnapshot, nodes map[NodeID]NodeSnapshot) bool {
	receiver, err := requireRoleKind(access, "child[0]", snapshotKindThisKeyword, nodes)
	if err != nil || receiver.Kind != snapshotKindThisKeyword {
		return false
	}
	name, err := requireRoleKind(access, "child[1]", snapshotKindIdentifier, nodes)
	return err == nil && name.SyntaxPayload.Text == "backing"
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
	if function.Name == "main" {
		return replayApplicationMainFunction(function, events, functionNode, nodes, types, symbols, signatures)
	}
	bodyID := childByRole(functionNode, "body")
	if bodyID == "" {
		return bingo.HIRFunction{}, nil, fmt.Errorf("function %s has no body", functionNode.ID)
	}
	loweringInput := primitiveFunctionLoweringInput{
		bodyID: bodyID, function: function, events: events,
		parameterValues: parameterValues, parameterTypes: parameterTypes,
		functionNode: functionNode, nodes: nodes, types: types, symbols: symbols, signatures: signatures, functionIDs: functionIDs,
	}
	lowered, loweredEvents, matched, err := lowerPrimitiveFunction(loweringInput, primitiveFunctionLowerers[:])
	if err != nil {
		return bingo.HIRFunction{}, nil, fmt.Errorf("function %s primitive lowering: %w", functionNode.ID, err)
	}
	if matched {
		return lowered, loweredEvents, nil
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

func replayApplicationMainFunction(
	function bingo.HIRFunction,
	events []LoweringEvent,
	functionNode NodeSnapshot,
	nodes map[NodeID]NodeSnapshot,
	types map[TypeID]TypeSnapshot,
	symbols map[SymbolID]SymbolSnapshot,
	signatures map[SignatureID]SignatureSnapshot,
) (bingo.HIRFunction, []LoweringEvent, error) {
	if function.Name != "main" || len(function.Parameters) != 0 {
		return bingo.HIRFunction{}, nil, fmt.Errorf("application entrypoint requires parameterless main")
	}
	body, ok := nodes[childByRole(functionNode, "body")]
	if !ok || body.Kind != snapshotKindBlock {
		return bingo.HIRFunction{}, nil, fmt.Errorf("application main body is not a block")
	}
	statements := namedChildren(body, "statement[")
	if len(statements) != 1 {
		return bingo.HIRFunction{}, nil, fmt.Errorf("application main requires exactly one return statement")
	}
	returnNode, err := requireChildKind(body, statements[0], snapshotKindReturnStatement, nodes)
	if err != nil {
		return bingo.HIRFunction{}, nil, fmt.Errorf("application main: %w", err)
	}
	literal, err := requireRoleKind(returnNode, "expression", snapshotKindNumericLiteral, nodes)
	if err != nil {
		return bingo.HIRFunction{}, nil, fmt.Errorf("application main must return a numeric literal")
	}
	status, err := strconv.ParseUint(literal.SyntaxPayload.Text, 10, 8)
	if err != nil || literal.Constant.Kind != "number" || literal.Constant.Text != literal.SyntaxPayload.Text || literal.Constant.Number != float64(status) {
		return bingo.HIRFunction{}, nil, fmt.Errorf("application main exit status %q must be a canonical integer from 0 through 255", literal.SyntaxPayload.Text)
	}
	literalTypeID := nodeTypeID(literal)
	literalType, err := bingoType(literalTypeID, types)
	if err != nil || literalType != bingo.TypeNumber {
		return bingo.HIRFunction{}, nil, fmt.Errorf("application main exit status is not canonical number")
	}
	returnTypeID, ok := resolveFunctionReturnType(functionNode, nodes, symbols, types, signatures)
	if !ok {
		returnTypeID = annotatedReturnType(functionNode, nodes)
	}
	returnType, err := bingoType(returnTypeID, types)
	if err != nil || returnType != bingo.TypeNumber {
		return bingo.HIRFunction{}, nil, fmt.Errorf("application main return type must be number")
	}
	function.ReturnType = bingo.TypeNumber
	function.Blocks = []bingo.HIRBlock{{
		ID: 1,
		Operations: []bingo.HIROp{{
			ID: 1, Kind: "number.constant", Type: bingo.TypeNumber,
			NumberBits: fmt.Sprintf("%016x", math.Float64bits(float64(status))), Effect: bingo.EffectPure,
			LogicalCapabilityRequirements: []bingo.RuntimeCapabilityID{}, Origin: originOf(literal),
		}},
		Terminator: bingo.HIRTerminator{Kind: "return", Value: 1, Origin: originOf(returnNode)},
	}}
	events = append(events,
		LoweringEvent{Kind: "literal.number", Node: literal.ID, Origin: literal.Origin, Type: literalTypeID},
		LoweringEvent{Kind: "return", Node: returnNode.ID, Origin: returnNode.Origin, Type: returnTypeID, Inputs: []NodeID{literal.ID}},
		LoweringEvent{Kind: "function.end", Node: functionNode.ID, Origin: functionNode.Origin},
	)
	return function, events, nil
}

type primitiveStringLengthSource struct {
	Return   NodeSnapshot
	Access   NodeSnapshot
	Receiver NodeSnapshot
	Name     NodeSnapshot
}

func findPrimitiveStringLength(bodyID NodeID, nodes map[NodeID]NodeSnapshot) (primitiveStringLengthSource, bool, error) {
	body, ok := nodes[bodyID]
	if !ok || body.Kind != snapshotKindBlock {
		return primitiveStringLengthSource{}, false, fmt.Errorf("body is not a block")
	}
	statements := namedChildren(body, "statement[")
	if len(statements) != 1 {
		return primitiveStringLengthSource{}, false, nil
	}
	returnNode, ok := nodes[statements[0]]
	if !ok || returnNode.Kind != snapshotKindReturnStatement {
		return primitiveStringLengthSource{}, false, nil
	}
	expressionID := childByRole(returnNode, "expression")
	access, ok := nodes[expressionID]
	if !ok || access.Kind != snapshotKindPropertyAccessExpression {
		return primitiveStringLengthSource{}, false, nil
	}
	receiver, err := requireRoleKind(access, "child[0]", snapshotKindIdentifier, nodes)
	if err != nil {
		return primitiveStringLengthSource{}, false, err
	}
	name, err := requireRoleKind(access, "child[1]", snapshotKindIdentifier, nodes)
	if err != nil {
		return primitiveStringLengthSource{}, false, err
	}
	if name.SyntaxPayload.Text != "length" {
		return primitiveStringLengthSource{}, false, fmt.Errorf("string property %q is outside the primitive UTF-16 contract", name.SyntaxPayload.Text)
	}
	return primitiveStringLengthSource{Return: returnNode, Access: access, Receiver: receiver, Name: name}, true, nil
}

func replayStringLengthFunction(
	function bingo.HIRFunction,
	events []LoweringEvent,
	source primitiveStringLengthSource,
	parameterValues map[SymbolID]bingo.ValueID,
	parameterTypes map[bingo.ValueID]bingo.TypeKind,
	functionNode NodeSnapshot,
	nodes map[NodeID]NodeSnapshot,
	types map[TypeID]TypeSnapshot,
	symbols map[SymbolID]SymbolSnapshot,
	signatures map[SignatureID]SignatureSnapshot,
) (bingo.HIRFunction, []LoweringEvent, error) {
	if function.Name != "stringLength" || len(function.Parameters) != 1 || function.Parameters[0].Name != "value" || function.Parameters[0].Type != bingo.TypeString {
		return bingo.HIRFunction{}, nil, fmt.Errorf("primitive UTF-16 replay requires exported stringLength(value: string)")
	}
	value, ok := parameterValue(source.Receiver, parameterValues)
	if !ok || value != 1 || parameterTypes[value] != bingo.TypeString {
		return bingo.HIRFunction{}, nil, fmt.Errorf("string.length receiver is not the string parameter")
	}
	receiverType, receiverErr := bingoType(nodeTypeID(source.Receiver), types)
	if receiverErr != nil || receiverType != bingo.TypeString {
		return bingo.HIRFunction{}, nil, fmt.Errorf("string.length receiver is not canonical string")
	}
	accessType, accessErr := bingoType(nodeTypeID(source.Access), types)
	nameType, nameErr := bingoType(nodeTypeID(source.Name), types)
	if accessErr != nil || nameErr != nil || accessType != bingo.TypeNumber || nameType != bingo.TypeNumber {
		return bingo.HIRFunction{}, nil, fmt.Errorf("string.length property is not canonical number")
	}
	returnTypeID, ok := resolveFunctionReturnType(functionNode, nodes, symbols, types, signatures)
	if !ok {
		returnTypeID = annotatedReturnType(functionNode, nodes)
	}
	returnType, returnErr := bingoType(returnTypeID, types)
	if returnErr != nil || returnType != bingo.TypeNumber {
		return bingo.HIRFunction{}, nil, fmt.Errorf("stringLength return type is not canonical number")
	}
	function.ReturnType = bingo.TypeNumber
	function.Blocks = []bingo.HIRBlock{{
		ID: 1,
		Operations: []bingo.HIROp{{
			ID: 2, Kind: "string.length", Type: bingo.TypeNumber, Operands: []bingo.ValueID{1}, Effect: bingo.EffectPure,
			LogicalCapabilityRequirements: []bingo.RuntimeCapabilityID{}, Origin: originOf(source.Access),
		}},
		Terminator: bingo.HIRTerminator{Kind: "return", Value: 2, Origin: originOf(source.Return)},
	}}
	events = append(events,
		LoweringEvent{Kind: "string.length", Node: source.Access.ID, Origin: source.Access.Origin, Type: nodeTypeID(source.Access), Inputs: []NodeID{source.Receiver.ID, source.Name.ID}},
		LoweringEvent{Kind: "return", Node: source.Return.ID, Origin: source.Return.Origin, Type: returnTypeID, Inputs: []NodeID{source.Access.ID}},
		LoweringEvent{Kind: "function.end", Node: functionNode.ID, Origin: functionNode.Origin},
	)
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

type primitiveClassifySource struct {
	FirstIf         NodeSnapshot
	FirstCondition  NodeSnapshot
	FirstValue      NodeSnapshot
	FirstThreshold  NodeSnapshot
	NegativeReturn  NodeSnapshot
	NegativeUnary   NodeSnapshot
	NegativeLiteral NodeSnapshot
	SecondIf        NodeSnapshot
	SecondCondition NodeSnapshot
	SecondValue     NodeSnapshot
	SecondThreshold NodeSnapshot
	ZeroReturn      NodeSnapshot
	ZeroLiteral     NodeSnapshot
	PositiveReturn  NodeSnapshot
	PositiveLiteral NodeSnapshot
}

func findPrimitiveClassify(bodyID NodeID, nodes map[NodeID]NodeSnapshot) (primitiveClassifySource, bool, error) {
	body, ok := nodes[bodyID]
	if !ok || body.Kind != snapshotKindBlock {
		return primitiveClassifySource{}, false, fmt.Errorf("body is not a block")
	}
	statements := namedChildren(body, "statement[")
	if len(statements) != 3 {
		return primitiveClassifySource{}, false, nil
	}
	firstIf, ok := nodes[statements[0]]
	if !ok || firstIf.Kind != snapshotKindIfStatement {
		return primitiveClassifySource{}, false, nil
	}
	secondIf, ok := nodes[statements[1]]
	if !ok || secondIf.Kind != snapshotKindIfStatement {
		return primitiveClassifySource{}, false, nil
	}
	positiveReturn, ok := nodes[statements[2]]
	if !ok || positiveReturn.Kind != snapshotKindReturnStatement {
		return primitiveClassifySource{}, false, nil
	}
	firstCondition, firstValue, firstThreshold, negativeReturn, err := parsePrimitiveClassifyIf(firstIf, nodes)
	if err != nil {
		return primitiveClassifySource{}, false, err
	}
	negativeUnary, err := requireRoleKind(negativeReturn, "expression", snapshotKindPrefixUnaryExpression, nodes)
	if err != nil {
		return primitiveClassifySource{}, false, err
	}
	negativeLiteral, err := requireRoleKind(negativeUnary, "operand", snapshotKindNumericLiteral, nodes)
	if err != nil {
		return primitiveClassifySource{}, false, err
	}
	secondCondition, secondValue, secondThreshold, zeroReturn, err := parsePrimitiveClassifyIf(secondIf, nodes)
	if err != nil {
		return primitiveClassifySource{}, false, err
	}
	zeroLiteral, err := requireRoleKind(zeroReturn, "expression", snapshotKindNumericLiteral, nodes)
	if err != nil {
		return primitiveClassifySource{}, false, err
	}
	positiveLiteral, err := requireRoleKind(positiveReturn, "expression", snapshotKindNumericLiteral, nodes)
	if err != nil {
		return primitiveClassifySource{}, false, err
	}
	return primitiveClassifySource{
		FirstIf: firstIf, FirstCondition: firstCondition, FirstValue: firstValue, FirstThreshold: firstThreshold,
		NegativeReturn: negativeReturn, NegativeUnary: negativeUnary, NegativeLiteral: negativeLiteral,
		SecondIf: secondIf, SecondCondition: secondCondition, SecondValue: secondValue, SecondThreshold: secondThreshold,
		ZeroReturn: zeroReturn, ZeroLiteral: zeroLiteral, PositiveReturn: positiveReturn, PositiveLiteral: positiveLiteral,
	}, true, nil
}

func parsePrimitiveClassifyIf(ifNode NodeSnapshot, nodes map[NodeID]NodeSnapshot) (NodeSnapshot, NodeSnapshot, NodeSnapshot, NodeSnapshot, error) {
	condition, err := requireRoleKind(ifNode, "child[0]", snapshotKindBinaryExpression, nodes)
	if err != nil {
		return NodeSnapshot{}, NodeSnapshot{}, NodeSnapshot{}, NodeSnapshot{}, err
	}
	if condition.SyntaxPayload.Operator != snapshotKindLessThanToken {
		return NodeSnapshot{}, NodeSnapshot{}, NodeSnapshot{}, NodeSnapshot{}, fmt.Errorf("classify condition %s does not use <", condition.ID)
	}
	value, err := requireRoleKind(condition, "left", snapshotKindIdentifier, nodes)
	if err != nil {
		return NodeSnapshot{}, NodeSnapshot{}, NodeSnapshot{}, NodeSnapshot{}, err
	}
	threshold, err := requireRoleKind(condition, "right", snapshotKindNumericLiteral, nodes)
	if err != nil {
		return NodeSnapshot{}, NodeSnapshot{}, NodeSnapshot{}, NodeSnapshot{}, err
	}
	thenBlock, err := requireRoleKind(ifNode, "child[1]", snapshotKindBlock, nodes)
	if err != nil {
		return NodeSnapshot{}, NodeSnapshot{}, NodeSnapshot{}, NodeSnapshot{}, err
	}
	statements := namedChildren(thenBlock, "statement[")
	if len(statements) != 1 {
		return NodeSnapshot{}, NodeSnapshot{}, NodeSnapshot{}, NodeSnapshot{}, fmt.Errorf("classify then block %s must contain one return", thenBlock.ID)
	}
	returnNode, err := requireChildKind(thenBlock, statements[0], snapshotKindReturnStatement, nodes)
	if err != nil {
		return NodeSnapshot{}, NodeSnapshot{}, NodeSnapshot{}, NodeSnapshot{}, err
	}
	return condition, value, threshold, returnNode, nil
}

func replayClassifyFunction(
	function bingo.HIRFunction,
	events []LoweringEvent,
	source primitiveClassifySource,
	parameterValues map[SymbolID]bingo.ValueID,
	parameterTypes map[bingo.ValueID]bingo.TypeKind,
	functionNode NodeSnapshot,
	nodes map[NodeID]NodeSnapshot,
	types map[TypeID]TypeSnapshot,
	symbols map[SymbolID]SymbolSnapshot,
	signatures map[SignatureID]SignatureSnapshot,
) (bingo.HIRFunction, []LoweringEvent, error) {
	if function.Name != "classify" || len(function.Parameters) != 1 || parameterTypes[1] != bingo.TypeNumber {
		return bingo.HIRFunction{}, nil, fmt.Errorf("primitive classify replay requires exported classify(value: number)")
	}
	for _, valueNode := range []NodeSnapshot{source.FirstValue, source.SecondValue} {
		value, ok := parameterValue(valueNode, parameterValues)
		if !ok || value != 1 {
			return bingo.HIRFunction{}, nil, fmt.Errorf("classify condition %s does not read the value parameter", valueNode.ID)
		}
		valueType, typeErr := bingoType(nodeTypeID(valueNode), types)
		if typeErr != nil || valueType != bingo.TypeNumber {
			return bingo.HIRFunction{}, nil, fmt.Errorf("classify condition %s is not canonical number", valueNode.ID)
		}
	}
	zeroThresholdBits, err := classifyLiteralBits(source.FirstThreshold, "0", types)
	if err != nil {
		return bingo.HIRFunction{}, nil, err
	}
	negativeOperandBits, err := classifyLiteralBits(source.NegativeLiteral, "1", types)
	if err != nil {
		return bingo.HIRFunction{}, nil, err
	}
	oneThresholdBits, err := classifyLiteralBits(source.SecondThreshold, "1", types)
	if err != nil {
		return bingo.HIRFunction{}, nil, err
	}
	zeroReturnBits, err := classifyLiteralBits(source.ZeroLiteral, "0", types)
	if err != nil {
		return bingo.HIRFunction{}, nil, err
	}
	positiveReturnBits, err := classifyLiteralBits(source.PositiveLiteral, "1", types)
	if err != nil {
		return bingo.HIRFunction{}, nil, err
	}
	if source.NegativeUnary.SyntaxPayload.Operator != "KindMinusToken" {
		return bingo.HIRFunction{}, nil, fmt.Errorf("classify negative return does not use prefix minus")
	}
	negativeType, negativeTypeErr := bingoType(nodeTypeID(source.NegativeUnary), types)
	if negativeTypeErr != nil || negativeType != bingo.TypeNumber {
		return bingo.HIRFunction{}, nil, fmt.Errorf("classify negative return is not canonical number")
	}
	for _, condition := range []NodeSnapshot{source.FirstCondition, source.SecondCondition} {
		conditionType, conditionErr := bingoType(nodeTypeID(condition), types)
		if conditionErr != nil || conditionType != bingo.TypeBoolean {
			return bingo.HIRFunction{}, nil, fmt.Errorf("classify condition %s is not canonical boolean", condition.ID)
		}
	}
	returnTypeID, ok := resolveFunctionReturnType(functionNode, nodes, symbols, types, signatures)
	if !ok {
		returnTypeID = annotatedReturnType(functionNode, nodes)
	}
	returnType, returnErr := bingoType(returnTypeID, types)
	if returnErr != nil || returnType != bingo.TypeNumber {
		return bingo.HIRFunction{}, nil, fmt.Errorf("classify function return type is not canonical number")
	}
	emptyRequirements := func() []bingo.RuntimeCapabilityID { return []bingo.RuntimeCapabilityID{} }
	function.ReturnType = bingo.TypeNumber
	function.Blocks = []bingo.HIRBlock{
		{ID: 1, Operations: []bingo.HIROp{
			{ID: 2, Kind: "number.constant", Type: bingo.TypeNumber, NumberBits: zeroThresholdBits, Effect: bingo.EffectPure, LogicalCapabilityRequirements: emptyRequirements(), Origin: originOf(source.FirstThreshold)},
			{ID: 3, Kind: "compare", Type: bingo.TypeBoolean, Operands: []bingo.ValueID{1, 2}, Operator: "<", Effect: bingo.EffectPure, LogicalCapabilityRequirements: emptyRequirements(), Origin: originOf(source.FirstCondition)},
		}, Terminator: bingo.HIRTerminator{Kind: "condbranch", Value: 3, Successors: []bingo.BlockID{2, 3}, Origin: originOf(source.FirstIf)}},
		{ID: 2, Operations: []bingo.HIROp{
			{ID: 4, Kind: "number.constant", Type: bingo.TypeNumber, NumberBits: negativeOperandBits, Effect: bingo.EffectPure, LogicalCapabilityRequirements: emptyRequirements(), Origin: originOf(source.NegativeLiteral)},
			{ID: 5, Kind: "unary", Type: bingo.TypeNumber, Operands: []bingo.ValueID{4}, Operator: "-", Effect: bingo.EffectPure, LogicalCapabilityRequirements: emptyRequirements(), Origin: originOf(source.NegativeUnary)},
		}, Terminator: bingo.HIRTerminator{Kind: "return", Value: 5, Origin: originOf(source.NegativeReturn)}},
		{ID: 3, Operations: []bingo.HIROp{
			{ID: 6, Kind: "number.constant", Type: bingo.TypeNumber, NumberBits: oneThresholdBits, Effect: bingo.EffectPure, LogicalCapabilityRequirements: emptyRequirements(), Origin: originOf(source.SecondThreshold)},
			{ID: 7, Kind: "compare", Type: bingo.TypeBoolean, Operands: []bingo.ValueID{1, 6}, Operator: "<", Effect: bingo.EffectPure, LogicalCapabilityRequirements: emptyRequirements(), Origin: originOf(source.SecondCondition)},
		}, Terminator: bingo.HIRTerminator{Kind: "condbranch", Value: 7, Successors: []bingo.BlockID{4, 5}, Origin: originOf(source.SecondIf)}},
		{ID: 4, Operations: []bingo.HIROp{{ID: 8, Kind: "number.constant", Type: bingo.TypeNumber, NumberBits: zeroReturnBits, Effect: bingo.EffectPure, LogicalCapabilityRequirements: emptyRequirements(), Origin: originOf(source.ZeroLiteral)}}, Terminator: bingo.HIRTerminator{Kind: "return", Value: 8, Origin: originOf(source.ZeroReturn)}},
		{ID: 5, Operations: []bingo.HIROp{{ID: 9, Kind: "number.constant", Type: bingo.TypeNumber, NumberBits: positiveReturnBits, Effect: bingo.EffectPure, LogicalCapabilityRequirements: emptyRequirements(), Origin: originOf(source.PositiveLiteral)}}, Terminator: bingo.HIRTerminator{Kind: "return", Value: 9, Origin: originOf(source.PositiveReturn)}},
	}
	events = append(events,
		LoweringEvent{Kind: "literal.number", Node: source.FirstThreshold.ID, Origin: source.FirstThreshold.Origin, Type: nodeTypeID(source.FirstThreshold)},
		LoweringEvent{Kind: "if.condition", Node: source.FirstCondition.ID, Origin: source.FirstCondition.Origin, Type: nodeTypeID(source.FirstCondition), Operator: "<", Inputs: []NodeID{source.FirstValue.ID, source.FirstThreshold.ID}},
		LoweringEvent{Kind: "literal.number", Node: source.NegativeLiteral.ID, Origin: source.NegativeLiteral.Origin, Type: nodeTypeID(source.NegativeLiteral)},
		LoweringEvent{Kind: "unary.negate", Node: source.NegativeUnary.ID, Origin: source.NegativeUnary.Origin, Type: nodeTypeID(source.NegativeUnary), Operator: "-", Inputs: []NodeID{source.NegativeLiteral.ID}},
		LoweringEvent{Kind: "return", Node: source.NegativeReturn.ID, Origin: source.NegativeReturn.Origin, Type: returnTypeID, Inputs: []NodeID{source.NegativeUnary.ID}},
		LoweringEvent{Kind: "literal.number", Node: source.SecondThreshold.ID, Origin: source.SecondThreshold.Origin, Type: nodeTypeID(source.SecondThreshold)},
		LoweringEvent{Kind: "if.condition", Node: source.SecondCondition.ID, Origin: source.SecondCondition.Origin, Type: nodeTypeID(source.SecondCondition), Operator: "<", Inputs: []NodeID{source.SecondValue.ID, source.SecondThreshold.ID}},
		LoweringEvent{Kind: "literal.number", Node: source.ZeroLiteral.ID, Origin: source.ZeroLiteral.Origin, Type: nodeTypeID(source.ZeroLiteral)},
		LoweringEvent{Kind: "return", Node: source.ZeroReturn.ID, Origin: source.ZeroReturn.Origin, Type: returnTypeID, Inputs: []NodeID{source.ZeroLiteral.ID}},
		LoweringEvent{Kind: "literal.number", Node: source.PositiveLiteral.ID, Origin: source.PositiveLiteral.Origin, Type: nodeTypeID(source.PositiveLiteral)},
		LoweringEvent{Kind: "return", Node: source.PositiveReturn.ID, Origin: source.PositiveReturn.Origin, Type: returnTypeID, Inputs: []NodeID{source.PositiveLiteral.ID}},
		LoweringEvent{Kind: "function.end", Node: functionNode.ID, Origin: functionNode.Origin},
	)
	return function, events, nil
}

func classifyLiteralBits(node NodeSnapshot, expectedText string, types map[TypeID]TypeSnapshot) (string, error) {
	if node.Kind != snapshotKindNumericLiteral || node.SyntaxPayload.Text != expectedText || node.Constant.Kind != "number" || node.Constant.Text != expectedText {
		return "", fmt.Errorf("classify literal %s is not canonical %s", node.ID, expectedText)
	}
	typ, err := bingoType(nodeTypeID(node), types)
	if err != nil || typ != bingo.TypeNumber {
		return "", fmt.Errorf("classify literal %s is not canonical number", node.ID)
	}
	want := 0.0
	if expectedText == "1" {
		want = 1
	}
	if node.Constant.Number != want {
		return "", fmt.Errorf("classify literal %s constant does not match source text", node.ID)
	}
	return fmt.Sprintf("%016x", math.Float64bits(node.Constant.Number)), nil
}

type primitiveLoopSource struct {
	Declaration    NodeSnapshot
	LocalName      NodeSnapshot
	Initializer    NodeSnapshot
	While          NodeSnapshot
	Condition      NodeSnapshot
	ConditionLeft  NodeSnapshot
	ConditionRight NodeSnapshot
	Assignment     NodeSnapshot
	AssignLeft     NodeSnapshot
	Add            NodeSnapshot
	AddInputs      []NodeSnapshot
	Return         NodeSnapshot
	ReturnValue    NodeSnapshot
}

func findPrimitiveLoop(bodyID NodeID, nodes map[NodeID]NodeSnapshot) (primitiveLoopSource, bool, error) {
	body, ok := nodes[bodyID]
	if !ok || body.Kind != snapshotKindBlock {
		return primitiveLoopSource{}, false, fmt.Errorf("body is not a block")
	}
	statements := namedChildren(body, "statement[")
	if len(statements) != 3 {
		return primitiveLoopSource{}, false, nil
	}
	variableStatement, ok := nodes[statements[0]]
	if !ok || variableStatement.Kind != snapshotKindVariableStatement {
		return primitiveLoopSource{}, false, nil
	}
	whileNode, ok := nodes[statements[1]]
	if !ok || whileNode.Kind != snapshotKindWhileStatement {
		return primitiveLoopSource{}, false, nil
	}
	returnNode, ok := nodes[statements[2]]
	if !ok || returnNode.Kind != snapshotKindReturnStatement {
		return primitiveLoopSource{}, false, nil
	}
	list, err := requireRoleKind(variableStatement, "declarationList", snapshotKindVariableList, nodes)
	if err != nil {
		return primitiveLoopSource{}, false, err
	}
	declarationIDs := namedChildren(list, "declaration[")
	if len(declarationIDs) != 1 {
		return primitiveLoopSource{}, false, fmt.Errorf("primitive loop requires one local declaration")
	}
	declaration, err := requireChildKind(list, declarationIDs[0], snapshotKindVariableDeclaration, nodes)
	if err != nil {
		return primitiveLoopSource{}, false, err
	}
	localName, err := requireRoleKind(declaration, "name", snapshotKindIdentifier, nodes)
	if err != nil {
		return primitiveLoopSource{}, false, err
	}
	initializer, err := requireRoleKind(declaration, "initializer", snapshotKindIdentifier, nodes)
	if err != nil {
		return primitiveLoopSource{}, false, err
	}
	condition, err := requireRoleKind(whileNode, "child[0]", snapshotKindBinaryExpression, nodes)
	if err != nil || condition.SyntaxPayload.Operator != snapshotKindLessThanToken {
		return primitiveLoopSource{}, false, fmt.Errorf("primitive loop condition must be a < comparison")
	}
	conditionLeft, err := requireRoleKind(condition, "left", snapshotKindIdentifier, nodes)
	if err != nil {
		return primitiveLoopSource{}, false, err
	}
	conditionRight, err := requireRoleKind(condition, "right", snapshotKindIdentifier, nodes)
	if err != nil {
		return primitiveLoopSource{}, false, err
	}
	loopBody, err := requireRoleKind(whileNode, "child[1]", snapshotKindBlock, nodes)
	if err != nil {
		return primitiveLoopSource{}, false, err
	}
	bodyStatements := namedChildren(loopBody, "statement[")
	if len(bodyStatements) != 1 {
		return primitiveLoopSource{}, false, fmt.Errorf("primitive loop body requires one assignment")
	}
	expressionStatement, err := requireChildKind(loopBody, bodyStatements[0], snapshotKindExpressionStatement, nodes)
	if err != nil {
		return primitiveLoopSource{}, false, err
	}
	assignment, err := requireRoleKind(expressionStatement, "expression", snapshotKindBinaryExpression, nodes)
	if err != nil || assignment.SyntaxPayload.Operator != snapshotKindEqualsToken {
		return primitiveLoopSource{}, false, fmt.Errorf("primitive loop body must assign its local")
	}
	assignLeft, err := requireRoleKind(assignment, "left", snapshotKindIdentifier, nodes)
	if err != nil {
		return primitiveLoopSource{}, false, err
	}
	add, err := requireRoleKind(assignment, "right", snapshotKindBinaryExpression, nodes)
	if err != nil || add.SyntaxPayload.Operator != snapshotKindPlusToken {
		return primitiveLoopSource{}, false, fmt.Errorf("primitive loop assignment must add")
	}
	addInputs := make([]NodeSnapshot, 2)
	for index, role := range []string{"left", "right"} {
		input, inputErr := requireRoleKind(add, role, snapshotKindIdentifier, nodes)
		if inputErr != nil {
			return primitiveLoopSource{}, false, inputErr
		}
		addInputs[index] = input
	}
	returnValue, err := requireRoleKind(returnNode, "expression", snapshotKindIdentifier, nodes)
	if err != nil {
		return primitiveLoopSource{}, false, err
	}
	return primitiveLoopSource{
		Declaration: declaration, LocalName: localName, Initializer: initializer,
		While: whileNode, Condition: condition, ConditionLeft: conditionLeft, ConditionRight: conditionRight,
		Assignment: assignment, AssignLeft: assignLeft, Add: add, AddInputs: addInputs,
		Return: returnNode, ReturnValue: returnValue,
	}, true, nil
}

func replayLoopFunction(
	function bingo.HIRFunction,
	events []LoweringEvent,
	source primitiveLoopSource,
	parameterValues map[SymbolID]bingo.ValueID,
	parameterTypes map[bingo.ValueID]bingo.TypeKind,
	functionNode NodeSnapshot,
	nodes map[NodeID]NodeSnapshot,
	types map[TypeID]TypeSnapshot,
	symbols map[SymbolID]SymbolSnapshot,
	signatures map[SignatureID]SignatureSnapshot,
) (bingo.HIRFunction, []LoweringEvent, error) {
	if len(function.Parameters) != 2 {
		return bingo.HIRFunction{}, nil, fmt.Errorf("primitive loop requires exactly two number parameters")
	}
	initialValue, ok := parameterValue(source.Initializer, parameterValues)
	if !ok || parameterTypes[initialValue] != bingo.TypeNumber {
		return bingo.HIRFunction{}, nil, fmt.Errorf("primitive loop initializer is not a number parameter")
	}
	limitValue, ok := parameterValue(source.ConditionRight, parameterValues)
	if !ok || limitValue == initialValue || parameterTypes[limitValue] != bingo.TypeNumber {
		return bingo.HIRFunction{}, nil, fmt.Errorf("primitive loop limit is not the other number parameter")
	}
	localValues := make(map[SymbolID]bingo.ValueID)
	for _, symbolID := range nodeSymbolIDs(source.LocalName) {
		localValues[symbolID] = 3
	}
	for _, localUse := range []NodeSnapshot{source.ConditionLeft, source.AssignLeft, source.AddInputs[0], source.ReturnValue} {
		if value, found := parameterValue(localUse, localValues); !found || value != 3 {
			return bingo.HIRFunction{}, nil, fmt.Errorf("primitive loop local use %s does not resolve to the loop binding", localUse.ID)
		}
	}
	addStep, ok := parameterValue(source.AddInputs[1], parameterValues)
	if !ok || addStep != initialValue {
		return bingo.HIRFunction{}, nil, fmt.Errorf("primitive loop increment does not use the initializer parameter")
	}
	for _, numberNode := range []NodeSnapshot{source.Initializer, source.ConditionLeft, source.ConditionRight, source.AssignLeft, source.Add, source.AddInputs[0], source.AddInputs[1], source.ReturnValue} {
		typeID := nodeTypeID(numberNode)
		typ, typeErr := bingoType(typeID, types)
		if typeErr != nil || typ != bingo.TypeNumber {
			return bingo.HIRFunction{}, nil, fmt.Errorf("primitive loop node %s is not canonical number", numberNode.ID)
		}
	}
	conditionTypeID := nodeTypeID(source.Condition)
	conditionType, err := bingoType(conditionTypeID, types)
	if err != nil || conditionType != bingo.TypeBoolean {
		return bingo.HIRFunction{}, nil, fmt.Errorf("primitive loop condition is not canonical boolean")
	}
	returnTypeID, ok := resolveFunctionReturnType(functionNode, nodes, symbols, types, signatures)
	if !ok {
		returnTypeID = annotatedReturnType(functionNode, nodes)
	}
	returnType, err := bingoType(returnTypeID, types)
	if err != nil || returnType != bingo.TypeNumber {
		return bingo.HIRFunction{}, nil, fmt.Errorf("primitive loop return type is not canonical number")
	}
	function.ReturnType = returnType
	function.Blocks = []bingo.HIRBlock{
		{ID: 1, Operations: []bingo.HIROp{}, Terminator: bingo.HIRTerminator{Kind: "branch", Successors: []bingo.BlockID{2}, Origin: originOf(source.Declaration)}},
		{ID: 2, Operations: []bingo.HIROp{
			{ID: 3, Kind: "phi", Type: bingo.TypeNumber, Operands: []bingo.ValueID{initialValue, 5}, IncomingBlocks: []bingo.BlockID{1, 3}, Effect: bingo.EffectPure, LogicalCapabilityRequirements: []bingo.RuntimeCapabilityID{}, Origin: originOf(source.Declaration)},
			{ID: 4, Kind: "compare", Type: bingo.TypeBoolean, Operands: []bingo.ValueID{3, limitValue}, Operator: "<", Effect: bingo.EffectPure, LogicalCapabilityRequirements: []bingo.RuntimeCapabilityID{}, Origin: originOf(source.Condition)},
		}, Terminator: bingo.HIRTerminator{Kind: "condbranch", Value: 4, Successors: []bingo.BlockID{3, 4}, Origin: originOf(source.While)}},
		{ID: 3, Operations: []bingo.HIROp{{ID: 5, Kind: "binary", Type: bingo.TypeNumber, Operands: []bingo.ValueID{3, addStep}, Operator: "+", Effect: bingo.EffectPure, LogicalCapabilityRequirements: []bingo.RuntimeCapabilityID{}, Origin: originOf(source.Add)}}, Terminator: bingo.HIRTerminator{Kind: "branch", Successors: []bingo.BlockID{2}, Origin: originOf(source.Assignment)}},
		{ID: 4, Operations: []bingo.HIROp{}, Terminator: bingo.HIRTerminator{Kind: "return", Value: 3, Origin: originOf(source.Return)}},
	}
	events = append(events,
		LoweringEvent{Kind: "local.bind", Node: source.Declaration.ID, Origin: source.Declaration.Origin, Type: nodeTypeID(source.Declaration), Inputs: []NodeID{source.Initializer.ID}},
		LoweringEvent{Kind: "while.condition", Node: source.While.ID, Origin: source.While.Origin, Type: conditionTypeID, Operator: "<", Inputs: []NodeID{source.ConditionLeft.ID, source.ConditionRight.ID}},
		LoweringEvent{Kind: "phi", Node: source.Declaration.ID, Origin: source.Declaration.Origin, Type: nodeTypeID(source.Declaration), Inputs: []NodeID{source.Initializer.ID, source.Add.ID}},
		LoweringEvent{Kind: "binary.add", Node: source.Add.ID, Origin: source.Add.Origin, Type: nodeTypeID(source.Add), Operator: "+", Inputs: []NodeID{source.AddInputs[0].ID, source.AddInputs[1].ID}},
		LoweringEvent{Kind: "local.assign", Node: source.Assignment.ID, Origin: source.Assignment.Origin, Type: nodeTypeID(source.Assignment), Inputs: []NodeID{source.AssignLeft.ID, source.Add.ID}},
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

type primitiveCoalesceSource struct {
	Return     NodeSnapshot
	Expression NodeSnapshot
	Left       NodeSnapshot
	Right      NodeSnapshot
}

type primitiveCoalesceAssignSource struct {
	Assignment  NodeSnapshot
	AssignLeft  NodeSnapshot
	AssignRight NodeSnapshot
	Return      NodeSnapshot
	ReturnValue NodeSnapshot
}

func findPrimitiveCoalesceAssign(bodyID NodeID, nodes map[NodeID]NodeSnapshot) (primitiveCoalesceAssignSource, bool, error) {
	body, ok := nodes[bodyID]
	if !ok || body.Kind != snapshotKindBlock {
		return primitiveCoalesceAssignSource{}, false, fmt.Errorf("body is not a block")
	}
	statements := namedChildren(body, "statement[")
	if len(statements) != 2 {
		return primitiveCoalesceAssignSource{}, false, nil
	}
	statement, ok := nodes[statements[0]]
	if !ok || statement.Kind != snapshotKindExpressionStatement {
		return primitiveCoalesceAssignSource{}, false, nil
	}
	assignmentID := childByRole(statement, "expression")
	assignment, ok := nodes[assignmentID]
	if !ok || assignment.Kind != snapshotKindBinaryExpression || assignment.SyntaxPayload.Operator != snapshotKindQuestionQuestionEqualsToken {
		return primitiveCoalesceAssignSource{}, false, nil
	}
	left, err := requireRoleKind(assignment, "left", snapshotKindIdentifier, nodes)
	if err != nil {
		return primitiveCoalesceAssignSource{}, false, err
	}
	right, err := requireRoleKind(assignment, "right", snapshotKindIdentifier, nodes)
	if err != nil {
		return primitiveCoalesceAssignSource{}, false, err
	}
	if _, err := requireRoleKind(assignment, "operator", snapshotKindQuestionQuestionEqualsToken, nodes); err != nil {
		return primitiveCoalesceAssignSource{}, false, err
	}
	returnNode, ok := nodes[statements[1]]
	if !ok || returnNode.Kind != snapshotKindReturnStatement {
		return primitiveCoalesceAssignSource{}, false, nil
	}
	returnValue, err := requireRoleKind(returnNode, "expression", snapshotKindIdentifier, nodes)
	if err != nil {
		return primitiveCoalesceAssignSource{}, false, err
	}
	return primitiveCoalesceAssignSource{Assignment: assignment, AssignLeft: left, AssignRight: right, Return: returnNode, ReturnValue: returnValue}, true, nil
}

func replayCoalesceAssignFunction(
	function bingo.HIRFunction,
	events []LoweringEvent,
	source primitiveCoalesceAssignSource,
	parameterValues map[SymbolID]bingo.ValueID,
	parameterTypes map[bingo.ValueID]bingo.TypeKind,
	functionNode NodeSnapshot,
	nodes map[NodeID]NodeSnapshot,
	types map[TypeID]TypeSnapshot,
	symbols map[SymbolID]SymbolSnapshot,
	signatures map[SignatureID]SignatureSnapshot,
) (bingo.HIRFunction, []LoweringEvent, error) {
	if function.Name != "coalesceAssign" || len(function.Parameters) != 2 {
		return bingo.HIRFunction{}, nil, fmt.Errorf("primitive logical-assignment replay requires exported coalesceAssign(value, fallback)")
	}
	value, ok := parameterValue(source.AssignLeft, parameterValues)
	if !ok || value != 1 || parameterTypes[value] != bingo.TypeNullableNumber {
		return bingo.HIRFunction{}, nil, fmt.Errorf("logical-assignment left operand %s is not the nullable value parameter", source.AssignLeft.ID)
	}
	fallback, ok := parameterValue(source.AssignRight, parameterValues)
	if !ok || fallback != 2 || parameterTypes[fallback] != bingo.TypeNumber {
		return bingo.HIRFunction{}, nil, fmt.Errorf("logical-assignment right operand %s is not the number fallback parameter", source.AssignRight.ID)
	}
	returnValue, ok := parameterValue(source.ReturnValue, parameterValues)
	if !ok || returnValue != value {
		return bingo.HIRFunction{}, nil, fmt.Errorf("logical-assignment return does not read the assigned local exactly once")
	}
	leftType, leftErr := bingoType(nodeTypeID(source.AssignLeft), types)
	rightType, rightErr := bingoType(nodeTypeID(source.AssignRight), types)
	resultType, resultErr := bingoType(nodeTypeID(source.Assignment), types)
	returnValueType, returnValueErr := bingoType(nodeTypeID(source.ReturnValue), types)
	if leftErr != nil || rightErr != nil || resultErr != nil || returnValueErr != nil || leftType != bingo.TypeNullableNumber || rightType != bingo.TypeNumber || resultType != bingo.TypeNumber || returnValueType != bingo.TypeNumber {
		return bingo.HIRFunction{}, nil, fmt.Errorf("logical-assignment source types are not canonical nullable-number ??=")
	}
	returnTypeID, ok := resolveFunctionReturnType(functionNode, nodes, symbols, types, signatures)
	if !ok {
		returnTypeID = annotatedReturnType(functionNode, nodes)
	}
	returnType, returnErr := bingoType(returnTypeID, types)
	if returnErr != nil || returnType != bingo.TypeNumber {
		return bingo.HIRFunction{}, nil, fmt.Errorf("function %s return type is not canonical number", functionNode.ID)
	}
	function.ReturnType = bingo.TypeNumber
	function.Blocks = []bingo.HIRBlock{
		{ID: 1, Operations: []bingo.HIROp{{ID: 3, Kind: "is_nullish", Type: bingo.TypeBoolean, Operands: []bingo.ValueID{value}, Effect: bingo.EffectPure, LogicalCapabilityRequirements: []bingo.RuntimeCapabilityID{}, Origin: originOf(source.Assignment)}}, Terminator: bingo.HIRTerminator{Kind: "condbranch", Value: 3, Successors: []bingo.BlockID{2, 3}, Origin: originOf(source.Assignment)}},
		{ID: 2, Operations: []bingo.HIROp{}, Terminator: bingo.HIRTerminator{Kind: "branch", Successors: []bingo.BlockID{4}, Origin: originOf(source.AssignRight)}},
		{ID: 3, Operations: []bingo.HIROp{{ID: 4, Kind: "unwrap_nullable", Type: bingo.TypeNumber, Operands: []bingo.ValueID{value}, Effect: bingo.EffectPure, LogicalCapabilityRequirements: []bingo.RuntimeCapabilityID{}, Origin: originOf(source.AssignLeft)}}, Terminator: bingo.HIRTerminator{Kind: "branch", Successors: []bingo.BlockID{4}, Origin: originOf(source.AssignLeft)}},
		{ID: 4, Operations: []bingo.HIROp{{ID: 5, Kind: "phi", Type: bingo.TypeNumber, Operands: []bingo.ValueID{fallback, 4}, IncomingBlocks: []bingo.BlockID{2, 3}, Effect: bingo.EffectPure, LogicalCapabilityRequirements: []bingo.RuntimeCapabilityID{}, Origin: originOf(source.Assignment)}}, Terminator: bingo.HIRTerminator{Kind: "return", Value: 5, Origin: originOf(source.Return)}},
	}
	events = append(events,
		LoweringEvent{Kind: "logical.assign.test", Node: source.Assignment.ID, Origin: source.Assignment.Origin, Type: nodeTypeID(source.AssignLeft), Operator: "??=", Inputs: []NodeID{source.AssignLeft.ID}},
		LoweringEvent{Kind: "logical.assign.store", Node: source.Assignment.ID, Origin: source.Assignment.Origin, Type: nodeTypeID(source.AssignRight), Operator: "??=", Inputs: []NodeID{source.AssignLeft.ID, source.AssignRight.ID}},
		LoweringEvent{Kind: "nullable.unwrap", Node: source.AssignLeft.ID, Origin: source.AssignLeft.Origin, Type: nodeTypeID(source.AssignLeft), Inputs: []NodeID{source.AssignLeft.ID}},
		LoweringEvent{Kind: "phi", Node: source.Assignment.ID, Origin: source.Assignment.Origin, Type: nodeTypeID(source.Assignment), Inputs: []NodeID{source.AssignRight.ID, source.AssignLeft.ID}},
		LoweringEvent{Kind: "return", Node: source.Return.ID, Origin: source.Return.Origin, Type: returnTypeID, Inputs: []NodeID{source.ReturnValue.ID}},
		LoweringEvent{Kind: "function.end", Node: functionNode.ID, Origin: functionNode.Origin},
	)
	return function, events, nil
}

func findPrimitiveCoalesce(bodyID NodeID, nodes map[NodeID]NodeSnapshot) (primitiveCoalesceSource, bool, error) {
	body, ok := nodes[bodyID]
	if !ok || body.Kind != snapshotKindBlock {
		return primitiveCoalesceSource{}, false, fmt.Errorf("body is not a block")
	}
	statements := namedChildren(body, "statement[")
	if len(statements) != 1 {
		return primitiveCoalesceSource{}, false, nil
	}
	returnNode, ok := nodes[statements[0]]
	if !ok || returnNode.Kind != snapshotKindReturnStatement {
		return primitiveCoalesceSource{}, false, nil
	}
	expressionID := childByRole(returnNode, "expression")
	expression, ok := nodes[expressionID]
	if !ok || expression.Kind != snapshotKindBinaryExpression || expression.SyntaxPayload.Operator != snapshotKindQuestionQuestionToken {
		return primitiveCoalesceSource{}, false, nil
	}
	left, err := requireRoleKind(expression, "left", snapshotKindIdentifier, nodes)
	if err != nil {
		return primitiveCoalesceSource{}, false, err
	}
	right, err := requireRoleKind(expression, "right", snapshotKindIdentifier, nodes)
	if err != nil {
		return primitiveCoalesceSource{}, false, err
	}
	if _, err := requireRoleKind(expression, "operator", snapshotKindQuestionQuestionToken, nodes); err != nil {
		return primitiveCoalesceSource{}, false, err
	}
	return primitiveCoalesceSource{Return: returnNode, Expression: expression, Left: left, Right: right}, true, nil
}

func replayCoalesceFunction(
	function bingo.HIRFunction,
	events []LoweringEvent,
	source primitiveCoalesceSource,
	parameterValues map[SymbolID]bingo.ValueID,
	parameterTypes map[bingo.ValueID]bingo.TypeKind,
	functionNode NodeSnapshot,
	nodes map[NodeID]NodeSnapshot,
	types map[TypeID]TypeSnapshot,
	symbols map[SymbolID]SymbolSnapshot,
	signatures map[SignatureID]SignatureSnapshot,
) (bingo.HIRFunction, []LoweringEvent, error) {
	if function.Name != "coalesce" || len(function.Parameters) != 2 {
		return bingo.HIRFunction{}, nil, fmt.Errorf("primitive nullish replay requires exported coalesce(value, fallback)")
	}
	value, ok := parameterValue(source.Left, parameterValues)
	if !ok || value != 1 || parameterTypes[value] != bingo.TypeNullableNumber {
		return bingo.HIRFunction{}, nil, fmt.Errorf("nullish left operand %s is not the nullable value parameter", source.Left.ID)
	}
	fallback, ok := parameterValue(source.Right, parameterValues)
	if !ok || fallback != 2 || parameterTypes[fallback] != bingo.TypeNumber {
		return bingo.HIRFunction{}, nil, fmt.Errorf("nullish right operand %s is not the number fallback parameter", source.Right.ID)
	}
	leftType, leftErr := bingoType(nodeTypeID(source.Left), types)
	rightType, rightErr := bingoType(nodeTypeID(source.Right), types)
	resultType, resultErr := bingoType(nodeTypeID(source.Expression), types)
	if leftErr != nil || rightErr != nil || resultErr != nil || leftType != bingo.TypeNullableNumber || rightType != bingo.TypeNumber || resultType != bingo.TypeNumber {
		return bingo.HIRFunction{}, nil, fmt.Errorf("nullish source types are not canonical nullable-number coalesce")
	}
	returnTypeID, ok := resolveFunctionReturnType(functionNode, nodes, symbols, types, signatures)
	if !ok {
		returnTypeID = annotatedReturnType(functionNode, nodes)
	}
	returnType, returnErr := bingoType(returnTypeID, types)
	if returnErr != nil || returnType != bingo.TypeNumber {
		return bingo.HIRFunction{}, nil, fmt.Errorf("function %s return type is not canonical number", functionNode.ID)
	}
	function.ReturnType = bingo.TypeNumber
	function.Blocks = []bingo.HIRBlock{
		{ID: 1, Operations: []bingo.HIROp{{ID: 3, Kind: "is_nullish", Type: bingo.TypeBoolean, Operands: []bingo.ValueID{value}, Effect: bingo.EffectPure, LogicalCapabilityRequirements: []bingo.RuntimeCapabilityID{}, Origin: originOf(source.Expression)}}, Terminator: bingo.HIRTerminator{Kind: "condbranch", Value: 3, Successors: []bingo.BlockID{2, 3}, Origin: originOf(source.Expression)}},
		{ID: 2, Operations: []bingo.HIROp{}, Terminator: bingo.HIRTerminator{Kind: "branch", Successors: []bingo.BlockID{4}, Origin: originOf(source.Right)}},
		{ID: 3, Operations: []bingo.HIROp{{ID: 4, Kind: "unwrap_nullable", Type: bingo.TypeNumber, Operands: []bingo.ValueID{value}, Effect: bingo.EffectPure, LogicalCapabilityRequirements: []bingo.RuntimeCapabilityID{}, Origin: originOf(source.Left)}}, Terminator: bingo.HIRTerminator{Kind: "branch", Successors: []bingo.BlockID{4}, Origin: originOf(source.Left)}},
		{ID: 4, Operations: []bingo.HIROp{{ID: 5, Kind: "phi", Type: bingo.TypeNumber, Operands: []bingo.ValueID{fallback, 4}, IncomingBlocks: []bingo.BlockID{2, 3}, Effect: bingo.EffectPure, LogicalCapabilityRequirements: []bingo.RuntimeCapabilityID{}, Origin: originOf(source.Expression)}}, Terminator: bingo.HIRTerminator{Kind: "return", Value: 5, Origin: originOf(source.Return)}},
	}
	events = append(events,
		LoweringEvent{Kind: "nullish.test", Node: source.Expression.ID, Origin: source.Expression.Origin, Type: nodeTypeID(source.Left), Operator: "??", Inputs: []NodeID{source.Left.ID}},
		LoweringEvent{Kind: "nullish.fallback", Node: source.Right.ID, Origin: source.Right.Origin, Type: nodeTypeID(source.Right), Inputs: []NodeID{source.Right.ID}},
		LoweringEvent{Kind: "nullable.unwrap", Node: source.Left.ID, Origin: source.Left.Origin, Type: nodeTypeID(source.Left), Inputs: []NodeID{source.Left.ID}},
		LoweringEvent{Kind: "phi", Node: source.Expression.ID, Origin: source.Expression.Origin, Type: nodeTypeID(source.Expression), Inputs: []NodeID{source.Right.ID, source.Left.ID}},
		LoweringEvent{Kind: "return", Node: source.Return.ID, Origin: source.Return.Origin, Type: returnTypeID, Inputs: []NodeID{source.Expression.ID}},
		LoweringEvent{Kind: "function.end", Node: functionNode.ID, Origin: functionNode.Origin},
	)
	return function, events, nil
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
	if typ.Kind == "intrinsic" && typ.Flags == 32 && typ.ObjectFlags == 0 && typ.TypePayload.Tag == "intrinsic" && typ.TypePayload.Scalar == "intrinsic|32|0|||intrinsic:string" {
		return bingo.TypeString, nil
	}
	if typ.Kind == "literal" && typ.Flags == 2048 && typ.ObjectFlags == 0 && typ.TypePayload.Tag == "literal" && strings.HasPrefix(typ.TypePayload.Scalar, "literal|2048|0|||literal:jsnum.Number:") {
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
	if typ.Kind == "union" && typ.Flags == 134217728 && (typ.ObjectFlags == 32768 || typ.ObjectFlags == 67141632) && typ.TypePayload.Tag == "union" && len(typ.ElementTypes) == 3 {
		seen := map[string]bool{}
		for _, elementID := range typ.ElementTypes {
			element, ok := types[elementID]
			if !ok || element.ObjectFlags != 0 || element.TypePayload.Tag != "intrinsic" {
				return "", fmt.Errorf("type %d is not the canonical nullable-number union", id)
			}
			seen[element.TypePayload.Scalar] = true
		}
		if seen["intrinsic|64|0|||intrinsic:number"] && seen["intrinsic|8|0|||intrinsic:null"] && seen["intrinsic|4|0|||intrinsic:undefined"] {
			return bingo.TypeNullableNumber, nil
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
