package ast2bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const FunctionThunkReplaySchemaVersion uint32 = 1
const maxFunctionThunkReplayBytes = 2 << 20

type FunctionThunkReplayResult struct {
	SchemaVersion         uint32                      `json:"schemaVersion"`
	FrontendSnapshotHash  string                      `json:"frontendSnapshotHash"`
	CompilerBuildIdentity bingo.CompilerBuildIdentity `json:"compilerBuildIdentity"`
	FrontendSnapshot      ProgramSnapshot             `json:"frontendSnapshot"`
	SourceSignatureHash   string                      `json:"sourceSignatureHash"`
	TargetSignatureHash   string                      `json:"targetSignatureHash"`
	AssignmentNodeID      NodeID                      `json:"assignmentNodeId"`
	Thunk                 bingo.FunctionThunkContract `json:"thunk"`
	ContentHash           string                      `json:"contentHash"`
}

// LowerFunctionThunkHIR materializes the verified replay into the additive
// thunk HIR artifact. Keeping this join here ensures HIR provenance begins
// with the self-contained frontend replay rather than loose signature hashes.
func LowerFunctionThunkHIR(replay FunctionThunkReplayResult) (bingo.FunctionThunkHIRArtifact, error) {
	if _, err := replay.CanonicalBytes(); err != nil {
		return bingo.FunctionThunkHIRArtifact{}, err
	}
	return bingo.BuildFunctionThunkHIRArtifact(replay.FrontendSnapshotHash, replay.SourceSignatureHash, replay.TargetSignatureHash, string(replay.AssignmentNodeID), replay.Thunk)
}

func ReplayFunctionThunkSnapshot(snapshot ProgramSnapshot, identity bingo.CompilerBuildIdentity) (FunctionThunkReplayResult, error) {
	if err := frontendwire.ValidateProgramSnapshot(snapshot); err != nil {
		return FunctionThunkReplayResult{}, fmt.Errorf("validate function thunk snapshot: %w", err)
	}
	if err := validateCompilerIdentityForSnapshot(identity, snapshot); err != nil {
		return FunctionThunkReplayResult{}, err
	}
	source, target, assignment, thunk, err := deriveFunctionThunk(snapshot)
	if err != nil {
		return FunctionThunkReplayResult{}, err
	}
	result := FunctionThunkReplayResult{SchemaVersion: FunctionThunkReplaySchemaVersion, FrontendSnapshotHash: snapshot.ContentHash, CompilerBuildIdentity: identity, FrontendSnapshot: snapshot, SourceSignatureHash: source.CanonicalHash, TargetSignatureHash: target.CanonicalHash, AssignmentNodeID: assignment, Thunk: thunk}
	_, hash, err := canonicalFunctionThunkReplay(result)
	result.ContentHash = hash
	return result, err
}

func (result FunctionThunkReplayResult) CanonicalBytes() ([]byte, error) {
	encoded, hash, err := canonicalFunctionThunkReplay(result)
	if err != nil {
		return nil, err
	}
	if result.ContentHash == "" || result.ContentHash != hash {
		return nil, fmt.Errorf("function thunk replay content hash mismatch")
	}
	return encoded, nil
}

func DecodeFunctionThunkReplay(data []byte) (*FunctionThunkReplayResult, error) {
	if len(data) > maxFunctionThunkReplayBytes {
		return nil, fmt.Errorf("function thunk replay exceeds %d bytes", maxFunctionThunkReplayBytes)
	}
	var result FunctionThunkReplayResult
	if err := jsonx.Unmarshal(data, &result, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode function thunk replay: %w", err)
	}
	if _, err := result.CanonicalBytes(); err != nil {
		return nil, err
	}
	return &result, nil
}

func canonicalFunctionThunkReplay(result FunctionThunkReplayResult) ([]byte, string, error) {
	result.ContentHash = ""
	if result.SchemaVersion != FunctionThunkReplaySchemaVersion || result.FrontendSnapshotHash == "" || result.AssignmentNodeID == "" || result.SourceSignatureHash == "" || result.TargetSignatureHash == "" {
		return nil, "", fmt.Errorf("invalid function thunk replay identity")
	}
	if err := bingo.ValidateCompilerBuildIdentity(result.CompilerBuildIdentity); err != nil {
		return nil, "", err
	}
	if err := frontendwire.ValidateProgramSnapshot(result.FrontendSnapshot); err != nil || result.FrontendSnapshot.ContentHash != result.FrontendSnapshotHash {
		return nil, "", fmt.Errorf("function thunk replay frontend mismatch")
	}
	if err := bingo.VerifyCanonicalFunctionThunkContract(result.Thunk); err != nil {
		return nil, "", err
	}
	source, target, assignment, thunk, err := deriveFunctionThunk(result.FrontendSnapshot)
	if err != nil || source.CanonicalHash != result.SourceSignatureHash || target.CanonicalHash != result.TargetSignatureHash || assignment != result.AssignmentNodeID || thunk.ContentHash != result.Thunk.ContentHash {
		return nil, "", fmt.Errorf("function thunk replay does not match embedded frontend")
	}
	encoded, err := jsonx.Marshal(result)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	result.ContentHash = hash
	encoded, err = jsonx.Marshal(result)
	return encoded, hash, err
}

