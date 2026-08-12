package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

// VERT010MIRSchemaVersion identifies the first target-aware object MIR reader.
const VERT010MIRSchemaVersion uint32 = 7

const VERT010BoundMIRSchemaVersion uint32 = 1

// VERT010RepType is intentionally separate from primitive RepType until the
// general MIR schema is upgraded with the full object operation vocabulary.
type VERT010RepType string

const (
	VERT010RepF64   VERT010RepType = "f64"
	VERT010RepGcRef VERT010RepType = "gc-ref"
)

type VERT010MIRFieldBinding struct {
	PropertySymbolKey string         `json:"propertySymbolKey"`
	Representation    VERT010RepType `json:"representation"`
	FieldOffset       uint32         `json:"fieldOffset"`
	PresenceBit       int32          `json:"presenceBit"`
	Trace             bool           `json:"trace"`
}

type VERT010MIRLayoutBinding struct {
	SemanticTypeKey   string                   `json:"semanticTypeKey"`
	LayoutContentHash string                   `json:"layoutContentHash"`
	SchemaHash        string                   `json:"schemaHash"`
	Target            ObjectLayoutTarget       `json:"target"`
	ObjectSize        uint32                   `json:"objectSize"`
	ObjectAlign       uint32                   `json:"objectAlign"`
	Fields            []VERT010MIRFieldBinding `json:"fields"`
	Contract          ObjectLayoutContract     `json:"contract"`
}

type VERT010MIRInstruction struct {
	ID                ValueID        `json:"id"`
	Kind              string         `json:"kind"`
	Type              VERT010RepType `json:"type"`
	Operands          []ValueID      `json:"operands,omitempty"`
	NumberBits        string         `json:"numberBits,omitempty"`
	PropertySymbolKey string         `json:"propertySymbolKey,omitempty"`
	FieldOffset       uint32         `json:"fieldOffset,omitempty"`
	Effect            Effect         `json:"effect"`
	Origin            Origin         `json:"origin"`
}

type VERT010MIRFunction struct {
	Name         string                  `json:"name"`
	Instructions []VERT010MIRInstruction `json:"instructions"`
	ReturnType   VERT010RepType          `json:"returnType"`
	Origin       Origin                  `json:"origin"`
}

// VERT010MIRModule is a target-aware object artifact prior to LLVM emission.
type VERT010MIRModule struct {
	SchemaVersion                 uint32                  `json:"schemaVersion"`
	HIRHash                       string                  `json:"hirHash"`
	LogicalCapabilityRequirements []RuntimeCapabilityID   `json:"logicalCapabilityRequirements"`
	Layout                        VERT010MIRLayoutBinding `json:"layout"`
	GCSafety                      GCSafetyPlan            `json:"gcSafety"`
	Function                      VERT010MIRFunction      `json:"function"`
	ContentHash                   string                  `json:"contentHash"`
}

// VERT010BoundMIR binds structural object MIR to one resolved target context
// and the exact runtime implementations selected from its catalog.
type VERT010BoundMIR struct {
	SchemaVersion     uint32                 `json:"schemaVersion"`
	TargetContextHash string                 `json:"targetContextHash"`
	MIR               VERT010MIRModule       `json:"mir"`
	Closure           BoundCapabilityClosure `json:"closure"`
	ContentHash       string                 `json:"contentHash"`
}

