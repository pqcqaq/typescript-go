package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const FunctionThunkMIRSchemaVersion uint32 = 1
const maxFunctionThunkMIRBytes = 2 << 20

const (
	FunctionThunkObjectRepresentation = "gc-ref"
	FunctionThunkCodeRepresentation   = "code-ptr"
)

type FunctionThunkMIRFunctionRefABI struct {
	CallingConvention     string `json:"callingConvention"`
	CodeRepresentation    string `json:"codeRepresentation"`
	ObjectRepresentation  string `json:"objectRepresentation"`
	EnvironmentABI        string `json:"environmentAbi"`
	EnvironmentFieldIndex uint32 `json:"environmentFieldIndex"`
	CodeFieldIndex        uint32 `json:"codeFieldIndex"`
}

type FunctionThunkMIRInstruction struct {
	ID                  ValueID               `json:"id"`
	Operation           string                `json:"operation"`
	Operands            []ValueID             `json:"operands"`
	Representation      string                `json:"representation"`
	SourceTypeKey       string                `json:"sourceTypeKey"`
	TargetTypeKey       string                `json:"targetTypeKey"`
	RelationPath        []string              `json:"relationPath"`
	CalleeSignatureHash string                `json:"calleeSignatureHash,omitempty"`
	Effects             []FunctionThunkEffect `json:"effects"`
	MaySafepoint        bool                  `json:"maySafepoint"`
}

type FunctionThunkMIRArtifact struct {
	SchemaVersion           uint32                         `json:"schemaVersion"`
	HIRHash                 string                         `json:"hirHash"`
	HIR                     FunctionThunkHIRArtifact       `json:"hir"`
	TargetTriple            string                         `json:"targetTriple"`
	DataLayoutHash          string                         `json:"dataLayoutHash"`
	FunctionRefABI          FunctionThunkMIRFunctionRefABI `json:"functionRefAbi"`
	ParameterRepresentation string                         `json:"parameterRepresentation"`
	ReturnRepresentation    string                         `json:"returnRepresentation"`
	PreservesEnvironment    bool                           `json:"preservesEnvironment"`
	Instructions            []FunctionThunkMIRInstruction  `json:"instructions"`
	ReturnValueID           ValueID                        `json:"returnValueId"`
	ContentHash             string                         `json:"contentHash"`
}

func LowerFunctionThunkMIR(hir FunctionThunkHIRArtifact, targetTriple, dataLayoutHash string) (FunctionThunkMIRArtifact, error) {
	module := FunctionThunkMIRArtifact{SchemaVersion: FunctionThunkMIRSchemaVersion, HIRHash: hir.ContentHash, HIR: hir, TargetTriple: targetTriple, DataLayoutHash: dataLayoutHash}
	abi, instructions, returnValue, err := deriveFunctionThunkMIR(module)
	if err != nil {
		return FunctionThunkMIRArtifact{}, err
	}
	module.FunctionRefABI, module.ParameterRepresentation, module.ReturnRepresentation = abi, FunctionThunkObjectRepresentation, FunctionThunkObjectRepresentation
	module.PreservesEnvironment, module.Instructions, module.ReturnValueID = true, instructions, returnValue
	_, hash, err := CanonicalFunctionThunkMIR(module)
	module.ContentHash = hash
	return module, err
}

func CanonicalFunctionThunkMIR(module FunctionThunkMIRArtifact) ([]byte, string, error) {
	module.ContentHash = ""
	if module.SchemaVersion != FunctionThunkMIRSchemaVersion || module.HIRHash != module.HIR.ContentHash || strings.TrimSpace(module.TargetTriple) == "" || !validFunctionThunkHash(module.DataLayoutHash) {
		return nil, "", fmt.Errorf("invalid function thunk MIR header")
	}
	abi, instructions, returnValue, err := deriveFunctionThunkMIR(module)
	if err != nil {
		return nil, "", err
	}
	if module.FunctionRefABI != abi || module.ParameterRepresentation != FunctionThunkObjectRepresentation || module.ReturnRepresentation != FunctionThunkObjectRepresentation || !module.PreservesEnvironment || module.ReturnValueID != returnValue || !equalFunctionThunkMIRInstructions(module.Instructions, instructions) {
		return nil, "", fmt.Errorf("function thunk MIR does not match canonical lowering")
	}
	encoded, err := jsonx.Marshal(module)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	module.ContentHash = hash
	encoded, err = jsonx.Marshal(module)
	return encoded, hash, err
}

