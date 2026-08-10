package bingo

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const (
	RepresentationPlanSchemaVersion uint32 = 1
	FirstSliceMIRSchemaVersion      uint32 = 1
	BoundCapabilitySchemaVersion    uint32 = 1
)

// RepType is a concrete target representation. It is deliberately distinct
// from TypeKind, which records source-language semantics.
type RepType string

const (
	RepI1  RepType = "i1"
	RepF64 RepType = "f64"
)

type RepresentationBinding struct {
	SourceType TypeKind `json:"sourceType"`
	RepType    RepType  `json:"repType"`
	BitWidth   uint32   `json:"bitWidth"`
	ABIAlign   uint32   `json:"abiAlign"`
}

// TargetProvenance is the immutable join of target-independent HIR and every
// target/toolchain/runtime identity that can change physical representation.
type TargetProvenance struct {
	HIRHash                        string                `json:"hirHash"`
	FrontendSnapshotHash           string                `json:"frontendSnapshotHash"`
	BuildPlanHash                  string                `json:"buildPlanHash"`
	CompilerBuildIdentity          CompilerBuildIdentity `json:"compilerBuildIdentity"`
	TargetContextHash              string                `json:"targetContextHash"`
	DataLayoutHash                 string                `json:"dataLayoutHash"`
	AvailableCapabilityCatalogHash string                `json:"availableCapabilityCatalogHash"`
	ToolchainManifestHash          string                `json:"toolchainManifestHash"`
	RuntimeManifestHash            string                `json:"runtimeManifestHash"`
}

// RepresentationPlan is the first target-aware artifact. It proves that the
// JavaScript number semantic type is represented by the observed LLVM f64 ABI.
type RepresentationPlan struct {
	SchemaVersion uint32                  `json:"schemaVersion"`
	Provenance    TargetProvenance        `json:"provenance"`
	Bindings      []RepresentationBinding `json:"bindings"`
	ContentHash   string                  `json:"contentHash"`
}

func NewRepresentationPlan(provenance TargetProvenance) (RepresentationPlan, error) {
	number, err := PrimitiveRepresentationBinding(TypeNumber)
	if err != nil {
		return RepresentationPlan{}, err
	}
	plan := RepresentationPlan{
		SchemaVersion: RepresentationPlanSchemaVersion,
		Provenance:    provenance,
		Bindings:      []RepresentationBinding{number},
	}
	digest, err := representationPlanContentHash(plan)
	if err != nil {
		return RepresentationPlan{}, err
	}
	plan.ContentHash = digest
	if err := VerifyRepresentationPlan(plan); err != nil {
		return RepresentationPlan{}, err
	}
	return plan, nil
}

// PrimitiveRepresentationBinding is the only mapping from source primitive
// types to target MIR representations. ABI widening is a separate boundary.
func PrimitiveRepresentationBinding(sourceType TypeKind) (RepresentationBinding, error) {
	switch sourceType {
	case TypeBoolean:
		if err := ValidateBooleanContract(BooleanContract()); err != nil {
			return RepresentationBinding{}, err
		}
		return RepresentationBinding{SourceType: TypeBoolean, RepType: RepI1, BitWidth: 1, ABIAlign: 1}, nil
	case TypeNumber:
		if err := ValidateNumberContract(NumberContract()); err != nil {
			return RepresentationBinding{}, err
		}
		return RepresentationBinding{SourceType: TypeNumber, RepType: RepF64, BitWidth: 64, ABIAlign: 8}, nil
	default:
		return RepresentationBinding{}, fmt.Errorf("primitive source type %q has no representation binding", sourceType)
	}
}

