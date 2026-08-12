package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const VERT013aMIRSchemaVersion uint32 = 10
const VERT013aBoundMIRSchemaVersion uint32 = 1

type VERT013aRepType string

const (
	VERT013aRepF64   VERT013aRepType = "f64"
	VERT013aRepGcRef VERT013aRepType = "gc-ref"
)

type VERT013aMIRInstruction struct {
	ID                ValueID         `json:"id"`
	Kind              string          `json:"kind"`
	Type              VERT013aRepType `json:"type"`
	Operands          []ValueID       `json:"operands,omitempty"`
	Callee            FunctionID      `json:"callee,omitempty"`
	FieldOffset       uint32          `json:"fieldOffset,omitempty"`
	PropertySymbolKey string          `json:"propertySymbolKey,omitempty"`
	NumberBits        string          `json:"numberBits,omitempty"`
	Effect            Effect          `json:"effect"`
	Effects           []Effect        `json:"effects"`
	Origin            Origin          `json:"origin"`
}

type VERT013aMIRFunction struct {
	ID           FunctionID               `json:"id"`
	Name         string                   `json:"name"`
	Exported     bool                     `json:"exported,omitempty"`
	ParameterRep VERT013aRepType          `json:"parameterRep"`
	Instructions []VERT013aMIRInstruction `json:"instructions"`
	ReturnType   VERT013aRepType          `json:"returnType"`
	Origin       Origin                   `json:"origin"`
}

type VERT013aMIRModule struct {
	SchemaVersion                 uint32                `json:"schemaVersion"`
	HIRHash                       string                `json:"hirHash"`
	LogicalCapabilityRequirements []RuntimeCapabilityID `json:"logicalCapabilityRequirements"`
	ClassContractHash             string                `json:"classContractHash"`
	ClassContract                 ClassContract         `json:"classContract"`
	InstanceTypeKey               string                `json:"instanceTypeKey"`
	Layout                        ObjectLayoutContract  `json:"layout"`
	GCSafety                      GCSafetyPlan          `json:"gcSafety"`
	Functions                     []VERT013aMIRFunction `json:"functions"`
	ContentHash                   string                `json:"contentHash"`
}

type VERT013aBoundMIR struct {
	SchemaVersion     uint32                 `json:"schemaVersion"`
	TargetContextHash string                 `json:"targetContextHash"`
	MIR               VERT013aMIRModule      `json:"mir"`
	Closure           BoundCapabilityClosure `json:"closure"`
	ContentHash       string                 `json:"contentHash"`
}

func LowerVERT013aMIR(hir HIRModule, layout ObjectLayoutContract) (VERT013aMIRModule, error) {
	if err := VerifyCanonicalVERT013aClassHIR(hir); err != nil {
		return VERT013aMIRModule{}, fmt.Errorf("verify VERT-013a HIR: %w", err)
	}
	class := hir.Classes.Classes[0]
	if err := verifyVERT013aLayout(layout, class.InstanceTypeKey); err != nil {
		return VERT013aMIRModule{}, err
	}
	gc, err := buildVERT013aGCSafety(class.InstanceTypeKey, layout.ContentHash)
	if err != nil {
		return VERT013aMIRModule{}, err
	}
	module := VERT013aMIRModule{
		SchemaVersion: VERT013aMIRSchemaVersion, HIRHash: hir.ContentHash,
		LogicalCapabilityRequirements: slices.Clone(hir.LogicalCapabilityRequirements), ClassContractHash: hir.Classes.ContentHash,
		ClassContract: *hir.Classes, InstanceTypeKey: class.InstanceTypeKey, Layout: layout, GCSafety: gc,
		Functions: fixedVERT013aMIRFunctions(hir.Functions[0].Origin, hir.Functions[1].Origin, hir.Functions[2].Origin, layout.Properties[0].FieldOffset, class.Fields[0].SymbolKey),
	}
	_, hash, err := CanonicalVERT013aMIR(module)
	if err != nil {
		return VERT013aMIRModule{}, err
	}
	module.ContentHash = hash
	return module, nil
}

