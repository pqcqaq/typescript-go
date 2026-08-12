package ast2bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const CheckedObjectCastReplaySchemaVersion uint32 = 4
const CheckedObjectCastFrontendEvidenceSchemaVersion uint32 = 2
const maxCheckedObjectCastReplayBytes = 2 << 20

// CheckedObjectCastReplayResult binds the checked-cast admission inputs to a
// real frontend snapshot. It intentionally captures an ambient boundary and a
// target shape, not a TypeScript assertion expression.
type CheckedObjectCastReplayResult struct {
	SchemaVersion         uint32                            `json:"schemaVersion"`
	FrontendSnapshotHash  string                            `json:"frontendSnapshotHash"`
	CompilerBuildIdentity bingo.CompilerBuildIdentity       `json:"compilerBuildIdentity"`
	FrontendSnapshot      ProgramSnapshot                   `json:"frontendSnapshot"`
	Evidence              CheckedObjectCastFrontendEvidence `json:"evidence"`
	Cast                  bingo.CheckedObjectCastContract   `json:"cast"`
	ContentHash           string                            `json:"contentHash"`
}

type CheckedObjectCastFrontendEvidence struct {
	SchemaVersion         uint32                      `json:"schemaVersion"`
	FrontendSnapshotHash  string                      `json:"frontendSnapshotHash"`
	CompilerBuildIdentity bingo.CompilerBuildIdentity `json:"compilerBuildIdentity"`
	HostSymbolID          string                      `json:"hostSymbolId"`
	HostSignatureHash     string                      `json:"hostSignatureHash"`
	SourceUnknownTypeHash string                      `json:"sourceUnknownTypeHash"`
	TargetTypeHash        string                      `json:"targetTypeHash"`
	TargetPropertyKey     string                      `json:"targetPropertyKey"`
	CaseUnionTypeHash     string                      `json:"caseUnionTypeHash"`
	ContentHash           string                      `json:"contentHash"`
}

func ReplayCheckedObjectCastFrontendSnapshot(data []byte, identity bingo.CompilerBuildIdentity) (CheckedObjectCastReplayResult, error) {
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		return CheckedObjectCastReplayResult{}, err
	}
	return ReplayCheckedObjectCastSnapshot(frontend.Program, identity)
}

func ReplayCheckedObjectCastSnapshot(snapshot ProgramSnapshot, identity bingo.CompilerBuildIdentity) (CheckedObjectCastReplayResult, error) {
	if err := frontendwire.ValidateProgramSnapshot(snapshot); err != nil {
		return CheckedObjectCastReplayResult{}, fmt.Errorf("validate checked object cast frontend snapshot: %w", err)
	}
	if err := validateCompilerIdentityForSnapshot(identity, snapshot); err != nil {
		return CheckedObjectCastReplayResult{}, err
	}
	evidence, cast, err := deriveCheckedObjectCast(snapshot, identity)
	if err != nil {
		return CheckedObjectCastReplayResult{}, err
	}
	result := CheckedObjectCastReplayResult{SchemaVersion: CheckedObjectCastReplaySchemaVersion, FrontendSnapshotHash: snapshot.ContentHash, CompilerBuildIdentity: identity, FrontendSnapshot: snapshot, Evidence: evidence, Cast: cast}
	_, hash, err := canonicalCheckedObjectCastReplay(result)
	result.ContentHash = hash
	return result, err
}