func DecodeRepresentationPlan(data []byte) (*RepresentationPlan, error) {
	var plan RepresentationPlan
	if err := jsonx.Unmarshal(data, &plan, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode representation plan: %w", err)
	}
	if err := VerifyRepresentationPlan(plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

func (plan RepresentationPlan) CanonicalBytes() ([]byte, error) {
	if err := VerifyRepresentationPlan(plan); err != nil {
		return nil, err
	}
	return canonicalWire(plan)
}

func VerifyRepresentationPlan(plan RepresentationPlan) error {
	if plan.SchemaVersion != RepresentationPlanSchemaVersion {
		return fmt.Errorf("unsupported representation plan schema %d", plan.SchemaVersion)
	}
	if err := validateTargetProvenance(plan.Provenance); err != nil {
		return fmt.Errorf("invalid representation plan provenance: %w", err)
	}
	number, err := PrimitiveRepresentationBinding(TypeNumber)
	if err != nil {
		return err
	}
	if len(plan.Bindings) != 1 || plan.Bindings[0] != number {
		return fmt.Errorf("first-slice representation bindings are invalid: %#v", plan.Bindings)
	}
	want, err := representationPlanContentHash(plan)
	if err != nil {
		return err
	}
	if plan.ContentHash != want {
		return fmt.Errorf("representation plan content hash mismatch: got %q, want %q", plan.ContentHash, want)
	}
	return nil
}

type FirstSliceMIRProvenance struct {
	TargetProvenance
	RepresentationPlanHash              string `json:"representationPlanHash"`
	LogicalCapabilityRequirementsDigest string `json:"logicalCapabilityRequirementsDigest"`
}

type FirstSliceMIRArtifact struct {
	SchemaVersion                 uint32                  `json:"schemaVersion"`
	Provenance                    FirstSliceMIRProvenance `json:"provenance"`
	LogicalCapabilityRequirements []RuntimeCapabilityID   `json:"logicalCapabilityRequirements"`
	Functions                     []FirstSliceMIRFunction `json:"functions"`
	BoundCapabilityClosure        *BoundCapabilityClosure `json:"boundCapabilityClosure,omitempty"`
	ContentHash                   string                  `json:"contentHash"`
}

type FirstSliceMIRFunction struct {
	ID         FunctionID               `json:"id"`
	Name       string                   `json:"name"`
	Parameters []FirstSliceMIRParameter `json:"parameters"`
	Blocks     []FirstSliceMIRBlock     `json:"blocks"`
	ReturnType RepType                  `json:"returnType"`
	Origin     Origin                   `json:"origin"`
}

type FirstSliceMIRParameter struct {
	Name   string  `json:"name"`
	Value  ValueID `json:"value"`
	Type   RepType `json:"type"`
	Origin Origin  `json:"origin"`
}

type FirstSliceMIRBlock struct {
	ID           BlockID                    `json:"id"`
	Instructions []FirstSliceMIRInstruction `json:"instructions"`
	Terminator   FirstSliceMIRTerminator    `json:"terminator"`
}

type FirstSliceMIRInstruction struct {
	ID                            ValueID               `json:"id"`
	Kind                          string                `json:"kind"`
	Type                          RepType               `json:"type"`
	Operands                      []ValueID             `json:"operands"`
	Effect                        Effect                `json:"effect"`
	LogicalCapabilityRequirements []RuntimeCapabilityID `json:"logicalCapabilityRequirements"`
	Origin                        Origin                `json:"origin"`
}

type FirstSliceMIRTerminator struct {
	Kind   string  `json:"kind"`
	Value  ValueID `json:"value"`
	Origin Origin  `json:"origin"`
}

type BoundCapability struct {
	LogicalName   RuntimeCapabilityID `json:"logicalName"`
	SymbolName    string              `json:"symbolName"`
	SignatureHash string              `json:"signatureHash"`
}

// BoundCapabilityClosure is derived from structural MIR, never from the
// runtime's available catalog alone. The first number-add slice is explicitly
// empty because f64 addition requires no runtime call.
type BoundCapabilityClosure struct {
	SchemaVersion                       uint32            `json:"schemaVersion"`
	AvailableCapabilityCatalogHash      string            `json:"availableCapabilityCatalogHash"`
	LogicalCapabilityRequirementsDigest string            `json:"logicalCapabilityRequirementsDigest"`
	Bindings                            []BoundCapability `json:"bindings"`
	ContentHash                         string            `json:"contentHash"`
}

func LowerFirstSliceMIR(hir HIRModule, plan RepresentationPlan) (FirstSliceMIRArtifact, error) {
	if err := VerifyCanonicalHIR(hir); err != nil {
		return FirstSliceMIRArtifact{}, fmt.Errorf("verify HIR before MIR lowering: %w", err)
	}
	if err := VerifyRepresentationPlan(plan); err != nil {
		return FirstSliceMIRArtifact{}, err
	}
	if plan.Provenance.HIRHash != hir.ContentHash || plan.Provenance.FrontendSnapshotHash != hir.Provenance.FrontendSnapshotHash ||
		plan.Provenance.CompilerBuildIdentity != hir.Provenance.CompilerBuildIdentity {
		return FirstSliceMIRArtifact{}, fmt.Errorf("representation plan is not bound to the HIR input")
	}
	function := hir.Functions[0]
	parameters := make([]FirstSliceMIRParameter, len(function.Parameters))
	for index, parameter := range function.Parameters {
		parameters[index] = FirstSliceMIRParameter{Name: parameter.Name, Value: parameter.Value, Type: RepF64, Origin: parameter.Origin}
	}
	operation := function.Blocks[0].Operations[0]
	instruction := FirstSliceMIRInstruction{
		ID:                            operation.ID,
		Kind:                          "fadd",
		Type:                          RepF64,
		Operands:                      slices.Clone(operation.Operands),
		Effect:                        EffectPure,
		LogicalCapabilityRequirements: slices.Clone(operation.LogicalCapabilityRequirements),
		Origin:                        operation.Origin,
	}
	module := FirstSliceMIRArtifact{
		SchemaVersion: FirstSliceMIRSchemaVersion,
		Provenance: FirstSliceMIRProvenance{
			TargetProvenance:                    plan.Provenance,
			RepresentationPlanHash:              plan.ContentHash,
			LogicalCapabilityRequirementsDigest: hir.Provenance.LogicalCapabilityRequirementsDigest,
		},
		LogicalCapabilityRequirements: slices.Clone(hir.LogicalCapabilityRequirements),
		Functions: []FirstSliceMIRFunction{{
			ID:         function.ID,
			Name:       function.Name,
			Parameters: parameters,
			Blocks: []FirstSliceMIRBlock{{
				ID:           function.Blocks[0].ID,
				Instructions: []FirstSliceMIRInstruction{instruction},
				Terminator: FirstSliceMIRTerminator{
					Kind:   function.Blocks[0].Terminator.Kind,
					Value:  function.Blocks[0].Terminator.Value,
					Origin: function.Blocks[0].Terminator.Origin,
				},
			}},
			ReturnType: RepF64,
			Origin:     function.Origin,
		}},
	}
	digest, err := firstSliceMIRContentHash(module)
	if err != nil {
		return FirstSliceMIRArtifact{}, err
	}
	module.ContentHash = digest
	if err := VerifyStructuralFirstSliceMIR(module); err != nil {
		return FirstSliceMIRArtifact{}, err
	}
	return module, nil
}

func DecodeStructuralFirstSliceMIR(data []byte) (*FirstSliceMIRArtifact, error) {
	module, err := decodeFirstSliceMIR(data)
	if err != nil {
		return nil, err
	}
	if err := VerifyStructuralFirstSliceMIR(module); err != nil {
		return nil, err
	}
	return &module, nil
}

func DecodeBoundFirstSliceMIR(data []byte) (*FirstSliceMIRArtifact, error) {
	module, err := decodeFirstSliceMIR(data)
	if err != nil {
		return nil, err
	}
	if err := VerifyBoundFirstSliceMIR(module); err != nil {
		return nil, err
	}
	return &module, nil
}

func (module FirstSliceMIRArtifact) CanonicalStructuralBytes() ([]byte, error) {
	if err := VerifyStructuralFirstSliceMIR(module); err != nil {
		return nil, err
	}
	return canonicalWire(module)
}

func (module FirstSliceMIRArtifact) CanonicalBoundBytes() ([]byte, error) {
	if err := VerifyBoundFirstSliceMIR(module); err != nil {
		return nil, err
	}
	return canonicalWire(module)
}

func VerifyStructuralFirstSliceMIR(module FirstSliceMIRArtifact) error {
	if module.BoundCapabilityClosure != nil {
		return fmt.Errorf("structural MIR already contains a bound capability closure")
	}
	return verifyFirstSliceMIR(module)
}

func VerifyBoundFirstSliceMIR(module FirstSliceMIRArtifact) error {
	if module.BoundCapabilityClosure == nil {
		return fmt.Errorf("bound MIR has no capability closure")
	}
	if err := verifyBoundCapabilityClosure(*module.BoundCapabilityClosure, module); err != nil {
		return err
	}
	return verifyFirstSliceMIR(module)
}

func BindFirstSliceCapabilities(module FirstSliceMIRArtifact) (FirstSliceMIRArtifact, error) {
	if err := VerifyStructuralFirstSliceMIR(module); err != nil {
		return FirstSliceMIRArtifact{}, err
	}
	if len(module.LogicalCapabilityRequirements) != 0 {
		return FirstSliceMIRArtifact{}, fmt.Errorf("first-slice MIR unexpectedly requires runtime capabilities")
	}
	closure := BoundCapabilityClosure{
		SchemaVersion:                       BoundCapabilitySchemaVersion,
		AvailableCapabilityCatalogHash:      module.Provenance.AvailableCapabilityCatalogHash,
		LogicalCapabilityRequirementsDigest: module.Provenance.LogicalCapabilityRequirementsDigest,
		Bindings:                            []BoundCapability{},
	}
	digest, err := boundCapabilityContentHash(closure)
	if err != nil {
		return FirstSliceMIRArtifact{}, err
	}
	closure.ContentHash = digest
	module.BoundCapabilityClosure = &closure
	module.ContentHash, err = firstSliceMIRContentHash(module)
	if err != nil {
		return FirstSliceMIRArtifact{}, err
	}
	if err := VerifyBoundFirstSliceMIR(module); err != nil {
		return FirstSliceMIRArtifact{}, err
	}
	return module, nil
}

func verifyFirstSliceMIR(module FirstSliceMIRArtifact) error {
	if module.SchemaVersion != FirstSliceMIRSchemaVersion {
		return fmt.Errorf("unsupported first-slice MIR schema %d", module.SchemaVersion)
	}
	if err := validateTargetProvenance(module.Provenance.TargetProvenance); err != nil {
		return fmt.Errorf("invalid first-slice MIR target provenance: %w", err)
	}
	if !validDigest(module.Provenance.RepresentationPlanHash) || !validDigest(module.Provenance.LogicalCapabilityRequirementsDigest) {
		return fmt.Errorf("invalid first-slice MIR representation or capability provenance")
	}
	digest, err := LogicalCapabilityRequirementsDigest(module.LogicalCapabilityRequirements)
	if err != nil {
		return err
	}
	if digest != module.Provenance.LogicalCapabilityRequirementsDigest || len(module.LogicalCapabilityRequirements) != 0 {
		return fmt.Errorf("first-slice MIR logical capability requirements are invalid")
	}
	if len(module.Functions) != 1 {
		return fmt.Errorf("first-slice MIR requires exactly one function, got %d", len(module.Functions))
	}
	function := module.Functions[0]
	if function.ID != 1 || function.Name == "" || function.ReturnType != RepF64 || !validOrigin(function.Origin) || len(function.Parameters) != 2 || len(function.Blocks) != 1 {
		return fmt.Errorf("first-slice MIR function is invalid")
	}
	for index, parameter := range function.Parameters {
		if parameter.Name == "" || parameter.Value != ValueID(index+1) || parameter.Type != RepF64 || !validOrigin(parameter.Origin) {
			return fmt.Errorf("first-slice MIR parameter %d is invalid", index)
		}
	}
	block := function.Blocks[0]
	if block.ID != 1 || len(block.Instructions) != 1 {
		return fmt.Errorf("first-slice MIR block is invalid")
	}
	instruction := block.Instructions[0]
	if instruction.ID != 3 || instruction.Kind != "fadd" || instruction.Type != RepF64 ||
		!slices.Equal(instruction.Operands, []ValueID{1, 2}) || instruction.Effect != EffectPure ||
		instruction.LogicalCapabilityRequirements == nil || len(instruction.LogicalCapabilityRequirements) != 0 || !validOrigin(instruction.Origin) {
		return fmt.Errorf("first-slice MIR instruction is invalid")
	}
	if block.Terminator.Kind != "return" || block.Terminator.Value != instruction.ID || !validOrigin(block.Terminator.Origin) {
		return fmt.Errorf("first-slice MIR terminator is invalid")
	}
	want, err := firstSliceMIRContentHash(module)
	if err != nil {
		return err
	}
	if module.ContentHash != want {
		return fmt.Errorf("first-slice MIR content hash mismatch: got %q, want %q", module.ContentHash, want)
	}
	return nil
}

func verifyBoundCapabilityClosure(closure BoundCapabilityClosure, module FirstSliceMIRArtifact) error {
	if closure.SchemaVersion != BoundCapabilitySchemaVersion ||
		closure.AvailableCapabilityCatalogHash != module.Provenance.AvailableCapabilityCatalogHash ||
		closure.LogicalCapabilityRequirementsDigest != module.Provenance.LogicalCapabilityRequirementsDigest ||
		closure.Bindings == nil || len(closure.Bindings) != 0 {
		return fmt.Errorf("first-slice bound capability closure is invalid")
	}
	want, err := boundCapabilityContentHash(closure)
	if err != nil {
		return err
	}
	if closure.ContentHash != want {
		return fmt.Errorf("bound capability closure content hash mismatch: got %q, want %q", closure.ContentHash, want)
	}
	return nil
}

func validateTargetProvenance(provenance TargetProvenance) error {
	for _, digest := range []struct {
		name  string
		value string
	}{
		{name: "HIR", value: provenance.HIRHash},
		{name: "frontend snapshot", value: provenance.FrontendSnapshotHash},
		{name: "build plan", value: provenance.BuildPlanHash},
		{name: "target context", value: provenance.TargetContextHash},
		{name: "data layout", value: provenance.DataLayoutHash},
		{name: "available capability catalog", value: provenance.AvailableCapabilityCatalogHash},
		{name: "toolchain manifest", value: provenance.ToolchainManifestHash},
		{name: "runtime manifest", value: provenance.RuntimeManifestHash},
	} {
		if !validDigest(digest.value) {
			return fmt.Errorf("%s hash %q is invalid", digest.name, digest.value)
		}
	}
	return ValidateCompilerBuildIdentity(provenance.CompilerBuildIdentity)
}

func representationPlanContentHash(plan RepresentationPlan) (string, error) {
	return canonicalDigest(struct {
		SchemaVersion uint32                  `json:"schemaVersion"`
		Provenance    TargetProvenance        `json:"provenance"`
		Bindings      []RepresentationBinding `json:"bindings"`
	}{plan.SchemaVersion, plan.Provenance, plan.Bindings})
}

func firstSliceMIRContentHash(module FirstSliceMIRArtifact) (string, error) {
	return canonicalDigest(struct {
		SchemaVersion                 uint32                  `json:"schemaVersion"`
		Provenance                    FirstSliceMIRProvenance `json:"provenance"`
		LogicalCapabilityRequirements []RuntimeCapabilityID   `json:"logicalCapabilityRequirements"`
		Functions                     []FirstSliceMIRFunction `json:"functions"`
		BoundCapabilityClosure        *BoundCapabilityClosure `json:"boundCapabilityClosure,omitempty"`
	}{module.SchemaVersion, module.Provenance, module.LogicalCapabilityRequirements, module.Functions, module.BoundCapabilityClosure})
}

func boundCapabilityContentHash(closure BoundCapabilityClosure) (string, error) {
	return canonicalDigest(struct {
		SchemaVersion                       uint32            `json:"schemaVersion"`
		AvailableCapabilityCatalogHash      string            `json:"availableCapabilityCatalogHash"`
		LogicalCapabilityRequirementsDigest string            `json:"logicalCapabilityRequirementsDigest"`
		Bindings                            []BoundCapability `json:"bindings"`
	}{closure.SchemaVersion, closure.AvailableCapabilityCatalogHash, closure.LogicalCapabilityRequirementsDigest, closure.Bindings})
}

func decodeFirstSliceMIR(data []byte) (FirstSliceMIRArtifact, error) {
	var module FirstSliceMIRArtifact
	if err := jsonx.Unmarshal(data, &module, jsonx.RejectUnknownMembers(true)); err != nil {
		return FirstSliceMIRArtifact{}, fmt.Errorf("decode first-slice MIR: %w", err)
	}
	return module, nil
}

func canonicalDigest(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func canonicalWire(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("JSON contains multiple values")
		}
		return nil, err
	}
	return json.Marshal(decoded)
}

func validDigest(value string) bool {
	return len(value) == sha256.Size*2 && isLowerHex(value)
}
