package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const ClassAccessLayoutSchemaVersion uint32 = 1
const maxClassAccessLayoutBytes = 384 << 10

type ClassAccessLayoutContract struct {
	SchemaVersion        uint32               `json:"schemaVersion"`
	MIRHash              string               `json:"mirHash"`
	MIR                  ClassAccessMIRModule `json:"mir"`
	BaseClassID          uint32               `json:"baseClassId"`
	DerivedClassID       uint32               `json:"derivedClassId"`
	Base                 ObjectLayoutContract `json:"base"`
	Derived              ObjectLayoutContract `json:"derived"`
	BasePrefixFieldCount uint32               `json:"basePrefixFieldCount"`
	ContentHash          string               `json:"contentHash"`
}

func PlanClassAccessLayout(mir ClassAccessMIRModule, target ObjectLayoutTarget) (ClassAccessLayoutContract, error) {
	if err := VerifyCanonicalClassAccessMIR(mir); err != nil {
		return ClassAccessLayoutContract{}, err
	}
	if target.Triple != mir.Target.Triple || target.DataLayoutHash != mir.Target.LLVMDataLayoutHash {
		return ClassAccessLayoutContract{}, fmt.Errorf("OBJ-003b layout target does not match access MIR")
	}
	contract := mir.HIR.ClassAccess
	base, derived := contract.Classes[0], contract.Classes[1]
	fields := contractFields(*contract, base.ID)
	if len(fields) != 2 {
		return ClassAccessLayoutContract{}, fmt.Errorf("OBJ-003b base class requires exactly two fields")
	}
	baseInputs := make([]ObjectLayoutPropertyInput, 0, len(fields))
	for _, field := range fields {
		baseInputs = append(baseInputs, ObjectLayoutPropertyInput{Key: field.SymbolKey, Kind: ObjectPropertyData, Representation: "f64"})
	}
	baseLayout, err := PlanObjectLayout(base.InstanceTypeKey, target, baseInputs)
	if err != nil {
		return ClassAccessLayoutContract{}, err
	}
	derivedInputs := make([]ObjectLayoutPropertyInput, 0, len(fields))
	for _, field := range fields {
		derivedInputs = append(derivedInputs, ObjectLayoutPropertyInput{Key: field.SymbolKey, Kind: ObjectPropertyData, Representation: "f64"})
	}
	derivedLayout, err := PlanObjectLayout(derived.InstanceTypeKey, target, derivedInputs)
	if err != nil {
		return ClassAccessLayoutContract{}, err
	}
	result := ClassAccessLayoutContract{SchemaVersion: ClassAccessLayoutSchemaVersion, MIRHash: mir.ContentHash, MIR: mir, BaseClassID: base.ID, DerivedClassID: derived.ID, Base: baseLayout, Derived: derivedLayout, BasePrefixFieldCount: uint32(len(fields))}
	_, hash, err := CanonicalClassAccessLayout(result)
	if err != nil {
		return ClassAccessLayoutContract{}, err
	}
	result.ContentHash = hash
	return result, nil
}

func contractFields(contract ClassAccessContract, owner uint32) []ClassAccessMember {
	fields := make([]ClassAccessMember, 0)
	for _, member := range contract.Members {
		if member.OwnerClassID == owner && member.Kind == ClassAccessField {
			fields = append(fields, member)
		}
	}
	return fields
}

func VerifyClassAccessLayout(layout ClassAccessLayoutContract) error {
	if layout.SchemaVersion != ClassAccessLayoutSchemaVersion || layout.MIRHash != layout.MIR.ContentHash || layout.BaseClassID == 0 || layout.DerivedClassID == 0 || layout.BasePrefixFieldCount != 2 {
		return fmt.Errorf("invalid OBJ-003b layout envelope")
	}
	if err := VerifyCanonicalClassAccessMIR(layout.MIR); err != nil {
		return err
	}
	contract := layout.MIR.HIR.ClassAccess
	if layout.BaseClassID != contract.Classes[0].ID || layout.DerivedClassID != contract.Classes[1].ID || layout.Base.TypeKey != contract.Classes[0].InstanceTypeKey || layout.Derived.TypeKey != contract.Classes[1].InstanceTypeKey || layout.Base.Target != layout.Derived.Target {
		return fmt.Errorf("OBJ-003b layout class identity mismatch")
	}
	if err := verifyObjectLayoutContractHash(layout.Base); err != nil {
		return fmt.Errorf("OBJ-003b base layout: %w", err)
	}
	if err := verifyObjectLayoutContractHash(layout.Derived); err != nil {
		return fmt.Errorf("OBJ-003b derived layout: %w", err)
	}
	fields := contractFields(*contract, layout.BaseClassID)
	if len(fields) != 2 || len(layout.Base.Properties) != 2 || len(layout.Derived.Properties) != 2 {
		return fmt.Errorf("OBJ-003b layout field count mismatch")
	}
	for index, field := range fields {
		baseProperty, derivedProperty := layout.Base.Properties[index], layout.Derived.Properties[index]
		if baseProperty.Key != field.SymbolKey || derivedProperty.Key != field.SymbolKey || baseProperty.Kind != ObjectPropertyData || derivedProperty.Kind != ObjectPropertyData || baseProperty.Representation != "f64" || derivedProperty.Representation != "f64" || baseProperty.FieldOffset != derivedProperty.FieldOffset || baseProperty.FieldOffset == 0 {
			return fmt.Errorf("OBJ-003b layout prefix field %d mismatch", index+1)
		}
	}
	if !slices.Equal(layout.Base.TraceOffsets, []uint32{}) || !slices.Equal(layout.Derived.TraceOffsets, []uint32{}) {
		return fmt.Errorf("OBJ-003b access layout unexpectedly traces scalar fields")
	}
	return nil
}

func CanonicalClassAccessLayout(layout ClassAccessLayoutContract) ([]byte, string, error) {
	layout.ContentHash = ""
	if err := VerifyClassAccessLayout(layout); err != nil {
		return nil, "", err
	}
	encoded, err := jsonx.Marshal(layout)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	layout.ContentHash = hex.EncodeToString(digest[:])
	encoded, err = jsonx.Marshal(layout)
	return encoded, layout.ContentHash, err
}

func VerifyCanonicalClassAccessLayout(layout ClassAccessLayoutContract) error {
	claimed := layout.ContentHash
	_, want, err := CanonicalClassAccessLayout(layout)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("OBJ-003b layout content hash mismatch")
	}
	return nil
}

func DecodeClassAccessLayout(data []byte) (*ClassAccessLayoutContract, error) {
	if len(data) > maxClassAccessLayoutBytes {
		return nil, fmt.Errorf("OBJ-003b layout exceeds %d bytes", maxClassAccessLayoutBytes)
	}
	var layout ClassAccessLayoutContract
	if err := jsonx.Unmarshal(data, &layout, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	if err := VerifyCanonicalClassAccessLayout(layout); err != nil {
		return nil, err
	}
	return &layout, nil
}