// VerifyVERT010MIR rejects any target/layout/root proof substitution before
// an object operation can reach LLVM.
func VerifyVERT010MIR(module VERT010MIRModule) error {
	if module.SchemaVersion != VERT010MIRSchemaVersion || !validSHA256Hex(module.HIRHash) {
		return fmt.Errorf("invalid VERT-010 MIR envelope")
	}
	if !slices.Equal(module.LogicalCapabilityRequirements, VERT010LogicalCapabilities()) {
		return fmt.Errorf("VERT-010 MIR capability closure mismatch")
	}
	if err := verifyVERT010LayoutBinding(module.Layout); err != nil {
		return err
	}
	if err := VerifyGCSafetyPlanStructure(module.GCSafety); err != nil {
		return fmt.Errorf("VERT-010 MIR GC safety: %w", err)
	}
	if module.Function.Name != "objectAlias" || module.Function.ReturnType != VERT010RepF64 || len(module.Function.Instructions) != 8 {
		return fmt.Errorf("invalid VERT-010 MIR function")
	}
	field := module.Layout.Fields[0]
	for index, instruction := range module.Function.Instructions {
		if instruction.ID != ValueID(index+2) || !validOrigin(instruction.Origin) {
			return fmt.Errorf("VERT-010 MIR instruction %d is not canonical", instruction.ID)
		}
		switch index {
		case 0:
			if instruction.Kind != "object.alloc" || instruction.Type != VERT010RepGcRef || instruction.Effect != EffectAllocate || len(instruction.Operands) != 0 {
				return fmt.Errorf("invalid VERT-010 MIR allocation")
			}
		case 1:
			if instruction.Kind != "object.field.init" || instruction.Type != VERT010RepGcRef || !slices.Equal(instruction.Operands, []ValueID{2, 1}) || instruction.FieldOffset != field.FieldOffset || instruction.PropertySymbolKey != field.PropertySymbolKey || instruction.Effect != EffectWrite {
				return fmt.Errorf("invalid VERT-010 MIR initialization")
			}
		case 2:
			if instruction.Kind != "object.alias" || instruction.Type != VERT010RepGcRef || !slices.Equal(instruction.Operands, []ValueID{3}) || instruction.Effect != EffectPure {
				return fmt.Errorf("invalid VERT-010 MIR alias")
			}
		case 3:
			if instruction.Kind != "object.field.load" || instruction.Type != VERT010RepF64 || !slices.Equal(instruction.Operands, []ValueID{4}) || instruction.FieldOffset != field.FieldOffset || instruction.PropertySymbolKey != field.PropertySymbolKey || instruction.Effect != EffectRead {
				return fmt.Errorf("invalid VERT-010 MIR alias load")
			}
		case 4:
			if instruction.Kind != "f64.const" || instruction.Type != VERT010RepF64 || instruction.NumberBits != "3ff0000000000000" || instruction.Effect != EffectPure {
				return fmt.Errorf("invalid VERT-010 MIR constant")
			}
		case 5:
			if instruction.Kind != "fadd" || instruction.Type != VERT010RepF64 || !slices.Equal(instruction.Operands, []ValueID{5, 6}) || instruction.Effect != EffectPure {
				return fmt.Errorf("invalid VERT-010 MIR add")
			}
		case 6:
			if instruction.Kind != "object.field.store" || instruction.Type != VERT010RepGcRef || !slices.Equal(instruction.Operands, []ValueID{4, 7}) || instruction.FieldOffset != field.FieldOffset || instruction.PropertySymbolKey != field.PropertySymbolKey || instruction.Effect != EffectWrite {
				return fmt.Errorf("invalid VERT-010 MIR store")
			}
		case 7:
			if instruction.Kind != "object.field.load" || instruction.Type != VERT010RepF64 || !slices.Equal(instruction.Operands, []ValueID{3}) || instruction.FieldOffset != field.FieldOffset || instruction.PropertySymbolKey != field.PropertySymbolKey || instruction.Effect != EffectRead {
				return fmt.Errorf("invalid VERT-010 MIR original load")
			}
		}
	}
	return nil
}

