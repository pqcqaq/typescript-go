package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const ClassAccessHIRSchemaVersion uint32 = 15
const maxClassAccessHIRBytes = 128 << 10

func ClassAccessLogicalCapabilities() []RuntimeCapabilityID { return VERT013bLogicalCapabilities() }

func fixedClassAccessHIR(provenance HIRProvenance, contract ClassAccessContract) HIRModule {
	origin := func(start, end int) Origin { return Origin{File: "/project/classaccess.ts", Start: start, End: end} }
	proof := func(id uint32, symbol string, request ClassAccessRequest, start, end int) HIRClassAccessProof {
		decision, _ := PlanClassMemberAccess(contract, request)
		return HIRClassAccessProof{ID: id, MemberSymbolKey: symbol, Request: request, Decision: decision, Origin: origin(start, end)}
	}
	execution, _ := NewClassAccessExecutionContract(contract)
	return HIRModule{
		SchemaVersion:                 ClassAccessHIRSchemaVersion,
		Provenance:                    provenance,
		LogicalCapabilityRequirements: ClassAccessLogicalCapabilities(),
		ClassAccess:                   &contract,
		ClassAccessExecution:          &execution,
		ClassAccessProofs: []HIRClassAccessProof{
			proof(1, contract.Members[0].SymbolKey, ClassAccessRequest{AccessingClassID: 1, ReceiverClassID: 1, MemberID: 1, PrivateIdentity: contract.Members[0].PrivateIdentity}, 123, 135),
			proof(2, contract.Members[1].SymbolKey, ClassAccessRequest{AccessingClassID: 2, ReceiverClassID: 2, MemberID: 2}, 233, 244),
			proof(3, contract.Members[2].SymbolKey, ClassAccessRequest{ReceiverClassID: 2, MemberID: 3}, 338, 354),
			proof(4, contract.Members[3].SymbolKey, ClassAccessRequest{ReceiverClassID: 2, MemberID: 4}, 364, 379),
		},
		Functions: fixedClassAccessHIRFunctions(contract),
	}
}

