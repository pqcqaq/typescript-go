package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const VERT013bMIRSchemaVersion uint32 = 11
const VERT013bBoundMIRSchemaVersion uint32 = 1
const maxVERT013bMIRBytes = 384 << 10
const maxVERT013bBoundMIRBytes = 512 << 10

type VERT013bMIRFunction struct {
	ID            FunctionID               `json:"id"`
	Name          string                   `json:"name"`
	Exported      bool                     `json:"exported,omitempty"`
	ParameterReps []VERT013aRepType        `json:"parameterReps"`
	Instructions  []VERT013aMIRInstruction `json:"instructions"`
	ReturnType    VERT013aRepType          `json:"returnType"`
	Origin        Origin                   `json:"origin"`
}

type VERT013bMIRModule struct {
	SchemaVersion                 uint32                 `json:"schemaVersion"`
	HIRHash                       string                 `json:"hirHash"`
	LogicalCapabilityRequirements []RuntimeCapabilityID  `json:"logicalCapabilityRequirements"`
	ClassContractHash             string                 `json:"classContractHash"`
	ClassContract                 VERT013bClassContract  `json:"classContract"`
	Layout                        VERT013bLayoutContract `json:"layout"`
	GCSafety                      GCSafetyPlan           `json:"gcSafety"`
	Functions                     []VERT013bMIRFunction  `json:"functions"`
	ContentHash                   string                 `json:"contentHash"`
}

type VERT013bBoundMIR struct {
	SchemaVersion     uint32                 `json:"schemaVersion"`
	TargetContextHash string                 `json:"targetContextHash"`
	MIR               VERT013bMIRModule      `json:"mir"`
	Closure           BoundCapabilityClosure `json:"closure"`
	ContentHash       string                 `json:"contentHash"`
}

func LowerVERT013bMIR(hir HIRModule, layout VERT013bLayoutContract) (VERT013bMIRModule, error) {
	if err := VerifyCanonicalVERT013bDerivedHIR(hir); err != nil {
		return VERT013bMIRModule{}, fmt.Errorf("verify VERT-013b HIR: %w", err)
	}
	contract := *hir.DerivedClasses
	if err := VerifyCanonicalVERT013bLayout(layout, contract); err != nil {
		return VERT013bMIRModule{}, err
	}
	gc, err := buildVERT013bGCSafety(contract.Classes[1].InstanceTypeKey, layout.Derived.ContentHash)
	if err != nil {
		return VERT013bMIRModule{}, err
	}
	module := VERT013bMIRModule{
		SchemaVersion: VERT013bMIRSchemaVersion, HIRHash: hir.ContentHash,
		LogicalCapabilityRequirements: slices.Clone(hir.LogicalCapabilityRequirements),
		ClassContractHash:             contract.ContentHash, ClassContract: contract, Layout: layout, GCSafety: gc,
		Functions: fixedVERT013bMIRFunctions(hir, layout),
	}
	_, hash, err := CanonicalVERT013bMIR(module)
	if err != nil {
		return VERT013bMIRModule{}, err
	}
	module.ContentHash = hash
	return module, nil
}

