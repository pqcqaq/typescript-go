package ast2bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const ObjectLayoutCopyReplaySchemaVersion uint32 = 1
const maxObjectLayoutCopyReplayBytes = 8 << 20

type ObjectLayoutCopyFrontendEvidence struct {
	FunctionNodeID       string `json:"functionNodeId"`
	SourceLiteralNodeID  string `json:"sourceLiteralNodeId"`
	TargetLiteralNodeID  string `json:"targetLiteralNodeId"`
	SourcePropertySymbol string `json:"sourcePropertySymbol"`
	TargetPropertySymbol string `json:"targetPropertySymbol"`
}

type ObjectLayoutCopyReplayResult struct {
	SchemaVersion         uint32                            `json:"schemaVersion"`
	FrontendSnapshotHash  string                            `json:"frontendSnapshotHash"`
	CompilerBuildIdentity bingo.CompilerBuildIdentity       `json:"compilerBuildIdentity"`
	FrontendSnapshot      ProgramSnapshot                   `json:"frontendSnapshot"`
	Evidence              ObjectLayoutCopyFrontendEvidence  `json:"evidence"`
	Copy                  bingo.ObjectLayoutCopyContract    `json:"copy"`
	HIR                   bingo.ObjectLayoutCopyHIRArtifact `json:"hir"`
	MIR                   bingo.ObjectLayoutCopyMIRArtifact `json:"mir"`
	ContentHash           string                            `json:"contentHash"`
}

func ReplayObjectLayoutCopyFrontendSnapshot(data []byte, identity bingo.CompilerBuildIdentity) (ObjectLayoutCopyReplayResult, error) {
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		return ObjectLayoutCopyReplayResult{}, err
	}
	return ReplayObjectLayoutCopySnapshot(frontend.Program, identity)
}

func ReplayObjectLayoutCopySnapshot(snapshot ProgramSnapshot, identity bingo.CompilerBuildIdentity) (ObjectLayoutCopyReplayResult, error) {
	if err := frontendwire.ValidateProgramSnapshot(snapshot); err != nil {
		return ObjectLayoutCopyReplayResult{}, err
	}
	if err := validateCompilerIdentityForSnapshot(identity, snapshot); err != nil {
		return ObjectLayoutCopyReplayResult{}, err
	}
	evidence, copyContract, hir, mir, err := deriveObjectLayoutCopy(snapshot)
	if err != nil {
		return ObjectLayoutCopyReplayResult{}, err
	}
	r := ObjectLayoutCopyReplayResult{ObjectLayoutCopyReplaySchemaVersion, snapshot.ContentHash, identity, snapshot, evidence, copyContract, hir, mir, ""}
	_, r.ContentHash, err = canonicalObjectLayoutCopyReplay(r)
	return r, err
}

