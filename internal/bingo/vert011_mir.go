package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

// VERT011MIRSchemaVersion is the independent target-aware PlaceRef MIR major.
const VERT011MIRSchemaVersion uint32 = 8

const VERT011BoundMIRSchemaVersion uint32 = 1

type VERT011RepType string

const (
	VERT011RepF64         VERT011RepType = "f64"
	VERT011RepNullableF64 VERT011RepType = "nullable-f64"
	VERT011RepGcRef       VERT011RepType = "gc-ref"
	VERT011RepStaticKey   VERT011RepType = "static-key"
	VERT011RepPlace       VERT011RepType = "place-ref"
	VERT011RepBool        VERT011RepType = "bool"
	VERT011RepVoid        VERT011RepType = "void"
)

const (
	VERT011GetterSignature = "cdecl(ptr,ptr,ptr)->void"
	VERT011SetterSignature = "cdecl(ptr,f64)->void"
)

type VERT011MIRAccessorBinding struct {
	GetterSymbolKey string `json:"getterSymbolKey"`
	SetterSymbolKey string `json:"setterSymbolKey"`
	GetterSignature string `json:"getterSignature"`
	SetterSignature string `json:"setterSignature"`
}

type VERT011MIRFieldBinding struct {
	PropertyKey       string             `json:"propertyKey"`
	PropertySymbolKey string             `json:"propertySymbolKey"`
	Kind              ObjectPropertyKind `json:"kind"`
	Representation    VERT011RepType     `json:"representation,omitempty"`
	FieldOffset       uint32             `json:"fieldOffset,omitempty"`
	PresenceBit       int32              `json:"presenceBit"`
}

type VERT011MIRLayoutBinding struct {
	SemanticTypeKey   string                   `json:"semanticTypeKey"`
	LayoutContentHash string                   `json:"layoutContentHash"`
	SchemaHash        string                   `json:"schemaHash"`
	Target            ObjectLayoutTarget       `json:"target"`
	ObjectSize        uint32                   `json:"objectSize"`
	ObjectAlign       uint32                   `json:"objectAlign"`
	Fields            []VERT011MIRFieldBinding `json:"fields"`
	Contract          ObjectLayoutContract     `json:"contract"`
}

type VERT011MIRInstruction struct {
	ID                ValueID        `json:"id"`
	Kind              string         `json:"kind"`
	Type              VERT011RepType `json:"type"`
	Operands          []ValueID      `json:"operands,omitempty"`
	IncomingBlocks    []BlockID      `json:"incomingBlocks,omitempty"`
	NumberBits        string         `json:"numberBits,omitempty"`
	PlaceID           PlaceID        `json:"placeId,omitempty"`
	ObjectTypeKey     string         `json:"objectTypeKey,omitempty"`
	PropertySymbolKey string         `json:"propertySymbolKey,omitempty"`
	AccessorSymbolKey string         `json:"accessorSymbolKey,omitempty"`
	FieldOffset       uint32         `json:"fieldOffset,omitempty"`
	Effect            Effect         `json:"effect"`
	Effects           []Effect       `json:"effects"`
	Origin            Origin         `json:"origin"`
}

type VERT011MIRTerminator struct {
	Kind       string    `json:"kind"`
	Value      ValueID   `json:"value,omitempty"`
	Successors []BlockID `json:"successors,omitempty"`
	Origin     Origin    `json:"origin"`
}

type VERT011MIRBlock struct {
	ID           BlockID                 `json:"id"`
	Instructions []VERT011MIRInstruction `json:"instructions"`
	Terminator   VERT011MIRTerminator    `json:"terminator"`
}

type VERT011MIRFunction struct {
	Name           string            `json:"name"`
	ParameterTypes []VERT011RepType  `json:"parameterTypes"`
	Blocks         []VERT011MIRBlock `json:"blocks"`
	ReturnType     VERT011RepType    `json:"returnType"`
	Origin         Origin            `json:"origin"`
}

// VERT011MIRModule is the canonical target-aware computed-accessor artifact.
type VERT011MIRModule struct {
	SchemaVersion                 uint32                    `json:"schemaVersion"`
	HIRHash                       string                    `json:"hirHash"`
	LogicalCapabilityRequirements []RuntimeCapabilityID     `json:"logicalCapabilityRequirements"`
	Layout                        VERT011MIRLayoutBinding   `json:"layout"`
	GCSafety                      GCSafetyPlan              `json:"gcSafety"`
	Place                         PropertyPlaceRef          `json:"place"`
	Accessors                     VERT011MIRAccessorBinding `json:"accessors"`
	Function                      VERT011MIRFunction        `json:"function"`
	ContentHash                   string                    `json:"contentHash"`
}

