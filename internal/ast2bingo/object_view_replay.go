package ast2bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const ObjectViewReplaySchemaVersion uint32 = 1
const maxObjectViewReplayBytes = 8 << 20

// ObjectViewReplayResult binds a real frontend structural assignment to every
// independently verified OBJ-005 artifact through target-aware MIR.
type ObjectViewReplayResult struct {
	SchemaVersion         uint32                      `json:"schemaVersion"`
	FrontendSnapshotHash  string                      `json:"frontendSnapshotHash"`
	CompilerBuildIdentity bingo.CompilerBuildIdentity `json:"compilerBuildIdentity"`
	HIR                   bingo.HIRModule             `json:"hir"`
	View                  bingo.ObjectViewProof       `json:"view"`
	Gate                  bingo.ObjectViewHIRGate     `json:"gate"`
	Artifact              bingo.ObjectViewHIRArtifact `json:"artifact"`
	MIR                   bingo.ObjectViewMIRModule   `json:"mir"`
	ContentHash           string                      `json:"contentHash"`
}

func ReplayObjectViewFrontendSnapshot(data []byte, identity bingo.CompilerBuildIdentity) (ObjectViewReplayResult, error) {
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		return ObjectViewReplayResult{}, err
	}
	return ReplayObjectViewSnapshot(frontend.Program, identity)
}

func ReplayObjectViewSnapshot(snapshot ProgramSnapshot, identity bingo.CompilerBuildIdentity) (ObjectViewReplayResult, error) {
	if err := frontendwire.ValidateProgramSnapshot(snapshot); err != nil {
		return ObjectViewReplayResult{}, fmt.Errorf("validate ObjectView frontend snapshot: %w", err)
	}
	if err := validateCompilerIdentityForSnapshot(identity, snapshot); err != nil {
		return ObjectViewReplayResult{}, err
	}
	indexes := indexPrimitiveSnapshot(snapshot)
	var functions []NodeSnapshot
	for _, node := range snapshot.Nodes {
		if node.Kind == snapshotKindFunctionDeclaration {
			functions = append(functions, node)
		}
	}
	if len(functions) != 1 {
		return ObjectViewReplayResult{}, fmt.Errorf("ObjectView requires one source function")
	}
	functionNode := functions[0]
	source, sourceType, targetType, err := findObjectViewSource(functionNode, indexes)
	if err != nil {
		return ObjectViewReplayResult{}, err
	}
	function, objectType, _, err := lowerVERT010ObjectAliasSource(functionNode, source, indexes)
	if err != nil {
		return ObjectViewReplayResult{}, fmt.Errorf("lower ObjectView base HIR: %w", err)
	}
	requirements := bingo.VERT010LogicalCapabilities()
	digest, err := bingo.LogicalCapabilityRequirementsDigest(requirements)
	if err != nil {
		return ObjectViewReplayResult{}, err
	}
	hir := bingo.HIRModule{SchemaVersion: bingo.VERT010HIRSchemaVersion, Provenance: primitiveHIRProvenance(snapshot, identity, digest), LogicalCapabilityRequirements: requirements, ObjectTypes: []bingo.HIRObjectType{objectType}, Functions: []bingo.HIRFunction{function}}
	_, hirHash, err := bingo.CanonicalVERT010ObjectHIR(hir)
	if err != nil {
		return ObjectViewReplayResult{}, err
	}
	hir.ContentHash = hirHash

	sourceContract, err := objectViewSemanticContract(sourceType, indexes)
	if err != nil {
		return ObjectViewReplayResult{}, fmt.Errorf("source ObjectView contract: %w", err)
	}
	targetContract, err := objectViewSemanticContract(targetType, indexes)
	if err != nil {
		return ObjectViewReplayResult{}, fmt.Errorf("target ObjectView contract: %w", err)
	}
	if len(sourceContract.Properties) != 1 || sourceContract.Properties[0].WriteTypeKey == "" || len(targetContract.Properties) != 1 || targetContract.Properties[0].WriteTypeKey != "" {
		return ObjectViewReplayResult{}, fmt.Errorf("ObjectView source/target mutability contract mismatch")
	}
	readType := sourceContract.Properties[0].ReadTypeKey
	if readType != targetContract.Properties[0].ReadTypeKey {
		return ObjectViewReplayResult{}, fmt.Errorf("ObjectView fixture read types differ")
	}
	relations, err := bingo.BuildTypeRelationGraph([]bingo.TypeRelationNode{{TypeKey: readType, DeclarationKey: readType}}, nil)
	if err != nil {
		return ObjectViewReplayResult{}, err
	}
	layoutTarget, err := bingo.CanonicalObjectLayoutTarget(bingo.ObjectLayoutX8664Triple)
	if err != nil {
		return ObjectViewReplayResult{}, err
	}
	sourceLayout, err := bingo.PlanObjectLayout(sourceContract.TypeKey, layoutTarget, []bingo.ObjectLayoutPropertyInput{{Key: "value", Kind: bingo.ObjectPropertyData, Representation: "f64"}})
	if err != nil {
		return ObjectViewReplayResult{}, err
	}
	targetLayout, err := bingo.PlanObjectLayout(targetContract.TypeKey, layoutTarget, []bingo.ObjectLayoutPropertyInput{{Key: "value", Kind: bingo.ObjectPropertyData, Representation: "f64"}})
	if err != nil {
		return ObjectViewReplayResult{}, err
	}
	view, err := bingo.BuildObjectViewProof(sourceContract, targetContract, relations, sourceLayout, targetLayout)
	if err != nil {
		return ObjectViewReplayResult{}, err
	}
	gate, err := bingo.BuildObjectViewHIRGate(hir, 1, 3, view)
	if err != nil {
		return ObjectViewReplayResult{}, err
	}
	artifact, err := bingo.BuildObjectViewHIRArtifact(gate)
	if err != nil {
		return ObjectViewReplayResult{}, err
	}
	mir, err := bingo.LowerObjectViewMIR(artifact)
	if err != nil {
		return ObjectViewReplayResult{}, err
	}
	result := ObjectViewReplayResult{SchemaVersion: ObjectViewReplaySchemaVersion, FrontendSnapshotHash: snapshot.ContentHash, CompilerBuildIdentity: identity, HIR: hir, View: view, Gate: gate, Artifact: artifact, MIR: mir}
	_, hash, err := canonicalObjectViewReplay(result)
	result.ContentHash = hash
	return result, err
}

