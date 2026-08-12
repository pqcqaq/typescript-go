package bingo

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const (
	RepresentationPlanSchemaVersion uint32 = 2
	FirstSliceMIRSchemaVersion      uint32 = 6
	BoundCapabilitySchemaVersion    uint32 = 1
)

// RepType is a concrete target representation. It is deliberately distinct
// from TypeKind, which records source-language semantics.
type RepType string

const (
	RepI1          RepType = "i1"
	RepF64         RepType = "f64"
	RepNullableF64 RepType = "nullable-f64"
	RepUTF16String RepType = "utf16-string"
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
	return NewPrimitiveRepresentationPlan(provenance, []TypeKind{TypeNumber})
}

// NewPrimitiveRepresentationPlan binds the exact primitive source types used
// by a verified HIR module. The canonical order is boolean before number;
// number-only plans retain the original single-binding bytes.
func NewPrimitiveRepresentationPlan(provenance TargetProvenance, sourceTypes []TypeKind) (RepresentationPlan, error) {
	canonicalTypes, err := canonicalPrimitiveSourceTypes(sourceTypes)
	if err != nil {
		return RepresentationPlan{}, err
	}
	bindings := make([]RepresentationBinding, 0, len(canonicalTypes))
	for _, sourceType := range canonicalTypes {
		binding, bindingErr := PrimitiveRepresentationBinding(sourceType)
		if bindingErr != nil {
			return RepresentationPlan{}, bindingErr
		}
		bindings = append(bindings, binding)
	}
	plan := RepresentationPlan{
		SchemaVersion: RepresentationPlanSchemaVersion,
		Provenance:    provenance,
		Bindings:      bindings,
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

func canonicalPrimitiveSourceTypes(sourceTypes []TypeKind) ([]TypeKind, error) {
	seen := make(map[TypeKind]bool, len(sourceTypes))
	for _, sourceType := range sourceTypes {
		if sourceType != TypeBoolean && sourceType != TypeNumber && sourceType != TypeString && sourceType != TypeNullableNumber {
			return nil, fmt.Errorf("primitive source type %q has no representation binding", sourceType)
		}
		seen[sourceType] = true
	}
	if len(seen) == 0 {
		return nil, fmt.Errorf("primitive representation plan requires at least one source type")
	}
	result := make([]TypeKind, 0, len(seen))
	for _, sourceType := range []TypeKind{TypeBoolean, TypeNumber, TypeString, TypeNullableNumber} {
		if seen[sourceType] {
			result = append(result, sourceType)
		}
	}
	return result, nil
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
	case TypeString:
		if err := ValidateUTF16StringContract(CanonicalUTF16StringContract()); err != nil {
			return RepresentationBinding{}, err
		}
		return RepresentationBinding{SourceType: TypeString, RepType: RepUTF16String, BitWidth: UTF16StringABIByteWidth, ABIAlign: UTF16StringABIAlign}, nil
	case TypeNullableNumber:
		if err := ValidateNullableNumberContract(CanonicalNullableNumberContract()); err != nil {
			return RepresentationBinding{}, err
		}
		return RepresentationBinding{SourceType: TypeNullableNumber, RepType: RepNullableF64, BitWidth: NullableNumberABIByteWidth, ABIAlign: NullableNumberABIAlign}, nil
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
	if plan.Bindings == nil {
		return fmt.Errorf("primitive representation bindings are missing")
	}
	sourceTypes := make([]TypeKind, len(plan.Bindings))
	for index, binding := range plan.Bindings {
		sourceTypes[index] = binding.SourceType
		want, err := PrimitiveRepresentationBinding(binding.SourceType)
		if err != nil || binding != want {
			return fmt.Errorf("primitive representation binding %d is invalid: %#v", index, binding)
		}
	}
	canonicalTypes, err := canonicalPrimitiveSourceTypes(sourceTypes)
	if err != nil || !slices.Equal(sourceTypes, canonicalTypes) {
		return fmt.Errorf("primitive representation bindings are not canonical: %#v", plan.Bindings)
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
	Exported   bool                     `json:"exported,omitempty"`
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
	IncomingBlocks                []BlockID             `json:"incomingBlocks,omitempty"`
	NumberBits                    string                `json:"numberBits,omitempty"`
	UTF16CodeUnits                string                `json:"utf16CodeUnits,omitempty"`
	Callee                        FunctionID            `json:"callee,omitempty"`
	Effect                        Effect                `json:"effect"`
	LogicalCapabilityRequirements []RuntimeCapabilityID `json:"logicalCapabilityRequirements"`
	Origin                        Origin                `json:"origin"`
}

type FirstSliceMIRTerminator struct {
	Kind       string    `json:"kind"`
	Value      ValueID   `json:"value"`
	Successors []BlockID `json:"successors,omitempty"`
	Origin     Origin    `json:"origin"`
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

// NewRepresentationPlanForHIR derives the exact primitive representation set
// from canonical HIR and binds it to the supplied target provenance.
func NewRepresentationPlanForHIR(provenance TargetProvenance, hir HIRModule) (RepresentationPlan, error) {
	if err := verifyPrimitiveHIRForMIR(hir); err != nil {
		return RepresentationPlan{}, err
	}
	if provenance.HIRHash != hir.ContentHash || provenance.FrontendSnapshotHash != hir.Provenance.FrontendSnapshotHash || provenance.CompilerBuildIdentity != hir.Provenance.CompilerBuildIdentity {
		return RepresentationPlan{}, fmt.Errorf("target provenance is not bound to the HIR input")
	}
	sourceTypes := make([]TypeKind, 0)
	for _, function := range hir.Functions {
		sourceTypes = append(sourceTypes, function.ReturnType)
		for _, parameter := range function.Parameters {
			sourceTypes = append(sourceTypes, parameter.Type)
		}
		for _, block := range function.Blocks {
			for _, operation := range block.Operations {
				sourceTypes = append(sourceTypes, operation.Type)
			}
		}
	}
	return NewPrimitiveRepresentationPlan(provenance, sourceTypes)
}

func verifyPrimitiveHIRForMIR(hir HIRModule) error {
	if len(hir.Functions) != 1 || hir.Functions[0].Name != "add" || len(hir.Functions[0].Blocks) != 1 {
		if err := VerifyCanonicalPhase2HIR(hir); err != nil {
			return fmt.Errorf("verify Phase 2B HIR before MIR lowering: %w", err)
		}
		return nil
	}
	if err := VerifyCanonicalHIR(hir); err != nil {
		return fmt.Errorf("verify Phase 2A HIR before MIR lowering: %w", err)
	}
	return nil
}

func LowerFirstSliceMIR(hir HIRModule, plan RepresentationPlan) (FirstSliceMIRArtifact, error) {
	if err := verifyPrimitiveHIRForMIR(hir); err != nil {
		return FirstSliceMIRArtifact{}, err
	}
	if err := VerifyRepresentationPlan(plan); err != nil {
		return FirstSliceMIRArtifact{}, err
	}
	if plan.Provenance.HIRHash != hir.ContentHash || plan.Provenance.FrontendSnapshotHash != hir.Provenance.FrontendSnapshotHash ||
		plan.Provenance.CompilerBuildIdentity != hir.Provenance.CompilerBuildIdentity {
		return FirstSliceMIRArtifact{}, fmt.Errorf("representation plan is not bound to the HIR input")
	}
	expectedPlan, err := NewRepresentationPlanForHIR(plan.Provenance, hir)
	if err != nil {
		return FirstSliceMIRArtifact{}, err
	}
	if plan.ContentHash != expectedPlan.ContentHash || !slices.Equal(plan.Bindings, expectedPlan.Bindings) {
		return FirstSliceMIRArtifact{}, fmt.Errorf("representation plan does not bind the exact HIR primitive types")
	}
	bindingByType := make(map[TypeKind]RepresentationBinding, len(plan.Bindings))
	for _, binding := range plan.Bindings {
		bindingByType[binding.SourceType] = binding
	}
	functions := make([]FirstSliceMIRFunction, len(hir.Functions))
	for functionIndex, function := range hir.Functions {
		parameters := make([]FirstSliceMIRParameter, len(function.Parameters))
		for index, parameter := range function.Parameters {
			binding, ok := bindingByType[parameter.Type]
			if !ok {
				return FirstSliceMIRArtifact{}, fmt.Errorf("missing representation binding for parameter type %q", parameter.Type)
			}
			parameters[index] = FirstSliceMIRParameter{Name: parameter.Name, Value: parameter.Value, Type: binding.RepType, Origin: parameter.Origin}
		}
		blocks := make([]FirstSliceMIRBlock, len(function.Blocks))
		for blockIndex, hirBlock := range function.Blocks {
			instructions := make([]FirstSliceMIRInstruction, len(hirBlock.Operations))
			for operationIndex, operation := range hirBlock.Operations {
				binding, ok := bindingByType[operation.Type]
				if !ok {
					return FirstSliceMIRArtifact{}, fmt.Errorf("operation %d has no representation binding", operation.ID)
				}
				instruction := FirstSliceMIRInstruction{ID: operation.ID, Type: binding.RepType, Operands: slices.Clone(operation.Operands), IncomingBlocks: slices.Clone(operation.IncomingBlocks), NumberBits: operation.NumberBits, UTF16CodeUnits: operation.UTF16CodeUnits, Callee: operation.Callee, Effect: operation.Effect, LogicalCapabilityRequirements: slices.Clone(operation.LogicalCapabilityRequirements), Origin: operation.Origin}
				switch operation.Kind {
				case "number.constant":
					if operation.Type != TypeNumber || binding.RepType != RepF64 || !validCanonicalNumberBits(operation.NumberBits) {
						return FirstSliceMIRArtifact{}, fmt.Errorf("number constant operation %d has no canonical f64 representation", operation.ID)
					}
					instruction.Kind = "f64.const"
				case "string.constant":
					if operation.Type != TypeString || binding.RepType != RepUTF16String || ValidateUTF16CodeUnits(operation.UTF16CodeUnits) != nil {
						return FirstSliceMIRArtifact{}, fmt.Errorf("string constant operation %d has no canonical UTF-16 representation", operation.ID)
					}
					instruction.Kind = "utf16.const"
				case "unary":
					if operation.Operator != "-" || binding.RepType != RepF64 {
						return FirstSliceMIRArtifact{}, fmt.Errorf("unary operation %d has no f64 representation", operation.ID)
					}
					instruction.Kind = "fneg"
				case "binary":
					if operation.Operator != "+" || binding.RepType != RepF64 {
						return FirstSliceMIRArtifact{}, fmt.Errorf("binary operation %d has no f64 representation", operation.ID)
					}
					instruction.Kind = "fadd"
				case "compare":
					if operation.Operator != "<" || binding.RepType != RepI1 {
						return FirstSliceMIRArtifact{}, fmt.Errorf("comparison operation %d has no i1 representation", operation.ID)
					}
					instruction.Kind = "fcmp.olt"
				case "phi":
					instruction.Kind = "phi"
				case "call":
					instruction.Kind = "call"
				case "is_nullish":
					if operation.Type != TypeBoolean || binding.RepType != RepI1 {
						return FirstSliceMIRArtifact{}, fmt.Errorf("is_nullish operation %d has no i1 representation", operation.ID)
					}
					instruction.Kind = "nullable.is-nullish"
				case "unwrap_nullable":
					if operation.Type != TypeNumber || binding.RepType != RepF64 {
						return FirstSliceMIRArtifact{}, fmt.Errorf("unwrap_nullable operation %d has no f64 representation", operation.ID)
					}
					instruction.Kind = "nullable.unwrap"
				case "string.length":
					if operation.Type != TypeNumber || binding.RepType != RepF64 {
						return FirstSliceMIRArtifact{}, fmt.Errorf("string.length operation %d has no f64 representation", operation.ID)
					}
					instruction.Kind = "utf16.length"
				default:
					return FirstSliceMIRArtifact{}, fmt.Errorf("unsupported Phase 2B HIR operation %q", operation.Kind)
				}
				instructions[operationIndex] = instruction
			}
			terminator := FirstSliceMIRTerminator{Kind: hirBlock.Terminator.Kind, Value: hirBlock.Terminator.Value, Successors: slices.Clone(hirBlock.Terminator.Successors), Origin: hirBlock.Terminator.Origin}
			blocks[blockIndex] = FirstSliceMIRBlock{ID: hirBlock.ID, Instructions: instructions, Terminator: terminator}
		}
		returnBinding, ok := bindingByType[function.ReturnType]
		if !ok {
			return FirstSliceMIRArtifact{}, fmt.Errorf("missing representation binding for return type %q", function.ReturnType)
		}
		functions[functionIndex] = FirstSliceMIRFunction{ID: function.ID, Name: function.Name, Exported: function.Exported, Parameters: parameters, Blocks: blocks, ReturnType: returnBinding.RepType, Origin: function.Origin}
	}
	module := FirstSliceMIRArtifact{
		SchemaVersion: FirstSliceMIRSchemaVersion,
		Provenance: FirstSliceMIRProvenance{
			TargetProvenance:                    plan.Provenance,
			RepresentationPlanHash:              plan.ContentHash,
			LogicalCapabilityRequirementsDigest: hir.Provenance.LogicalCapabilityRequirementsDigest,
		},
		LogicalCapabilityRequirements: slices.Clone(hir.LogicalCapabilityRequirements),
		Functions:                     functions,
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
	for _, function := range module.Functions {
		for _, block := range function.Blocks {
			for _, instruction := range block.Instructions {
				if instruction.Kind == "f64.const" {
					if !validCanonicalNumberBits(instruction.NumberBits) {
						return fmt.Errorf("f64 constant instruction %d has invalid number bits", instruction.ID)
					}
				} else if instruction.NumberBits != "" {
					return fmt.Errorf("instruction %d carries unexpected number bits", instruction.ID)
				}
				if instruction.Kind == "utf16.const" {
					if err := ValidateUTF16CodeUnits(instruction.UTF16CodeUnits); err != nil {
						return fmt.Errorf("UTF-16 constant instruction %d: %w", instruction.ID, err)
					}
				} else if instruction.UTF16CodeUnits != "" {
					return fmt.Errorf("instruction %d carries unexpected UTF-16 code units", instruction.ID)
				}
			}
		}
	}
	matched := false
	for _, verifier := range firstSliceMIRFunctionSetVerifiers {
		if !verifier.matches(module.Functions) {
			continue
		}
		if matched {
			return fmt.Errorf("first-slice MIR function set matches multiple verifier contracts")
		}
		matched = true
		if err := verifier.verify(module.Functions); err != nil {
			return fmt.Errorf("%s MIR contract: %w", verifier.name, err)
		}
	}
	if !matched {
		return fmt.Errorf("first-slice MIR function set is invalid")
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

func verifyApplicationMainMIRFunction(function FirstSliceMIRFunction) error {
	if function.ID != 1 || function.Name != "main" || !function.Exported || function.ReturnType != RepF64 ||
		!validOrigin(function.Origin) || len(function.Parameters) != 0 || len(function.Blocks) != 1 {
		return fmt.Errorf("Phase 2B application main MIR function is invalid")
	}
	block := function.Blocks[0]
	if block.ID != 1 || len(block.Instructions) != 1 {
		return fmt.Errorf("Phase 2B application main MIR block is invalid")
	}
	instruction := block.Instructions[0]
	bits, err := strconv.ParseUint(instruction.NumberBits, 16, 64)
	status := math.Float64frombits(bits)
	if err != nil || status < 0 || status > 255 || math.Trunc(status) != status ||
		!validF64ConstInstruction(instruction, 1, instruction.NumberBits) {
		return fmt.Errorf("Phase 2B application main exit status is invalid")
	}
	if block.Terminator.Kind != "return" || block.Terminator.Value != 1 || len(block.Terminator.Successors) != 0 || !validOrigin(block.Terminator.Origin) {
		return fmt.Errorf("Phase 2B application main return is invalid")
	}
	return nil
}

func verifyNumberAddMIRFunction(function FirstSliceMIRFunction) error {
	if function.ID != 1 || function.ReturnType != RepF64 || !validOrigin(function.Origin) || len(function.Parameters) != 2 || len(function.Blocks) != 1 {
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
		!slices.Equal(instruction.Operands, []ValueID{1, 2}) || len(instruction.IncomingBlocks) != 0 || instruction.Effect != EffectPure ||
		instruction.LogicalCapabilityRequirements == nil || len(instruction.LogicalCapabilityRequirements) != 0 || !validOrigin(instruction.Origin) {
		return fmt.Errorf("first-slice MIR instruction is invalid")
	}
	if block.Terminator.Kind != "return" || block.Terminator.Value != instruction.ID || len(block.Terminator.Successors) != 0 || !validOrigin(block.Terminator.Origin) {
		return fmt.Errorf("first-slice MIR terminator is invalid")
	}
	return nil
}

func verifyBooleanChooseMIRFunction(function FirstSliceMIRFunction) error {
	if function.ID != 1 || function.ReturnType != RepF64 || !validOrigin(function.Origin) || len(function.Parameters) != 3 || len(function.Blocks) != 3 {
		return fmt.Errorf("Phase 2B choose MIR function is invalid")
	}
	wantParameterTypes := []RepType{RepI1, RepF64, RepF64}
	for index, parameter := range function.Parameters {
		if parameter.Name == "" || parameter.Value != ValueID(index+1) || parameter.Type != wantParameterTypes[index] || !validOrigin(parameter.Origin) {
			return fmt.Errorf("Phase 2B choose MIR parameter %d is invalid", index)
		}
	}
	for index, block := range function.Blocks {
		if block.ID != BlockID(index+1) || block.Instructions == nil || len(block.Instructions) != 0 {
			return fmt.Errorf("Phase 2B choose MIR block %d is invalid", index+1)
		}
	}
	entry := function.Blocks[0].Terminator
	if entry.Kind != "condbranch" || entry.Value != function.Parameters[0].Value || !slices.Equal(entry.Successors, []BlockID{2, 3}) || !validOrigin(entry.Origin) {
		return fmt.Errorf("Phase 2B choose MIR conditional branch is invalid")
	}
	trueReturn := function.Blocks[1].Terminator
	if trueReturn.Kind != "return" || trueReturn.Value != function.Parameters[1].Value || len(trueReturn.Successors) != 0 || !validOrigin(trueReturn.Origin) {
		return fmt.Errorf("Phase 2B choose MIR true return is invalid")
	}
	falseReturn := function.Blocks[2].Terminator
	if falseReturn.Kind != "return" || falseReturn.Value != function.Parameters[2].Value || len(falseReturn.Successors) != 0 || !validOrigin(falseReturn.Origin) {
		return fmt.Errorf("Phase 2B choose MIR false return is invalid")
	}
	return nil
}

func verifyClassifyMIRFunction(function FirstSliceMIRFunction) error {
	if function.ID != 1 || function.Name != "classify" || !function.Exported || function.ReturnType != RepF64 || !validOrigin(function.Origin) || len(function.Parameters) != 1 || len(function.Blocks) != 5 {
		return fmt.Errorf("Phase 2B classify MIR function is invalid")
	}
	parameter := function.Parameters[0]
	if parameter.Name != "value" || parameter.Value != 1 || parameter.Type != RepF64 || !validOrigin(parameter.Origin) {
		return fmt.Errorf("Phase 2B classify parameter is invalid")
	}
	for index, block := range function.Blocks {
		if block.ID != BlockID(index+1) || block.Instructions == nil || !validOrigin(block.Terminator.Origin) {
			return fmt.Errorf("Phase 2B classify block %d is invalid", index+1)
		}
	}
	entry := function.Blocks[0]
	if len(entry.Instructions) != 2 || !validF64ConstInstruction(entry.Instructions[0], 2, "0000000000000000") ||
		!validFCmpOLTInstruction(entry.Instructions[1], 3, []ValueID{1, 2}) || entry.Terminator.Kind != "condbranch" || entry.Terminator.Value != 3 ||
		!slices.Equal(entry.Terminator.Successors, []BlockID{2, 3}) {
		return fmt.Errorf("Phase 2B classify negative branch is invalid")
	}
	negative := function.Blocks[1]
	if len(negative.Instructions) != 2 || !validF64ConstInstruction(negative.Instructions[0], 4, "3ff0000000000000") ||
		!validFNegInstruction(negative.Instructions[1], 5, 4) || negative.Terminator.Kind != "return" || negative.Terminator.Value != 5 || len(negative.Terminator.Successors) != 0 {
		return fmt.Errorf("Phase 2B classify negative return is invalid")
	}
	second := function.Blocks[2]
	if len(second.Instructions) != 2 || !validF64ConstInstruction(second.Instructions[0], 6, "3ff0000000000000") ||
		!validFCmpOLTInstruction(second.Instructions[1], 7, []ValueID{1, 6}) || second.Terminator.Kind != "condbranch" || second.Terminator.Value != 7 ||
		!slices.Equal(second.Terminator.Successors, []BlockID{4, 5}) {
		return fmt.Errorf("Phase 2B classify zero branch is invalid")
	}
	zero := function.Blocks[3]
	if len(zero.Instructions) != 1 || !validF64ConstInstruction(zero.Instructions[0], 8, "0000000000000000") || zero.Terminator.Kind != "return" || zero.Terminator.Value != 8 || len(zero.Terminator.Successors) != 0 {
		return fmt.Errorf("Phase 2B classify zero return is invalid")
	}
	positive := function.Blocks[4]
	if len(positive.Instructions) != 1 || !validF64ConstInstruction(positive.Instructions[0], 9, "3ff0000000000000") || positive.Terminator.Kind != "return" || positive.Terminator.Value != 9 || len(positive.Terminator.Successors) != 0 {
		return fmt.Errorf("Phase 2B classify positive return is invalid")
	}
	return nil
}

func validF64ConstInstruction(instruction FirstSliceMIRInstruction, id ValueID, bits string) bool {
	return instruction.ID == id && instruction.Kind == "f64.const" && instruction.Type == RepF64 && len(instruction.Operands) == 0 && len(instruction.IncomingBlocks) == 0 && instruction.NumberBits == bits && instruction.Callee == 0 && instruction.Effect == EffectPure && instruction.LogicalCapabilityRequirements != nil && len(instruction.LogicalCapabilityRequirements) == 0 && validOrigin(instruction.Origin)
}

func validFNegInstruction(instruction FirstSliceMIRInstruction, id ValueID, operand ValueID) bool {
	return instruction.ID == id && instruction.Kind == "fneg" && instruction.Type == RepF64 && slices.Equal(instruction.Operands, []ValueID{operand}) && len(instruction.IncomingBlocks) == 0 && instruction.NumberBits == "" && instruction.Callee == 0 && instruction.Effect == EffectPure && instruction.LogicalCapabilityRequirements != nil && len(instruction.LogicalCapabilityRequirements) == 0 && validOrigin(instruction.Origin)
}

func validFCmpOLTInstruction(instruction FirstSliceMIRInstruction, id ValueID, operands []ValueID) bool {
	return instruction.ID == id && instruction.Kind == "fcmp.olt" && instruction.Type == RepI1 && slices.Equal(instruction.Operands, operands) && len(instruction.IncomingBlocks) == 0 && instruction.NumberBits == "" && instruction.Callee == 0 && instruction.Effect == EffectPure && instruction.LogicalCapabilityRequirements != nil && len(instruction.LogicalCapabilityRequirements) == 0 && validOrigin(instruction.Origin)
}

func verifyLocalCallMIRFunctions(functions []FirstSliceMIRFunction) error {
	if len(functions) != 2 {
		return fmt.Errorf("Phase 2B local-call MIR function set is invalid")
	}
	add := functions[0]
	if add.ID != 1 || add.Name != "add" || add.Exported || add.ReturnType != RepF64 || !validOrigin(add.Origin) || len(add.Parameters) != 2 || len(add.Blocks) != 1 {
		return fmt.Errorf("Phase 2B local-call helper is invalid")
	}
	if err := verifyNumberFunctionParameters(add.Parameters); err != nil {
		return fmt.Errorf("Phase 2B local-call helper: %w", err)
	}
	addBlock := add.Blocks[0]
	if addBlock.ID != 1 || len(addBlock.Instructions) != 1 {
		return fmt.Errorf("Phase 2B local-call helper block is invalid")
	}
	addInstruction := addBlock.Instructions[0]
	if !validFAddInstruction(addInstruction, 3, []ValueID{1, 2}) || addBlock.Terminator.Kind != "return" || addBlock.Terminator.Value != 3 || len(addBlock.Terminator.Successors) != 0 || !validOrigin(addBlock.Terminator.Origin) {
		return fmt.Errorf("Phase 2B local-call helper instruction or return is invalid")
	}

	compute := functions[1]
	if compute.ID != 2 || compute.Name != "compute" || !compute.Exported || compute.ReturnType != RepF64 || !validOrigin(compute.Origin) || len(compute.Parameters) != 2 || len(compute.Blocks) != 1 {
		return fmt.Errorf("Phase 2B local-call entry is invalid")
	}
	if err := verifyNumberFunctionParameters(compute.Parameters); err != nil {
		return fmt.Errorf("Phase 2B local-call entry: %w", err)
	}
	block := compute.Blocks[0]
	if block.ID != 1 || len(block.Instructions) != 2 {
		return fmt.Errorf("Phase 2B local-call entry block is invalid")
	}
	call := block.Instructions[0]
	if call.ID != 3 || call.Kind != "call" || call.Type != RepF64 || !slices.Equal(call.Operands, []ValueID{1, 2}) || len(call.IncomingBlocks) != 0 || call.Callee != 1 || call.Effect != EffectCall || call.LogicalCapabilityRequirements == nil || len(call.LogicalCapabilityRequirements) != 0 || !validOrigin(call.Origin) {
		return fmt.Errorf("Phase 2B local-call direct call is invalid")
	}
	if !validFAddInstruction(block.Instructions[1], 4, []ValueID{3, 2}) {
		return fmt.Errorf("Phase 2B local-call assignment value is invalid")
	}
	if block.Terminator.Kind != "return" || block.Terminator.Value != 4 || len(block.Terminator.Successors) != 0 || !validOrigin(block.Terminator.Origin) {
		return fmt.Errorf("Phase 2B local-call return is invalid")
	}
	return nil
}

func verifyLoopMIRFunction(function FirstSliceMIRFunction) error {
	if function.ID != 1 || function.Name != "compute" || !function.Exported || function.ReturnType != RepF64 || !validOrigin(function.Origin) || len(function.Parameters) != 2 || len(function.Blocks) != 4 {
		return fmt.Errorf("Phase 2B loop MIR function is invalid")
	}
	if err := verifyNumberFunctionParameters(function.Parameters); err != nil {
		return fmt.Errorf("Phase 2B loop parameters: %w", err)
	}
	for index, block := range function.Blocks {
		if block.ID != BlockID(index+1) || block.Instructions == nil {
			return fmt.Errorf("Phase 2B loop block %d is invalid", index+1)
		}
	}
	entry := function.Blocks[0]
	if len(entry.Instructions) != 0 || entry.Terminator.Kind != "branch" || entry.Terminator.Value != 0 || !slices.Equal(entry.Terminator.Successors, []BlockID{2}) || !validOrigin(entry.Terminator.Origin) {
		return fmt.Errorf("Phase 2B loop entry is invalid")
	}
	header := function.Blocks[1]
	if len(header.Instructions) != 2 {
		return fmt.Errorf("Phase 2B loop header is invalid")
	}
	phi := header.Instructions[0]
	if phi.ID != 3 || phi.Kind != "phi" || phi.Type != RepF64 || !slices.Equal(phi.Operands, []ValueID{1, 5}) || !slices.Equal(phi.IncomingBlocks, []BlockID{1, 3}) || phi.Callee != 0 || phi.Effect != EffectPure || phi.LogicalCapabilityRequirements == nil || len(phi.LogicalCapabilityRequirements) != 0 || !validOrigin(phi.Origin) {
		return fmt.Errorf("Phase 2B loop phi is invalid")
	}
	comparison := header.Instructions[1]
	if comparison.ID != 4 || comparison.Kind != "fcmp.olt" || comparison.Type != RepI1 || !slices.Equal(comparison.Operands, []ValueID{3, 2}) || len(comparison.IncomingBlocks) != 0 || comparison.Callee != 0 || comparison.Effect != EffectPure || comparison.LogicalCapabilityRequirements == nil || len(comparison.LogicalCapabilityRequirements) != 0 || !validOrigin(comparison.Origin) {
		return fmt.Errorf("Phase 2B loop comparison is invalid")
	}
	if header.Terminator.Kind != "condbranch" || header.Terminator.Value != 4 || !slices.Equal(header.Terminator.Successors, []BlockID{3, 4}) || !validOrigin(header.Terminator.Origin) {
		return fmt.Errorf("Phase 2B loop conditional branch is invalid")
	}
	body := function.Blocks[2]
	if len(body.Instructions) != 1 || !validFAddInstruction(body.Instructions[0], 5, []ValueID{3, 1}) || body.Terminator.Kind != "branch" || body.Terminator.Value != 0 || !slices.Equal(body.Terminator.Successors, []BlockID{2}) || !validOrigin(body.Terminator.Origin) {
		return fmt.Errorf("Phase 2B loop body is invalid")
	}
	exit := function.Blocks[3]
	if len(exit.Instructions) != 0 || exit.Terminator.Kind != "return" || exit.Terminator.Value != 3 || len(exit.Terminator.Successors) != 0 || !validOrigin(exit.Terminator.Origin) {
		return fmt.Errorf("Phase 2B loop exit is invalid")
	}
	return nil
}

func verifyCoalesceMIRFunction(function FirstSliceMIRFunction) error {
	if function.ID != 1 || !function.Exported || function.ReturnType != RepF64 || !validOrigin(function.Origin) || len(function.Parameters) != 2 || len(function.Blocks) != 4 {
		return fmt.Errorf("Phase 2B coalesce MIR function is invalid")
	}
	wantParameterTypes := []RepType{RepNullableF64, RepF64}
	for index, parameter := range function.Parameters {
		if parameter.Name == "" || parameter.Value != ValueID(index+1) || parameter.Type != wantParameterTypes[index] || !validOrigin(parameter.Origin) {
			return fmt.Errorf("Phase 2B coalesce MIR parameter %d is invalid", index)
		}
	}
	for index, block := range function.Blocks {
		if block.ID != BlockID(index+1) || block.Instructions == nil || !validOrigin(block.Terminator.Origin) {
			return fmt.Errorf("Phase 2B coalesce MIR block %d is invalid", index+1)
		}
	}
	entry := function.Blocks[0]
	if len(entry.Instructions) != 1 || !validNullableIsNullishInstruction(entry.Instructions[0], 3, 1) ||
		entry.Terminator.Kind != "condbranch" || entry.Terminator.Value != 3 || !slices.Equal(entry.Terminator.Successors, []BlockID{2, 3}) {
		return fmt.Errorf("Phase 2B coalesce MIR nullish branch is invalid")
	}
	fallback := function.Blocks[1]
	if len(fallback.Instructions) != 0 || fallback.Terminator.Kind != "branch" || fallback.Terminator.Value != 0 || !slices.Equal(fallback.Terminator.Successors, []BlockID{4}) {
		return fmt.Errorf("Phase 2B coalesce MIR fallback block is invalid")
	}
	value := function.Blocks[2]
	if len(value.Instructions) != 1 || !validNullableUnwrapInstruction(value.Instructions[0], 4, 1) ||
		value.Terminator.Kind != "branch" || value.Terminator.Value != 0 || !slices.Equal(value.Terminator.Successors, []BlockID{4}) {
		return fmt.Errorf("Phase 2B coalesce MIR value block is invalid")
	}
	merge := function.Blocks[3]
	if len(merge.Instructions) != 1 {
		return fmt.Errorf("Phase 2B coalesce MIR merge block is invalid")
	}
	phi := merge.Instructions[0]
	if phi.ID != 5 || phi.Kind != "phi" || phi.Type != RepF64 || !slices.Equal(phi.Operands, []ValueID{2, 4}) || !slices.Equal(phi.IncomingBlocks, []BlockID{2, 3}) ||
		phi.Callee != 0 || phi.Effect != EffectPure || phi.LogicalCapabilityRequirements == nil || len(phi.LogicalCapabilityRequirements) != 0 || !validOrigin(phi.Origin) ||
		merge.Terminator.Kind != "return" || merge.Terminator.Value != 5 || len(merge.Terminator.Successors) != 0 {
		return fmt.Errorf("Phase 2B coalesce MIR phi or return is invalid")
	}
	return nil
}

func verifyStringLengthMIRFunction(function FirstSliceMIRFunction) error {
	if function.ID != 1 || function.Name != "stringLength" || !function.Exported || function.ReturnType != RepF64 || !validOrigin(function.Origin) || len(function.Parameters) != 1 || len(function.Blocks) != 1 {
		return fmt.Errorf("Phase 2B UTF-16 string length MIR function is invalid")
	}
	parameter := function.Parameters[0]
	if parameter.Name != "value" || parameter.Value != 1 || parameter.Type != RepUTF16String || !validOrigin(parameter.Origin) {
		return fmt.Errorf("Phase 2B UTF-16 string length parameter is invalid")
	}
	block := function.Blocks[0]
	if block.ID != 1 || len(block.Instructions) != 1 || !validOrigin(block.Terminator.Origin) {
		return fmt.Errorf("Phase 2B UTF-16 string length block is invalid")
	}
	instruction := block.Instructions[0]
	if instruction.ID != 2 || instruction.Kind != "utf16.length" || instruction.Type != RepF64 || !slices.Equal(instruction.Operands, []ValueID{1}) || len(instruction.IncomingBlocks) != 0 || instruction.NumberBits != "" || instruction.UTF16CodeUnits != "" || instruction.Callee != 0 || instruction.Effect != EffectPure || instruction.LogicalCapabilityRequirements == nil || len(instruction.LogicalCapabilityRequirements) != 0 || !validOrigin(instruction.Origin) {
		return fmt.Errorf("Phase 2B UTF-16 string length instruction is invalid")
	}
	if block.Terminator.Kind != "return" || block.Terminator.Value != 2 || len(block.Terminator.Successors) != 0 {
		return fmt.Errorf("Phase 2B UTF-16 string length return is invalid")
	}
	return nil
}

func validNullableIsNullishInstruction(instruction FirstSliceMIRInstruction, id, operand ValueID) bool {
	return instruction.ID == id && instruction.Kind == "nullable.is-nullish" && instruction.Type == RepI1 && slices.Equal(instruction.Operands, []ValueID{operand}) &&
		len(instruction.IncomingBlocks) == 0 && instruction.Callee == 0 && instruction.Effect == EffectPure && instruction.LogicalCapabilityRequirements != nil &&
		len(instruction.LogicalCapabilityRequirements) == 0 && validOrigin(instruction.Origin)
}

func validNullableUnwrapInstruction(instruction FirstSliceMIRInstruction, id, operand ValueID) bool {
	return instruction.ID == id && instruction.Kind == "nullable.unwrap" && instruction.Type == RepF64 && slices.Equal(instruction.Operands, []ValueID{operand}) &&
		len(instruction.IncomingBlocks) == 0 && instruction.Callee == 0 && instruction.Effect == EffectPure && instruction.LogicalCapabilityRequirements != nil &&
		len(instruction.LogicalCapabilityRequirements) == 0 && validOrigin(instruction.Origin)
}

func verifyNumberFunctionParameters(parameters []FirstSliceMIRParameter) error {
	for index, parameter := range parameters {
		if parameter.Name == "" || parameter.Value != ValueID(index+1) || parameter.Type != RepF64 || !validOrigin(parameter.Origin) {
			return fmt.Errorf("parameter %d is invalid", index)
		}
	}
	return nil
}

func validFAddInstruction(instruction FirstSliceMIRInstruction, id ValueID, operands []ValueID) bool {
	return instruction.ID == id && instruction.Kind == "fadd" && instruction.Type == RepF64 && slices.Equal(instruction.Operands, operands) && len(instruction.IncomingBlocks) == 0 && instruction.Callee == 0 && instruction.Effect == EffectPure && instruction.LogicalCapabilityRequirements != nil && len(instruction.LogicalCapabilityRequirements) == 0 && validOrigin(instruction.Origin)
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
