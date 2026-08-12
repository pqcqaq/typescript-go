package ast2bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const ObjectAccessorViewReplaySchemaVersion uint32 = 1
const maxObjectAccessorViewReplayBytes = 8 << 20

type ObjectAccessorViewReplayResult struct {
	SchemaVersion         uint32                      `json:"schemaVersion"`
	FrontendSnapshotHash  string                      `json:"frontendSnapshotHash"`
	CompilerBuildIdentity bingo.CompilerBuildIdentity `json:"compilerBuildIdentity"`
	HIR                   bingo.HIRModule             `json:"hir"`
	View                  bingo.ObjectViewProof       `json:"view"`
	Artifact              bingo.ObjectViewHIRArtifact `json:"artifact"`
	MIR                   bingo.ObjectViewMIRModule   `json:"mir"`
	ContentHash           string                      `json:"contentHash"`
}

func ReplayObjectAccessorViewFrontendSnapshot(data []byte, identity bingo.CompilerBuildIdentity) (ObjectAccessorViewReplayResult, error) {
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		return ObjectAccessorViewReplayResult{}, err
	}
	return ReplayObjectAccessorViewSnapshot(frontend.Program, identity)
}

func ReplayObjectAccessorViewSnapshot(snapshot ProgramSnapshot, identity bingo.CompilerBuildIdentity) (ObjectAccessorViewReplayResult, error) {
	if err := frontendwire.ValidateProgramSnapshot(snapshot); err != nil {
		return ObjectAccessorViewReplayResult{}, err
	}
	if err := validateCompilerIdentityForSnapshot(identity, snapshot); err != nil {
		return ObjectAccessorViewReplayResult{}, err
	}
	function, indexes, sourceType, targetType, err := objectAccessorViewSource(snapshot)
	if err != nil {
		return ObjectAccessorViewReplayResult{}, err
	}
	hirFunction, places, _, err := lowerVERT011PropertyNullishAssign(function, indexes)
	if err != nil {
		return ObjectAccessorViewReplayResult{}, fmt.Errorf("lower accessor-view base HIR: %w", err)
	}
	requirements := bingo.VERT010LogicalCapabilities()
	digest, err := bingo.LogicalCapabilityRequirementsDigest(requirements)
	if err != nil {
		return ObjectAccessorViewReplayResult{}, err
	}
	hir := bingo.HIRModule{SchemaVersion: bingo.VERT011HIRSchemaVersion, Provenance: primitiveHIRProvenance(snapshot, identity, digest), LogicalCapabilityRequirements: requirements, PlaceRefs: &places, Functions: []bingo.HIRFunction{hirFunction}}
	_, hash, err := bingo.CanonicalVERT011PlaceHIR(hir)
	if err != nil {
		return ObjectAccessorViewReplayResult{}, err
	}
	hir.ContentHash = hash
	source := places.ObjectContracts[0]
	if source.TypeKey != sourceType.CanonicalHash {
		return ObjectAccessorViewReplayResult{}, fmt.Errorf("accessor-view source type binding mismatch")
	}
	target, err := objectAccessorViewTargetContract(targetType, indexes)
	if err != nil {
		return ObjectAccessorViewReplayResult{}, err
	}
	read := source.Properties[1].ReadTypeKey
	if target.Properties[0].ReadTypeKey != read {
		return ObjectAccessorViewReplayResult{}, fmt.Errorf("accessor-view read type mismatch")
	}
	relations, err := bingo.BuildTypeRelationGraph([]bingo.TypeRelationNode{{TypeKey: read, DeclarationKey: read}}, nil)
	if err != nil {
		return ObjectAccessorViewReplayResult{}, err
	}
	layoutTarget, err := bingo.CanonicalObjectLayoutTarget(bingo.ObjectLayoutX8664Triple)
	if err != nil {
		return ObjectAccessorViewReplayResult{}, err
	}
	sourceLayout, err := bingo.PlanObjectLayout(source.TypeKey, layoutTarget, []bingo.ObjectLayoutPropertyInput{{Key: "backing", Kind: bingo.ObjectPropertyData, Representation: string(bingo.VERT011RepNullableF64)}, {Key: "result", Kind: bingo.ObjectPropertyAccessor}})
	if err != nil {
		return ObjectAccessorViewReplayResult{}, err
	}
	targetLayout, err := bingo.PlanObjectLayout(target.TypeKey, layoutTarget, []bingo.ObjectLayoutPropertyInput{{Key: "result", Kind: bingo.ObjectPropertyAccessor}})
	if err != nil {
		return ObjectAccessorViewReplayResult{}, err
	}
	view, err := bingo.BuildObjectViewProof(source, target, relations, sourceLayout, targetLayout)
	if err != nil {
		return ObjectAccessorViewReplayResult{}, err
	}
	gate, err := bingo.BuildObjectViewHIRGate(hir, 1, 3, view)
	if err != nil {
		return ObjectAccessorViewReplayResult{}, err
	}
	artifact, err := bingo.BuildObjectViewHIRArtifact(gate)
	if err != nil {
		return ObjectAccessorViewReplayResult{}, err
	}
	mir, err := bingo.LowerObjectViewMIR(artifact)
	if err != nil {
		return ObjectAccessorViewReplayResult{}, err
	}
	result := ObjectAccessorViewReplayResult{SchemaVersion: ObjectAccessorViewReplaySchemaVersion, FrontendSnapshotHash: snapshot.ContentHash, CompilerBuildIdentity: identity, HIR: hir, View: view, Artifact: artifact, MIR: mir}
	_, hash, err = canonicalObjectAccessorViewReplay(result)
	result.ContentHash = hash
	return result, err
}