func (result ObjectViewReplayResult) CanonicalBytes() ([]byte, error) {
	claimed := result.ContentHash
	encoded, hash, err := canonicalObjectViewReplay(result)
	if err != nil {
		return nil, err
	}
	if claimed == "" || claimed != hash {
		return nil, fmt.Errorf("ObjectView replay content hash mismatch")
	}
	return encoded, nil
}

func DecodeObjectViewReplay(data []byte) (*ObjectViewReplayResult, error) {
	if len(data) > maxObjectViewReplayBytes {
		return nil, fmt.Errorf("ObjectView replay exceeds %d bytes", maxObjectViewReplayBytes)
	}
	var result ObjectViewReplayResult
	if err := jsonx.Unmarshal(data, &result, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode ObjectView replay: %w", err)
	}
	if _, err := result.CanonicalBytes(); err != nil {
		return nil, err
	}
	return &result, nil
}

func canonicalObjectViewReplay(result ObjectViewReplayResult) ([]byte, string, error) {
	result.ContentHash = ""
	if result.SchemaVersion != ObjectViewReplaySchemaVersion || result.FrontendSnapshotHash == "" || result.FrontendSnapshotHash != result.HIR.Provenance.FrontendSnapshotHash || result.CompilerBuildIdentity != result.HIR.Provenance.CompilerBuildIdentity {
		return nil, "", fmt.Errorf("invalid ObjectView replay identity")
	}
	if err := bingo.ValidateCompilerBuildIdentity(result.CompilerBuildIdentity); err != nil {
		return nil, "", err
	}
	if err := bingo.VerifyCanonicalVERT010ObjectHIR(result.HIR); err != nil {
		return nil, "", err
	}
	if err := bingo.VerifyCanonicalObjectViewProof(result.View); err != nil {
		return nil, "", err
	}
	if err := bingo.VerifyCanonicalObjectViewHIRGate(result.Gate); err != nil {
		return nil, "", err
	}
	if err := bingo.VerifyCanonicalObjectViewHIRArtifact(result.Artifact); err != nil {
		return nil, "", err
	}
	if err := bingo.VerifyCanonicalObjectViewMIR(result.MIR); err != nil {
		return nil, "", err
	}
	if result.Gate.HIR.ContentHash != result.HIR.ContentHash || result.Gate.View.ContentHash != result.View.ContentHash || result.Artifact.Gate.ContentHash != result.Gate.ContentHash || result.MIR.HIRHash != result.Artifact.ContentHash {
		return nil, "", fmt.Errorf("ObjectView replay artifact chain mismatch")
	}
	encoded, err := jsonx.Marshal(result)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	result.ContentHash = hash
	encoded, err = jsonx.Marshal(result)
	if err != nil {
		return nil, "", err
	}
	return encoded, hash, nil
}