func verifyVERT013aLayout(layout ObjectLayoutContract, typeKey string) error {
	claimed := layout.ContentHash
	_, want, err := CanonicalObjectLayoutContract(layout)
	if err != nil || claimed == "" || claimed != want {
		return fmt.Errorf("VERT-013a layout is not canonical")
	}
	if layout.TypeKey != typeKey || len(layout.Properties) != 1 || layout.Properties[0].Key != "value" || layout.Properties[0].Kind != ObjectPropertyData || layout.Properties[0].Representation != "f64" || layout.Properties[0].PresenceBit != -1 || len(layout.TraceOffsets) != 0 {
		return fmt.Errorf("invalid VERT-013a instance layout")
	}
	return nil
}

func fixedVERT013aMIRFunctions(constructorOrigin, methodOrigin, entryOrigin Origin, offset uint32, field string) []VERT013aMIRFunction {
	return []VERT013aMIRFunction{
		{ID: 1, Name: "Counter.constructor", ParameterRep: VERT013aRepF64, ReturnType: VERT013aRepGcRef, Origin: constructorOrigin, Instructions: []VERT013aMIRInstruction{
			{ID: 2, Kind: "class.alloc", Type: VERT013aRepGcRef, Effect: EffectAllocate, Effects: []Effect{EffectAllocate}, Origin: constructorOrigin},
			{ID: 3, Kind: "f64.const", Type: VERT013aRepF64, NumberBits: "0000000000000000", Effect: EffectPure, Effects: []Effect{EffectPure}, Origin: constructorOrigin},
			{ID: 4, Kind: "class.field.init", Type: VERT013aRepGcRef, Operands: []ValueID{2, 3}, FieldOffset: offset, PropertySymbolKey: field, Effect: EffectWrite, Effects: []Effect{EffectWrite}, Origin: constructorOrigin},
			{ID: 5, Kind: "class.field.store", Type: VERT013aRepGcRef, Operands: []ValueID{2, 1}, FieldOffset: offset, PropertySymbolKey: field, Effect: EffectWrite, Effects: []Effect{EffectWrite}, Origin: constructorOrigin},
		}},
		{ID: 2, Name: "Counter.increment", ParameterRep: VERT013aRepGcRef, ReturnType: VERT013aRepF64, Origin: methodOrigin, Instructions: []VERT013aMIRInstruction{
			{ID: 2, Kind: "class.field.load", Type: VERT013aRepF64, Operands: []ValueID{1}, FieldOffset: offset, PropertySymbolKey: field, Effect: EffectRead, Effects: []Effect{EffectRead}, Origin: methodOrigin},
			{ID: 3, Kind: "f64.const", Type: VERT013aRepF64, NumberBits: "3ff0000000000000", Effect: EffectPure, Effects: []Effect{EffectPure}, Origin: methodOrigin},
			{ID: 4, Kind: "fadd", Type: VERT013aRepF64, Operands: []ValueID{2, 3}, Effect: EffectPure, Effects: []Effect{EffectPure}, Origin: methodOrigin},
			{ID: 5, Kind: "class.field.store", Type: VERT013aRepF64, Operands: []ValueID{1, 4}, FieldOffset: offset, PropertySymbolKey: field, Effect: EffectWrite, Effects: []Effect{EffectWrite}, Origin: methodOrigin},
		}},
		{ID: 3, Name: "classCounter", Exported: true, ParameterRep: VERT013aRepF64, ReturnType: VERT013aRepF64, Origin: entryOrigin, Instructions: []VERT013aMIRInstruction{
			{ID: 2, Kind: "call.constructor", Type: VERT013aRepGcRef, Operands: []ValueID{1}, Callee: 1, Effect: EffectCall, Effects: []Effect{EffectCall, EffectAllocate, EffectWrite}, Origin: entryOrigin},
			{ID: 3, Kind: "call.method", Type: VERT013aRepF64, Operands: []ValueID{2}, Callee: 2, Effect: EffectCall, Effects: []Effect{EffectCall, EffectRead, EffectWrite}, Origin: entryOrigin},
			{ID: 4, Kind: "call.method", Type: VERT013aRepF64, Operands: []ValueID{2}, Callee: 2, Effect: EffectCall, Effects: []Effect{EffectCall, EffectRead, EffectWrite}, Origin: entryOrigin},
			{ID: 5, Kind: "fadd", Type: VERT013aRepF64, Operands: []ValueID{3, 4}, Effect: EffectPure, Effects: []Effect{EffectPure}, Origin: entryOrigin},
		}},
	}
}

