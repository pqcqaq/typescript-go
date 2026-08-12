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

const PropertyAccessAdmissionReplaySchemaVersion uint32 = 1
const maxPropertyAccessAdmissionReplayBytes = 3 << 20

type PropertyAccessAdmissionEvidence struct {
	FunctionName     string                        `json:"functionName"`
	AccessNodeID     NodeID                        `json:"accessNodeId"`
	ReceiverTypeHash string                        `json:"receiverTypeHash"`
	KeyTypeHash      string                        `json:"keyTypeHash"`
	Admission        bingo.PropertyAccessAdmission `json:"admission"`
}

type PropertyAccessAdmissionReplay struct {
	SchemaVersion         uint32                            `json:"schemaVersion"`
	FrontendSnapshotHash  string                            `json:"frontendSnapshotHash"`
	CompilerBuildIdentity bingo.CompilerBuildIdentity       `json:"compilerBuildIdentity"`
	FrontendSnapshot      ProgramSnapshot                   `json:"frontendSnapshot"`
	Accesses              []PropertyAccessAdmissionEvidence `json:"accesses"`
	ContentHash           string                            `json:"contentHash"`
}

func LowerPropertyAccessAdmissionHIR(replay PropertyAccessAdmissionReplay) (bingo.PropertyAccessHIRArtifact, error) {
	if _, err := replay.CanonicalBytes(); err != nil {
		return bingo.PropertyAccessHIRArtifact{}, err
	}
	inputs := make([]bingo.PropertyAccessHIRInput, len(replay.Accesses))
	for i, access := range replay.Accesses {
		inputs[i] = bingo.PropertyAccessHIRInput{FunctionName: access.FunctionName, AccessNodeID: string(access.AccessNodeID), ReceiverTypeHash: access.ReceiverTypeHash, KeyTypeHash: access.KeyTypeHash, Admission: access.Admission}
	}
	return bingo.BuildPropertyAccessHIRArtifact(replay.FrontendSnapshotHash, replay.ContentHash, inputs)
}

func ReplayPropertyAccessAdmissionSnapshot(snapshot ProgramSnapshot, identity bingo.CompilerBuildIdentity) (PropertyAccessAdmissionReplay, error) {
	if err := frontendwire.ValidateProgramSnapshot(snapshot); err != nil {
		return PropertyAccessAdmissionReplay{}, err
	}
	if err := validateCompilerIdentityForSnapshot(identity, snapshot); err != nil {
		return PropertyAccessAdmissionReplay{}, err
	}
	accesses, err := derivePropertyAccessAdmissions(snapshot)
	if err != nil {
		return PropertyAccessAdmissionReplay{}, err
	}
	result := PropertyAccessAdmissionReplay{SchemaVersion: PropertyAccessAdmissionReplaySchemaVersion, FrontendSnapshotHash: snapshot.ContentHash, CompilerBuildIdentity: identity, FrontendSnapshot: snapshot, Accesses: accesses}
	_, hash, err := canonicalPropertyAccessAdmissionReplay(result)
	result.ContentHash = hash
	return result, err
}

func (result PropertyAccessAdmissionReplay) CanonicalBytes() ([]byte, error) {
	encoded, hash, err := canonicalPropertyAccessAdmissionReplay(result)
	if err != nil {
		return nil, err
	}
	if result.ContentHash == "" || result.ContentHash != hash {
		return nil, fmt.Errorf("property access admission replay content hash mismatch")
	}
	return encoded, nil
}

func DecodePropertyAccessAdmissionReplay(data []byte) (*PropertyAccessAdmissionReplay, error) {
	data = slices.Clone(data)
	for len(data) > 0 && (data[len(data)-1] == '\n' || data[len(data)-1] == '\r') {
		data = data[:len(data)-1]
	}
	if len(data) > maxPropertyAccessAdmissionReplayBytes {
		return nil, fmt.Errorf("property access admission replay exceeds %d bytes", maxPropertyAccessAdmissionReplayBytes)
	}
	var result PropertyAccessAdmissionReplay
	if err := jsonx.Unmarshal(data, &result, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode property access admission replay: %w", err)
	}
	if _, err := result.CanonicalBytes(); err != nil {
		return nil, err
	}
	return &result, nil
}