func checkedObjectCastHostBoundary(indexes snapshotSemanticFactIndexes) (NodeSnapshot, SignatureSnapshot, error) {
	var host NodeSnapshot
	for _, node := range indexes.Nodes {
		if node.Kind != snapshotKindFunctionDeclaration || childText(node, "name", indexes.Nodes) != "hostObject" {
			continue
		}
		if host.ID != "" || node.ModifierBits != snapshotModifierDeclare {
			return NodeSnapshot{}, SignatureSnapshot{}, fmt.Errorf("checked object cast requires one ambient hostObject declaration")
		}
		host = node
	}
	if host.ID == "" || len(host.Children) != 4 || childByRole(host, "modifier[0]") == "" || childByRole(host, "parameter[0]") == "" || childByRole(host, "returnType") == "" {
		return NodeSnapshot{}, SignatureSnapshot{}, fmt.Errorf("checked object cast host boundary shape is invalid")
	}
	var signature SignatureSnapshot
	for _, candidate := range indexes.Signatures {
		if candidate.Declaration == host.ID {
			if signature.ID != 0 {
				return NodeSnapshot{}, SignatureSnapshot{}, fmt.Errorf("checked object cast host boundary is overloaded")
			}
			signature = candidate
		}
	}
	if signature.ID == 0 || signature.MinArgumentCount != 1 || signature.HasRest || len(signature.Parameters) != 1 || len(signature.ParameterFacts) != 1 || signature.ParameterFacts[0].Optional || signature.ParameterFacts[0].Rest || len(signature.Effects) != 1 || signature.Effects[0] != "unknown" || signature.EffectProof.Kind != "declaration-only" || signature.EffectProof.Complete {
		return NodeSnapshot{}, SignatureSnapshot{}, fmt.Errorf("checked object cast host boundary signature is invalid")
	}
	shape, ok := indexes.Types[signature.ParameterFacts[0].Type]
	if !ok || shape.Kind != "union" || shape.TypePayload.Tag != "union" || len(shape.ElementTypes) != 2 {
		return NodeSnapshot{}, SignatureSnapshot{}, fmt.Errorf("checked object cast host boundary cases are invalid")
	}
	cases := map[string]bool{}
	for _, id := range shape.ElementTypes {
		member, ok := indexes.Types[id]
		if !ok || member.Kind != "literal" || member.TypePayload.Tag != "literal" {
			return NodeSnapshot{}, SignatureSnapshot{}, fmt.Errorf("checked object cast host boundary case is not literal")
		}
		const marker = "|literal:string:"
		before, value, ok := strings.Cut(member.TypePayload.Scalar, marker)
		if !ok || value == "" || strings.Contains(before, marker) {
			return NodeSnapshot{}, SignatureSnapshot{}, fmt.Errorf("checked object cast host boundary case payload is invalid")
		}
		cases[value] = true
	}
	if !cases["matching"] || !cases["missing"] || len(cases) != 2 {
		return NodeSnapshot{}, SignatureSnapshot{}, fmt.Errorf("checked object cast host boundary cases are not matching/missing")
	}
	return host, signature, nil
}

func checkedObjectCastTargetContract(targetType TypeSnapshot, indexes snapshotSemanticFactIndexes) (bingo.ObjectSemanticContract, error) {
	if len(targetType.Properties) != 1 || len(targetType.PropertyFacts) != 1 || targetType.Properties[0] != targetType.PropertyFacts[0].Symbol {
		return bingo.ObjectSemanticContract{}, fmt.Errorf("checked object cast target property binding is invalid")
	}
	fact := targetType.PropertyFacts[0]
	property, ok := indexes.Symbols[fact.Symbol]
	if !ok || property.Name != "value" || property.Parent != targetType.Symbol || property.Type != fact.ReadType || len(property.Declarations) != 1 || property.ValueDeclaration != property.Declarations[0] {
		return bingo.ObjectSemanticContract{}, fmt.Errorf("checked object cast target property symbol is invalid")
	}
	declaration, ok := indexes.Nodes[property.Declarations[0]]
	if !ok || declaration.Kind != "KindPropertySignature" || declaration.Parent == "" {
		return bingo.ObjectSemanticContract{}, fmt.Errorf("checked object cast target property declaration is invalid")
	}
	owner := indexes.Nodes[declaration.Parent]
	ownerName := indexes.Nodes[childByRole(owner, "child[0]")]
	if owner.Kind != "KindInterfaceDeclaration" || ownerName.Symbol != targetType.Symbol || childText(declaration, "child[1]", indexes.Nodes) != "value" || declaration.ModifierBits != 1<<3 || !fact.Readonly || !fact.HasGetter || fact.HasSetter || fact.Optional || fact.Visibility != "public" || fact.PrivateIdentity != "" || fact.ReadType == 0 || fact.WriteType != 0 {
		return bingo.ObjectSemanticContract{}, fmt.Errorf("checked object cast target property contract is invalid")
	}
	if kind, err := bingoType(fact.ReadType, indexes.Types); err != nil || kind != bingo.TypeNumber {
		return bingo.ObjectSemanticContract{}, fmt.Errorf("checked object cast target property is not number")
	}
	target, err := objectViewSemanticContract(targetType.ID, indexes)
	if err != nil || len(target.Properties) != 1 || target.Properties[0].Key != "value" || target.Properties[0].Kind != bingo.ObjectPropertyData || !target.Properties[0].Readonly || target.Properties[0].WriteTypeKey != "" {
		return bingo.ObjectSemanticContract{}, fmt.Errorf("checked object cast target is invalid")
	}
	return target, nil
}