// VERT011BoundMIR binds MIR v8 to one validated target and runtime catalog.
type VERT011BoundMIR struct {
	SchemaVersion     uint32                 `json:"schemaVersion"`
	TargetContextHash string                 `json:"targetContextHash"`
	MIR               VERT011MIRModule       `json:"mir"`
	Closure           BoundCapabilityClosure `json:"closure"`
	ContentHash       string                 `json:"contentHash"`
}

// LowerVERT011MIR joins HIR v10 to one verified physical object layout.
func LowerVERT011MIR(hir HIRModule, layout ObjectLayoutContract) (VERT011MIRModule, error) {
	if err := VerifyCanonicalVERT011PlaceHIR(hir); err != nil {
		return VERT011MIRModule{}, fmt.Errorf("verify VERT-011 HIR before MIR: %w", err)
	}
	claimed := layout.ContentHash
	_, want, err := CanonicalObjectLayoutContract(layout)
	if err != nil || claimed == "" || claimed != want {
		return VERT011MIRModule{}, fmt.Errorf("VERT-011 object layout is not canonical")
	}
	place := hir.PlaceRefs.Places[0]
	if layout.TypeKey != place.ObjectTypeKey || len(layout.Properties) != 2 || len(layout.TraceOffsets) != 0 {
		return VERT011MIRModule{}, fmt.Errorf("VERT-011 layout does not bind the PlaceRef object")
	}
	backing, accessor := layout.Properties[0], layout.Properties[1]
	if backing.Key != place.BackingPropertyKey || backing.Kind != ObjectPropertyData || backing.Representation != string(VERT011RepNullableF64) || backing.PresenceBit != -1 {
		return VERT011MIRModule{}, fmt.Errorf("VERT-011 backing layout is invalid")
	}
	if accessor.Key != place.PropertyKey || accessor.Kind != ObjectPropertyAccessor || accessor.FieldOffset != 0 || accessor.Representation != "" || accessor.PresenceBit != -1 {
		return VERT011MIRModule{}, fmt.Errorf("VERT-011 accessor layout is invalid")
	}
	gc, err := buildVERT010GCSafety(place.ObjectTypeKey, layout.ContentHash)
	if err != nil {
		return VERT011MIRModule{}, err
	}
	function, err := lowerVERT011MIRFunction(hir.Functions[0], place, backing)
	if err != nil {
		return VERT011MIRModule{}, err
	}
	module := VERT011MIRModule{
		SchemaVersion:                 VERT011MIRSchemaVersion,
		HIRHash:                       hir.ContentHash,
		LogicalCapabilityRequirements: slices.Clone(hir.LogicalCapabilityRequirements),
		Layout: VERT011MIRLayoutBinding{
			SemanticTypeKey: place.ObjectTypeKey, LayoutContentHash: layout.ContentHash,
			SchemaHash: layout.SchemaHash, Target: layout.Target, ObjectSize: layout.ObjectSize,
			ObjectAlign: layout.ObjectAlign, Contract: layout,
			Fields: []VERT011MIRFieldBinding{
				{PropertyKey: place.BackingPropertyKey, PropertySymbolKey: place.BackingPropertySymbolKey, Kind: ObjectPropertyData, Representation: VERT011RepNullableF64, FieldOffset: backing.FieldOffset, PresenceBit: -1},
				{PropertyKey: place.PropertyKey, PropertySymbolKey: place.PropertySymbolKey, Kind: ObjectPropertyAccessor, PresenceBit: -1},
			},
		},
		GCSafety: gc,
		Place:    place,
		Accessors: VERT011MIRAccessorBinding{
			GetterSymbolKey: place.GetterSymbolKey, SetterSymbolKey: place.SetterSymbolKey,
			GetterSignature: VERT011GetterSignature, SetterSignature: VERT011SetterSignature,
		},
		Function: function,
	}
	_, hash, err := CanonicalVERT011MIR(module)
	if err != nil {
		return VERT011MIRModule{}, err
	}
	module.ContentHash = hash
	return module, nil
}

