package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const VERT013bHIRSchemaVersion uint32 = 13
const maxVERT013bHIRBytes = 320 << 10

func VERT013bLogicalCapabilities() []RuntimeCapabilityID { return VERT013aLogicalCapabilities() }

func fixedVERT013bHIR(provenance HIRProvenance, contract VERT013bClassContract) HIRModule {
	empty := []RuntimeCapabilityID{}
	base, derived := contract.Classes[0], contract.Classes[1]
	origin := Origin{File: "/project/derivedcounter.ts", Start: 1, End: 2}
	module := HIRModule{SchemaVersion: VERT013bHIRSchemaVersion, Provenance: provenance, LogicalCapabilityRequirements: VERT013bLogicalCapabilities(), DerivedClasses: &contract, Functions: []HIRFunction{
		{ID: 1, Name: "Counter.constructor", Parameters: []HIRParameter{{Name: "receiver", Value: 1, Type: TypeObject, Origin: origin}, {Name: "start", Value: 2, Type: TypeNumber, Origin: origin}}, ReturnType: TypeObject, Origin: origin, Blocks: []HIRBlock{{ID: 1, Operations: []HIROp{{ID: 3, Kind: "number.constant", Type: TypeNumber, NumberBits: "0000000000000000", Effect: EffectPure, Effects: []Effect{EffectPure}, LogicalCapabilityRequirements: empty, Origin: origin}, {ID: 4, Kind: "class.field.init", Type: TypeNumber, Operands: []ValueID{1, 3}, ObjectTypeKey: base.InstanceTypeKey, PropertySymbolKey: base.Fields[0].SymbolKey, Effect: EffectWrite, Effects: []Effect{EffectWrite}, LogicalCapabilityRequirements: empty, Origin: origin}, {ID: 5, Kind: "class.field.store", Type: TypeNumber, Operands: []ValueID{1, 2}, ObjectTypeKey: base.InstanceTypeKey, PropertySymbolKey: base.Fields[0].SymbolKey, Effect: EffectWrite, Effects: []Effect{EffectWrite}, LogicalCapabilityRequirements: empty, Origin: origin}}, Terminator: HIRTerminator{Kind: "return", Value: 1, Origin: origin}}}},
		{ID: 2, Name: "StepCounter.constructor", Parameters: []HIRParameter{{Name: "start", Value: 1, Type: TypeNumber, Origin: origin}, {Name: "step", Value: 2, Type: TypeNumber, Origin: origin}}, ReturnType: TypeObject, Origin: origin, Blocks: []HIRBlock{{ID: 1, Operations: []HIROp{{ID: 3, Kind: "class.alloc", Type: TypeObject, ObjectTypeKey: derived.InstanceTypeKey, Effect: EffectAllocate, Effects: []Effect{EffectAllocate}, LogicalCapabilityRequirements: []RuntimeCapabilityID{"rt.gc.alloc"}, Origin: origin}, {ID: 4, Kind: "call.super", Type: TypeObject, Operands: []ValueID{3, 1}, Callee: 1, ObjectTypeKey: base.InstanceTypeKey, Effect: EffectCall, Effects: []Effect{EffectCall, EffectWrite}, LogicalCapabilityRequirements: empty, Origin: origin}, {ID: 5, Kind: "number.constant", Type: TypeNumber, NumberBits: "3ff0000000000000", Effect: EffectPure, Effects: []Effect{EffectPure}, LogicalCapabilityRequirements: empty, Origin: origin}, {ID: 6, Kind: "class.field.init", Type: TypeNumber, Operands: []ValueID{3, 5}, ObjectTypeKey: derived.InstanceTypeKey, PropertySymbolKey: derived.Fields[0].SymbolKey, Effect: EffectWrite, Effects: []Effect{EffectWrite}, LogicalCapabilityRequirements: empty, Origin: origin}, {ID: 7, Kind: "class.field.store", Type: TypeNumber, Operands: []ValueID{3, 2}, ObjectTypeKey: derived.InstanceTypeKey, PropertySymbolKey: derived.Fields[0].SymbolKey, Effect: EffectWrite, Effects: []Effect{EffectWrite}, LogicalCapabilityRequirements: empty, Origin: origin}}, Terminator: HIRTerminator{Kind: "return", Value: 3, Origin: origin}}}},
		{ID: 3, Name: "StepCounter.increment", Parameters: []HIRParameter{{Name: "receiver", Value: 1, Type: TypeObject, Origin: origin}}, ReturnType: TypeNumber, Origin: origin, Blocks: []HIRBlock{{ID: 1, Operations: []HIROp{{ID: 2, Kind: "class.field.load", Type: TypeNumber, Operands: []ValueID{1}, ObjectTypeKey: base.InstanceTypeKey, PropertySymbolKey: base.Fields[0].SymbolKey, Effect: EffectRead, Effects: []Effect{EffectRead}, LogicalCapabilityRequirements: empty, Origin: origin}, {ID: 3, Kind: "class.field.load", Type: TypeNumber, Operands: []ValueID{1}, ObjectTypeKey: derived.InstanceTypeKey, PropertySymbolKey: derived.Fields[0].SymbolKey, Effect: EffectRead, Effects: []Effect{EffectRead}, LogicalCapabilityRequirements: empty, Origin: origin}, {ID: 4, Kind: "binary", Type: TypeNumber, Operands: []ValueID{2, 3}, Operator: "+", Effect: EffectPure, Effects: []Effect{EffectPure}, LogicalCapabilityRequirements: empty, Origin: origin}, {ID: 5, Kind: "class.field.store", Type: TypeNumber, Operands: []ValueID{1, 4}, ObjectTypeKey: base.InstanceTypeKey, PropertySymbolKey: base.Fields[0].SymbolKey, Effect: EffectWrite, Effects: []Effect{EffectWrite}, LogicalCapabilityRequirements: empty, Origin: origin}}, Terminator: HIRTerminator{Kind: "return", Value: 4, Origin: origin}}}},
		{ID: 4, Name: "derivedCounter", Exported: true, Parameters: []HIRParameter{{Name: "start", Value: 1, Type: TypeNumber, Origin: origin}, {Name: "step", Value: 2, Type: TypeNumber, Origin: origin}}, ReturnType: TypeNumber, Origin: origin, Blocks: []HIRBlock{{ID: 1, Operations: []HIROp{{ID: 3, Kind: "call.constructor", Type: TypeObject, Operands: []ValueID{1, 2}, Callee: 2, ObjectTypeKey: derived.InstanceTypeKey, Effect: EffectCall, Effects: []Effect{EffectCall, EffectAllocate, EffectWrite}, LogicalCapabilityRequirements: []RuntimeCapabilityID{"rt.gc.alloc"}, Origin: origin}, {ID: 4, Kind: "call.method", Type: TypeNumber, Operands: []ValueID{3}, Callee: 3, ObjectTypeKey: derived.InstanceTypeKey, PropertySymbolKey: derived.Methods[0].SymbolKey, Effect: EffectCall, Effects: []Effect{EffectCall, EffectRead, EffectWrite}, LogicalCapabilityRequirements: empty, Origin: origin}, {ID: 5, Kind: "call.method", Type: TypeNumber, Operands: []ValueID{3}, Callee: 3, ObjectTypeKey: derived.InstanceTypeKey, PropertySymbolKey: derived.Methods[0].SymbolKey, Effect: EffectCall, Effects: []Effect{EffectCall, EffectRead, EffectWrite}, LogicalCapabilityRequirements: empty, Origin: origin}, {ID: 6, Kind: "binary", Type: TypeNumber, Operands: []ValueID{4, 5}, Operator: "+", Effect: EffectPure, Effects: []Effect{EffectPure}, LogicalCapabilityRequirements: empty, Origin: origin}}, Terminator: HIRTerminator{Kind: "return", Value: 6, Origin: origin}}}},
	}}
	return module
}

