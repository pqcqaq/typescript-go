package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const VERT012MIRSchemaVersion uint32 = 9
const VERT012BoundMIRSchemaVersion uint32 = 1

type VERT012RepType string

const (
	VERT012RepF64      VERT012RepType = "f64"
	VERT012RepGcRef    VERT012RepType = "gc-ref"
	VERT012RepFunction VERT012RepType = "function-ref"
)

type VERT012MIRLayout struct {
	Role     string               `json:"role"`
	Contract ObjectLayoutContract `json:"contract"`
}

type VERT012MIRInstruction struct {
	ID          ValueID        `json:"id"`
	Kind        string         `json:"kind"`
	Type        VERT012RepType `json:"type"`
	Operands    []ValueID      `json:"operands,omitempty"`
	Callee      FunctionID     `json:"callee,omitempty"`
	FieldOffset uint32         `json:"fieldOffset,omitempty"`
	NumberBits  string         `json:"numberBits,omitempty"`
	Effect      Effect         `json:"effect"`
	Effects     []Effect       `json:"effects"`
	Origin      Origin         `json:"origin"`
}

type VERT012MIRFunction struct {
	ID           FunctionID              `json:"id"`
	Name         string                  `json:"name"`
	Exported     bool                    `json:"exported,omitempty"`
	ParameterRep VERT012RepType          `json:"parameterRep"`
	Instructions []VERT012MIRInstruction `json:"instructions"`
	ReturnType   VERT012RepType          `json:"returnType"`
	Origin       Origin                  `json:"origin"`
}

type VERT012MIRModule struct {
	SchemaVersion                 uint32                `json:"schemaVersion"`
	HIRHash                       string                `json:"hirHash"`
	LogicalCapabilityRequirements []RuntimeCapabilityID `json:"logicalCapabilityRequirements"`
	ClosureContractHash           string                `json:"closureContractHash"`
	Layouts                       []VERT012MIRLayout    `json:"layouts"`
	GCSafety                      GCSafetyPlan          `json:"gcSafety"`
	Functions                     []VERT012MIRFunction  `json:"functions"`
	ContentHash                   string                `json:"contentHash"`
}

type VERT012BoundMIR struct {
	SchemaVersion     uint32                 `json:"schemaVersion"`
	TargetContextHash string                 `json:"targetContextHash"`
	MIR               VERT012MIRModule       `json:"mir"`
	Closure           BoundCapabilityClosure `json:"closure"`
	ContentHash       string                 `json:"contentHash"`
}

func VERT012LayoutTypeKeys(contractHash string) (cell, environment string) {
	digest := sha256.Sum256([]byte("vert012-cell:" + contractHash))
	cell = hex.EncodeToString(digest[:])
	digest = sha256.Sum256([]byte("vert012-environment:" + contractHash))
	environment = hex.EncodeToString(digest[:])
	return
}