func lowerVERT011MIRFunction(hir HIRFunction, place PropertyPlaceRef, backing ObjectLayoutProperty) (VERT011MIRFunction, error) {
	blocks := make([]VERT011MIRBlock, len(hir.Blocks))
	for blockIndex, hirBlock := range hir.Blocks {
		instructions := make([]VERT011MIRInstruction, len(hirBlock.Operations))
		for operationIndex, operation := range hirBlock.Operations {
			instruction, err := lowerVERT011MIROperation(operation, place, backing)
			if err != nil {
				return VERT011MIRFunction{}, err
			}
			instructions[operationIndex] = instruction
		}
		blocks[blockIndex] = VERT011MIRBlock{
			ID: hirBlock.ID, Instructions: instructions,
			Terminator: VERT011MIRTerminator{Kind: hirBlock.Terminator.Kind, Value: hirBlock.Terminator.Value, Successors: slices.Clone(hirBlock.Terminator.Successors), Origin: hirBlock.Terminator.Origin},
		}
	}
	return VERT011MIRFunction{Name: hir.Name, ParameterTypes: []VERT011RepType{VERT011RepNullableF64}, Blocks: blocks, ReturnType: VERT011RepF64, Origin: hir.Origin}, nil
}

func lowerVERT011MIROperation(operation HIROp, place PropertyPlaceRef, backing ObjectLayoutProperty) (VERT011MIRInstruction, error) {
	instruction := VERT011MIRInstruction{
		ID: operation.ID, Operands: slices.Clone(operation.Operands), IncomingBlocks: slices.Clone(operation.IncomingBlocks),
		NumberBits: operation.NumberBits, PlaceID: operation.PlaceID, Effect: operation.Effect,
		Effects: slices.Clone(operation.Effects), Origin: operation.Origin,
	}
	switch operation.Kind {
	case "object.alloc":
		instruction.Kind, instruction.Type, instruction.ObjectTypeKey = operation.Kind, VERT011RepGcRef, place.ObjectTypeKey
	case "object.field.init":
		instruction.Kind, instruction.Type = operation.Kind, VERT011RepGcRef
		instruction.ObjectTypeKey, instruction.PropertySymbolKey, instruction.FieldOffset = place.ObjectTypeKey, place.BackingPropertySymbolKey, backing.FieldOffset
	case "evaluate.key":
		instruction.Kind, instruction.Type, instruction.PropertySymbolKey = "static.key", VERT011RepStaticKey, place.PropertySymbolKey
	case "place.make":
		instruction.Kind, instruction.Type = operation.Kind, VERT011RepPlace
	case "place.load":
		instruction.Kind, instruction.Type, instruction.AccessorSymbolKey = "accessor.get", VERT011RepNullableF64, place.GetterSymbolKey
	case "is_nullish":
		instruction.Kind, instruction.Type = operation.Kind, VERT011RepBool
	case "number.constant":
		instruction.Kind, instruction.Type = "f64.const", VERT011RepF64
	case "place.store":
		instruction.Kind, instruction.Type, instruction.AccessorSymbolKey = "accessor.set", VERT011RepF64, place.SetterSymbolKey
		instruction.Operands = []ValueID{place.Receiver, place.Key, operation.Operands[0]}
	case "unwrap_nullable":
		instruction.Kind, instruction.Type = operation.Kind, VERT011RepF64
	case "phi":
		instruction.Kind, instruction.Type = operation.Kind, VERT011RepF64
	default:
		return VERT011MIRInstruction{}, fmt.Errorf("unsupported VERT-011 HIR operation %q", operation.Kind)
	}
	if operation.Kind == "place.load" {
		instruction.Operands = []ValueID{place.Receiver, place.Key}
	}
	return instruction, nil
}