func NewVERT013bDerivedHIR(provenance HIRProvenance, contract VERT013bClassContract) (HIRModule, error) {
	if err := VerifyCanonicalVERT013bClassContract(contract); err != nil {
		return HIRModule{}, err
	}
	module := fixedVERT013bHIR(provenance, contract)
	if err := VerifyVERT013bDerivedHIR(module); err != nil {
		return HIRModule{}, err
	}
	_, hash, err := CanonicalVERT013bDerivedHIR(module)
	if err != nil {
		return HIRModule{}, err
	}
	module.ContentHash = hash
	return module, nil
}

func VerifyVERT013bDerivedHIR(module HIRModule) error {
	if module.SchemaVersion != VERT013bHIRSchemaVersion || module.DerivedClasses == nil || module.Classes != nil || module.ClassAccess != nil || len(module.ClassAccessProofs) != 0 || module.ClassAccessExecution != nil || module.ObjectTypes != nil || module.PlaceRefs != nil || module.Closures != nil || len(module.Functions) != 4 {
		return fmt.Errorf("invalid VERT-013b HIR header")
	}
	if err := validateHIRProvenance(module.Provenance); err != nil {
		return err
	}
	if err := VerifyCanonicalVERT013bClassContract(*module.DerivedClasses); err != nil {
		return err
	}
	digest, err := LogicalCapabilityRequirementsDigest(VERT013bLogicalCapabilities())
	if err != nil || module.Provenance.LogicalCapabilityRequirementsDigest != digest || !slices.Equal(module.LogicalCapabilityRequirements, VERT013bLogicalCapabilities()) {
		return fmt.Errorf("invalid VERT-013b capabilities")
	}
	want := fixedVERT013bHIR(module.Provenance, *module.DerivedClasses)
	left, err := jsonx.Marshal(module.Functions)
	if err != nil {
		return err
	}
	right, err := jsonx.Marshal(want.Functions)
	if err != nil {
		return err
	}
	if !slices.Equal(left, right) {
		return fmt.Errorf("VERT-013b derived class CFG or operation mismatch")
	}
	return nil
}

func CanonicalVERT013bDerivedHIR(module HIRModule) ([]byte, string, error) {
	module.ContentHash = ""
	if err := VerifyVERT013bDerivedHIR(module); err != nil {
		return nil, "", err
	}
	encoded, err := jsonx.Marshal(module)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	module.ContentHash = hex.EncodeToString(digest[:])
	encoded, err = jsonx.Marshal(module)
	return encoded, module.ContentHash, err
}
func VerifyCanonicalVERT013bDerivedHIR(module HIRModule) error {
	claimed := module.ContentHash
	_, want, err := CanonicalVERT013bDerivedHIR(module)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("VERT-013b HIR content hash mismatch")
	}
	return nil
}
func DecodeVERT013bDerivedHIR(data []byte) (*HIRModule, error) {
	if len(data) > maxVERT013bHIRBytes {
		return nil, fmt.Errorf("VERT-013b HIR exceeds %d bytes", maxVERT013bHIRBytes)
	}
	var module HIRModule
	if err := jsonx.Unmarshal(data, &module, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	if err := VerifyCanonicalVERT013bDerivedHIR(module); err != nil {
		return nil, err
	}
	return &module, nil
}