func LowerVERT012MIR(hir HIRModule, cellLayout, environmentLayout ObjectLayoutContract) (VERT012MIRModule, error) {
	if err := VerifyCanonicalVERT012ClosureHIR(hir); err != nil {
		return VERT012MIRModule{}, fmt.Errorf("verify VERT-012 HIR: %w", err)
	}
	cellKey, environmentKey := VERT012LayoutTypeKeys(hir.Closures.ContentHash)
	if err := verifyVERT012Layouts(cellLayout, environmentLayout, cellKey, environmentKey); err != nil {
		return VERT012MIRModule{}, err
	}
	gc, err := buildVERT012GCSafety(environmentKey, cellLayout.ContentHash, environmentLayout.ContentHash)
	if err != nil {
		return VERT012MIRModule{}, err
	}
	origin := hir.Functions[0].Origin
	cellOffset := cellLayout.Properties[0].FieldOffset
	environmentOffset := environmentLayout.Properties[0].FieldOffset
	module := VERT012MIRModule{SchemaVersion: VERT012MIRSchemaVersion, HIRHash: hir.ContentHash, LogicalCapabilityRequirements: slices.Clone(hir.LogicalCapabilityRequirements), ClosureContractHash: hir.Closures.ContentHash,
		Layouts: []VERT012MIRLayout{{Role: "cell", Contract: cellLayout}, {Role: "environment", Contract: environmentLayout}}, GCSafety: gc,
		Functions: []VERT012MIRFunction{
			{ID: 1, Name: "closure.increment", ParameterRep: VERT012RepGcRef, ReturnType: VERT012RepF64, Origin: origin, Instructions: []VERT012MIRInstruction{
				{ID: 2, Kind: "environment.cell.load", Type: VERT012RepGcRef, Operands: []ValueID{1}, FieldOffset: environmentOffset, Effect: EffectRead, Effects: []Effect{EffectRead}, Origin: origin},
				{ID: 3, Kind: "cell.value.load", Type: VERT012RepF64, Operands: []ValueID{2}, FieldOffset: cellOffset, Effect: EffectRead, Effects: []Effect{EffectRead}, Origin: origin},
				{ID: 4, Kind: "f64.const", Type: VERT012RepF64, NumberBits: "3ff0000000000000", Effect: EffectPure, Effects: []Effect{EffectPure}, Origin: origin},
				{ID: 5, Kind: "fadd", Type: VERT012RepF64, Operands: []ValueID{3, 4}, Effect: EffectPure, Effects: []Effect{EffectPure}, Origin: origin},
				{ID: 6, Kind: "cell.value.store", Type: VERT012RepF64, Operands: []ValueID{2, 5}, FieldOffset: cellOffset, Effect: EffectWrite, Effects: []Effect{EffectWrite}, Origin: origin},
			}},
			{ID: 2, Name: "closureCounter", Exported: true, ParameterRep: VERT012RepF64, ReturnType: VERT012RepF64, Origin: hir.Functions[1].Origin, Instructions: []VERT012MIRInstruction{
				{ID: 2, Kind: "cell.alloc", Type: VERT012RepGcRef, Effect: EffectAllocate, Effects: []Effect{EffectAllocate}, Origin: origin},
				{ID: 3, Kind: "cell.value.init", Type: VERT012RepGcRef, Operands: []ValueID{2, 1}, FieldOffset: cellOffset, Effect: EffectWrite, Effects: []Effect{EffectWrite}, Origin: origin},
				{ID: 4, Kind: "environment.alloc", Type: VERT012RepGcRef, Effect: EffectAllocate, Effects: []Effect{EffectAllocate}, Origin: origin},
				{ID: 5, Kind: "environment.cell.init", Type: VERT012RepGcRef, Operands: []ValueID{4, 2}, FieldOffset: environmentOffset, Effect: EffectWrite, Effects: []Effect{EffectWrite}, Origin: origin},
				{ID: 6, Kind: "closure.make", Type: VERT012RepFunction, Operands: []ValueID{4}, Callee: 1, Effect: EffectPure, Effects: []Effect{EffectPure}, Origin: origin},
				{ID: 7, Kind: "call.indirect", Type: VERT012RepF64, Operands: []ValueID{6}, Effect: EffectCall, Effects: []Effect{EffectCall, EffectRead, EffectWrite}, Origin: origin},
				{ID: 8, Kind: "call.indirect", Type: VERT012RepF64, Operands: []ValueID{6}, Effect: EffectCall, Effects: []Effect{EffectCall, EffectRead, EffectWrite}, Origin: origin},
				{ID: 9, Kind: "fadd", Type: VERT012RepF64, Operands: []ValueID{7, 8}, Effect: EffectPure, Effects: []Effect{EffectPure}, Origin: origin},
			}},
		}}
	_, hash, err := CanonicalVERT012MIR(module)
	if err != nil {
		return VERT012MIRModule{}, err
	}
	module.ContentHash = hash
	return module, nil
}

func verifyVERT012Layouts(cell, environment ObjectLayoutContract, cellKey, environmentKey string) error {
	for _, layout := range []*ObjectLayoutContract{&cell, &environment} {
		claimed := layout.ContentHash
		_, want, err := CanonicalObjectLayoutContract(*layout)
		if err != nil || claimed == "" || claimed != want {
			return fmt.Errorf("VERT-012 layout is not canonical")
		}
	}
	if cell.TypeKey != cellKey || len(cell.Properties) != 1 || cell.Properties[0].Key != "value" || cell.Properties[0].Representation != "f64" || len(cell.TraceOffsets) != 0 {
		return fmt.Errorf("invalid VERT-012 cell layout")
	}
	if environment.TypeKey != environmentKey || len(environment.Properties) != 1 || environment.Properties[0].Key != "cell" || environment.Properties[0].Representation != "gc-ref" || !slices.Equal(environment.TraceOffsets, []uint32{environment.Properties[0].FieldOffset}) {
		return fmt.Errorf("invalid VERT-012 environment layout")
	}
	if cell.Target != environment.Target {
		return fmt.Errorf("VERT-012 layouts target mismatch")
	}
	return nil
}