// VerifyVERT011MIR rejects layout, accessor, SSA, CFG, effect, and GC substitutions.
func VerifyVERT011MIR(module VERT011MIRModule) error {
	if module.SchemaVersion != VERT011MIRSchemaVersion || !validSHA256Hex(module.HIRHash) || !slices.Equal(module.LogicalCapabilityRequirements, VERT010LogicalCapabilities()) {
		return fmt.Errorf("invalid VERT-011 MIR envelope")
	}
	if err := verifyVERT011MIRLayout(module.Layout, module.Place); err != nil {
		return err
	}
	if err := verifyPropertyPlaceRef(module.Place); err != nil {
		return fmt.Errorf("VERT-011 MIR place: %w", err)
	}
	if err := VerifyGCSafetyPlanStructure(module.GCSafety); err != nil {
		return fmt.Errorf("VERT-011 MIR GC safety: %w", err)
	}
	if module.GCSafety.FunctionKey != module.Place.ObjectTypeKey || len(module.GCSafety.Slots) != 1 || module.GCSafety.Slots[0].TraceLayoutHash != module.Layout.LayoutContentHash {
		return fmt.Errorf("VERT-011 MIR GC safety identity mismatch")
	}
	if module.Accessors.GetterSymbolKey != module.Place.GetterSymbolKey || module.Accessors.SetterSymbolKey != module.Place.SetterSymbolKey || module.Accessors.GetterSignature != VERT011GetterSignature || module.Accessors.SetterSignature != VERT011SetterSignature {
		return fmt.Errorf("VERT-011 MIR accessor ABI mismatch")
	}
	return verifyVERT011MIRFunction(module.Function, module.Place, module.Layout.Fields[0])
}

func verifyVERT011MIRLayout(binding VERT011MIRLayoutBinding, place PropertyPlaceRef) error {
	claimed := binding.Contract.ContentHash
	_, want, err := CanonicalObjectLayoutContract(binding.Contract)
	if err != nil || claimed == "" || claimed != want {
		return fmt.Errorf("VERT-011 MIR layout contract is not canonical")
	}
	if binding.SemanticTypeKey != place.ObjectTypeKey || binding.LayoutContentHash != claimed || binding.SchemaHash != binding.Contract.SchemaHash || binding.Target != binding.Contract.Target || binding.ObjectSize != binding.Contract.ObjectSize || binding.ObjectAlign != binding.Contract.ObjectAlign || len(binding.Fields) != 2 || len(binding.Contract.Properties) != 2 || len(binding.Contract.TraceOffsets) != 0 {
		return fmt.Errorf("VERT-011 MIR layout binding mismatch")
	}
	backing, accessor := binding.Fields[0], binding.Fields[1]
	physicalBacking, physicalAccessor := binding.Contract.Properties[0], binding.Contract.Properties[1]
	if backing.PropertyKey != place.BackingPropertyKey || backing.PropertySymbolKey != place.BackingPropertySymbolKey || backing.Kind != ObjectPropertyData || backing.Representation != VERT011RepNullableF64 || backing.FieldOffset != physicalBacking.FieldOffset || backing.PresenceBit != -1 || physicalBacking.Representation != string(VERT011RepNullableF64) {
		return fmt.Errorf("VERT-011 MIR backing binding mismatch")
	}
	if accessor.PropertyKey != place.PropertyKey || accessor.PropertySymbolKey != place.PropertySymbolKey || accessor.Kind != ObjectPropertyAccessor || accessor.Representation != "" || accessor.FieldOffset != 0 || accessor.PresenceBit != -1 || physicalAccessor.Kind != ObjectPropertyAccessor {
		return fmt.Errorf("VERT-011 MIR accessor binding mismatch")
	}
	return nil
}