func fixedClassAccessHIRFunctions(contract ClassAccessContract) []HIRFunction {
	empty := []RuntimeCapabilityID{}
	baseType, derivedType := contract.Classes[0].InstanceTypeKey, contract.Classes[1].InstanceTypeKey
	secret, value := contract.Members[0].SymbolKey, contract.Members[1].SymbolKey
	readSecret, readValue := contract.Members[2].SymbolKey, contract.Members[3].SymbolKey
	o := func(start, end int) Origin { return Origin{File: "/project/classaccess.ts", Start: start, End: end} }
	baseOrigin, derivedOrigin, secretOrigin, valueOrigin, entryOrigin := o(0, 143), o(143, 258), o(74, 142), o(179, 257), o(258, 390)
	return []HIRFunction{
		{ID: 1, Name: "Vault.constructor", Parameters: []HIRParameter{{Name: "receiver", Value: 1, Type: TypeObject, Origin: baseOrigin}}, ReturnType: TypeObject, Origin: baseOrigin, Blocks: []HIRBlock{{ID: 1, Operations: []HIROp{
			{ID: 2, Kind: "number.constant", Type: TypeNumber, NumberBits: "3ff0000000000000", Effect: EffectPure, Effects: []Effect{EffectPure}, LogicalCapabilityRequirements: empty, Origin: baseOrigin},
			{ID: 3, Kind: "class.field.init", Type: TypeNumber, Operands: []ValueID{1, 2}, ObjectTypeKey: baseType, PropertySymbolKey: secret, Effect: EffectWrite, Effects: []Effect{EffectWrite}, LogicalCapabilityRequirements: empty, Origin: baseOrigin},
			{ID: 4, Kind: "number.constant", Type: TypeNumber, NumberBits: "4000000000000000", Effect: EffectPure, Effects: []Effect{EffectPure}, LogicalCapabilityRequirements: empty, Origin: baseOrigin},
			{ID: 5, Kind: "class.field.init", Type: TypeNumber, Operands: []ValueID{1, 4}, ObjectTypeKey: baseType, PropertySymbolKey: value, Effect: EffectWrite, Effects: []Effect{EffectWrite}, LogicalCapabilityRequirements: empty, Origin: baseOrigin},
		}, Terminator: HIRTerminator{Kind: "return", Value: 1, Origin: baseOrigin}}}},
		{ID: 2, Name: "DerivedVault.constructor", ReturnType: TypeObject, Origin: derivedOrigin, Blocks: []HIRBlock{{ID: 1, Operations: []HIROp{
			{ID: 1, Kind: "class.alloc", Type: TypeObject, ObjectTypeKey: derivedType, Effect: EffectAllocate, Effects: []Effect{EffectAllocate}, LogicalCapabilityRequirements: []RuntimeCapabilityID{"rt.gc.alloc"}, Origin: derivedOrigin},
			{ID: 2, Kind: "call.super", Type: TypeObject, Operands: []ValueID{1}, Callee: 1, ObjectTypeKey: baseType, Effect: EffectCall, Effects: []Effect{EffectCall, EffectWrite}, LogicalCapabilityRequirements: empty, Origin: derivedOrigin},
		}, Terminator: HIRTerminator{Kind: "return", Value: 1, Origin: derivedOrigin}}}},
		{ID: 3, Name: "Vault.readSecret", Parameters: []HIRParameter{{Name: "receiver", Value: 1, Type: TypeObject, Origin: secretOrigin}, {Name: "other", Value: 2, Type: TypeObject, Origin: secretOrigin}}, ReturnType: TypeNumber, Origin: secretOrigin, Blocks: []HIRBlock{{ID: 1, Operations: []HIROp{{ID: 3, Kind: "class.field.load.authorized", Type: TypeNumber, Operands: []ValueID{2}, ObjectTypeKey: baseType, PropertySymbolKey: secret, Effect: EffectRead, Effects: []Effect{EffectRead}, LogicalCapabilityRequirements: empty, Origin: secretOrigin}}, Terminator: HIRTerminator{Kind: "return", Value: 3, Origin: secretOrigin}}}},
		{ID: 4, Name: "DerivedVault.readValue", Parameters: []HIRParameter{{Name: "receiver", Value: 1, Type: TypeObject, Origin: valueOrigin}, {Name: "other", Value: 2, Type: TypeObject, Origin: valueOrigin}}, ReturnType: TypeNumber, Origin: valueOrigin, Blocks: []HIRBlock{{ID: 1, Operations: []HIROp{{ID: 3, Kind: "class.field.load.authorized", Type: TypeNumber, Operands: []ValueID{2}, ObjectTypeKey: derivedType, PropertySymbolKey: value, Effect: EffectRead, Effects: []Effect{EffectRead}, LogicalCapabilityRequirements: empty, Origin: valueOrigin}}, Terminator: HIRTerminator{Kind: "return", Value: 3, Origin: valueOrigin}}}},
		{ID: 5, Name: "classAccess", Exported: true, ReturnType: TypeNumber, Origin: entryOrigin, Blocks: []HIRBlock{{ID: 1, Operations: []HIROp{
			{ID: 1, Kind: "call.constructor", Type: TypeObject, Callee: 2, ObjectTypeKey: derivedType, Effect: EffectCall, Effects: []Effect{EffectCall, EffectAllocate, EffectWrite}, LogicalCapabilityRequirements: []RuntimeCapabilityID{"rt.gc.alloc"}, Origin: entryOrigin},
			{ID: 2, Kind: "call.method.authorized", Type: TypeNumber, Operands: []ValueID{1, 1}, Callee: 3, ObjectTypeKey: derivedType, PropertySymbolKey: readSecret, Effect: EffectCall, Effects: []Effect{EffectCall, EffectRead}, LogicalCapabilityRequirements: empty, Origin: entryOrigin},
			{ID: 3, Kind: "call.method.authorized", Type: TypeNumber, Operands: []ValueID{1, 1}, Callee: 4, ObjectTypeKey: derivedType, PropertySymbolKey: readValue, Effect: EffectCall, Effects: []Effect{EffectCall, EffectRead}, LogicalCapabilityRequirements: empty, Origin: entryOrigin},
			{ID: 4, Kind: "binary", Type: TypeNumber, Operands: []ValueID{2, 3}, Operator: "+", Effect: EffectPure, Effects: []Effect{EffectPure}, LogicalCapabilityRequirements: empty, Origin: entryOrigin},
		}, Terminator: HIRTerminator{Kind: "return", Value: 4, Origin: entryOrigin}}}},
	}
}