func checkedObjectCastTargetType(indexes snapshotSemanticFactIndexes) (TypeSnapshot, error) {
	var declaration NodeSnapshot
	for _, node := range indexes.Nodes {
		if node.Kind != "KindInterfaceDeclaration" {
			continue
		}
		nameID := childByRole(node, "child[0]")
		name, ok := indexes.Nodes[nameID]
		if !ok || name.Kind != snapshotKindIdentifier || name.SyntaxPayload.Text != "HostValue" {
			continue
		}
		if declaration.ID != "" {
			return TypeSnapshot{}, fmt.Errorf("checked object cast target declaration is ambiguous")
		}
		declaration = node
	}
	if declaration.ID == "" {
		return TypeSnapshot{}, fmt.Errorf("checked object cast target declaration is unavailable")
	}
	name := indexes.Nodes[childByRole(declaration, "child[0]")]
	symbol, ok := indexes.Symbols[name.Symbol]
	if !ok || symbol.Name != "HostValue" || len(symbol.Declarations) != 1 || symbol.Declarations[0] != declaration.ID {
		return TypeSnapshot{}, fmt.Errorf("checked object cast target symbol is invalid")
	}
	var target TypeSnapshot
	for _, candidate := range indexes.Types {
		if candidate.Kind == "object" && candidate.Symbol == symbol.ID {
			if target.ID != 0 {
				return TypeSnapshot{}, fmt.Errorf("checked object cast target type is ambiguous")
			}
			target = candidate
		}
	}
	if target.ID == 0 || target.TypePayload.Tag != "object" || target.TypePayload.Scalar != fmt.Sprintf("object|%d|%d|%s|", target.Flags, target.ObjectFlags, symbol.ID) {
		return TypeSnapshot{}, fmt.Errorf("checked object cast target type is invalid")
	}
	return target, nil
}

func (result CheckedObjectCastReplayResult) CanonicalBytes() ([]byte, error) {
	encoded, hash, err := canonicalCheckedObjectCastReplay(result)
	if err != nil {
		return nil, err
	}
	if result.ContentHash == "" || result.ContentHash != hash {
		return nil, fmt.Errorf("checked object cast replay content hash mismatch")
	}
	return encoded, nil
}

func DecodeCheckedObjectCastReplay(data []byte) (*CheckedObjectCastReplayResult, error) {
	if len(data) > maxCheckedObjectCastReplayBytes {
		return nil, fmt.Errorf("checked object cast replay exceeds %d bytes", maxCheckedObjectCastReplayBytes)
	}
	var result CheckedObjectCastReplayResult
	if err := jsonx.Unmarshal(data, &result, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode checked object cast replay: %w", err)
	}
	if _, err := result.CanonicalBytes(); err != nil {
		return nil, err
	}
	return &result, nil
}