func deriveObjectLayoutCopy(snapshot ProgramSnapshot) (ObjectLayoutCopyFrontendEvidence, bingo.ObjectLayoutCopyContract, bingo.ObjectLayoutCopyHIRArtifact, bingo.ObjectLayoutCopyMIRArtifact, error) {
	fail := func(message string) (ObjectLayoutCopyFrontendEvidence, bingo.ObjectLayoutCopyContract, bingo.ObjectLayoutCopyHIRArtifact, bingo.ObjectLayoutCopyMIRArtifact, error) {
		return ObjectLayoutCopyFrontendEvidence{}, bingo.ObjectLayoutCopyContract{}, bingo.ObjectLayoutCopyHIRArtifact{}, bingo.ObjectLayoutCopyMIRArtifact{}, fmt.Errorf("%s", message)
	}
	indexes := indexPrimitiveSnapshot(snapshot)
	var function NodeSnapshot
	for _, node := range snapshot.Nodes {
		if node.Kind == snapshotKindFunctionDeclaration {
			if function.ID != "" {
				return fail("object layout copy requires exactly one function")
			}
			function = node
		}
	}
	if function.ID == "" || function.ModifierBits != snapshotModifierExport || childText(function, "name", indexes.Nodes) != "objectLayoutCopy" {
		return fail("object layout copy function is invalid")
	}
	body := indexes.Nodes[childByRole(function, "body")]
	statements := namedChildren(body, "statement[")
	if body.Kind != snapshotKindBlock || len(statements) != 4 {
		return fail("object layout copy body is invalid")
	}
	sourceDecl, sourceLiteral, err := vert010Variable(statements[0], indexes.Nodes)
	if err != nil || childText(sourceDecl, "name", indexes.Nodes) != "source" || sourceLiteral.Kind != snapshotKindObjectLiteralExpression {
		return fail("object layout copy source is invalid")
	}
	targetDecl, targetLiteral, err := vert010Variable(statements[1], indexes.Nodes)
	if err != nil || childText(targetDecl, "name", indexes.Nodes) != "copy" || targetLiteral.Kind != snapshotKindObjectLiteralExpression || childByRole(targetDecl, "type") == "" || targetLiteral.ContextualType != nodeTypeID(targetDecl) || sourceLiteral.ID == targetLiteral.ID {
		return fail("object layout copy target is invalid")
	}
	sp, err := requireRoleKind(sourceLiteral, "child[0]", snapshotKindShorthandPropertyAssignment, indexes.Nodes)
	if err != nil || childText(sp, "name", indexes.Nodes) != "value" {
		return fail("object layout copy shorthand is invalid")
	}
	tp, err := requireRoleKind(targetLiteral, "child[0]", "KindPropertyAssignment", indexes.Nodes)
	if err != nil || childText(tp, "name", indexes.Nodes) != "value" {
		return fail("object layout copy property is invalid")
	}
	copyLoad, err := requireRoleKind(tp, "initializer", snapshotKindPropertyAccessExpression, indexes.Nodes)
	if err != nil || !vert010Access(copyLoad, "source", indexes.Nodes) {
		return fail("object layout copy load is invalid")
	}
	expressionStatement := indexes.Nodes[statements[2]]
	assignment, err := requireRoleKind(expressionStatement, "expression", snapshotKindBinaryExpression, indexes.Nodes)
	if err != nil || assignment.SyntaxPayload.Operator != snapshotKindEqualsToken {
		return fail("object layout copy mutation is invalid")
	}
	store, e1 := requireRoleKind(assignment, "left", snapshotKindPropertyAccessExpression, indexes.Nodes)
	add, e2 := requireRoleKind(assignment, "right", snapshotKindBinaryExpression, indexes.Nodes)
	load, e3 := requireRoleKind(add, "left", snapshotKindPropertyAccessExpression, indexes.Nodes)
	one, e4 := requireRoleKind(add, "right", snapshotKindNumericLiteral, indexes.Nodes)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || !vert010Access(store, "source", indexes.Nodes) || add.SyntaxPayload.Operator != snapshotKindPlusToken || !vert010Access(load, "source", indexes.Nodes) || one.Constant.Kind != "number" || one.Constant.Number != 1 {
		return fail("object layout copy mutation proof is invalid")
	}
	ret := indexes.Nodes[statements[3]]
	retLoad, err := requireRoleKind(ret, "expression", snapshotKindPropertyAccessExpression, indexes.Nodes)
	if ret.Kind != snapshotKindReturnStatement || err != nil || !vert010Access(retLoad, "copy", indexes.Nodes) {
		return fail("object layout copy return is invalid")
	}
	if copyLoad.Symbol == "" || copyLoad.Symbol != store.Symbol || retLoad.Symbol == "" || retLoad.Symbol == copyLoad.Symbol {
		return fail("object layout copy symbols are invalid")
	}
	source, err := objectViewSemanticContract(nodeTypeID(sourceDecl), indexes)
	if err != nil {
		return fail("object layout copy source semantics are invalid")
	}
	target, err := objectViewSemanticContract(nodeTypeID(targetDecl), indexes)
	if err != nil || len(target.Properties) != 1 || !target.Properties[0].Readonly {
		return fail("object layout copy target semantics are invalid")
	}
	relations, err := bingo.BuildTypeRelationGraph([]bingo.TypeRelationNode{{TypeKey: source.Properties[0].ReadTypeKey, DeclarationKey: source.Properties[0].ReadTypeKey}}, nil)
	if err != nil {
		return fail(err.Error())
	}
	layoutTarget, err := bingo.CanonicalObjectLayoutTarget(bingo.ObjectLayoutX8664Triple)
	if err != nil {
		return fail(err.Error())
	}
	sl, err := bingo.PlanObjectLayout(source.TypeKey, layoutTarget, []bingo.ObjectLayoutPropertyInput{{Key: "value", Kind: bingo.ObjectPropertyData, Representation: "f64"}})
	if err != nil {
		return fail(err.Error())
	}
	tl, err := bingo.PlanObjectLayout(target.TypeKey, layoutTarget, []bingo.ObjectLayoutPropertyInput{{Key: "value", Kind: bingo.ObjectPropertyData, Representation: "f64"}})
	if err != nil {
		return fail(err.Error())
	}
	copyContract, err := bingo.BuildObjectLayoutCopyContract(source, target, relations, sl, tl)
	if err != nil {
		return fail(err.Error())
	}
	hir, err := bingo.BuildObjectLayoutCopyHIRArtifact(copyContract)
	if err != nil {
		return fail(err.Error())
	}
	mir, err := bingo.LowerObjectLayoutCopyMIR(hir)
	if err != nil {
		return fail(err.Error())
	}
	e := ObjectLayoutCopyFrontendEvidence{string(function.ID), string(sourceLiteral.ID), string(targetLiteral.ID), string(copyLoad.Symbol), string(retLoad.Symbol)}
	return e, copyContract, hir, mir, nil
}