func deriveFunctionThunk(snapshot ProgramSnapshot) (SignatureSnapshot, SignatureSnapshot, NodeID, bingo.FunctionThunkContract, error) {
	indexes := indexPrimitiveSnapshot(snapshot)
	animal, err := functionThunkNamedObjectType("Animal", indexes)
	if err != nil {
		return SignatureSnapshot{}, SignatureSnapshot{}, "", bingo.FunctionThunkContract{}, err
	}
	dog, err := functionThunkNamedObjectType("Dog", indexes)
	if err != nil {
		return SignatureSnapshot{}, SignatureSnapshot{}, "", bingo.FunctionThunkContract{}, err
	}
	sourceNode, sourceSig, err := functionThunkNamedSignature("source", indexes)
	if err != nil {
		return SignatureSnapshot{}, SignatureSnapshot{}, "", bingo.FunctionThunkContract{}, err
	}
	targetNode, targetSig, err := functionThunkNamedSignature("target", indexes)
	if err != nil {
		return SignatureSnapshot{}, SignatureSnapshot{}, "", bingo.FunctionThunkContract{}, err
	}
	if sourceSig.ParameterFacts[0].Type != animal.ID || sourceSig.ReturnType != dog.ID || targetSig.ParameterFacts[0].Type != dog.ID || targetSig.ReturnType != animal.ID {
		return SignatureSnapshot{}, SignatureSnapshot{}, "", bingo.FunctionThunkContract{}, fmt.Errorf("function thunk source/target type direction is invalid")
	}
	var assignment NodeSnapshot
	for _, node := range indexes.Nodes {
		if node.Kind != snapshotKindVariableDeclaration || childText(node, "name", indexes.Nodes) != "adapted" {
			continue
		}
		initializer := indexes.Nodes[childByRole(node, "initializer")]
		if assignment.ID != "" || initializer.Kind != snapshotKindIdentifier || initializer.SyntaxPayload.Text != "source" || initializer.NarrowedType != sourceNode.NarrowedType || initializer.ContextualType != targetNode.NarrowedType {
			return SignatureSnapshot{}, SignatureSnapshot{}, "", bingo.FunctionThunkContract{}, fmt.Errorf("function thunk assignment is invalid")
		}
		assignment = node
	}
	if assignment.ID == "" {
		return SignatureSnapshot{}, SignatureSnapshot{}, "", bingo.FunctionThunkContract{}, fmt.Errorf("function thunk assignment is unavailable")
	}
	relations, err := functionThunkRelationGraph(indexes)
	if err != nil {
		return SignatureSnapshot{}, SignatureSnapshot{}, "", bingo.FunctionThunkContract{}, err
	}
	source, err := functionThunkSignature(sourceSig, indexes)
	if err != nil {
		return SignatureSnapshot{}, SignatureSnapshot{}, "", bingo.FunctionThunkContract{}, err
	}
	target, err := functionThunkSignature(targetSig, indexes)
	if err != nil {
		return SignatureSnapshot{}, SignatureSnapshot{}, "", bingo.FunctionThunkContract{}, err
	}
	thunk, err := bingo.BuildFunctionThunkContract(source, target, relations)
	return sourceSig, targetSig, assignment.ID, thunk, err
}

func functionThunkNamedObjectType(name string, indexes snapshotSemanticFactIndexes) (TypeSnapshot, error) {
	var declaration NodeSnapshot
	for _, node := range indexes.Nodes {
		if node.Kind != "KindInterfaceDeclaration" {
			continue
		}
		nameID := childByRole(node, "name")
		if nameID == "" {
			nameID = childByRole(node, "child[1]")
		}
		identifier := indexes.Nodes[nameID]
		if identifier.Kind != snapshotKindIdentifier || identifier.SyntaxPayload.Text != name {
			continue
		}
		if declaration.ID != "" {
			return TypeSnapshot{}, fmt.Errorf("function thunk %s interface is ambiguous", name)
		}
		declaration = node
	}
	if declaration.ID == "" {
		return TypeSnapshot{}, fmt.Errorf("function thunk %s interface is unavailable", name)
	}
	nameID := childByRole(declaration, "name")
	if nameID == "" {
		nameID = childByRole(declaration, "child[1]")
	}
	identifier := indexes.Nodes[nameID]
	symbol, ok := indexes.Symbols[identifier.Symbol]
	if !ok || symbol.Name != name || len(symbol.Declarations) != 1 || symbol.Declarations[0] != declaration.ID {
		return TypeSnapshot{}, fmt.Errorf("function thunk %s interface symbol is invalid", name)
	}
	var result TypeSnapshot
	for _, typ := range indexes.Types {
		if typ.Kind == "object" && typ.Symbol == symbol.ID {
			if result.ID != 0 {
				return TypeSnapshot{}, fmt.Errorf("function thunk %s object type is ambiguous", name)
			}
			result = typ
		}
	}
	if result.ID == 0 || result.TypePayload.Tag != "object" || !strings.Contains(result.TypePayload.Scalar, string(symbol.ID)) {
		return TypeSnapshot{}, fmt.Errorf("function thunk %s object type is invalid", name)
	}
	return result, nil
}