func verifyVERT011MIRFunction(function VERT011MIRFunction, place PropertyPlaceRef, backing VERT011MIRFieldBinding) error {
	if function.Name != "propertyNullishAssign" || !slices.Equal(function.ParameterTypes, []VERT011RepType{VERT011RepNullableF64}) || function.ReturnType != VERT011RepF64 || len(function.Blocks) != 4 || !validOrigin(function.Origin) {
		return fmt.Errorf("invalid VERT-011 MIR function")
	}
	want := [][]VERT011MIRInstruction{
		{
			{ID: 2, Kind: "object.alloc", Type: VERT011RepGcRef, ObjectTypeKey: place.ObjectTypeKey, Effect: EffectAllocate, Effects: []Effect{EffectAllocate}},
			{ID: 3, Kind: "object.field.init", Type: VERT011RepGcRef, Operands: []ValueID{2, 1}, ObjectTypeKey: place.ObjectTypeKey, PropertySymbolKey: place.BackingPropertySymbolKey, FieldOffset: backing.FieldOffset, Effect: EffectWrite, Effects: []Effect{EffectWrite}},
			{ID: 4, Kind: "static.key", Type: VERT011RepStaticKey, PropertySymbolKey: place.PropertySymbolKey, Effect: EffectPure, Effects: []Effect{EffectPure}},
			{ID: 5, Kind: "place.make", Type: VERT011RepPlace, Operands: []ValueID{3, 4}, PlaceID: 1, Effect: EffectPure, Effects: []Effect{EffectPure}},
			{ID: 6, Kind: "accessor.get", Type: VERT011RepNullableF64, Operands: []ValueID{3, 4}, PlaceID: 1, AccessorSymbolKey: place.GetterSymbolKey, Effect: EffectCall, Effects: place.LoadEffects},
			{ID: 7, Kind: "is_nullish", Type: VERT011RepBool, Operands: []ValueID{6}, Effect: EffectPure, Effects: []Effect{EffectPure}},
		},
		{
			{ID: 8, Kind: "f64.const", Type: VERT011RepF64, NumberBits: "3ff0000000000000", Effect: EffectPure, Effects: []Effect{EffectPure}},
			{ID: 9, Kind: "accessor.set", Type: VERT011RepF64, Operands: []ValueID{3, 4, 8}, PlaceID: 1, AccessorSymbolKey: place.SetterSymbolKey, Effect: EffectCall, Effects: place.StoreEffects},
		},
		{{ID: 10, Kind: "unwrap_nullable", Type: VERT011RepF64, Operands: []ValueID{6}, Effect: EffectPure, Effects: []Effect{EffectPure}}},
		{{ID: 11, Kind: "phi", Type: VERT011RepF64, Operands: []ValueID{9, 10}, IncomingBlocks: []BlockID{2, 3}, Effect: EffectPure, Effects: []Effect{EffectPure}}},
	}
	for blockIndex, block := range function.Blocks {
		if block.ID != BlockID(blockIndex+1) || len(block.Instructions) != len(want[blockIndex]) || !validOrigin(block.Terminator.Origin) {
			return fmt.Errorf("VERT-011 MIR block %d is invalid", blockIndex+1)
		}
		for index, expected := range want[blockIndex] {
			actual := block.Instructions[index]
			expected.Origin = actual.Origin
			if !equalVERT011MIRInstruction(actual, expected) || !validOrigin(actual.Origin) {
				return fmt.Errorf("VERT-011 MIR instruction %d is invalid", expected.ID)
			}
		}
	}
	terminators := []VERT011MIRTerminator{
		{Kind: "condbranch", Value: 7, Successors: []BlockID{2, 3}},
		{Kind: "branch", Successors: []BlockID{4}},
		{Kind: "branch", Successors: []BlockID{4}},
		{Kind: "return", Value: 11},
	}
	for index, expected := range terminators {
		actual := function.Blocks[index].Terminator
		expected.Origin = actual.Origin
		if actual.Kind != expected.Kind || actual.Value != expected.Value || !slices.Equal(actual.Successors, expected.Successors) {
			return fmt.Errorf("VERT-011 MIR terminator %d is invalid", index+1)
		}
	}
	return nil
}

func equalVERT011MIRInstruction(left, right VERT011MIRInstruction) bool {
	return left.ID == right.ID && left.Kind == right.Kind && left.Type == right.Type &&
		slices.Equal(left.Operands, right.Operands) && slices.Equal(left.IncomingBlocks, right.IncomingBlocks) &&
		left.NumberBits == right.NumberBits && left.PlaceID == right.PlaceID &&
		left.ObjectTypeKey == right.ObjectTypeKey && left.PropertySymbolKey == right.PropertySymbolKey &&
		left.AccessorSymbolKey == right.AccessorSymbolKey && left.FieldOffset == right.FieldOffset &&
		left.Effect == right.Effect && slices.Equal(left.Effects, right.Effects) && left.Origin == right.Origin
}

// CanonicalVERT011MIR verifies, serializes, and hashes MIR v8.
func CanonicalVERT011MIR(module VERT011MIRModule) ([]byte, string, error) {
	module.ContentHash = ""
	if err := VerifyVERT011MIR(module); err != nil {
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
	return encoded, hash, err
}

func VerifyCanonicalVERT011MIR(module VERT011MIRModule) error {
	claimed := module.ContentHash
	_, want, err := CanonicalVERT011MIR(module)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("VERT-011 MIR content hash mismatch: got %q, want %q", claimed, want)
	}
	return nil
}