func VerifyCanonicalFunctionThunkMIR(module FunctionThunkMIRArtifact) error {
	claimed := module.ContentHash
	_, want, err := CanonicalFunctionThunkMIR(module)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("function thunk MIR content hash mismatch")
	}
	return nil
}

func DecodeFunctionThunkMIR(data []byte) (*FunctionThunkMIRArtifact, error) {
	if len(data) > maxFunctionThunkMIRBytes {
		return nil, fmt.Errorf("function thunk MIR exceeds %d bytes", maxFunctionThunkMIRBytes)
	}
	var module FunctionThunkMIRArtifact
	if err := jsonx.Unmarshal(data, &module, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode function thunk MIR: %w", err)
	}
	if err := VerifyCanonicalFunctionThunkMIR(module); err != nil {
		return nil, err
	}
	return &module, nil
}

func deriveFunctionThunkMIR(module FunctionThunkMIRArtifact) (FunctionThunkMIRFunctionRefABI, []FunctionThunkMIRInstruction, ValueID, error) {
	if err := VerifyCanonicalFunctionThunkHIRArtifact(module.HIR); err != nil {
		return FunctionThunkMIRFunctionRefABI{}, nil, 0, err
	}
	abi := FunctionThunkMIRFunctionRefABI{CallingConvention: FunctionThunkCallingConvention, CodeRepresentation: FunctionThunkCodeRepresentation, ObjectRepresentation: FunctionThunkObjectRepresentation, EnvironmentABI: FunctionThunkEnvironmentABI, CodeFieldIndex: 0, EnvironmentFieldIndex: 1}
	h := module.HIR
	operations := []FunctionThunkMIRInstruction{
		{ID: 2, Operation: "function.thunk.parameter.identity", Operands: []ValueID{1}, Representation: FunctionThunkObjectRepresentation, SourceTypeKey: h.Thunk.Target.ParameterTypeKey, TargetTypeKey: h.Thunk.Source.ParameterTypeKey, RelationPath: slices.Clone(h.Thunk.ParameterPath), Effects: []FunctionThunkEffect{}, MaySafepoint: false},
		{ID: 3, Operation: "function.thunk.source.call", Operands: []ValueID{2}, Representation: FunctionThunkObjectRepresentation, SourceTypeKey: h.Thunk.Source.ParameterTypeKey, TargetTypeKey: h.Thunk.Source.ReturnTypeKey, CalleeSignatureHash: h.SourceSignatureHash, Effects: slices.Clone(h.Thunk.Source.Effects), MaySafepoint: slices.Contains(h.Thunk.Source.Effects, FunctionThunkEffectAllocate)},
		{ID: 4, Operation: "function.thunk.return.identity", Operands: []ValueID{3}, Representation: FunctionThunkObjectRepresentation, SourceTypeKey: h.Thunk.Source.ReturnTypeKey, TargetTypeKey: h.Thunk.Target.ReturnTypeKey, RelationPath: slices.Clone(h.Thunk.ReturnPath), Effects: []FunctionThunkEffect{}, MaySafepoint: false},
	}
	return abi, operations, 4, nil
}

func equalFunctionThunkMIRInstructions(left, right []FunctionThunkMIRInstruction) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].ID != right[i].ID || left[i].Operation != right[i].Operation || left[i].Representation != right[i].Representation || left[i].SourceTypeKey != right[i].SourceTypeKey || left[i].TargetTypeKey != right[i].TargetTypeKey || left[i].CalleeSignatureHash != right[i].CalleeSignatureHash || left[i].MaySafepoint != right[i].MaySafepoint || !slices.Equal(left[i].Operands, right[i].Operands) || !slices.Equal(left[i].RelationPath, right[i].RelationPath) || !slices.Equal(left[i].Effects, right[i].Effects) {
			return false
		}
	}
	return true
}