func fixedVERT013bMIRFunctions(hir HIRModule, layout VERT013bLayoutContract) []VERT013bMIRFunction {
	base, derived := hir.DerivedClasses.Classes[0], hir.DerivedClasses.Classes[1]
	baseOffset := layout.Derived.Properties[0].FieldOffset
	derivedOffset := layout.Derived.Properties[1].FieldOffset
	o := []Origin{hir.Functions[0].Origin, hir.Functions[1].Origin, hir.Functions[2].Origin, hir.Functions[3].Origin}
	return []VERT013bMIRFunction{
		{ID: 1, Name: "Counter.constructor", ParameterReps: []VERT013aRepType{VERT013aRepGcRef, VERT013aRepF64}, ReturnType: VERT013aRepGcRef, Origin: o[0], Instructions: []VERT013aMIRInstruction{
			{ID: 3, Kind: "f64.const", Type: VERT013aRepF64, NumberBits: "0000000000000000", Effect: EffectPure, Effects: []Effect{EffectPure}, Origin: o[0]},
			{ID: 4, Kind: "class.field.init", Type: VERT013aRepGcRef, Operands: []ValueID{1, 3}, FieldOffset: baseOffset, PropertySymbolKey: base.Fields[0].SymbolKey, Effect: EffectWrite, Effects: []Effect{EffectWrite}, Origin: o[0]},
			{ID: 5, Kind: "class.field.store", Type: VERT013aRepGcRef, Operands: []ValueID{1, 2}, FieldOffset: baseOffset, PropertySymbolKey: base.Fields[0].SymbolKey, Effect: EffectWrite, Effects: []Effect{EffectWrite}, Origin: o[0]},
		}},
		{ID: 2, Name: "StepCounter.constructor", ParameterReps: []VERT013aRepType{VERT013aRepF64, VERT013aRepF64}, ReturnType: VERT013aRepGcRef, Origin: o[1], Instructions: []VERT013aMIRInstruction{
			{ID: 3, Kind: "class.alloc", Type: VERT013aRepGcRef, Effect: EffectAllocate, Effects: []Effect{EffectAllocate}, Origin: o[1]},
			{ID: 4, Kind: "call.super", Type: VERT013aRepGcRef, Operands: []ValueID{3, 1}, Callee: 1, Effect: EffectCall, Effects: []Effect{EffectCall, EffectWrite}, Origin: o[1]},
			{ID: 5, Kind: "f64.const", Type: VERT013aRepF64, NumberBits: "3ff0000000000000", Effect: EffectPure, Effects: []Effect{EffectPure}, Origin: o[1]},
			{ID: 6, Kind: "class.field.init", Type: VERT013aRepGcRef, Operands: []ValueID{3, 5}, FieldOffset: derivedOffset, PropertySymbolKey: derived.Fields[0].SymbolKey, Effect: EffectWrite, Effects: []Effect{EffectWrite}, Origin: o[1]},
			{ID: 7, Kind: "class.field.store", Type: VERT013aRepGcRef, Operands: []ValueID{3, 2}, FieldOffset: derivedOffset, PropertySymbolKey: derived.Fields[0].SymbolKey, Effect: EffectWrite, Effects: []Effect{EffectWrite}, Origin: o[1]},
		}},
		{ID: 3, Name: "StepCounter.increment", ParameterReps: []VERT013aRepType{VERT013aRepGcRef}, ReturnType: VERT013aRepF64, Origin: o[2], Instructions: []VERT013aMIRInstruction{
			{ID: 2, Kind: "class.field.load", Type: VERT013aRepF64, Operands: []ValueID{1}, FieldOffset: baseOffset, PropertySymbolKey: base.Fields[0].SymbolKey, Effect: EffectRead, Effects: []Effect{EffectRead}, Origin: o[2]},
			{ID: 3, Kind: "class.field.load", Type: VERT013aRepF64, Operands: []ValueID{1}, FieldOffset: derivedOffset, PropertySymbolKey: derived.Fields[0].SymbolKey, Effect: EffectRead, Effects: []Effect{EffectRead}, Origin: o[2]},
			{ID: 4, Kind: "fadd", Type: VERT013aRepF64, Operands: []ValueID{2, 3}, Effect: EffectPure, Effects: []Effect{EffectPure}, Origin: o[2]},
			{ID: 5, Kind: "class.field.store", Type: VERT013aRepF64, Operands: []ValueID{1, 4}, FieldOffset: baseOffset, PropertySymbolKey: base.Fields[0].SymbolKey, Effect: EffectWrite, Effects: []Effect{EffectWrite}, Origin: o[2]},
		}},
		{ID: 4, Name: "derivedCounter", Exported: true, ParameterReps: []VERT013aRepType{VERT013aRepF64, VERT013aRepF64}, ReturnType: VERT013aRepF64, Origin: o[3], Instructions: []VERT013aMIRInstruction{
			{ID: 3, Kind: "call.constructor", Type: VERT013aRepGcRef, Operands: []ValueID{1, 2}, Callee: 2, Effect: EffectCall, Effects: []Effect{EffectCall, EffectAllocate, EffectWrite}, Origin: o[3]},
			{ID: 4, Kind: "call.method", Type: VERT013aRepF64, Operands: []ValueID{3}, Callee: 3, Effect: EffectCall, Effects: []Effect{EffectCall, EffectRead, EffectWrite}, Origin: o[3]},
			{ID: 5, Kind: "call.method", Type: VERT013aRepF64, Operands: []ValueID{3}, Callee: 3, Effect: EffectCall, Effects: []Effect{EffectCall, EffectRead, EffectWrite}, Origin: o[3]},
			{ID: 6, Kind: "fadd", Type: VERT013aRepF64, Operands: []ValueID{4, 5}, Effect: EffectPure, Effects: []Effect{EffectPure}, Origin: o[3]},
		}},
	}
}