func DecodeVERT011MIR(data []byte) (*VERT011MIRModule, error) {
	var module VERT011MIRModule
	if err := jsonx.Unmarshal(data, &module, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode VERT-011 MIR: %w", err)
	}
	if err := VerifyCanonicalVERT011MIR(module); err != nil {
		return nil, err
	}
	return &module, nil
}

func NewVERT011BoundMIR(module VERT011MIRModule, targetContextHash, catalogHash string, bindings []BoundCapability) (VERT011BoundMIR, error) {
	if err := VerifyCanonicalVERT011MIR(module); err != nil {
		return VERT011BoundMIR{}, err
	}
	if !validSHA256Hex(targetContextHash) || !validSHA256Hex(catalogHash) {
		return VERT011BoundMIR{}, fmt.Errorf("invalid VERT-011 bound target identity")
	}
	digest, err := LogicalCapabilityRequirementsDigest(module.LogicalCapabilityRequirements)
	if err != nil {
		return VERT011BoundMIR{}, err
	}
	closure := BoundCapabilityClosure{
		SchemaVersion: BoundCapabilitySchemaVersion, AvailableCapabilityCatalogHash: catalogHash,
		LogicalCapabilityRequirementsDigest: digest, Bindings: slices.Clone(bindings),
	}
	closure.ContentHash, err = boundCapabilityContentHash(closure)
	if err != nil {
		return VERT011BoundMIR{}, err
	}
	bound := VERT011BoundMIR{SchemaVersion: VERT011BoundMIRSchemaVersion, TargetContextHash: targetContextHash, MIR: module, Closure: closure}
	bound.ContentHash, err = vert011BoundMIRContentHash(bound)
	if err != nil {
		return VERT011BoundMIR{}, err
	}
	if err := VerifyVERT011BoundMIR(bound); err != nil {
		return VERT011BoundMIR{}, err
	}
	return bound, nil
}

func VerifyVERT011BoundMIR(bound VERT011BoundMIR) error {
	if bound.SchemaVersion != VERT011BoundMIRSchemaVersion || !validSHA256Hex(bound.TargetContextHash) || !validSHA256Hex(bound.ContentHash) {
		return fmt.Errorf("invalid VERT-011 bound MIR envelope")
	}
	if err := VerifyCanonicalVERT011MIR(bound.MIR); err != nil {
		return err
	}
	digest, err := LogicalCapabilityRequirementsDigest(bound.MIR.LogicalCapabilityRequirements)
	closure := bound.Closure
	if err != nil || closure.SchemaVersion != BoundCapabilitySchemaVersion || !validSHA256Hex(closure.AvailableCapabilityCatalogHash) || closure.LogicalCapabilityRequirementsDigest != digest || len(closure.Bindings) != len(bound.MIR.LogicalCapabilityRequirements) {
		return fmt.Errorf("VERT-011 bound capability closure mismatch")
	}
	for index, requirement := range bound.MIR.LogicalCapabilityRequirements {
		binding := closure.Bindings[index]
		if binding.LogicalName != requirement || binding.SymbolName == "" || !validSHA256Hex(binding.SignatureHash) {
			return fmt.Errorf("VERT-011 capability %q is not exactly bound", requirement)
		}
	}
	wantClosure, err := boundCapabilityContentHash(closure)
	if err != nil || closure.ContentHash != wantClosure {
		return fmt.Errorf("VERT-011 bound capability content hash mismatch")
	}
	want, err := vert011BoundMIRContentHash(bound)
	if err != nil || bound.ContentHash != want {
		return fmt.Errorf("VERT-011 bound MIR content hash mismatch")
	}
	return nil
}

func CanonicalVERT011BoundMIR(bound VERT011BoundMIR) ([]byte, string, error) {
	if err := VerifyVERT011BoundMIR(bound); err != nil {
		return nil, "", err
	}
	encoded, err := jsonx.Marshal(bound)
	return encoded, bound.ContentHash, err
}

func DecodeVERT011BoundMIR(data []byte) (*VERT011BoundMIR, error) {
	var bound VERT011BoundMIR
	if err := jsonx.Unmarshal(data, &bound, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode VERT-011 bound MIR: %w", err)
	}
	if err := VerifyVERT011BoundMIR(bound); err != nil {
		return nil, err
	}
	return &bound, nil
}

func vert011BoundMIRContentHash(bound VERT011BoundMIR) (string, error) {
	withoutHash := bound
	withoutHash.ContentHash = ""
	encoded, err := jsonx.Marshal(withoutHash)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