func functionThunkNamedSignature(name string, indexes snapshotSemanticFactIndexes) (NodeSnapshot, SignatureSnapshot, error) {
	var node NodeSnapshot
	for _, candidate := range indexes.Nodes {
		if candidate.Kind == snapshotKindFunctionDeclaration && childText(candidate, "name", indexes.Nodes) == name {
			if node.ID != "" || candidate.ModifierBits != snapshotModifierExport {
				return NodeSnapshot{}, SignatureSnapshot{}, fmt.Errorf("function thunk %s declaration is invalid", name)
			}
			node = candidate
		}
	}
	if node.ID == "" {
		return NodeSnapshot{}, SignatureSnapshot{}, fmt.Errorf("function thunk %s is unavailable", name)
	}
	var signature SignatureSnapshot
	for _, candidate := range indexes.Signatures {
		if candidate.Declaration == node.ID {
			if signature.ID != 0 {
				return NodeSnapshot{}, SignatureSnapshot{}, fmt.Errorf("function thunk %s is overloaded", name)
			}
			signature = candidate
		}
	}
	if signature.ID == 0 || len(signature.ParameterFacts) != 1 || signature.MinArgumentCount != 1 || signature.HasRest || signature.ParameterFacts[0].Optional || signature.ParameterFacts[0].Rest || signature.CallingConventionClass != "call" || signature.EffectProof.Kind != "body-resolved" || !signature.EffectProof.Complete || signature.EffectProof.Implementation != node.ID {
		return NodeSnapshot{}, SignatureSnapshot{}, fmt.Errorf("function thunk %s signature is invalid", name)
	}
	return node, signature, nil
}

func functionThunkSignature(signature SignatureSnapshot, indexes snapshotSemanticFactIndexes) (bingo.FunctionThunkSignature, error) {
	parameter, pok := indexes.Types[signature.ParameterFacts[0].Type]
	result, rok := indexes.Types[signature.ReturnType]
	if !pok || !rok || parameter.Kind != "object" || result.Kind != "object" || parameter.CanonicalHash == "" || result.CanonicalHash == "" {
		return bingo.FunctionThunkSignature{}, fmt.Errorf("function thunk signature types are invalid")
	}
	effects := make([]bingo.FunctionThunkEffect, 0, len(signature.Effects))
	for _, effect := range signature.Effects {
		switch effect {
		case "pure":
		case "read":
			effects = append(effects, bingo.FunctionThunkEffectRead)
		case "write":
			effects = append(effects, bingo.FunctionThunkEffectWrite)
		case "throw":
			effects = append(effects, bingo.FunctionThunkEffectThrow)
		case "alloc":
			effects = append(effects, bingo.FunctionThunkEffectAllocate)
		default:
			return bingo.FunctionThunkSignature{}, fmt.Errorf("function thunk effect %q is unsupported", effect)
		}
	}
	slices.Sort(effects)
	return bingo.FunctionThunkSignature{ParameterTypeKey: parameter.CanonicalHash, ReturnTypeKey: result.CanonicalHash, Effects: effects, CallingConvention: bingo.FunctionThunkCallingConvention, EnvironmentABI: bingo.FunctionThunkEnvironmentABI}, nil
}

func functionThunkRelationGraph(indexes snapshotSemanticFactIndexes) (bingo.TypeRelationGraph, error) {
	nodes := make([]bingo.TypeRelationNode, 0, len(indexes.Types))
	edges := make([]bingo.TypeRelationEdge, 0)
	for _, typ := range indexes.Types {
		if typ.CanonicalHash == "" {
			continue
		}
		declaration := typ.CanonicalHash
		if typ.Symbol != "" {
			declaration = string(typ.Symbol)
		}
		nodes = append(nodes, bingo.TypeRelationNode{TypeKey: typ.CanonicalHash, DeclarationKey: declaration})
		for _, baseID := range typ.BaseTypes {
			base, ok := indexes.Types[baseID]
			if !ok || base.CanonicalHash == "" {
				return bingo.TypeRelationGraph{}, fmt.Errorf("function thunk base type is invalid")
			}
			edges = append(edges, bingo.TypeRelationEdge{SubTypeKey: typ.CanonicalHash, SuperTypeKey: base.CanonicalHash, Path: "base:" + string(typ.Symbol) + ":" + string(base.Symbol)})
		}
	}
	slices.SortFunc(nodes, func(a, b bingo.TypeRelationNode) int { return strings.Compare(a.TypeKey, b.TypeKey) })
	return bingo.BuildTypeRelationGraph(nodes, edges)
}