func buildVERT013aGCSafety(functionKey, layoutHash string) (GCSafetyPlan, error) {
	return FinalizeGCSafetyPlan(GCSafetyPlan{FunctionKey: functionKey, Slots: []GCRootSlot{{ID: 1, TraceLayoutHash: layoutHash}}, Blocks: []GCSafetyBlock{{ID: 1, Terminator: "return", Instructions: []GCInstruction{
		{ID: 1, Kind: GCOpFrameLink}, {ID: 2, Kind: GCOpRootClear, Slot: 1}, {ID: 3, Kind: GCOpRootPublish, ActiveSlots: []GCRootSlotID{}},
		{ID: 4, Kind: GCOpSafepoint, SafepointKind: "receiver-allocation", MayAllocate: true}, {ID: 5, Kind: GCOpRefDef, Value: 1},
		{ID: 6, Kind: GCOpRootStore, Slot: 1, Value: 1}, {ID: 7, Kind: GCOpRootPublish, ActiveSlots: []GCRootSlotID{1}},
		{ID: 8, Kind: GCOpSafepoint, SafepointKind: "forced-collection", MayAllocate: true}, {ID: 9, Kind: GCOpRootReload, Slot: 1, Value: 1},
		{ID: 10, Kind: GCOpRefUse, Uses: []GCValueID{1}}, {ID: 11, Kind: GCOpRefUse, Uses: []GCValueID{1}}, {ID: 12, Kind: GCOpFrameUnlink},
	}}}})
}

func VerifyVERT013aMIR(module VERT013aMIRModule) error {
	if module.SchemaVersion != VERT013aMIRSchemaVersion || !validSHA256Hex(module.HIRHash) || !validSHA256Hex(module.ClassContractHash) || !validSHA256Hex(module.InstanceTypeKey) || len(module.Functions) != 3 {
		return fmt.Errorf("invalid VERT-013a MIR envelope")
	}
	if !slices.Equal(module.LogicalCapabilityRequirements, VERT013aLogicalCapabilities()) {
		return fmt.Errorf("VERT-013a MIR capabilities mismatch")
	}
	if _, hash, err := CanonicalClassContract(module.ClassContract); err != nil || module.ClassContract.ContentHash != hash || module.ClassContractHash != hash || module.ClassContract.Classes[0].InstanceTypeKey != module.InstanceTypeKey {
		return fmt.Errorf("VERT-013a MIR class contract mismatch")
	}
	if err := verifyVERT013aLayout(module.Layout, module.InstanceTypeKey); err != nil {
		return err
	}
	wantGC, err := buildVERT013aGCSafety(module.InstanceTypeKey, module.Layout.ContentHash)
	if err != nil {
		return err
	}
	left, _ := jsonx.Marshal(module.GCSafety)
	right, _ := jsonx.Marshal(wantGC)
	if !slices.Equal(left, right) {
		return fmt.Errorf("VERT-013a GC safety mismatch")
	}
	wantFunctions := fixedVERT013aMIRFunctions(module.Functions[0].Origin, module.Functions[1].Origin, module.Functions[2].Origin, module.Layout.Properties[0].FieldOffset, module.Functions[0].Instructions[2].PropertySymbolKey)
	left, _ = jsonx.Marshal(module.Functions)
	right, _ = jsonx.Marshal(wantFunctions)
	if module.Functions[0].Instructions[2].PropertySymbolKey == "" || !slices.Equal(left, right) {
		return fmt.Errorf("VERT-013a MIR function mismatch")
	}
	return nil
}