func objectViewSemanticContract(typeID TypeID, indexes snapshotSemanticFactIndexes) (bingo.ObjectSemanticContract, error) {
	record, ok := indexes.Types[typeID]
	if !ok || record.Kind != "object" || record.CanonicalHash == "" || len(record.PropertyFacts) != 1 {
		return bingo.ObjectSemanticContract{}, fmt.Errorf("type %d is not the one-property object shape", typeID)
	}
	fact := record.PropertyFacts[0]
	symbol, ok := indexes.Symbols[fact.Symbol]
	if !ok || symbol.Name != "value" || fact.Optional || fact.Visibility != "public" || fact.ReadType == 0 {
		return bingo.ObjectSemanticContract{}, fmt.Errorf("type %d has invalid value property", typeID)
	}
	read, ok := indexes.Types[fact.ReadType]
	if !ok || read.CanonicalHash == "" {
		return bingo.ObjectSemanticContract{}, fmt.Errorf("type %d has invalid read type", typeID)
	}
	property := bingo.ObjectPropertyContract{Key: "value", Kind: bingo.ObjectPropertyData, ReadTypeKey: read.CanonicalHash, Optional: fact.Optional, Readonly: fact.Readonly, Visibility: "public"}
	if fact.WriteType != 0 {
		write, ok := indexes.Types[fact.WriteType]
		if !ok || write.CanonicalHash == "" {
			return bingo.ObjectSemanticContract{}, fmt.Errorf("type %d has invalid write type", typeID)
		}
		property.WriteTypeKey = write.CanonicalHash
	}
	contract := bingo.ObjectSemanticContract{SchemaVersion: bingo.ObjectSemanticContractSchemaVersion, TypeKey: record.CanonicalHash, Identity: bingo.ObjectIdentityReference, Equality: bingo.ObjectEqualityReference, Properties: []bingo.ObjectPropertyContract{property}}
	_, hash, err := bingo.CanonicalObjectSemanticContract(contract)
	contract.ContentHash = hash
	return contract, err
}