func VerifyVERT012MIR(module VERT012MIRModule) error {
	if module.SchemaVersion != VERT012MIRSchemaVersion || !validSHA256Hex(module.HIRHash) || !validSHA256Hex(module.ClosureContractHash) || len(module.Layouts) != 2 || module.Layouts[0].Role != "cell" || module.Layouts[1].Role != "environment" || len(module.Functions) != 2 {
		return fmt.Errorf("invalid VERT-012 MIR envelope")
	}
	if !slices.Equal(module.LogicalCapabilityRequirements, VERT012LogicalCapabilities()) {
		return fmt.Errorf("VERT-012 MIR capabilities mismatch")
	}
	cellKey, environmentKey := VERT012LayoutTypeKeys(module.ClosureContractHash)
	if err := verifyVERT012Layouts(module.Layouts[0].Contract, module.Layouts[1].Contract, cellKey, environmentKey); err != nil {
		return err
	}
	if err := VerifyGCSafetyPlanStructure(module.GCSafety); err != nil {
		return fmt.Errorf("VERT-012 GC safety: %w", err)
	}
	wantGC, err := buildVERT012GCSafety(environmentKey, module.Layouts[0].Contract.ContentHash, module.Layouts[1].Contract.ContentHash)
	if err != nil {
		return err
	}
	left, _ := jsonx.Marshal(module.GCSafety)
	right, _ := jsonx.Marshal(wantGC)
	if !slices.Equal(left, right) {
		return fmt.Errorf("VERT-012 GC safety mismatch")
	}
	cellOffset := module.Layouts[0].Contract.Properties[0].FieldOffset
	environmentOffset := module.Layouts[1].Contract.Properties[0].FieldOffset
	want := fixedVERT012MIRFunctions(module.Functions[0].Origin, module.Functions[1].Origin, cellOffset, environmentOffset)
	left, _ = jsonx.Marshal(module.Functions)
	right, _ = jsonx.Marshal(want)
	if !slices.Equal(left, right) {
		return fmt.Errorf("VERT-012 MIR function mismatch")
	}
	return nil
}

func fixedVERT012MIRFunctions(closureOrigin, entryOrigin Origin, cellOffset, environmentOffset uint32) []VERT012MIRFunction {
	return []VERT012MIRFunction{
		{ID: 1, Name: "closure.increment", ParameterRep: VERT012RepGcRef, ReturnType: VERT012RepF64, Origin: closureOrigin,
			Instructions: []VERT012MIRInstruction{
				{ID: 2, Kind: "environment.cell.load", Type: VERT012RepGcRef, Operands: []ValueID{1}, FieldOffset: environmentOffset, Effect: EffectRead, Effects: []Effect{EffectRead}, Origin: closureOrigin},
				{ID: 3, Kind: "cell.value.load", Type: VERT012RepF64, Operands: []ValueID{2}, FieldOffset: cellOffset, Effect: EffectRead, Effects: []Effect{EffectRead}, Origin: closureOrigin},
				{ID: 4, Kind: "f64.const", Type: VERT012RepF64, NumberBits: "3ff0000000000000", Effect: EffectPure, Effects: []Effect{EffectPure}, Origin: closureOrigin},
				{ID: 5, Kind: "fadd", Type: VERT012RepF64, Operands: []ValueID{3, 4}, Effect: EffectPure, Effects: []Effect{EffectPure}, Origin: closureOrigin},
				{ID: 6, Kind: "cell.value.store", Type: VERT012RepF64, Operands: []ValueID{2, 5}, FieldOffset: cellOffset, Effect: EffectWrite, Effects: []Effect{EffectWrite}, Origin: closureOrigin},
			}},
		{ID: 2, Name: "closureCounter", Exported: true, ParameterRep: VERT012RepF64, ReturnType: VERT012RepF64, Origin: entryOrigin,
			Instructions: []VERT012MIRInstruction{
				{ID: 2, Kind: "cell.alloc", Type: VERT012RepGcRef, Effect: EffectAllocate, Effects: []Effect{EffectAllocate}, Origin: closureOrigin},
				{ID: 3, Kind: "cell.value.init", Type: VERT012RepGcRef, Operands: []ValueID{2, 1}, FieldOffset: cellOffset, Effect: EffectWrite, Effects: []Effect{EffectWrite}, Origin: closureOrigin},
				{ID: 4, Kind: "environment.alloc", Type: VERT012RepGcRef, Effect: EffectAllocate, Effects: []Effect{EffectAllocate}, Origin: closureOrigin},
				{ID: 5, Kind: "environment.cell.init", Type: VERT012RepGcRef, Operands: []ValueID{4, 2}, FieldOffset: environmentOffset, Effect: EffectWrite, Effects: []Effect{EffectWrite}, Origin: closureOrigin},
				{ID: 6, Kind: "closure.make", Type: VERT012RepFunction, Operands: []ValueID{4}, Callee: 1, Effect: EffectPure, Effects: []Effect{EffectPure}, Origin: closureOrigin},
				{ID: 7, Kind: "call.indirect", Type: VERT012RepF64, Operands: []ValueID{6}, Effect: EffectCall, Effects: []Effect{EffectCall, EffectRead, EffectWrite}, Origin: closureOrigin},
				{ID: 8, Kind: "call.indirect", Type: VERT012RepF64, Operands: []ValueID{6}, Effect: EffectCall, Effects: []Effect{EffectCall, EffectRead, EffectWrite}, Origin: closureOrigin},
				{ID: 9, Kind: "fadd", Type: VERT012RepF64, Operands: []ValueID{7, 8}, Effect: EffectPure, Effects: []Effect{EffectPure}, Origin: closureOrigin},
			}},
	}
}