func (r ObjectAccessorViewReplayResult) CanonicalBytes() ([]byte, error) {
	encoded, hash, err := canonicalObjectAccessorViewReplay(r)
	if err != nil {
		return nil, err
	}
	if r.ContentHash == "" || r.ContentHash != hash {
		return nil, fmt.Errorf("accessor-view replay content hash mismatch")
	}
	return encoded, nil
}
func DecodeObjectAccessorViewReplay(data []byte) (*ObjectAccessorViewReplayResult, error) {
	if len(data) > maxObjectAccessorViewReplayBytes {
		return nil, fmt.Errorf("accessor-view replay exceeds %d bytes", maxObjectAccessorViewReplayBytes)
	}
	var r ObjectAccessorViewReplayResult
	if err := jsonx.Unmarshal(data, &r, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	if _, err := r.CanonicalBytes(); err != nil {
		return nil, err
	}
	return &r, nil
}
func canonicalObjectAccessorViewReplay(r ObjectAccessorViewReplayResult) ([]byte, string, error) {
	r.ContentHash = ""
	if r.SchemaVersion != ObjectAccessorViewReplaySchemaVersion || r.FrontendSnapshotHash == "" || r.FrontendSnapshotHash != r.HIR.Provenance.FrontendSnapshotHash || r.CompilerBuildIdentity != r.HIR.Provenance.CompilerBuildIdentity {
		return nil, "", fmt.Errorf("invalid accessor-view replay identity")
	}
	if err := bingo.VerifyCanonicalVERT011PlaceHIR(r.HIR); err != nil {
		return nil, "", err
	}
	if err := bingo.VerifyCanonicalObjectViewProof(r.View); err != nil {
		return nil, "", err
	}
	if err := bingo.VerifyCanonicalObjectViewHIRArtifact(r.Artifact); err != nil {
		return nil, "", err
	}
	if err := bingo.VerifyCanonicalObjectViewMIR(r.MIR); err != nil {
		return nil, "", err
	}
	if r.Artifact.Gate.HIR.ContentHash != r.HIR.ContentHash || r.Artifact.Gate.View.ContentHash != r.View.ContentHash || r.MIR.HIRHash != r.Artifact.ContentHash {
		return nil, "", fmt.Errorf("accessor-view replay chain mismatch")
	}
	encoded, err := jsonx.Marshal(r)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(encoded)
	hash := hex.EncodeToString(sum[:])
	r.ContentHash = hash
	encoded, err = jsonx.Marshal(r)
	return encoded, hash, err
}

func objectAccessorViewSource(snapshot ProgramSnapshot) (NodeSnapshot, snapshotSemanticFactIndexes, TypeSnapshot, TypeSnapshot, error) {
	original := indexPrimitiveSnapshot(snapshot)
	var function NodeSnapshot
	count := 0
	for _, node := range snapshot.Nodes {
		if node.Kind == snapshotKindFunctionDeclaration {
			function = node
			count++
		}
	}
	if count != 1 || childText(function, "name", original.Nodes) != "propertyNullishAssign" {
		return NodeSnapshot{}, snapshotSemanticFactIndexes{}, TypeSnapshot{}, TypeSnapshot{}, fmt.Errorf("accessor-view function is invalid")
	}
	body, err := requireRoleKind(function, "body", snapshotKindBlock, original.Nodes)
	if err != nil {
		return NodeSnapshot{}, snapshotSemanticFactIndexes{}, TypeSnapshot{}, TypeSnapshot{}, err
	}
	statements := namedChildren(body, "statement[")
	if len(statements) != 4 {
		return NodeSnapshot{}, snapshotSemanticFactIndexes{}, TypeSnapshot{}, TypeSnapshot{}, fmt.Errorf("accessor-view requires four statements")
	}
	objectDecl, _, err := vert011Variable(statements[0], original.Nodes, "object")
	if err != nil {
		return NodeSnapshot{}, snapshotSemanticFactIndexes{}, TypeSnapshot{}, TypeSnapshot{}, err
	}
	viewDecl, viewInit, err := vert011Variable(statements[1], original.Nodes, "view")
	if err != nil || viewInit.Kind != snapshotKindIdentifier || viewInit.SyntaxPayload.Text != "object" {
		return NodeSnapshot{}, snapshotSemanticFactIndexes{}, TypeSnapshot{}, TypeSnapshot{}, fmt.Errorf("accessor-view contextual assignment is invalid")
	}
	if _, err := requireRoleKind(viewDecl, "type", snapshotKindTypeReference, original.Nodes); err != nil {
		return NodeSnapshot{}, snapshotSemanticFactIndexes{}, TypeSnapshot{}, TypeSnapshot{}, fmt.Errorf("accessor-view target annotation missing")
	}
	source, sourceOK := original.Types[nodeTypeID(objectDecl)]
	target, targetOK := original.Types[nodeTypeID(viewDecl)]
	if !sourceOK || !targetOK || source.CanonicalHash == target.CanonicalHash || nodeTypeID(viewInit) != nodeTypeID(objectDecl) || viewInit.ContextualType != nodeTypeID(viewDecl) {
		return NodeSnapshot{}, snapshotSemanticFactIndexes{}, TypeSnapshot{}, TypeSnapshot{}, fmt.Errorf("accessor-view type proof is invalid")
	}
	nodes := make(map[NodeID]NodeSnapshot, len(original.Nodes))
	for id, node := range original.Nodes {
		nodes[id] = node
	}
	normalizedBody := nodes[body.ID]
	normalizedBody.NamedChildren = []frontendwire.NamedChildSnapshot{{Role: "statement[0]", Node: statements[0]}, {Role: "statement[1]", Node: statements[2]}, {Role: "statement[2]", Node: statements[3]}}
	normalizedBody.Children = []NodeID{statements[0], statements[2], statements[3]}
	nodes[body.ID] = normalizedBody
	normalized := original
	normalized.Nodes = nodes
	return function, normalized, source, target, nil
}

func objectAccessorViewTargetContract(record TypeSnapshot, indexes snapshotSemanticFactIndexes) (bingo.ObjectSemanticContract, error) {
	if record.Kind != "object" || len(record.PropertyFacts) != 1 {
		return bingo.ObjectSemanticContract{}, fmt.Errorf("accessor-view target shape invalid")
	}
	fact := record.PropertyFacts[0]
	symbol, ok := indexes.Symbols[fact.Symbol]
	if !ok || symbol.Name != "result" || fact.WriteType != 0 || fact.Optional || fact.Visibility != "public" || len(symbol.Declarations) != 1 || indexes.Nodes[symbol.Declarations[0]].Kind != snapshotKindGetAccessor {
		return bingo.ObjectSemanticContract{}, fmt.Errorf("accessor-view target getter is invalid")
	}
	read, ok := indexes.Types[fact.ReadType]
	if !ok {
		return bingo.ObjectSemanticContract{}, fmt.Errorf("accessor-view target read type invalid")
	}
	c := bingo.ObjectSemanticContract{SchemaVersion: bingo.ObjectSemanticContractSchemaVersion, TypeKey: record.CanonicalHash, Identity: bingo.ObjectIdentityReference, Equality: bingo.ObjectEqualityReference, Properties: []bingo.ObjectPropertyContract{{Key: "result", Kind: bingo.ObjectPropertyAccessor, ReadTypeKey: read.CanonicalHash, Visibility: "public"}}}
	_, hash, err := bingo.CanonicalObjectSemanticContract(c)
	c.ContentHash = hash
	return c, err
}