func (r ObjectLayoutCopyReplayResult) CanonicalBytes() ([]byte, error) {
	encoded, hash, err := canonicalObjectLayoutCopyReplay(r)
	if err != nil {
		return nil, err
	}
	if r.ContentHash == "" || r.ContentHash != hash {
		return nil, fmt.Errorf("object layout copy replay content hash mismatch")
	}
	return encoded, nil
}
func DecodeObjectLayoutCopyReplay(data []byte) (*ObjectLayoutCopyReplayResult, error) {
	if len(data) > maxObjectLayoutCopyReplayBytes {
		return nil, fmt.Errorf("object layout copy replay exceeds %d bytes", maxObjectLayoutCopyReplayBytes)
	}
	var r ObjectLayoutCopyReplayResult
	if err := jsonx.Unmarshal(data, &r, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	if _, err := r.CanonicalBytes(); err != nil {
		return nil, err
	}
	return &r, nil
}
func canonicalObjectLayoutCopyReplay(r ObjectLayoutCopyReplayResult) ([]byte, string, error) {
	r.ContentHash = ""
	if r.SchemaVersion != ObjectLayoutCopyReplaySchemaVersion || r.FrontendSnapshotHash == "" || r.FrontendSnapshot.ContentHash != r.FrontendSnapshotHash {
		return nil, "", fmt.Errorf("invalid object layout copy replay identity")
	}
	if err := frontendwire.ValidateProgramSnapshot(r.FrontendSnapshot); err != nil {
		return nil, "", fmt.Errorf("invalid object layout copy frontend snapshot: %w", err)
	}
	if err := validateCompilerIdentityForSnapshot(r.CompilerBuildIdentity, r.FrontendSnapshot); err != nil {
		return nil, "", err
	}
	e, c, h, m, err := deriveObjectLayoutCopy(r.FrontendSnapshot)
	if err != nil || e != r.Evidence || c.ContentHash != r.Copy.ContentHash || h.ContentHash != r.HIR.ContentHash || m.ContentHash != r.MIR.ContentHash {
		return nil, "", fmt.Errorf("object layout copy replay does not match embedded snapshot")
	}
	encoded, err := jsonx.Marshal(r)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	r.ContentHash = hash
	encoded, err = jsonx.Marshal(r)
	return encoded, hash, err
}
