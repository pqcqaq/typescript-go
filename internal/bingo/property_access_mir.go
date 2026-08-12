package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const PropertyAccessMIRSchemaVersion uint32 = 1
const PropertyAccessBoundMIRSchemaVersion uint32 = 1
const maxPropertyAccessMIRBytes = 2 << 20

type PropertyAccessMIRFunction struct {
	ID            FunctionID                   `json:"id"`
	Name          string                       `json:"name"`
	Operations    []PropertyAccessHIROperation `json:"operations"`
	ReturnValueID ValueID                      `json:"returnValueId"`
}
type PropertyAccessMIRArtifact struct {
	SchemaVersion                 uint32                      `json:"schemaVersion"`
	HIRHash                       string                      `json:"hirHash"`
	HIR                           PropertyAccessHIRArtifact   `json:"hir"`
	TargetTriple                  string                      `json:"targetTriple"`
	DataLayoutHash                string                      `json:"dataLayoutHash"`
	DynamicABI                    DynamicValueABIContract     `json:"dynamicAbi"`
	LogicalCapabilityRequirements []RuntimeCapabilityID       `json:"logicalCapabilityRequirements"`
	Functions                     []PropertyAccessMIRFunction `json:"functions"`
	ContentHash                   string                      `json:"contentHash"`
}

type PropertyAccessBoundMIR struct {
	SchemaVersion     uint32                    `json:"schemaVersion"`
	TargetContextHash string                    `json:"targetContextHash"`
	CatalogHash       string                    `json:"catalogHash"`
	MIR               PropertyAccessMIRArtifact `json:"mir"`
	Binding           BoundCapability           `json:"binding"`
	ContentHash       string                    `json:"contentHash"`
}

func LowerPropertyAccessMIR(hir PropertyAccessHIRArtifact, targetTriple, dataLayoutHash string, dynamicABI DynamicValueABIContract) (PropertyAccessMIRArtifact, error) {
	module := PropertyAccessMIRArtifact{SchemaVersion: PropertyAccessMIRSchemaVersion, HIRHash: hir.ContentHash, HIR: hir, TargetTriple: targetTriple, DataLayoutHash: dataLayoutHash, DynamicABI: dynamicABI}
	requirements, functions, err := derivePropertyAccessMIR(module)
	if err != nil {
		return PropertyAccessMIRArtifact{}, err
	}
	module.LogicalCapabilityRequirements, module.Functions = requirements, functions
	_, hash, err := CanonicalPropertyAccessMIR(module)
	module.ContentHash = hash
	return module, err
}

func CanonicalPropertyAccessMIR(module PropertyAccessMIRArtifact) ([]byte, string, error) {
	module.ContentHash = ""
	if module.SchemaVersion != PropertyAccessMIRSchemaVersion || module.HIRHash != module.HIR.ContentHash || strings.TrimSpace(module.TargetTriple) == "" || !validSHA256Hex(module.DataLayoutHash) {
		return nil, "", fmt.Errorf("invalid property access MIR header")
	}
	requirements, functions, err := derivePropertyAccessMIR(module)
	if err != nil {
		return nil, "", err
	}
	left, _ := jsonx.Marshal(module.Functions)
	right, _ := jsonx.Marshal(functions)
	if !slices.Equal(module.LogicalCapabilityRequirements, requirements) || !slices.Equal(left, right) {
		return nil, "", fmt.Errorf("property access MIR does not match canonical lowering")
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

func VerifyCanonicalPropertyAccessMIR(module PropertyAccessMIRArtifact) error {
	claimed := module.ContentHash
	_, want, err := CanonicalPropertyAccessMIR(module)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("property access MIR content hash mismatch")
	}
	return nil
}
func DecodePropertyAccessMIR(data []byte) (*PropertyAccessMIRArtifact, error) {
	if len(data) > maxPropertyAccessMIRBytes {
		return nil, fmt.Errorf("property access MIR exceeds %d bytes", maxPropertyAccessMIRBytes)
	}
	var module PropertyAccessMIRArtifact
	if err := jsonx.Unmarshal(data, &module, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode property access MIR: %w", err)
	}
	if err := VerifyCanonicalPropertyAccessMIR(module); err != nil {
		return nil, err
	}
	return &module, nil
}

func derivePropertyAccessMIR(module PropertyAccessMIRArtifact) ([]RuntimeCapabilityID, []PropertyAccessMIRFunction, error) {
	if err := VerifyCanonicalPropertyAccessHIRArtifact(module.HIR); err != nil {
		return nil, nil, err
	}
	if err := VerifyCanonicalDynamicValueABIContract(module.DynamicABI); err != nil {
		return nil, nil, err
	}
	requirements := []RuntimeCapabilityID{DynamicPropertyLoadCapability}
	functions := make([]PropertyAccessMIRFunction, len(module.HIR.Functions))
	for i, function := range module.HIR.Functions {
		operations := slices.Clone(function.Operations)
		functions[i] = PropertyAccessMIRFunction{ID: function.ID, Name: function.Name, Operations: operations, ReturnValueID: function.ReturnValueID}
	}
	return requirements, functions, nil
}

func NewPropertyAccessBoundMIR(module PropertyAccessMIRArtifact, targetContextHash, catalogHash string, binding BoundCapability) (PropertyAccessBoundMIR, error) {
	bound := PropertyAccessBoundMIR{SchemaVersion: PropertyAccessBoundMIRSchemaVersion, TargetContextHash: targetContextHash, CatalogHash: catalogHash, MIR: module, Binding: binding}
	_, hash, err := CanonicalPropertyAccessBoundMIR(bound)
	bound.ContentHash = hash
	return bound, err
}
func CanonicalPropertyAccessBoundMIR(bound PropertyAccessBoundMIR) ([]byte, string, error) {
	bound.ContentHash = ""
	if bound.SchemaVersion != PropertyAccessBoundMIRSchemaVersion || !validSHA256Hex(bound.TargetContextHash) || !validSHA256Hex(bound.CatalogHash) {
		return nil, "", fmt.Errorf("invalid property access bound MIR header")
	}
	if err := VerifyCanonicalPropertyAccessMIR(bound.MIR); err != nil {
		return nil, "", err
	}
	if bound.Binding.LogicalName != DynamicPropertyLoadCapability || bound.Binding.SymbolName != DynamicPropertyLoadSymbol || bound.Binding.SignatureHash != bound.MIR.DynamicABI.SignatureHash {
		return nil, "", fmt.Errorf("invalid dynamic property load binding")
	}
	encoded, err := jsonx.Marshal(bound)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	bound.ContentHash = hash
	encoded, err = jsonx.Marshal(bound)
	return encoded, hash, err
}
func VerifyCanonicalPropertyAccessBoundMIR(bound PropertyAccessBoundMIR) error {
	claimed := bound.ContentHash
	_, want, err := CanonicalPropertyAccessBoundMIR(bound)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("property access bound MIR content hash mismatch")
	}
	return nil
}

func DecodePropertyAccessBoundMIR(data []byte) (*PropertyAccessBoundMIR, error) {
	if len(data) > maxPropertyAccessMIRBytes {
		return nil, fmt.Errorf("property access bound MIR exceeds %d bytes", maxPropertyAccessMIRBytes)
	}
	var bound PropertyAccessBoundMIR
	if err := jsonx.Unmarshal(data, &bound, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode property access bound MIR: %w", err)
	}
	if err := VerifyCanonicalPropertyAccessBoundMIR(bound); err != nil {
		return nil, err
	}
	return &bound, nil
}