func findObjectViewSource(functionNode NodeSnapshot, indexes snapshotSemanticFactIndexes) (vert010Source, TypeID, TypeID, error) {
	nodes := indexes.Nodes
	body, ok := nodes[childByRole(functionNode, "body")]
	if !ok || body.Kind != snapshotKindBlock {
		return vert010Source{}, 0, 0, fmt.Errorf("ObjectView body is not a block")
	}
	statements := namedChildren(body, "statement[")
	if len(statements) != 5 {
		return vert010Source{}, 0, 0, fmt.Errorf("ObjectView requires five statements")
	}
	objectDecl, objectInit, err := vert010Variable(statements[0], nodes)
	if err != nil || objectInit.Kind != snapshotKindObjectLiteralExpression || childText(objectDecl, "name", nodes) != "object" {
		return vert010Source{}, 0, 0, fmt.Errorf("ObjectView object declaration is invalid")
	}
	shorthand, err := requireRoleKind(objectInit, "child[0]", snapshotKindShorthandPropertyAssignment, nodes)
	if err != nil || childText(shorthand, "name", nodes) != "value" {
		return vert010Source{}, 0, 0, fmt.Errorf("ObjectView object property is invalid")
	}
	aliasDecl, aliasInit, err := vert010Variable(statements[1], nodes)
	if err != nil || aliasInit.Kind != snapshotKindIdentifier || childText(aliasDecl, "name", nodes) != "alias" || aliasInit.SyntaxPayload.Text != "object" {
		return vert010Source{}, 0, 0, fmt.Errorf("ObjectView mutable alias is invalid")
	}
	viewDecl, viewInit, err := vert010Variable(statements[2], nodes)
	if err != nil || viewInit.Kind != snapshotKindIdentifier || childText(viewDecl, "name", nodes) != "view" || viewInit.SyntaxPayload.Text != "object" {
		return vert010Source{}, 0, 0, fmt.Errorf("ObjectView readonly binding is invalid")
	}
	if _, err := requireRoleKind(viewDecl, "type", snapshotKindTypeReference, nodes); err != nil {
		return vert010Source{}, 0, 0, fmt.Errorf("ObjectView target annotation is missing")
	}
	sourceType, targetType := nodeTypeID(objectDecl), nodeTypeID(viewDecl)
	if sourceType == 0 || targetType == 0 || sourceType == targetType || nodeTypeID(viewInit) != sourceType || viewInit.ContextualType != targetType {
		return vert010Source{}, 0, 0, fmt.Errorf("ObjectView assignment type proof is invalid")
	}
	expressionStatement, ok := nodes[statements[3]]
	if !ok || expressionStatement.Kind != snapshotKindExpressionStatement {
		return vert010Source{}, 0, 0, fmt.Errorf("ObjectView mutation statement is invalid")
	}
	assignment, err := requireRoleKind(expressionStatement, "expression", snapshotKindBinaryExpression, nodes)
	if err != nil || assignment.SyntaxPayload.Operator != snapshotKindEqualsToken {
		return vert010Source{}, 0, 0, fmt.Errorf("ObjectView mutation is invalid")
	}
	storeAccess, err := requireRoleKind(assignment, "left", snapshotKindPropertyAccessExpression, nodes)
	if err != nil || !vert010Access(storeAccess, "alias", nodes) {
		return vert010Source{}, 0, 0, fmt.Errorf("ObjectView mutation receiver is invalid")
	}
	add, err := requireRoleKind(assignment, "right", snapshotKindBinaryExpression, nodes)
	if err != nil || add.SyntaxPayload.Operator != snapshotKindPlusToken {
		return vert010Source{}, 0, 0, fmt.Errorf("ObjectView increment is invalid")
	}
	loadAlias, err := requireRoleKind(add, "left", snapshotKindPropertyAccessExpression, nodes)
	if err != nil || !vert010Access(loadAlias, "alias", nodes) {
		return vert010Source{}, 0, 0, fmt.Errorf("ObjectView alias load is invalid")
	}
	one, err := requireRoleKind(add, "right", snapshotKindNumericLiteral, nodes)
	if err != nil || one.Constant.Kind != "number" || one.Constant.Number != 1 || one.Constant.Text != "1" {
		return vert010Source{}, 0, 0, fmt.Errorf("ObjectView increment constant is invalid")
	}
	returnNode, ok := nodes[statements[4]]
	if !ok || returnNode.Kind != snapshotKindReturnStatement {
		return vert010Source{}, 0, 0, fmt.Errorf("ObjectView return is invalid")
	}
	loadView, err := requireRoleKind(returnNode, "expression", snapshotKindPropertyAccessExpression, nodes)
	if err != nil || !vert010Access(loadView, "view", nodes) {
		return vert010Source{}, 0, 0, fmt.Errorf("ObjectView readonly load is invalid")
	}
	if storeAccess.Symbol == "" || storeAccess.Symbol != loadAlias.Symbol || loadView.Symbol == "" || loadView.Symbol == storeAccess.Symbol {
		return vert010Source{}, 0, 0, fmt.Errorf("ObjectView source/target property identity proof is invalid")
	}
	if !slices.Equal([]string{indexes.Symbols[storeAccess.Symbol].Name, indexes.Symbols[loadView.Symbol].Name}, []string{"value", "value"}) {
		return vert010Source{}, 0, 0, fmt.Errorf("ObjectView property names are invalid")
	}
	return vert010Source{objectDecl, objectInit, shorthand, aliasDecl, aliasInit, assignment, storeAccess, add, loadAlias, one, returnNode, loadView}, sourceType, targetType, nil
}