func CanonicalVERT013aMIR(module VERT013aMIRModule) ([]byte, string, error) {
	module.ContentHash = ""
	if err := VerifyVERT013aMIR(module); err != nil {
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

func VerifyCanonicalVERT013aMIR(module VERT013aMIRModule) error {
	claimed := module.ContentHash
	_, want, err := CanonicalVERT013aMIR(module)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("VERT-013a MIR content hash mismatch")
	}
	return nil
}

func DecodeVERT013aMIR(data []byte) (*VERT013aMIRModule, error) {
	var module VERT013aMIRModule
	if err := jsonx.Unmarshal(data, &module, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	if err := VerifyCanonicalVERT013aMIR(module); err != nil {
		return nil, err
	}
	return &module, nil
}

func NewVERT013aBoundMIR(module VERT013aMIRModule, targetContextHash, catalogHash string, bindings []BoundCapability) (VERT013aBoundMIR, error) {
	if err := VerifyCanonicalVERT013aMIR(module); err != nil {
		return VERT013aBoundMIR{}, err
	}
	if !validSHA256Hex(targetContextHash) || !validSHA256Hex(catalogHash) {
		return VERT013aBoundMIR{}, fmt.Errorf("invalid VERT-013a bound target identity")
	}
	digest, err := LogicalCapabilityRequirementsDigest(module.LogicalCapabilityRequirements)
	if err != nil {
		return VERT013aBoundMIR{}, err
	}
	closure := BoundCapabilityClosure{SchemaVersion: BoundCapabilitySchemaVersion, AvailableCapabilityCatalogHash: catalogHash, LogicalCapabilityRequirementsDigest: digest, Bindings: slices.Clone(bindings)}
	closure.ContentHash, err = boundCapabilityContentHash(closure)
	if err != nil {
		return VERT013aBoundMIR{}, err
	}
	bound := VERT013aBoundMIR{SchemaVersion: VERT013aBoundMIRSchemaVersion, TargetContextHash: targetContextHash, MIR: module, Closure: closure}
	bound.ContentHash, err = vert013aBoundMIRContentHash(bound)
	if err != nil {
		return VERT013aBoundMIR{}, err
	}
	if err := VerifyVERT013aBoundMIR(bound); err != nil {
		return VERT013aBoundMIR{}, err
	}
	return bound, nil
}

func VerifyVERT013aBoundMIR(bound VERT013aBoundMIR) error {
	if bound.SchemaVersion != VERT013aBoundMIRSchemaVersion || !validSHA256Hex(bound.TargetContextHash) || !validSHA256Hex(bound.ContentHash) {
		return fmt.Errorf("invalid VERT-013a bound MIR envelope")
	}
	if err := VerifyCanonicalVERT013aMIR(bound.MIR); err != nil {
		return err
	}
	digest, err := LogicalCapabilityRequirementsDigest(bound.MIR.LogicalCapabilityRequirements)
	closure := bound.Closure
	if err != nil || closure.SchemaVersion != BoundCapabilitySchemaVersion || !validSHA256Hex(closure.AvailableCapabilityCatalogHash) || closure.LogicalCapabilityRequirementsDigest != digest || len(closure.Bindings) != len(bound.MIR.LogicalCapabilityRequirements) {
		return fmt.Errorf("VERT-013a bound capability closure mismatch")
	}
	for index, requirement := range bound.MIR.LogicalCapabilityRequirements {
		binding := closure.Bindings[index]
		if binding.LogicalName != requirement || binding.SymbolName == "" || !validSHA256Hex(binding.SignatureHash) {
			return fmt.Errorf("VERT-013a capability %q is not exactly bound", requirement)
		}
	}
	wantClosure, err := boundCapabilityContentHash(closure)
	if err != nil || closure.ContentHash != wantClosure {
		return fmt.Errorf("VERT-013a bound capability hash mismatch")
	}
	want, err := vert013aBoundMIRContentHash(bound)
	if err != nil || bound.ContentHash != want {
		return fmt.Errorf("VERT-013a bound MIR content hash mismatch")
	}
	return nil
}

func CanonicalVERT013aBoundMIR(bound VERT013aBoundMIR) ([]byte, string, error) {
	if err := VerifyVERT013aBoundMIR(bound); err != nil {
		return nil, "", err
	}
	encoded, err := jsonx.Marshal(bound)
	return encoded, bound.ContentHash, err
}

func DecodeVERT013aBoundMIR(data []byte) (*VERT013aBoundMIR, error) {
	var bound VERT013aBoundMIR
	if err := jsonx.Unmarshal(data, &bound, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	if err := VerifyVERT013aBoundMIR(bound); err != nil {
		return nil, err
	}
	return &bound, nil
}

func vert013aBoundMIRContentHash(bound VERT013aBoundMIR) (string, error) {
	without := bound
	without.ContentHash = ""
	encoded, err := jsonx.Marshal(without)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