func buildVERT013bGCSafety(functionKey, layoutHash string) (GCSafetyPlan, error) {
	return FinalizeGCSafetyPlan(GCSafetyPlan{FunctionKey: functionKey, Slots: []GCRootSlot{{ID: 1, TraceLayoutHash: layoutHash}}, Blocks: []GCSafetyBlock{{ID: 1, Terminator: "return", Instructions: []GCInstruction{
		{ID: 1, Kind: GCOpFrameLink}, {ID: 2, Kind: GCOpRootClear, Slot: 1}, {ID: 3, Kind: GCOpRootPublish, ActiveSlots: []GCRootSlotID{}},
		{ID: 4, Kind: GCOpSafepoint, SafepointKind: "derived-receiver-allocation", MayAllocate: true}, {ID: 5, Kind: GCOpRefDef, Value: 1},
		{ID: 6, Kind: GCOpRootStore, Slot: 1, Value: 1}, {ID: 7, Kind: GCOpRootPublish, ActiveSlots: []GCRootSlotID{1}},
		{ID: 8, Kind: GCOpSafepoint, SafepointKind: "super-call", MayAllocate: true}, {ID: 9, Kind: GCOpRootReload, Slot: 1, Value: 1},
		{ID: 10, Kind: GCOpRefUse, Uses: []GCValueID{1}}, {ID: 11, Kind: GCOpRootStore, Slot: 1, Value: 1},
		{ID: 12, Kind: GCOpRootPublish, ActiveSlots: []GCRootSlotID{1}}, {ID: 13, Kind: GCOpSafepoint, SafepointKind: "first-method-call", MayAllocate: true},
		{ID: 14, Kind: GCOpRootReload, Slot: 1, Value: 1}, {ID: 15, Kind: GCOpRefUse, Uses: []GCValueID{1}},
		{ID: 16, Kind: GCOpRootStore, Slot: 1, Value: 1}, {ID: 17, Kind: GCOpRootPublish, ActiveSlots: []GCRootSlotID{1}},
		{ID: 18, Kind: GCOpSafepoint, SafepointKind: "second-method-call", MayAllocate: true}, {ID: 19, Kind: GCOpRootReload, Slot: 1, Value: 1},
		{ID: 20, Kind: GCOpRefUse, Uses: []GCValueID{1}}, {ID: 21, Kind: GCOpFrameUnlink},
	}}}})
}

func VerifyVERT013bMIR(module VERT013bMIRModule) error {
	if module.SchemaVersion != VERT013bMIRSchemaVersion || !validSHA256Hex(module.HIRHash) || module.ClassContractHash != module.ClassContract.ContentHash || len(module.Functions) != 4 {
		return fmt.Errorf("invalid VERT-013b MIR envelope")
	}
	if !slices.Equal(module.LogicalCapabilityRequirements, VERT013bLogicalCapabilities()) || VerifyCanonicalVERT013bClassContract(module.ClassContract) != nil || VerifyCanonicalVERT013bLayout(module.Layout, module.ClassContract) != nil {
		return fmt.Errorf("invalid VERT-013b MIR contract binding")
	}
	wantGC, err := buildVERT013bGCSafety(module.ClassContract.Classes[1].InstanceTypeKey, module.Layout.Derived.ContentHash)
	if err != nil {
		return err
	}
	left, _ := jsonx.Marshal(module.GCSafety)
	right, _ := jsonx.Marshal(wantGC)
	if !slices.Equal(left, right) {
		return fmt.Errorf("VERT-013b GC safety mismatch")
	}
	hir := fixedVERT013bHIR(HIRProvenance{}, module.ClassContract)
	for i := range hir.Functions {
		hir.Functions[i].Origin = module.Functions[i].Origin
	}
	want := fixedVERT013bMIRFunctions(hir, module.Layout)
	left, _ = jsonx.Marshal(module.Functions)
	right, _ = jsonx.Marshal(want)
	if !slices.Equal(left, right) {
		return fmt.Errorf("VERT-013b MIR function mismatch")
	}
	return nil
}

