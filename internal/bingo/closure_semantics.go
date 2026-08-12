package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const ClosureContractSchemaVersion uint32 = 1
const maxClosureContractBytes = 64 << 10

type ClosureCaptureMode string
type ClosureStorageClass string

const (
	ClosureCaptureByValue ClosureCaptureMode  = "by-value"
	ClosureCaptureByCell  ClosureCaptureMode  = "by-cell"
	ClosureStorageStack   ClosureStorageClass = "stack"
	ClosureStorageHeap    ClosureStorageClass = "heap-environment"
)

type ClosureCapture struct {
	ID              uint32              `json:"id"`
	SymbolKey       string              `json:"symbolKey"`
	Type            TypeKind            `json:"type"`
	Mutable         bool                `json:"mutable"`
	Mode            ClosureCaptureMode  `json:"mode"`
	Storage         ClosureStorageClass `json:"storage"`
	EnvironmentSlot uint32              `json:"environmentSlot"`
	Traced          bool                `json:"traced"`
}

type ClosureFunctionContract struct {
	ID            uint32           `json:"id"`
	SymbolKey     string           `json:"symbolKey"`
	Signature     string           `json:"signature"`
	Escapes       bool             `json:"escapes"`
	EnvironmentID uint32           `json:"environmentId"`
	Captures      []ClosureCapture `json:"captures"`
}

type ClosureEnvironmentContract struct {
	ID         uint32 `json:"id"`
	HeapOwned  bool   `json:"heapOwned"`
	FieldCount uint32 `json:"fieldCount"`
	TraceCount uint32 `json:"traceCount"`
}

type ClosureContract struct {
	SchemaVersion uint32                       `json:"schemaVersion"`
	Functions     []ClosureFunctionContract    `json:"functions"`
	Environments  []ClosureEnvironmentContract `json:"environments"`
	ContentHash   string                       `json:"contentHash"`
}

func VerifyClosureContract(contract ClosureContract) error {
	if contract.SchemaVersion != ClosureContractSchemaVersion || len(contract.Functions) != 1 || len(contract.Environments) != 1 {
		return fmt.Errorf("invalid closure contract header")
	}
	for index, environment := range contract.Environments {
		if environment.ID != uint32(index+1) || environment.FieldCount == 0 || environment.TraceCount > environment.FieldCount {
			return fmt.Errorf("invalid closure environment %d", index+1)
		}
	}
	for index, function := range contract.Functions {
		if function.ID != uint32(index+1) || function.SymbolKey == "" || function.Signature != "cdecl(ptr)->f64" || !function.Escapes || function.EnvironmentID != 1 || len(function.Captures) != 1 {
			return fmt.Errorf("invalid closure function %d", index+1)
		}
		environment := contract.Environments[function.EnvironmentID-1]
		if function.Escapes != environment.HeapOwned || environment.FieldCount != uint32(len(function.Captures)) {
			return fmt.Errorf("closure function %d lifetime mismatch", function.ID)
		}
		traceCount := uint32(0)
		for captureIndex, capture := range function.Captures {
			if capture.ID != uint32(captureIndex+1) || capture.EnvironmentSlot != uint32(captureIndex) || capture.SymbolKey == "" || capture.Type != TypeNumber || !capture.Mutable || capture.Mode != ClosureCaptureByCell || capture.Storage != ClosureStorageHeap || !capture.Traced {
				return fmt.Errorf("invalid closure capture %d", captureIndex+1)
			}
			if capture.Mutable != (capture.Mode == ClosureCaptureByCell) || function.Escapes != (capture.Storage == ClosureStorageHeap) {
				return fmt.Errorf("closure capture %d ownership mismatch", capture.ID)
			}
			if capture.Mode != ClosureCaptureByValue && capture.Mode != ClosureCaptureByCell {
				return fmt.Errorf("invalid closure capture mode")
			}
			if capture.Traced {
				traceCount++
			}
		}
		if traceCount != environment.TraceCount {
			return fmt.Errorf("closure environment trace mismatch")
		}
	}
	return nil
}

func CanonicalClosureContract(contract ClosureContract) ([]byte, string, error) {
	contract.ContentHash = ""
	if err := VerifyClosureContract(contract); err != nil {
		return nil, "", err
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

func DecodeClosureContract(data []byte) (*ClosureContract, error) {
	if len(data) > maxClosureContractBytes {
		return nil, fmt.Errorf("closure contract exceeds %d bytes", maxClosureContractBytes)
	}
	var contract ClosureContract
	if err := jsonx.Unmarshal(data, &contract, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode closure contract: %w", err)
	}
	claimed := contract.ContentHash
	_, want, err := CanonicalClosureContract(contract)
	if err != nil {
		return nil, err
	}
	if claimed == "" || claimed != want {
		return nil, fmt.Errorf("closure contract content hash mismatch")
	}
	return &contract, nil
}