func buildVERT012GCSafety(functionKey, cellHash, environmentHash string) (GCSafetyPlan, error) {
	return FinalizeGCSafetyPlan(GCSafetyPlan{
		FunctionKey: functionKey,
		Slots: []GCRootSlot{
			{ID: 1, TraceLayoutHash: cellHash},
			{ID: 2, TraceLayoutHash: environmentHash},
		},
		Blocks: []GCSafetyBlock{{
			ID: 1, Terminator: "return",
			Instructions: []GCInstruction{
				{ID: 1, Kind: GCOpFrameLink},
				{ID: 2, Kind: GCOpRootClear, Slot: 1},
				{ID: 3, Kind: GCOpRootClear, Slot: 2},
				{ID: 4, Kind: GCOpRootPublish, ActiveSlots: []GCRootSlotID{}},
				{ID: 5, Kind: GCOpSafepoint, SafepointKind: "cell-allocation", MayAllocate: true},
				{ID: 6, Kind: GCOpRefDef, Value: 1},
				{ID: 7, Kind: GCOpRootStore, Slot: 1, Value: 1},
				{ID: 8, Kind: GCOpRootClear, Slot: 2},
				{ID: 9, Kind: GCOpRootPublish, ActiveSlots: []GCRootSlotID{1}},
				{ID: 10, Kind: GCOpSafepoint, SafepointKind: "environment-allocation", MayAllocate: true},
				{ID: 11, Kind: GCOpRootReload, Slot: 1, Value: 1},
				{ID: 12, Kind: GCOpRefDef, Value: 2},
				{ID: 13, Kind: GCOpRootStore, Slot: 1, Value: 1},
				{ID: 14, Kind: GCOpRootStore, Slot: 2, Value: 2},
				{ID: 15, Kind: GCOpRootPublish, ActiveSlots: []GCRootSlotID{1, 2}},
				{ID: 16, Kind: GCOpSafepoint, SafepointKind: "forced-collection", MayAllocate: true},
				{ID: 17, Kind: GCOpRootReload, Slot: 1, Value: 1},
				{ID: 18, Kind: GCOpRootReload, Slot: 2, Value: 2},
				{ID: 19, Kind: GCOpRefUse, Uses: []GCValueID{1, 2}},
				{ID: 20, Kind: GCOpFrameUnlink},
			},
		}},
	})
}