func NewClassAccessHIR(provenance HIRProvenance, contract ClassAccessContract) (HIRModule, error) {
	if err := VerifyCanonicalClassAccessContract(contract); err != nil {
		return HIRModule{}, err
	}
	if len(contract.Classes) < 2 || len(contract.Members) != 4 {
		return HIRModule{}, fmt.Errorf("OBJ-003b access HIR requires two classes and four members")
	}
	module := fixedClassAccessHIR(provenance, contract)
	if err := VerifyClassAccessHIR(module); err != nil {
		return HIRModule{}, err
	}
	_, hash, err := CanonicalClassAccessHIR(module)
	if err != nil {
		return HIRModule{}, err
	}
	module.ContentHash = hash
	return module, nil
}

func VerifyClassAccessHIR(module HIRModule) error {
	if module.SchemaVersion != ClassAccessHIRSchemaVersion || module.ClassAccess == nil || module.ClassAccessExecution == nil || module.Classes != nil || module.DerivedClasses != nil || module.ObjectTypes != nil || module.PlaceRefs != nil || module.Closures != nil || len(module.Functions) != 5 || len(module.ClassAccessProofs) != 4 {
		return fmt.Errorf("invalid OBJ-003b access HIR header")
	}
	if err := validateHIRProvenance(module.Provenance); err != nil {
		return err
	}
	if err := VerifyCanonicalClassAccessContract(*module.ClassAccess); err != nil {
		return err
	}
	if err := VerifyCanonicalClassAccessExecution(*module.ClassAccessExecution); err != nil || module.ClassAccessExecution.ClassAccessHash != module.ClassAccess.ContentHash {
		return fmt.Errorf("invalid OBJ-003b execution HIR binding")
	}
	digest, err := LogicalCapabilityRequirementsDigest(ClassAccessLogicalCapabilities())
	if err != nil || module.Provenance.LogicalCapabilityRequirementsDigest != digest || !slices.Equal(module.LogicalCapabilityRequirements, ClassAccessLogicalCapabilities()) {
		return fmt.Errorf("invalid OBJ-003b access HIR capabilities")
	}
	want := fixedClassAccessHIR(module.Provenance, *module.ClassAccess)
	left, err := jsonx.Marshal(module.ClassAccessProofs)
	if err != nil {
		return err
	}
	right, err := jsonx.Marshal(want.ClassAccessProofs)
	if err != nil {
		return err
	}
	if !slices.Equal(left, right) {
		return fmt.Errorf("OBJ-003b access HIR proof mismatch")
	}
	left, err = jsonx.Marshal(module.Functions)
	if err != nil {
		return err
	}
	right, err = jsonx.Marshal(want.Functions)
	if err != nil {
		return err
	}
	if !slices.Equal(left, right) {
		return fmt.Errorf("OBJ-003b execution HIR CFG mismatch")
	}
	for _, proof := range module.ClassAccessProofs {
		if proof.ID == 0 || int(proof.Request.MemberID) > len(module.ClassAccess.Members) || module.ClassAccess.Members[proof.Request.MemberID-1].SymbolKey != proof.MemberSymbolKey {
			return fmt.Errorf("OBJ-003b access HIR member binding mismatch")
		}
		decision, err := PlanClassMemberAccess(*module.ClassAccess, proof.Request)
		if err != nil || decision != proof.Decision || !decision.Allowed {
			return fmt.Errorf("OBJ-003b access HIR authorization mismatch")
		}
	}
	return nil
}

func CanonicalClassAccessHIR(module HIRModule) ([]byte, string, error) {
	module.ContentHash = ""
	if err := VerifyClassAccessHIR(module); err != nil {
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

func VerifyCanonicalClassAccessHIR(module HIRModule) error {
	claimed := module.ContentHash
	_, want, err := CanonicalClassAccessHIR(module)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("OBJ-003b access HIR content hash mismatch")
	}
	return nil
}

func DecodeClassAccessHIR(data []byte) (*HIRModule, error) {
	if len(data) > maxClassAccessHIRBytes {
		return nil, fmt.Errorf("OBJ-003b access HIR exceeds %d bytes", maxClassAccessHIRBytes)
	}
	var module HIRModule
	if err := jsonx.Unmarshal(data, &module, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	if err := VerifyCanonicalClassAccessHIR(module); err != nil {
		return nil, err
	}
	return &module, nil
}
