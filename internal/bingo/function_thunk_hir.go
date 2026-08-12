package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const FunctionThunkHIRSchemaVersion uint32 = 1
const maxFunctionThunkHIRBytes = 1 << 20

type FunctionThunkHIROperation struct {
	ID                  ValueID               `json:"id"`
	Kind                string                `json:"kind"`
	Operands            []ValueID             `json:"operands"`
	SourceTypeKey       string                `json:"sourceTypeKey"`
	TargetTypeKey       string                `json:"targetTypeKey"`
	RelationPath        []string              `json:"relationPath"`
	CalleeSignatureHash string                `json:"calleeSignatureHash,omitempty"`
	Effects             []FunctionThunkEffect `json:"effects"`
}

type FunctionThunkHIRArtifact struct {
	SchemaVersion        uint32                      `json:"schemaVersion"`
	FrontendSnapshotHash string                      `json:"frontendSnapshotHash"`
	SourceSignatureHash  string                      `json:"sourceSignatureHash"`
	TargetSignatureHash  string                      `json:"targetSignatureHash"`
	AssignmentNodeID     string                      `json:"assignmentNodeId"`
	Thunk                FunctionThunkContract       `json:"thunk"`
	FunctionID           FunctionID                  `json:"functionId"`
	ParameterValueID     ValueID                     `json:"parameterValueId"`
	Operations           []FunctionThunkHIROperation `json:"operations"`
	ReturnValueID        ValueID                     `json:"returnValueId"`
	PreservesEnvironment bool                        `json:"preservesEnvironment"`
	ContentHash          string                      `json:"contentHash"`
}

func BuildFunctionThunkHIRArtifact(frontendHash, sourceSignatureHash, targetSignatureHash, assignmentNodeID string, thunk FunctionThunkContract) (FunctionThunkHIRArtifact, error) {
	artifact := FunctionThunkHIRArtifact{SchemaVersion: FunctionThunkHIRSchemaVersion, FrontendSnapshotHash: frontendHash, SourceSignatureHash: sourceSignatureHash, TargetSignatureHash: targetSignatureHash, AssignmentNodeID: assignmentNodeID, Thunk: thunk, FunctionID: 1, ParameterValueID: 1, PreservesEnvironment: true}
	operations, returnValue, err := deriveFunctionThunkHIR(artifact)
	if err != nil {
		return FunctionThunkHIRArtifact{}, err
	}
	artifact.Operations, artifact.ReturnValueID = operations, returnValue
	_, hash, err := CanonicalFunctionThunkHIRArtifact(artifact)
	artifact.ContentHash = hash
	return artifact, err
}

func CanonicalFunctionThunkHIRArtifact(artifact FunctionThunkHIRArtifact) ([]byte, string, error) {
	artifact.ContentHash = ""
	if artifact.SchemaVersion != FunctionThunkHIRSchemaVersion || artifact.FunctionID != 1 || artifact.ParameterValueID != 1 || !artifact.PreservesEnvironment || !validFunctionThunkHash(artifact.FrontendSnapshotHash) || !validFunctionThunkHash(artifact.SourceSignatureHash) || !validFunctionThunkHash(artifact.TargetSignatureHash) || strings.TrimSpace(artifact.AssignmentNodeID) == "" {
		return nil, "", fmt.Errorf("invalid function thunk HIR header")
	}
	want, returnValue, err := deriveFunctionThunkHIR(artifact)
	if err != nil {
		return nil, "", err
	}
	if artifact.ReturnValueID != returnValue || !equalFunctionThunkHIROperations(artifact.Operations, want) {
		return nil, "", fmt.Errorf("function thunk HIR does not match canonical lowering")
	}
	encoded, err := jsonx.Marshal(artifact)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	artifact.ContentHash = hash
	encoded, err = jsonx.Marshal(artifact)
	return encoded, hash, err
}

func validFunctionThunkHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f')) {
			return false
		}
	}
	return true
}

func VerifyCanonicalFunctionThunkHIRArtifact(artifact FunctionThunkHIRArtifact) error {
	claimed := artifact.ContentHash
	_, want, err := CanonicalFunctionThunkHIRArtifact(artifact)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("function thunk HIR content hash mismatch")
	}
	return nil
}

func DecodeFunctionThunkHIRArtifact(data []byte) (*FunctionThunkHIRArtifact, error) {
	if len(data) > maxFunctionThunkHIRBytes {
		return nil, fmt.Errorf("function thunk HIR exceeds %d bytes", maxFunctionThunkHIRBytes)
	}
	var artifact FunctionThunkHIRArtifact
	if err := jsonx.Unmarshal(data, &artifact, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode function thunk HIR: %w", err)
	}
	if err := VerifyCanonicalFunctionThunkHIRArtifact(artifact); err != nil {
		return nil, err
	}
	return &artifact, nil
}

func deriveFunctionThunkHIR(artifact FunctionThunkHIRArtifact) ([]FunctionThunkHIROperation, ValueID, error) {
	if err := VerifyCanonicalFunctionThunkContract(artifact.Thunk); err != nil {
		return nil, 0, err
	}
	source, target := artifact.Thunk.Source, artifact.Thunk.Target
	return []FunctionThunkHIROperation{
		{ID: 2, Kind: "function.thunk.parameter.convert", Operands: []ValueID{1}, SourceTypeKey: target.ParameterTypeKey, TargetTypeKey: source.ParameterTypeKey, RelationPath: slices.Clone(artifact.Thunk.ParameterPath), Effects: []FunctionThunkEffect{}},
		{ID: 3, Kind: "function.thunk.source.call", Operands: []ValueID{2}, SourceTypeKey: source.ParameterTypeKey, TargetTypeKey: source.ReturnTypeKey, CalleeSignatureHash: artifact.SourceSignatureHash, Effects: slices.Clone(source.Effects)},
		{ID: 4, Kind: "function.thunk.return.convert", Operands: []ValueID{3}, SourceTypeKey: source.ReturnTypeKey, TargetTypeKey: target.ReturnTypeKey, RelationPath: slices.Clone(artifact.Thunk.ReturnPath), Effects: []FunctionThunkEffect{}},
	}, 4, nil
}

func equalFunctionThunkHIROperations(left, right []FunctionThunkHIROperation) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].ID != right[i].ID || left[i].Kind != right[i].Kind || left[i].SourceTypeKey != right[i].SourceTypeKey || left[i].TargetTypeKey != right[i].TargetTypeKey || left[i].CalleeSignatureHash != right[i].CalleeSignatureHash || !slices.Equal(left[i].Operands, right[i].Operands) || !slices.Equal(left[i].RelationPath, right[i].RelationPath) || !slices.Equal(left[i].Effects, right[i].Effects) {
			return false
		}
	}
	return true
}