func canonicalCheckedObjectCastReplay(result CheckedObjectCastReplayResult) ([]byte, string, error) {
	result.ContentHash = ""
	if result.SchemaVersion != CheckedObjectCastReplaySchemaVersion || result.FrontendSnapshotHash == "" {
		return nil, "", fmt.Errorf("invalid checked object cast replay identity")
	}
	if err := bingo.ValidateCompilerBuildIdentity(result.CompilerBuildIdentity); err != nil {
		return nil, "", err
	}
	if err := frontendwire.ValidateProgramSnapshot(result.FrontendSnapshot); err != nil || result.FrontendSnapshot.ContentHash != result.FrontendSnapshotHash {
		return nil, "", fmt.Errorf("checked object cast replay frontend snapshot mismatch")
	}
	if err := bingo.VerifyCanonicalCheckedObjectCast(result.Cast); err != nil {
		return nil, "", err
	}
	claimedEvidenceHash := result.Evidence.ContentHash
	_, evidenceHash, err := canonicalCheckedObjectCastFrontendEvidence(result.Evidence)
	if err != nil || claimedEvidenceHash == "" || claimedEvidenceHash != evidenceHash {
		return nil, "", fmt.Errorf("invalid checked object cast frontend evidence")
	}
	if result.Evidence.FrontendSnapshotHash != result.FrontendSnapshotHash || result.Evidence.CompilerBuildIdentity != result.CompilerBuildIdentity || result.Cast.Boundary.SourceID != "frontend-evidence:"+result.Evidence.ContentHash || result.Cast.SourceTypeKey != result.Evidence.SourceUnknownTypeHash || result.Cast.Target.TypeKey != result.Evidence.TargetTypeHash || len(result.Cast.Target.Properties) != 1 || result.Cast.Target.Properties[0].ReadTypeKey == "" || result.Cast.Properties[0] != result.Evidence.TargetPropertyKey || result.Cast.Target.Properties[0].Key != result.Evidence.TargetPropertyKey {
		return nil, "", fmt.Errorf("checked object cast frontend evidence does not bind cast")
	}
	derivedEvidence, derivedCast, err := deriveCheckedObjectCast(result.FrontendSnapshot, result.CompilerBuildIdentity)
	if err != nil || derivedEvidence.ContentHash != result.Evidence.ContentHash || derivedCast.ContentHash != result.Cast.ContentHash {
		return nil, "", fmt.Errorf("checked object cast replay does not match embedded frontend snapshot")
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

func deriveCheckedObjectCast(snapshot ProgramSnapshot, identity bingo.CompilerBuildIdentity) (CheckedObjectCastFrontendEvidence, bingo.CheckedObjectCastContract, error) {
	indexes := indexPrimitiveSnapshot(snapshot)
	for _, node := range snapshot.Nodes {
		if node.Kind == snapshotKindAsExpression {
			return CheckedObjectCastFrontendEvidence{}, bingo.CheckedObjectCastContract{}, fmt.Errorf("checked object cast fixture must not contain a TypeScript assertion")
		}
	}
	host, signature, err := checkedObjectCastHostBoundary(indexes)
	if err != nil {
		return CheckedObjectCastFrontendEvidence{}, bingo.CheckedObjectCastContract{}, err
	}
	targetType, err := checkedObjectCastTargetType(indexes)
	if err != nil {
		return CheckedObjectCastFrontendEvidence{}, bingo.CheckedObjectCastContract{}, err
	}
	target, err := checkedObjectCastTargetContract(targetType, indexes)
	if err != nil {
		return CheckedObjectCastFrontendEvidence{}, bingo.CheckedObjectCastContract{}, err
	}
	hostName := indexes.Nodes[childByRole(host, "name")]
	unknown, ok := indexes.Types[signature.ReturnType]
	if hostName.Symbol == "" || hostName.SyntaxPayload.Text != "hostObject" || !ok || unknown.Kind != "unknown" || unknown.CanonicalHash == "" {
		return CheckedObjectCastFrontendEvidence{}, bingo.CheckedObjectCastContract{}, fmt.Errorf("checked object cast host source is invalid")
	}
	layoutTarget, err := bingo.CanonicalObjectLayoutTarget(bingo.ObjectLayoutX8664Triple)
	if err != nil {
		return CheckedObjectCastFrontendEvidence{}, bingo.CheckedObjectCastContract{}, err
	}
	layout, err := bingo.PlanObjectLayout(target.TypeKey, layoutTarget, []bingo.ObjectLayoutPropertyInput{{Key: "value", Kind: bingo.ObjectPropertyData, Representation: "f64"}})
	if err != nil {
		return CheckedObjectCastFrontendEvidence{}, bingo.CheckedObjectCastContract{}, err
	}
	evidence := CheckedObjectCastFrontendEvidence{SchemaVersion: CheckedObjectCastFrontendEvidenceSchemaVersion, FrontendSnapshotHash: snapshot.ContentHash, CompilerBuildIdentity: identity, HostSymbolID: string(hostName.Symbol), HostSignatureHash: signature.CanonicalHash, SourceUnknownTypeHash: unknown.CanonicalHash, TargetTypeHash: target.TypeKey, TargetPropertyKey: target.Properties[0].Key, CaseUnionTypeHash: indexes.Types[signature.ParameterFacts[0].Type].CanonicalHash}
	_, evidenceHash, err := canonicalCheckedObjectCastFrontendEvidence(evidence)
	if err != nil {
		return CheckedObjectCastFrontendEvidence{}, bingo.CheckedObjectCastContract{}, err
	}
	evidence.ContentHash = evidenceHash
	boundary := bingo.DynamicObjectBoundaryArtifact{SchemaVersion: bingo.DynamicObjectBoundarySchemaVersion, Kind: "ffi-import", SourceID: "frontend-evidence:" + evidenceHash}
	_, boundaryHash, err := bingo.CanonicalDynamicObjectBoundary(boundary)
	if err != nil {
		return CheckedObjectCastFrontendEvidence{}, bingo.CheckedObjectCastContract{}, err
	}
	boundary.ContentHash = boundaryHash
	cast := bingo.CheckedObjectCastContract{SchemaVersion: bingo.CheckedObjectCastSchemaVersion, Boundary: boundary, SourceTypeKey: unknown.CanonicalHash, Target: target, TargetLayout: layout, Properties: []string{"value"}, PreservesIdentity: true, ReadonlyResult: true}
	_, castHash, err := bingo.CanonicalCheckedObjectCast(cast)
	cast.ContentHash = castHash
	return evidence, cast, err
}

func canonicalCheckedObjectCastFrontendEvidence(evidence CheckedObjectCastFrontendEvidence) ([]byte, string, error) {
	evidence.ContentHash = ""
	if evidence.SchemaVersion != CheckedObjectCastFrontendEvidenceSchemaVersion || !validReplayHash(evidence.FrontendSnapshotHash) || bingo.ValidateCompilerBuildIdentity(evidence.CompilerBuildIdentity) != nil || evidence.HostSymbolID == "" || !validReplayHash(evidence.HostSignatureHash) || !validReplayHash(evidence.SourceUnknownTypeHash) || !validReplayHash(evidence.TargetTypeHash) || evidence.TargetPropertyKey == "" || !validReplayHash(evidence.CaseUnionTypeHash) {
		return nil, "", fmt.Errorf("invalid checked object cast frontend evidence")
	}
	encoded, err := jsonx.Marshal(evidence)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	evidence.ContentHash = hash
	encoded, err = jsonx.Marshal(evidence)
	return encoded, hash, err
}

func validReplayHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