// LowerVERT010MIR performs the semantic-to-physical join for the first object
// slice and derives its GC proof rather than accepting caller-authored events.
func LowerVERT010MIR(hir HIRModule, layout ObjectLayoutContract) (VERT010MIRModule, error) {
	if err := VerifyCanonicalVERT010ObjectHIR(hir); err != nil {
		return VERT010MIRModule{}, fmt.Errorf("verify VERT-010 HIR before MIR: %w", err)
	}
	claimed := layout.ContentHash
	_, want, err := CanonicalObjectLayoutContract(layout)
	if err != nil || claimed == "" || claimed != want {
		return VERT010MIRModule{}, fmt.Errorf("object layout is not canonical")
	}
	objectType := hir.ObjectTypes[0]
	if layout.TypeKey != objectType.TypeKey || len(layout.Properties) != 1 || len(layout.TraceOffsets) != 0 {
		return VERT010MIRModule{}, fmt.Errorf("object layout does not bind the HIR object type")
	}
	physical := layout.Properties[0]
	property := objectType.Properties[0]
	if physical.Key != property.Key || physical.Representation != "f64" || physical.PresenceBit != -1 {
		return VERT010MIRModule{}, fmt.Errorf("object layout does not bind the HIR property")
	}
	gc, err := buildVERT010GCSafety(objectType.TypeKey, layout.ContentHash)
	if err != nil {
		return VERT010MIRModule{}, err
	}
	hirOps := hir.Functions[0].Blocks[0].Operations
	instructions := make([]VERT010MIRInstruction, len(hirOps))
	for index, operation := range hirOps {
		instruction := VERT010MIRInstruction{ID: operation.ID, Operands: slices.Clone(operation.Operands), NumberBits: operation.NumberBits, Effect: operation.Effect, Origin: operation.Origin}
		switch operation.Kind {
		case "object.alloc", "object.alias":
			instruction.Kind, instruction.Type = operation.Kind, VERT010RepGcRef
		case "object.field.init", "object.field.store":
			instruction.Kind, instruction.Type = operation.Kind, VERT010RepGcRef
			instruction.PropertySymbolKey, instruction.FieldOffset = property.SymbolKey, physical.FieldOffset
		case "object.field.load":
			instruction.Kind, instruction.Type = operation.Kind, VERT010RepF64
			instruction.PropertySymbolKey, instruction.FieldOffset = property.SymbolKey, physical.FieldOffset
		case "number.constant":
			instruction.Kind, instruction.Type = "f64.const", VERT010RepF64
		case "binary":
			instruction.Kind, instruction.Type = "fadd", VERT010RepF64
		default:
			return VERT010MIRModule{}, fmt.Errorf("unsupported VERT-010 HIR operation %q", operation.Kind)
		}
		instructions[index] = instruction
	}
	module := VERT010MIRModule{
		SchemaVersion:                 VERT010MIRSchemaVersion,
		HIRHash:                       hir.ContentHash,
		LogicalCapabilityRequirements: slices.Clone(hir.LogicalCapabilityRequirements),
		Layout: VERT010MIRLayoutBinding{
			SemanticTypeKey: objectType.TypeKey, LayoutContentHash: layout.ContentHash, SchemaHash: layout.SchemaHash, Target: layout.Target,
			ObjectSize: layout.ObjectSize, ObjectAlign: layout.ObjectAlign, Contract: layout,
			Fields: []VERT010MIRFieldBinding{{PropertySymbolKey: property.SymbolKey, Representation: VERT010RepF64, FieldOffset: physical.FieldOffset, PresenceBit: physical.PresenceBit}},
		},
		GCSafety: gc,
		Function: VERT010MIRFunction{Name: hir.Functions[0].Name, Instructions: instructions, ReturnType: VERT010RepF64, Origin: hir.Functions[0].Origin},
	}
	_, hash, err := CanonicalVERT010MIR(module)
	if err != nil {
		return VERT010MIRModule{}, err
	}
	module.ContentHash = hash
	return module, nil
}

func buildVERT010GCSafety(functionKey, traceLayoutHash string) (GCSafetyPlan, error) {
	return FinalizeGCSafetyPlan(GCSafetyPlan{
		FunctionKey: functionKey,
		Slots:       []GCRootSlot{{ID: 1, TraceLayoutHash: traceLayoutHash}},
		Blocks: []GCSafetyBlock{{ID: 1, Terminator: "return", Instructions: []GCInstruction{
			{ID: 1, Kind: GCOpFrameLink},
			{ID: 2, Kind: GCOpRootClear, Slot: 1},
			{ID: 3, Kind: GCOpRootPublish, ActiveSlots: []GCRootSlotID{}},
			{ID: 4, Kind: GCOpSafepoint, SafepointKind: "allocation", MayAllocate: true},
			{ID: 5, Kind: GCOpRefDef, Value: 1},
			{ID: 6, Kind: GCOpRootStore, Slot: 1, Value: 1},
			{ID: 7, Kind: GCOpRootPublish, ActiveSlots: []GCRootSlotID{1}},
			{ID: 8, Kind: GCOpSafepoint, SafepointKind: "forced-collection", MayAllocate: true},
			{ID: 9, Kind: GCOpRootReload, Slot: 1, Value: 1},
			{ID: 10, Kind: GCOpRefUse, Uses: []GCValueID{1}},
			{ID: 11, Kind: GCOpFrameUnlink},
		}}},
	})
}

