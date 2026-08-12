package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const FunctionThunkSchemaVersion uint32 = 1
const maxFunctionThunkBytes = 512 << 10

const (
	FunctionThunkCallingConvention = "bingo.funcref.object.v1"
	FunctionThunkEnvironmentABI    = "gc-ref-or-null"
)

type FunctionThunkEffect string

const (
	FunctionThunkEffectRead     FunctionThunkEffect = "read"
	FunctionThunkEffectWrite    FunctionThunkEffect = "write"
	FunctionThunkEffectThrow    FunctionThunkEffect = "throw"
	FunctionThunkEffectAllocate FunctionThunkEffect = "allocate"
)

type FunctionThunkSignature struct {
	ParameterTypeKey  string                `json:"parameterTypeKey"`
	ReturnTypeKey     string                `json:"returnTypeKey"`
	Effects           []FunctionThunkEffect `json:"effects"`
	CallingConvention string                `json:"callingConvention"`
	EnvironmentABI    string                `json:"environmentAbi"`
}

type FunctionThunkContract struct {
	SchemaVersion        uint32                 `json:"schemaVersion"`
	Source               FunctionThunkSignature `json:"source"`
	Target               FunctionThunkSignature `json:"target"`
	Relations            TypeRelationGraph      `json:"relations"`
	ParameterPath        []string               `json:"parameterPath"`
	ReturnPath           []string               `json:"returnPath"`
	Allocates            bool                   `json:"allocates"`
	Copies               bool                   `json:"copies"`
	RuntimeChecks        bool                   `json:"runtimeChecks"`
	MaySuspend           bool                   `json:"maySuspend"`
	MayEnterHost         bool                   `json:"mayEnterHost"`
	PreservesEnvironment bool                   `json:"preservesEnvironment"`
	ContentHash          string                 `json:"contentHash"`
}

func BuildFunctionThunkContract(source, target FunctionThunkSignature, relations TypeRelationGraph) (FunctionThunkContract, error) {
	contract := FunctionThunkContract{SchemaVersion: FunctionThunkSchemaVersion, Source: source, Target: target, Relations: relations, PreservesEnvironment: true}
	parameterPath, returnPath, err := deriveFunctionThunkPaths(contract)
	if err != nil {
		return FunctionThunkContract{}, err
	}
	contract.ParameterPath, contract.ReturnPath = parameterPath, returnPath
	_, hash, err := CanonicalFunctionThunkContract(contract)
	contract.ContentHash = hash
	return contract, err
}

func CanonicalFunctionThunkContract(contract FunctionThunkContract) ([]byte, string, error) {
	contract.ContentHash = ""
	if contract.SchemaVersion != FunctionThunkSchemaVersion || contract.Allocates || contract.Copies || contract.RuntimeChecks || contract.MaySuspend || contract.MayEnterHost || !contract.PreservesEnvironment {
		return nil, "", fmt.Errorf("invalid function thunk header")
	}
	parameterPath, returnPath, err := deriveFunctionThunkPaths(contract)
	if err != nil {
		return nil, "", err
	}
	if !slices.Equal(contract.ParameterPath, parameterPath) || !slices.Equal(contract.ReturnPath, returnPath) {
		return nil, "", fmt.Errorf("function thunk conversion path mismatch")
	}
	encoded, err := jsonx.Marshal(contract)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	contract.ContentHash = hash
	encoded, err = jsonx.Marshal(contract)
	return encoded, hash, err
}

func VerifyCanonicalFunctionThunkContract(contract FunctionThunkContract) error {
	claimed := contract.ContentHash
	_, want, err := CanonicalFunctionThunkContract(contract)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("function thunk content hash mismatch")
	}
	return nil
}

func DecodeFunctionThunkContract(data []byte) (*FunctionThunkContract, error) {
	if len(data) > maxFunctionThunkBytes {
		return nil, fmt.Errorf("function thunk exceeds %d bytes", maxFunctionThunkBytes)
	}
	var contract FunctionThunkContract
	if err := jsonx.Unmarshal(data, &contract, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode function thunk: %w", err)
	}
	if err := VerifyCanonicalFunctionThunkContract(contract); err != nil {
		return nil, err
	}
	return &contract, nil
}

func deriveFunctionThunkPaths(contract FunctionThunkContract) ([]string, []string, error) {
	if err := VerifyCanonicalTypeRelationGraph(contract.Relations); err != nil {
		return nil, nil, fmt.Errorf("function thunk relations: %w", err)
	}
	if err := verifyFunctionThunkSignature(contract.Source); err != nil {
		return nil, nil, fmt.Errorf("source function thunk signature: %w", err)
	}
	if err := verifyFunctionThunkSignature(contract.Target); err != nil {
		return nil, nil, fmt.Errorf("target function thunk signature: %w", err)
	}
	if !functionThunkEffectsSubset(contract.Source.Effects, contract.Target.Effects) {
		return nil, nil, fmt.Errorf("function thunk source effects exceed target contract")
	}
	parameterPath, err := FindTypeRelationPath(contract.Relations, contract.Target.ParameterTypeKey, contract.Source.ParameterTypeKey)
	if err != nil {
		return nil, nil, fmt.Errorf("function thunk parameter is not contravariantly compatible: %w", err)
	}
	returnPath, err := FindTypeRelationPath(contract.Relations, contract.Source.ReturnTypeKey, contract.Target.ReturnTypeKey)
	if err != nil {
		return nil, nil, fmt.Errorf("function thunk return is not covariantly compatible: %w", err)
	}
	return parameterPath, returnPath, nil
}

func verifyFunctionThunkSignature(signature FunctionThunkSignature) error {
	if strings.TrimSpace(signature.ParameterTypeKey) == "" || strings.TrimSpace(signature.ReturnTypeKey) == "" || signature.CallingConvention != FunctionThunkCallingConvention || signature.EnvironmentABI != FunctionThunkEnvironmentABI || signature.Effects == nil {
		return fmt.Errorf("invalid object-reference ABI")
	}
	previous := FunctionThunkEffect("")
	for _, effect := range signature.Effects {
		if effect != FunctionThunkEffectRead && effect != FunctionThunkEffectWrite && effect != FunctionThunkEffectThrow && effect != FunctionThunkEffectAllocate {
			return fmt.Errorf("unsupported effect %q", effect)
		}
		if previous != "" && effect <= previous {
			return fmt.Errorf("effects are not canonical")
		}
		previous = effect
	}
	return nil
}

func functionThunkEffectsSubset(source, target []FunctionThunkEffect) bool {
	for _, effect := range source {
		if !slices.Contains(target, effect) {
			return false
		}
	}
	return true
}
