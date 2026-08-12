package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const VERT013aHIRSchemaVersion uint32 = 12
const maxVERT013aHIRBytes = 256 << 10

func VERT013aLogicalCapabilities() []RuntimeCapabilityID {
	return VERT010LogicalCapabilities()
}

func fixedVERT013aHIR(provenance HIRProvenance, contract ClassContract) HIRModule {
	origin := Origin{File: "/project/classcounter.ts", Start: 1, End: 2}
	empty := []RuntimeCapabilityID{}
	field := contract.Classes[0].Fields[0].SymbolKey
	typeKey := contract.Classes[0].InstanceTypeKey
	return HIRModule{SchemaVersion: VERT013aHIRSchemaVersion, Provenance: provenance, LogicalCapabilityRequirements: VERT013aLogicalCapabilities(), Classes: &contract,
		Functions: []HIRFunction{
			{ID: 1, Name: "Counter.constructor", Parameters: []HIRParameter{{Name: "start", Value: 1, Type: TypeNumber, Origin: origin}}, ReturnType: TypeObject, Origin: origin,
				Blocks: []HIRBlock{{ID: 1, Operations: []HIROp{
					{ID: 2, Kind: "class.alloc", Type: TypeObject, ObjectTypeKey: typeKey, Effect: EffectAllocate, Effects: []Effect{EffectAllocate}, LogicalCapabilityRequirements: []RuntimeCapabilityID{"rt.gc.alloc"}, Origin: origin},
					{ID: 3, Kind: "number.constant", Type: TypeNumber, NumberBits: "0000000000000000", Effect: EffectPure, Effects: []Effect{EffectPure}, LogicalCapabilityRequirements: empty, Origin: origin},
					{ID: 4, Kind: "class.field.init", Type: TypeNumber, Operands: []ValueID{2, 3}, ObjectTypeKey: typeKey, PropertySymbolKey: field, Effect: EffectWrite, Effects: []Effect{EffectWrite}, LogicalCapabilityRequirements: empty, Origin: origin},
					{ID: 5, Kind: "class.field.store", Type: TypeNumber, Operands: []ValueID{2, 1}, ObjectTypeKey: typeKey, PropertySymbolKey: field, Effect: EffectWrite, Effects: []Effect{EffectWrite}, LogicalCapabilityRequirements: empty, Origin: origin},
				}, Terminator: HIRTerminator{Kind: "return", Value: 2, Origin: origin}}}},
			{ID: 2, Name: "Counter.increment", Parameters: []HIRParameter{{Name: "receiver", Value: 1, Type: TypeObject, Origin: origin}}, ReturnType: TypeNumber, Origin: origin,
				Blocks: []HIRBlock{{ID: 1, Operations: []HIROp{
					{ID: 2, Kind: "class.field.load", Type: TypeNumber, Operands: []ValueID{1}, ObjectTypeKey: typeKey, PropertySymbolKey: field, Effect: EffectRead, Effects: []Effect{EffectRead}, LogicalCapabilityRequirements: empty, Origin: origin},
					{ID: 3, Kind: "number.constant", Type: TypeNumber, NumberBits: "3ff0000000000000", Effect: EffectPure, Effects: []Effect{EffectPure}, LogicalCapabilityRequirements: empty, Origin: origin},
					{ID: 4, Kind: "binary", Type: TypeNumber, Operands: []ValueID{2, 3}, Operator: "+", Effect: EffectPure, Effects: []Effect{EffectPure}, LogicalCapabilityRequirements: empty, Origin: origin},
					{ID: 5, Kind: "class.field.store", Type: TypeNumber, Operands: []ValueID{1, 4}, ObjectTypeKey: typeKey, PropertySymbolKey: field, Effect: EffectWrite, Effects: []Effect{EffectWrite}, LogicalCapabilityRequirements: empty, Origin: origin},
				}, Terminator: HIRTerminator{Kind: "return", Value: 5, Origin: origin}}}},
			{ID: 3, Name: "classCounter", Exported: true, Parameters: []HIRParameter{{Name: "start", Value: 1, Type: TypeNumber, Origin: origin}}, ReturnType: TypeNumber, Origin: origin,
				Blocks: []HIRBlock{{ID: 1, Operations: []HIROp{
					{ID: 2, Kind: "call.constructor", Type: TypeObject, Operands: []ValueID{1}, Callee: 1, ObjectTypeKey: typeKey, Effect: EffectCall, Effects: []Effect{EffectCall, EffectAllocate, EffectWrite}, LogicalCapabilityRequirements: []RuntimeCapabilityID{"rt.gc.alloc"}, Origin: origin},
					{ID: 3, Kind: "call.method", Type: TypeNumber, Operands: []ValueID{2}, Callee: 2, ObjectTypeKey: typeKey, PropertySymbolKey: contract.Classes[0].Methods[0].SymbolKey, Effect: EffectCall, Effects: []Effect{EffectCall, EffectRead, EffectWrite}, LogicalCapabilityRequirements: empty, Origin: origin},
					{ID: 4, Kind: "call.method", Type: TypeNumber, Operands: []ValueID{2}, Callee: 2, ObjectTypeKey: typeKey, PropertySymbolKey: contract.Classes[0].Methods[0].SymbolKey, Effect: EffectCall, Effects: []Effect{EffectCall, EffectRead, EffectWrite}, LogicalCapabilityRequirements: empty, Origin: origin},
					{ID: 5, Kind: "binary", Type: TypeNumber, Operands: []ValueID{3, 4}, Operator: "+", Effect: EffectPure, Effects: []Effect{EffectPure}, LogicalCapabilityRequirements: empty, Origin: origin},
				}, Terminator: HIRTerminator{Kind: "return", Value: 5, Origin: origin}}}},
		}}
}