func canonicalPropertyAccessAdmissionReplay(result PropertyAccessAdmissionReplay) ([]byte, string, error) {
	result.ContentHash = ""
	if result.SchemaVersion != PropertyAccessAdmissionReplaySchemaVersion || result.FrontendSnapshotHash == "" || result.FrontendSnapshot.ContentHash != result.FrontendSnapshotHash {
		return nil, "", fmt.Errorf("invalid property access admission replay identity")
	}
	if err := bingo.ValidateCompilerBuildIdentity(result.CompilerBuildIdentity); err != nil {
		return nil, "", err
	}
	if err := frontendwire.ValidateProgramSnapshot(result.FrontendSnapshot); err != nil {
		return nil, "", err
	}
	want, err := derivePropertyAccessAdmissions(result.FrontendSnapshot)
	if err != nil {
		return nil, "", err
	}
	left, _ := jsonx.Marshal(result.Accesses)
	right, _ := jsonx.Marshal(want)
	if !slices.Equal(left, right) {
		return nil, "", fmt.Errorf("property access admission replay does not match embedded frontend")
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

func derivePropertyAccessAdmissions(snapshot ProgramSnapshot) ([]PropertyAccessAdmissionEvidence, error) {
	if snapshot.Config.Bingo.Profile != frontendwire.ProfileInterop {
		return nil, fmt.Errorf("property access admission fixture requires interop profile")
	}
	indexes := indexPrimitiveSnapshot(snapshot)
	names := []string{"direct", "dynamic", "finite", "literal"}
	result := make([]PropertyAccessAdmissionEvidence, 0, len(names))
	for _, name := range names {
		function, err := propertyAccessNamedFunction(name, indexes)
		if err != nil {
			return nil, err
		}
		access, err := propertyAccessReturnExpression(function, indexes)
		if err != nil {
			return nil, err
		}
		receiver := indexes.Nodes[childByRole(access, "child[0]")]
		key := indexes.Nodes[childByRole(access, "child[1]")]
		receiverType, ok := indexes.Types[nodeTypeID(receiver)]
		if !ok || receiverType.CanonicalHash == "" {
			return nil, fmt.Errorf("property access %s receiver type is unavailable", name)
		}
		domain, keys, keyHash, sourceID, err := propertyAccessKeyDomain(name, access, key, indexes)
		if err != nil {
			return nil, err
		}
		admission, err := bingo.BuildPropertyAccessAdmission(receiverType.CanonicalHash, domain, keys, bingo.PropertyAccessInterop, sourceID)
		if err != nil {
			return nil, err
		}
		result = append(result, PropertyAccessAdmissionEvidence{FunctionName: name, AccessNodeID: access.ID, ReceiverTypeHash: receiverType.CanonicalHash, KeyTypeHash: keyHash, Admission: admission})
	}
	return result, nil
}

func propertyAccessNamedFunction(name string, indexes snapshotSemanticFactIndexes) (NodeSnapshot, error) {
	var found NodeSnapshot
	for _, node := range indexes.Nodes {
		if node.Kind == snapshotKindFunctionDeclaration && childText(node, "name", indexes.Nodes) == name {
			if found.ID != "" || node.ModifierBits != snapshotModifierExport {
				return NodeSnapshot{}, fmt.Errorf("property access function %s is invalid", name)
			}
			found = node
		}
	}
	if found.ID == "" {
		return NodeSnapshot{}, fmt.Errorf("property access function %s is unavailable", name)
	}
	return found, nil
}

func propertyAccessReturnExpression(function NodeSnapshot, indexes snapshotSemanticFactIndexes) (NodeSnapshot, error) {
	body, err := requireRoleKind(function, "body", snapshotKindBlock, indexes.Nodes)
	if err != nil {
		return NodeSnapshot{}, err
	}
	statements := namedChildren(body, "statement[")
	if len(statements) != 1 {
		return NodeSnapshot{}, fmt.Errorf("property access function must have one statement")
	}
	ret := indexes.Nodes[statements[0]]
	if ret.Kind != snapshotKindReturnStatement {
		return NodeSnapshot{}, fmt.Errorf("property access function must return access")
	}
	access := indexes.Nodes[childByRole(ret, "expression")]
	if access.Kind != snapshotKindPropertyAccessExpression && access.Kind != snapshotKindElementAccessExpression {
		return NodeSnapshot{}, fmt.Errorf("property access return is invalid")
	}
	return access, nil
}

func propertyAccessKeyDomain(name string, access, key NodeSnapshot, indexes snapshotSemanticFactIndexes) (bingo.PropertyKeyDomain, []string, string, string, error) {
	if access.Kind == snapshotKindPropertyAccessExpression {
		if name != "direct" || key.Kind != snapshotKindIdentifier || key.SyntaxPayload.Text != "left" {
			return "", nil, "", "", fmt.Errorf("direct property access is invalid")
		}
		typ := indexes.Types[nodeTypeID(key)]
		return bingo.PropertyKeyDirect, []string{"left"}, typ.CanonicalHash, "", nil
	}
	typ, ok := indexes.Types[nodeTypeID(key)]
	if !ok {
		return "", nil, "", "", fmt.Errorf("property key type is unavailable")
	}
	switch name {
	case "literal":
		if key.Kind != snapshotKindStringLiteral || key.SyntaxPayload.Text != "right" {
			return "", nil, "", "", fmt.Errorf("literal key is invalid")
		}
		return bingo.PropertyKeyLiteral, []string{"right"}, typ.CanonicalHash, "", nil
	case "finite":
		values, err := literalStringUnionValues(typ, indexes.Types)
		if err != nil || !slices.Equal(values, []string{"left", "right"}) {
			return "", nil, "", "", fmt.Errorf("finite key domain is invalid")
		}
		return bingo.PropertyKeyLiteralUnion, values, typ.CanonicalHash, "", nil
	case "dynamic":
		if key.Kind != snapshotKindIdentifier || key.SyntaxPayload.Text != "key" || typ.Kind != "intrinsic" || typ.TypePayload.Tag != "intrinsic" || !strings.HasSuffix(typ.TypePayload.Scalar, "|intrinsic:string") {
			return "", nil, "", "", fmt.Errorf("dynamic key is invalid")
		}
		receiver := indexes.Nodes[childByRole(access, "child[0]")]
		if receiver.Kind != snapshotKindCallExpression || childText(receiver, "callee", indexes.Nodes) != "hostRecord" {
			return "", nil, "", "", fmt.Errorf("dynamic receiver is not an explicit host boundary")
		}
		signature, ok := indexes.Signatures[receiver.SelectedSignature]
		declaration := indexes.Nodes[signature.Declaration]
		if !ok || declaration.Kind != snapshotKindFunctionDeclaration || declaration.ModifierBits != snapshotModifierDeclare || childText(declaration, "name", indexes.Nodes) != "hostRecord" || len(signature.Parameters) != 0 || len(signature.Effects) != 1 || signature.Effects[0] != "unknown" || signature.EffectProof.Kind != "declaration-only" || signature.EffectProof.Complete {
			return "", nil, "", "", fmt.Errorf("dynamic receiver host signature is invalid")
		}
		return bingo.PropertyKeyUnknown, nil, typ.CanonicalHash, "frontend-access:" + string(access.ID), nil
	default:
		return "", nil, "", "", fmt.Errorf("unexpected property access function")
	}
}

func literalStringUnionValues(typ TypeSnapshot, types map[TypeID]TypeSnapshot) ([]string, error) {
	if typ.Kind != "union" || typ.TypePayload.Tag != "union" || len(typ.ElementTypes) < 2 {
		return nil, fmt.Errorf("not a literal union")
	}
	values := make([]string, 0, len(typ.ElementTypes))
	for _, id := range typ.ElementTypes {
		member, ok := types[id]
		if !ok || member.Kind != "literal" {
			return nil, fmt.Errorf("non-literal union member")
		}
		const marker = "|literal:string:"
		_, value, ok := strings.Cut(member.TypePayload.Scalar, marker)
		if !ok || value == "" {
			return nil, fmt.Errorf("invalid literal payload")
		}
		values = append(values, value)
	}
	slices.Sort(values)
	return values, nil
}
