package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const VERT012HIRSchemaVersion uint32 = 11

var vert012Capabilities = append(VERT010LogicalCapabilities(), RuntimeCapabilityID("rt.gc.write_barrier"))

func VERT012LogicalCapabilities() []RuntimeCapabilityID {
	return slices.Clone(vert012Capabilities)
}

func fixedVERT012HIR(provenance HIRProvenance, contract ClosureContract) HIRModule {
	origin := Origin{File: "/project/closurecounter.ts", Start: 1, End: 2}
	empty := []RuntimeCapabilityID{}
	module := HIRModule{SchemaVersion: VERT012HIRSchemaVersion, Provenance: provenance, LogicalCapabilityRequirements: VERT012LogicalCapabilities(), Closures: &contract,
		Functions: []HIRFunction{
			{ID: 1, Name: "closure.increment", Parameters: []HIRParameter{{Name: "environment", Value: 1, Type: TypeEnvironment, Origin: origin}}, ReturnType: TypeNumber, Origin: origin,
				Blocks: []HIRBlock{{ID: 1, Operations: []HIROp{
					{ID: 2, Kind: "cell.load", Type: TypeNumber, Operands: []ValueID{1}, Effect: EffectRead, Effects: []Effect{EffectRead}, LogicalCapabilityRequirements: empty, Origin: origin},
					{ID: 3, Kind: "number.constant", Type: TypeNumber, NumberBits: "3ff0000000000000", Effect: EffectPure, Effects: []Effect{EffectPure}, LogicalCapabilityRequirements: empty, Origin: origin},
					{ID: 4, Kind: "binary", Type: TypeNumber, Operands: []ValueID{2, 3}, Operator: "+", Effect: EffectPure, Effects: []Effect{EffectPure}, LogicalCapabilityRequirements: empty, Origin: origin},
					{ID: 5, Kind: "cell.store", Type: TypeNumber, Operands: []ValueID{1, 4}, Effect: EffectWrite, Effects: []Effect{EffectWrite}, LogicalCapabilityRequirements: empty, Origin: origin},
				}, Terminator: HIRTerminator{Kind: "return", Value: 5, Origin: origin}}}},
			{ID: 2, Name: "closureCounter", Exported: true, Parameters: []HIRParameter{{Name: "start", Value: 1, Type: TypeNumber, Origin: origin}}, ReturnType: TypeNumber, Origin: origin,
				Blocks: []HIRBlock{{ID: 1, Operations: []HIROp{
					{ID: 2, Kind: "environment.alloc", Type: TypeEnvironment, Effect: EffectAllocate, Effects: []Effect{EffectAllocate}, LogicalCapabilityRequirements: []RuntimeCapabilityID{"rt.gc.alloc"}, Origin: origin},
					{ID: 3, Kind: "cell.init", Type: TypeCell, Operands: []ValueID{2, 1}, Effect: EffectWrite, Effects: []Effect{EffectWrite}, LogicalCapabilityRequirements: empty, Origin: origin},
					{ID: 4, Kind: "closure.make", Type: TypeFunction, Operands: []ValueID{2, 3}, Callee: 1, Effect: EffectPure, Effects: []Effect{EffectPure}, LogicalCapabilityRequirements: empty, Origin: origin},
					{ID: 5, Kind: "call.indirect", Type: TypeNumber, Operands: []ValueID{4}, Effect: EffectCall, Effects: []Effect{EffectCall, EffectRead, EffectWrite}, LogicalCapabilityRequirements: empty, Origin: origin},
					{ID: 6, Kind: "call.indirect", Type: TypeNumber, Operands: []ValueID{4}, Effect: EffectCall, Effects: []Effect{EffectCall, EffectRead, EffectWrite}, LogicalCapabilityRequirements: empty, Origin: origin},
					{ID: 7, Kind: "binary", Type: TypeNumber, Operands: []ValueID{5, 6}, Operator: "+", Effect: EffectPure, Effects: []Effect{EffectPure}, LogicalCapabilityRequirements: empty, Origin: origin},
				}, Terminator: HIRTerminator{Kind: "return", Value: 7, Origin: origin}}}},
		}}
	return module
}

func NewVERT012ClosureHIR(provenance HIRProvenance, contract ClosureContract) (HIRModule, error) {
	if _, hash, err := CanonicalClosureContract(contract); err != nil || contract.ContentHash != hash {
		return HIRModule{}, fmt.Errorf("invalid VERT-012 closure contract")
	}
	module := fixedVERT012HIR(provenance, contract)
	if err := VerifyVERT012ClosureHIR(module); err != nil {
		return HIRModule{}, err
	}
	_, hash, err := CanonicalVERT012ClosureHIR(module)
	if err != nil {
		return HIRModule{}, err
	}
	module.ContentHash = hash
	return module, nil
}

func VerifyVERT012ClosureHIR(module HIRModule) error {
	if module.SchemaVersion != VERT012HIRSchemaVersion || module.Closures == nil || module.ClassAccess != nil || len(module.ClassAccessProofs) != 0 || module.ClassAccessExecution != nil || module.ObjectTypes != nil || module.PlaceRefs != nil || len(module.Functions) != 2 {
		return fmt.Errorf("invalid VERT-012 HIR header")
	}
	if err := validateHIRProvenance(module.Provenance); err != nil {
		return err
	}
	digest, err := LogicalCapabilityRequirementsDigest(VERT012LogicalCapabilities())
	if err != nil || module.Provenance.LogicalCapabilityRequirementsDigest != digest || !slices.Equal(module.LogicalCapabilityRequirements, VERT012LogicalCapabilities()) {
		return fmt.Errorf("invalid VERT-012 capabilities")
	}
	if _, hash, err := CanonicalClosureContract(*module.Closures); err != nil || module.Closures.ContentHash != hash {
		return fmt.Errorf("invalid VERT-012 closure contract")
	}
	want, err := NewVERT012ClosureHIRUnchecked(module.Provenance, *module.Closures)
	if err != nil {
		return err
	}
	if !slices.EqualFunc(module.Functions, want.Functions, func(a, b HIRFunction) bool {
		x, _ := jsonx.Marshal(a)
		y, _ := jsonx.Marshal(b)
		return slices.Equal(x, y)
	}) {
		return fmt.Errorf("VERT-012 closure CFG or operation mismatch")
	}
	return nil
}

func NewVERT012ClosureHIRUnchecked(provenance HIRProvenance, contract ClosureContract) (HIRModule, error) {
	return fixedVERT012HIR(provenance, contract), nil
}

func CanonicalVERT012ClosureHIR(module HIRModule) ([]byte, string, error) {
	module.ContentHash = ""
	if err := VerifyVERT012ClosureHIR(module); err != nil {
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

func VerifyCanonicalVERT012ClosureHIR(module HIRModule) error {
	claimed := module.ContentHash
	_, want, err := CanonicalVERT012ClosureHIR(module)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("VERT-012 HIR content hash mismatch")
	}
	return nil
}
func DecodeVERT012ClosureHIR(data []byte) (*HIRModule, error) {
	var module HIRModule
	if err := jsonx.Unmarshal(data, &module, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	claimed := module.ContentHash
	_, want, err := CanonicalVERT012ClosureHIR(module)
	if err != nil {
		return nil, err
	}
	if claimed == "" || claimed != want {
		return nil, fmt.Errorf("VERT-012 HIR content hash mismatch")
	}
	return &module, nil
}