func verifyVERT010LayoutBinding(binding VERT010MIRLayoutBinding) error {
	if !validObjectSemanticTypeKey(binding.SemanticTypeKey) || binding.SchemaHash != CanonicalObjectLayoutSchemaHash() || !validSHA256Hex(binding.LayoutContentHash) || len(binding.Fields) != 1 {
		return fmt.Errorf("invalid VERT-010 MIR layout binding")
	}
	if err := verifyObjectLayoutTarget(binding.Target); err != nil {
		return err
	}
	claimed := binding.Contract.ContentHash
	_, want, err := CanonicalObjectLayoutContract(binding.Contract)
	if err != nil || claimed == "" || claimed != want {
		return fmt.Errorf("VERT-010 MIR object layout contract is not canonical")
	}
	if binding.LayoutContentHash != claimed || binding.SemanticTypeKey != binding.Contract.TypeKey || binding.SchemaHash != binding.Contract.SchemaHash || binding.Target != binding.Contract.Target || binding.ObjectSize != binding.Contract.ObjectSize || binding.ObjectAlign != binding.Contract.ObjectAlign || len(binding.Contract.Properties) != 1 || len(binding.Contract.TraceOffsets) != 0 {
		return fmt.Errorf("VERT-010 MIR layout binding disagrees with its contract")
	}
	field := binding.Fields[0]
	contractField := binding.Contract.Properties[0]
	if field.PropertySymbolKey == "" || field.Representation != VERT010RepF64 || field.PresenceBit != -1 || field.Trace {
		return fmt.Errorf("invalid VERT-010 MIR field binding")
	}
	if contractField.Key != "value" || contractField.Kind != ObjectPropertyData || contractField.Representation != "f64" || contractField.FieldOffset != field.FieldOffset || contractField.PresenceBit != field.PresenceBit {
		return fmt.Errorf("VERT-010 MIR field binding disagrees with object layout")
	}
	if binding.ObjectSize == 0 || binding.ObjectAlign == 0 || field.FieldOffset%fieldAlignment(field.Representation) != 0 || field.FieldOffset >= binding.ObjectSize {
		return fmt.Errorf("invalid VERT-010 MIR field extent")
	}
	return nil
}

func fieldAlignment(rep VERT010RepType) uint32 {
	if rep == VERT010RepF64 {
		return 8
	}
	return 8
}

// CanonicalVERT010MIR serializes and hashes a verified module.
func CanonicalVERT010MIR(module VERT010MIRModule) ([]byte, string, error) {
	module.ContentHash = ""
	if err := VerifyVERT010MIR(module); err != nil {
		return nil, "", err
	}
	encoded, err := jsonx.Marshal(module)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	module.ContentHash = hash
	encoded, err = jsonx.Marshal(module)
	if err != nil {
		return nil, "", err
	}
	return encoded, hash, nil
}

// VerifyCanonicalVERT010MIR verifies both structure and the claimed hash.
func VerifyCanonicalVERT010MIR(module VERT010MIRModule) error {
	claimed := module.ContentHash
	_, want, err := CanonicalVERT010MIR(module)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("VERT-010 MIR content hash mismatch: got %q, want %q", claimed, want)
	}
	return nil
}

// DecodeVERT010MIR strictly decodes the target-aware object MIR reader major.
func DecodeVERT010MIR(data []byte) (*VERT010MIRModule, error) {
	var module VERT010MIRModule
	if err := jsonx.Unmarshal(data, &module, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode VERT-010 MIR: %w", err)
	}
	if err := VerifyCanonicalVERT010MIR(module); err != nil {
		return nil, err
	}
	return &module, nil
}