func NewVERT013aClassHIR(provenance HIRProvenance, contract ClassContract) (HIRModule, error) {
	if _, hash, err := CanonicalClassContract(contract); err != nil || contract.ContentHash != hash {
		return HIRModule{}, fmt.Errorf("invalid VERT-013a class contract")
	}
	module := fixedVERT013aHIR(provenance, contract)
	if err := VerifyVERT013aClassHIR(module); err != nil {
		return HIRModule{}, err
	}
	_, hash, err := CanonicalVERT013aClassHIR(module)
	if err != nil {
		return HIRModule{}, err
	}
	module.ContentHash = hash
	return module, nil
}

func VerifyVERT013aClassHIR(module HIRModule) error {
	if module.SchemaVersion != VERT013aHIRSchemaVersion || module.Classes == nil || module.ClassAccess != nil || len(module.ClassAccessProofs) != 0 || module.ClassAccessExecution != nil || module.ObjectTypes != nil || module.PlaceRefs != nil || module.Closures != nil || len(module.Functions) != 3 {
		return fmt.Errorf("invalid VERT-013a HIR header")
	}
	if err := validateHIRProvenance(module.Provenance); err != nil {
		return err
	}
	digest, err := LogicalCapabilityRequirementsDigest(VERT013aLogicalCapabilities())
	if err != nil || module.Provenance.LogicalCapabilityRequirementsDigest != digest || !slices.Equal(module.LogicalCapabilityRequirements, VERT013aLogicalCapabilities()) {
		return fmt.Errorf("invalid VERT-013a capabilities")
	}
	if _, hash, err := CanonicalClassContract(*module.Classes); err != nil || module.Classes.ContentHash != hash {
		return fmt.Errorf("invalid VERT-013a class contract")
	}
	want := fixedVERT013aHIR(module.Provenance, *module.Classes)
	left, _ := jsonx.Marshal(module.Functions)
	right, _ := jsonx.Marshal(want.Functions)
	if !slices.Equal(left, right) {
		return fmt.Errorf("VERT-013a class CFG or operation mismatch")
	}
	return nil
}

func CanonicalVERT013aClassHIR(module HIRModule) ([]byte, string, error) {
	module.ContentHash = ""
	if err := VerifyVERT013aClassHIR(module); err != nil {
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

func VerifyCanonicalVERT013aClassHIR(module HIRModule) error {
	claimed := module.ContentHash
	_, want, err := CanonicalVERT013aClassHIR(module)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("VERT-013a HIR content hash mismatch")
	}
	return nil
}

func DecodeVERT013aClassHIR(data []byte) (*HIRModule, error) {
	if len(data) > maxVERT013aHIRBytes {
		return nil, fmt.Errorf("VERT-013a HIR exceeds %d bytes", maxVERT013aHIRBytes)
	}
	var module HIRModule
	if err := jsonx.Unmarshal(data, &module, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	if err := VerifyCanonicalVERT013aClassHIR(module); err != nil {
		return nil, err
	}
	return &module, nil
}