func CanonicalVERT013bMIR(module VERT013bMIRModule) ([]byte, string, error) {
	module.ContentHash = ""
	if err := VerifyVERT013bMIR(module); err != nil {
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

func VerifyCanonicalVERT013bMIR(module VERT013bMIRModule) error {
	claimed := module.ContentHash
	_, want, err := CanonicalVERT013bMIR(module)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("VERT-013b MIR content hash mismatch")
	}
	return nil
}

func DecodeVERT013bMIR(data []byte) (*VERT013bMIRModule, error) {
	if len(data) > maxVERT013bMIRBytes {
		return nil, fmt.Errorf("VERT-013b MIR exceeds %d bytes", maxVERT013bMIRBytes)
	}
	var module VERT013bMIRModule
	if err := jsonx.Unmarshal(data, &module, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	if err := VerifyCanonicalVERT013bMIR(module); err != nil {
		return nil, err
	}
	return &module, nil
}

func NewVERT013bBoundMIR(module VERT013bMIRModule, targetContextHash, catalogHash string, bindings []BoundCapability) (VERT013bBoundMIR, error) {
	if err := VerifyCanonicalVERT013bMIR(module); err != nil {
		return VERT013bBoundMIR{}, err
	}
	if !validSHA256Hex(targetContextHash) || !validSHA256Hex(catalogHash) {
		return VERT013bBoundMIR{}, fmt.Errorf("invalid VERT-013b bound target identity")
	}
	digest, err := LogicalCapabilityRequirementsDigest(module.LogicalCapabilityRequirements)
	if err != nil {
		return VERT013bBoundMIR{}, err
	}
	closure := BoundCapabilityClosure{SchemaVersion: BoundCapabilitySchemaVersion, AvailableCapabilityCatalogHash: catalogHash, LogicalCapabilityRequirementsDigest: digest, Bindings: slices.Clone(bindings)}
	closure.ContentHash, err = boundCapabilityContentHash(closure)
	if err != nil {
		return VERT013bBoundMIR{}, err
	}
	bound := VERT013bBoundMIR{SchemaVersion: VERT013bBoundMIRSchemaVersion, TargetContextHash: targetContextHash, MIR: module, Closure: closure}
	bound.ContentHash, err = vert013bBoundMIRContentHash(bound)
	if err != nil {
		return VERT013bBoundMIR{}, err
	}
	if err := VerifyVERT013bBoundMIR(bound); err != nil {
		return VERT013bBoundMIR{}, err
	}
	return bound, nil
}

func VerifyVERT013bBoundMIR(bound VERT013bBoundMIR) error {
	if bound.SchemaVersion != VERT013bBoundMIRSchemaVersion || !validSHA256Hex(bound.TargetContextHash) || !validSHA256Hex(bound.ContentHash) {
		return fmt.Errorf("invalid VERT-013b bound MIR envelope")
	}
	if err := VerifyCanonicalVERT013bMIR(bound.MIR); err != nil {
		return err
	}
	digest, err := LogicalCapabilityRequirementsDigest(bound.MIR.LogicalCapabilityRequirements)
	closure := bound.Closure
	if err != nil || closure.SchemaVersion != BoundCapabilitySchemaVersion || !validSHA256Hex(closure.AvailableCapabilityCatalogHash) || closure.LogicalCapabilityRequirementsDigest != digest || len(closure.Bindings) != len(bound.MIR.LogicalCapabilityRequirements) {
		return fmt.Errorf("VERT-013b bound capability closure mismatch")
	}
	for i, requirement := range bound.MIR.LogicalCapabilityRequirements {
		binding := closure.Bindings[i]
		if binding.LogicalName != requirement || binding.SymbolName == "" || !validSHA256Hex(binding.SignatureHash) {
			return fmt.Errorf("VERT-013b capability %q is not exactly bound", requirement)
		}
	}
	wantClosure, err := boundCapabilityContentHash(closure)
	if err != nil || closure.ContentHash != wantClosure {
		return fmt.Errorf("VERT-013b bound capability hash mismatch")
	}
	want, err := vert013bBoundMIRContentHash(bound)
	if err != nil || bound.ContentHash != want {
		return fmt.Errorf("VERT-013b bound MIR content hash mismatch")
	}
	return nil
}

func CanonicalVERT013bBoundMIR(bound VERT013bBoundMIR) ([]byte, string, error) {
	if err := VerifyVERT013bBoundMIR(bound); err != nil {
		return nil, "", err
	}
	encoded, err := jsonx.Marshal(bound)
	return encoded, bound.ContentHash, err
}
func DecodeVERT013bBoundMIR(data []byte) (*VERT013bBoundMIR, error) {
	if len(data) > maxVERT013bBoundMIRBytes {
		return nil, fmt.Errorf("VERT-013b bound MIR exceeds %d bytes", maxVERT013bBoundMIRBytes)
	}
	var bound VERT013bBoundMIR
	if err := jsonx.Unmarshal(data, &bound, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	if err := VerifyVERT013bBoundMIR(bound); err != nil {
		return nil, err
	}
	return &bound, nil
}
func vert013bBoundMIRContentHash(bound VERT013bBoundMIR) (string, error) {
	without := bound
	without.ContentHash = ""
	encoded, err := jsonx.Marshal(without)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
