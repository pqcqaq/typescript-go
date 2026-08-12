package llvmbackend

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	"github.com/microsoft/typescript-go/internal/bingo"
	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const FunctionThunkBackendPlanSchemaVersion uint32 = 1
const maxFunctionThunkBackendPlanBytes = 2 << 20

type FunctionThunkBackendPlan struct {
	SchemaVersion           uint32                         `json:"schemaVersion"`
	MIRHash                 string                         `json:"mirHash"`
	MIR                     bingo.FunctionThunkMIRArtifact `json:"mir"`
	FunctionName            string                         `json:"functionName"`
	SourceSignatureHash     string                         `json:"sourceSignatureHash"`
	CallingConvention       string                         `json:"callingConvention"`
	CodeRepresentation      string                         `json:"codeRepresentation"`
	EnvironmentABI          string                         `json:"environmentAbi"`
	ParameterRepresentation string                         `json:"parameterRepresentation"`
	ReturnRepresentation    string                         `json:"returnRepresentation"`
	PreservesEnvironment    bool                           `json:"preservesEnvironment"`
	Allocates               bool                           `json:"allocates"`
	RuntimeCalls            []string                       `json:"runtimeCalls"`
	ContentHash             string                         `json:"contentHash"`
}

func BuildFunctionThunkBackendPlan(mir bingo.FunctionThunkMIRArtifact) (FunctionThunkBackendPlan, error) {
	plan := FunctionThunkBackendPlan{SchemaVersion: FunctionThunkBackendPlanSchemaVersion, MIRHash: mir.ContentHash, MIR: mir, FunctionName: "bingo_function_thunk_object_v1", SourceSignatureHash: mir.HIR.SourceSignatureHash, CallingConvention: mir.FunctionRefABI.CallingConvention, CodeRepresentation: mir.FunctionRefABI.CodeRepresentation, EnvironmentABI: mir.FunctionRefABI.EnvironmentABI, ParameterRepresentation: mir.ParameterRepresentation, ReturnRepresentation: mir.ReturnRepresentation, PreservesEnvironment: true, RuntimeCalls: []string{}}
	_, hash, err := CanonicalFunctionThunkBackendPlan(plan)
	plan.ContentHash = hash
	return plan, err
}

func CanonicalFunctionThunkBackendPlan(plan FunctionThunkBackendPlan) ([]byte, string, error) {
	plan.ContentHash = ""
	if plan.SchemaVersion != FunctionThunkBackendPlanSchemaVersion || plan.FunctionName != "bingo_function_thunk_object_v1" || !plan.PreservesEnvironment || plan.Allocates || len(plan.RuntimeCalls) != 0 {
		return nil, "", fmt.Errorf("invalid function thunk backend plan header")
	}
	if err := bingo.VerifyCanonicalFunctionThunkMIR(plan.MIR); err != nil {
		return nil, "", err
	}
	if plan.MIRHash != plan.MIR.ContentHash || plan.SourceSignatureHash != plan.MIR.HIR.SourceSignatureHash || plan.CallingConvention != bingo.FunctionThunkCallingConvention || plan.CodeRepresentation != bingo.FunctionThunkCodeRepresentation || plan.EnvironmentABI != bingo.FunctionThunkEnvironmentABI || plan.ParameterRepresentation != bingo.FunctionThunkObjectRepresentation || plan.ReturnRepresentation != bingo.FunctionThunkObjectRepresentation {
		return nil, "", fmt.Errorf("function thunk backend ABI mismatch")
	}
	for _, instruction := range plan.MIR.Instructions {
		if instruction.MaySafepoint || slices.Contains(instruction.Effects, bingo.FunctionThunkEffectAllocate) {
			return nil, "", fmt.Errorf("function thunk backend requires a GC root plan for safepointing calls")
		}
	}
	encoded, err := jsonx.Marshal(plan)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	plan.ContentHash = hash
	encoded, err = jsonx.Marshal(plan)
	return encoded, hash, err
}

func VerifyCanonicalFunctionThunkBackendPlan(plan FunctionThunkBackendPlan) error {
	claimed := plan.ContentHash
	_, want, err := CanonicalFunctionThunkBackendPlan(plan)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("function thunk backend plan content hash mismatch")
	}
	return nil
}

func DecodeFunctionThunkBackendPlan(data []byte) (*FunctionThunkBackendPlan, error) {
	if len(data) > maxFunctionThunkBackendPlanBytes {
		return nil, fmt.Errorf("function thunk backend plan exceeds %d bytes", maxFunctionThunkBackendPlanBytes)
	}
	var plan FunctionThunkBackendPlan
	if err := jsonx.Unmarshal(data, &plan, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode function thunk backend plan: %w", err)
	}
	if err := VerifyCanonicalFunctionThunkBackendPlan(plan); err != nil {
		return nil, err
	}
	return &plan, nil
}