func CanonicalVERT012MIR(module VERT012MIRModule) ([]byte, string, error) {
	module.ContentHash = ""
	if err := VerifyVERT012MIR(module); err != nil {
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
func VerifyCanonicalVERT012MIR(module VERT012MIRModule) error {
	claimed := module.ContentHash
	_, want, err := CanonicalVERT012MIR(module)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("VERT-012 MIR content hash mismatch")
	}
	return nil
}
func DecodeVERT012MIR(data []byte) (*VERT012MIRModule, error) {
	var module VERT012MIRModule
	if err := jsonx.Unmarshal(data, &module, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	if err := VerifyCanonicalVERT012MIR(module); err != nil {
		return nil, err
	}
	return &module, nil
}

func NewVERT012BoundMIR(module VERT012MIRModule, targetContextHash, catalogHash string, bindings []BoundCapability) (VERT012BoundMIR, error) {
	if err := VerifyCanonicalVERT012MIR(module); err != nil {
		return VERT012BoundMIR{}, err
	}
	if !validSHA256Hex(targetContextHash) || !validSHA256Hex(catalogHash) {
		return VERT012BoundMIR{}, fmt.Errorf("invalid VERT-012 bound target identity")
	}
	digest, err := LogicalCapabilityRequirementsDigest(module.LogicalCapabilityRequirements)
	if err != nil {
		return VERT012BoundMIR{}, err
	}
	closure := BoundCapabilityClosure{SchemaVersion: BoundCapabilitySchemaVersion, AvailableCapabilityCatalogHash: catalogHash, LogicalCapabilityRequirementsDigest: digest, Bindings: slices.Clone(bindings)}
	closure.ContentHash, err = boundCapabilityContentHash(closure)
	if err != nil {
		return VERT012BoundMIR{}, err
	}
	bound := VERT012BoundMIR{SchemaVersion: VERT012BoundMIRSchemaVersion, TargetContextHash: targetContextHash, MIR: module, Closure: closure}
	bound.ContentHash, err = vert012BoundMIRContentHash(bound)
	if err != nil {
		return VERT012BoundMIR{}, err
	}
	if err := VerifyVERT012BoundMIR(bound); err != nil {
		return VERT012BoundMIR{}, err
	}
	return bound, nil
}

func VerifyVERT012BoundMIR(bound VERT012BoundMIR) error {
	if bound.SchemaVersion != VERT012BoundMIRSchemaVersion || !validSHA256Hex(bound.TargetContextHash) || !validSHA256Hex(bound.ContentHash) {
		return fmt.Errorf("invalid VERT-012 bound MIR envelope")
	}
	if err := VerifyCanonicalVERT012MIR(bound.MIR); err != nil {
		return err
	}
	digest, err := LogicalCapabilityRequirementsDigest(bound.MIR.LogicalCapabilityRequirements)
	closure := bound.Closure
	if err != nil || closure.SchemaVersion != BoundCapabilitySchemaVersion || !validSHA256Hex(closure.AvailableCapabilityCatalogHash) || closure.LogicalCapabilityRequirementsDigest != digest || len(closure.Bindings) != len(bound.MIR.LogicalCapabilityRequirements) {
		return fmt.Errorf("VERT-012 bound capability closure mismatch")
	}
	for index, requirement := range bound.MIR.LogicalCapabilityRequirements {
		binding := closure.Bindings[index]
		if binding.LogicalName != requirement || binding.SymbolName == "" || !validSHA256Hex(binding.SignatureHash) {
			return fmt.Errorf("VERT-012 capability %q is not exactly bound", requirement)
		}
	}
	wantClosure, err := boundCapabilityContentHash(closure)
	if err != nil || closure.ContentHash != wantClosure {
		return fmt.Errorf("VERT-012 bound capability content hash mismatch")
	}
	want, err := vert012BoundMIRContentHash(bound)
	if err != nil || bound.ContentHash != want {
		return fmt.Errorf("VERT-012 bound MIR content hash mismatch")
	}
	return nil
}

func CanonicalVERT012BoundMIR(bound VERT012BoundMIR) ([]byte, string, error) {
	if err := VerifyVERT012BoundMIR(bound); err != nil {
		return nil, "", err
	}
	encoded, err := jsonx.Marshal(bound)
	return encoded, bound.ContentHash, err
}
func DecodeVERT012BoundMIR(data []byte) (*VERT012BoundMIR, error) {
	var bound VERT012BoundMIR
	if err := jsonx.Unmarshal(data, &bound, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode VERT-012 bound MIR: %w", err)
	}
	if err := VerifyVERT012BoundMIR(bound); err != nil {
		return nil, err
	}
	return &bound, nil
}
func vert012BoundMIRContentHash(bound VERT012BoundMIR) (string, error) {
	withoutHash := bound
	withoutHash.ContentHash = ""
	encoded, err := jsonx.Marshal(withoutHash)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