// NewVERT010BoundMIR verifies an externally resolved capability selection and
// binds it to one target context. Catalog validation remains targetcontext's
// responsibility; this boundary verifies exact MIR usage closure.
func NewVERT010BoundMIR(module VERT010MIRModule, targetContextHash, catalogHash string, bindings []BoundCapability) (VERT010BoundMIR, error) {
	if err := VerifyCanonicalVERT010MIR(module); err != nil {
		return VERT010BoundMIR{}, err
	}
	if !validSHA256Hex(targetContextHash) || !validSHA256Hex(catalogHash) {
		return VERT010BoundMIR{}, fmt.Errorf("invalid VERT-010 bound target identity")
	}
	digest, err := LogicalCapabilityRequirementsDigest(module.LogicalCapabilityRequirements)
	if err != nil {
		return VERT010BoundMIR{}, err
	}
	closure := BoundCapabilityClosure{
		SchemaVersion:                       BoundCapabilitySchemaVersion,
		AvailableCapabilityCatalogHash:      catalogHash,
		LogicalCapabilityRequirementsDigest: digest,
		Bindings:                            slices.Clone(bindings),
	}
	closure.ContentHash, err = boundCapabilityContentHash(closure)
	if err != nil {
		return VERT010BoundMIR{}, err
	}
	bound := VERT010BoundMIR{SchemaVersion: VERT010BoundMIRSchemaVersion, TargetContextHash: targetContextHash, MIR: module, Closure: closure}
	bound.ContentHash, err = vert010BoundMIRContentHash(bound)
	if err != nil {
		return VERT010BoundMIR{}, err
	}
	if err := VerifyVERT010BoundMIR(bound); err != nil {
		return VERT010BoundMIR{}, err
	}
	return bound, nil
}

// VerifyVERT010BoundMIR checks exact sorted one-to-one capability binding.
func VerifyVERT010BoundMIR(bound VERT010BoundMIR) error {
	if bound.SchemaVersion != VERT010BoundMIRSchemaVersion || !validSHA256Hex(bound.TargetContextHash) || !validSHA256Hex(bound.ContentHash) {
		return fmt.Errorf("invalid VERT-010 bound MIR envelope")
	}
	if err := VerifyCanonicalVERT010MIR(bound.MIR); err != nil {
		return err
	}
	closure := bound.Closure
	digest, err := LogicalCapabilityRequirementsDigest(bound.MIR.LogicalCapabilityRequirements)
	if err != nil || closure.SchemaVersion != BoundCapabilitySchemaVersion || !validSHA256Hex(closure.AvailableCapabilityCatalogHash) || closure.LogicalCapabilityRequirementsDigest != digest || len(closure.Bindings) != len(bound.MIR.LogicalCapabilityRequirements) {
		return fmt.Errorf("VERT-010 bound capability closure mismatch")
	}
	for index, requirement := range bound.MIR.LogicalCapabilityRequirements {
		binding := closure.Bindings[index]
		if binding.LogicalName != requirement || binding.SymbolName == "" || !validSHA256Hex(binding.SignatureHash) {
			return fmt.Errorf("VERT-010 capability %q is not exactly bound", requirement)
		}
	}
	wantClosure, err := boundCapabilityContentHash(closure)
	if err != nil || closure.ContentHash != wantClosure {
		return fmt.Errorf("VERT-010 bound capability content hash mismatch")
	}
	want, err := vert010BoundMIRContentHash(bound)
	if err != nil || bound.ContentHash != want {
		return fmt.Errorf("VERT-010 bound MIR content hash mismatch")
	}
	return nil
}

// CanonicalVERT010BoundMIR serializes and hashes a verified target-bound MIR.
func CanonicalVERT010BoundMIR(bound VERT010BoundMIR) ([]byte, string, error) {
	if err := VerifyVERT010BoundMIR(bound); err != nil {
		return nil, "", err
	}
	encoded, err := jsonx.Marshal(bound)
	if err != nil {
		return nil, "", err
	}
	return encoded, bound.ContentHash, nil
}

// DecodeVERT010BoundMIR strictly decodes the target-bound object MIR reader major.
func DecodeVERT010BoundMIR(data []byte) (*VERT010BoundMIR, error) {
	var bound VERT010BoundMIR
	if err := jsonx.Unmarshal(data, &bound, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode VERT-010 bound MIR: %w", err)
	}
	if err := VerifyVERT010BoundMIR(bound); err != nil {
		return nil, err
	}
	return &bound, nil
}

func vert010BoundMIRContentHash(bound VERT010BoundMIR) (string, error) {
	withoutHash := bound
	withoutHash.ContentHash = ""
	encoded, err := jsonx.Marshal(withoutHash)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
