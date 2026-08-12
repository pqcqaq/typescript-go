package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const VERT013bLayoutSchemaVersion uint32 = 1
const maxVERT013bLayoutBytes = 192 << 10

type VERT013bLayoutContract struct {
	SchemaVersion           uint32               `json:"schemaVersion"`
	ClassContractHash       string               `json:"classContractHash"`
	BaseClassID             uint32               `json:"baseClassId"`
	DerivedClassID          uint32               `json:"derivedClassId"`
	Base                    ObjectLayoutContract `json:"base"`
	Derived                 ObjectLayoutContract `json:"derived"`
	BasePrefixPropertyCount uint32               `json:"basePrefixPropertyCount"`
	ContentHash             string               `json:"contentHash"`
}

func PlanVERT013bLayout(contract VERT013bClassContract, target ObjectLayoutTarget) (VERT013bLayoutContract, error) {
	if err := VerifyCanonicalVERT013bClassContract(contract); err != nil {
		return VERT013bLayoutContract{}, err
	}
	base, derived := contract.Classes[0], contract.Classes[1]
	baseLayout, err := PlanObjectLayout(base.InstanceTypeKey, target, []ObjectLayoutPropertyInput{{Key: base.Fields[0].SymbolKey, Kind: ObjectPropertyData, Representation: "f64"}})
	if err != nil {
		return VERT013bLayoutContract{}, err
	}
	derivedLayout, err := PlanObjectLayout(derived.InstanceTypeKey, target, []ObjectLayoutPropertyInput{
		{Key: base.Fields[0].SymbolKey, Kind: ObjectPropertyData, Representation: "f64"},
		{Key: derived.Fields[0].SymbolKey, Kind: ObjectPropertyData, Representation: "f64"},
	})
	if err != nil {
		return VERT013bLayoutContract{}, err
	}
	result := VERT013bLayoutContract{SchemaVersion: VERT013bLayoutSchemaVersion, ClassContractHash: contract.ContentHash, BaseClassID: base.ID, DerivedClassID: derived.ID, Base: baseLayout, Derived: derivedLayout, BasePrefixPropertyCount: 1}
	_, hash, err := CanonicalVERT013bLayout(result, contract)
	if err != nil {
		return VERT013bLayoutContract{}, err
	}
	result.ContentHash = hash
	return result, nil
}

func VerifyVERT013bLayout(layout VERT013bLayoutContract, contract VERT013bClassContract) error {
	if err := VerifyCanonicalVERT013bClassContract(contract); err != nil {
		return err
	}
	base, derived := contract.Classes[0], contract.Classes[1]
	if layout.SchemaVersion != VERT013bLayoutSchemaVersion || layout.ClassContractHash != contract.ContentHash || layout.BaseClassID != base.ID || layout.DerivedClassID != derived.ID || layout.BasePrefixPropertyCount != 1 {
		return fmt.Errorf("invalid VERT-013b layout envelope")
	}
	if err := verifyObjectLayoutContractHash(layout.Base); err != nil {
		return fmt.Errorf("VERT-013b base layout: %w", err)
	}
	if err := verifyObjectLayoutContractHash(layout.Derived); err != nil {
		return fmt.Errorf("VERT-013b derived layout: %w", err)
	}
	if layout.Base.TypeKey != base.InstanceTypeKey || layout.Derived.TypeKey != derived.InstanceTypeKey || layout.Base.Target != layout.Derived.Target {
		return fmt.Errorf("VERT-013b layout class or target mismatch")
	}
	if len(layout.Base.Properties) != 1 || len(layout.Derived.Properties) != 2 || layout.Base.Properties[0].Key != base.Fields[0].SymbolKey || layout.Derived.Properties[0].Key != base.Fields[0].SymbolKey || layout.Derived.Properties[1].Key != derived.Fields[0].SymbolKey {
		return fmt.Errorf("VERT-013b layout field identity mismatch")
	}
	baseProperty := layout.Base.Properties[0]
	derivedPrefix := layout.Derived.Properties[0]
	if baseProperty.Kind != ObjectPropertyData || baseProperty.Representation != "f64" || baseProperty.PresenceBit != -1 || derivedPrefix.Kind != baseProperty.Kind || derivedPrefix.Representation != baseProperty.Representation || derivedPrefix.PresenceBit != baseProperty.PresenceBit || derivedPrefix.FieldOffset != baseProperty.FieldOffset {
		return fmt.Errorf("VERT-013b base layout is not an exact derived prefix")
	}
	suffix := layout.Derived.Properties[1]
	if suffix.Kind != ObjectPropertyData || suffix.Representation != "f64" || suffix.PresenceBit != -1 || suffix.FieldOffset <= derivedPrefix.FieldOffset || len(layout.Base.TraceOffsets) != 0 || len(layout.Derived.TraceOffsets) != 0 {
		return fmt.Errorf("VERT-013b derived suffix or trace layout mismatch")
	}
	return nil
}

func CanonicalVERT013bLayout(layout VERT013bLayoutContract, contract VERT013bClassContract) ([]byte, string, error) {
	layout.ContentHash = ""
	if err := VerifyVERT013bLayout(layout, contract); err != nil {
		return nil, "", err
	}
	encoded, err := jsonx.Marshal(layout)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	layout.ContentHash = hash
	encoded, err = jsonx.Marshal(layout)
	return encoded, hash, err
}

func VerifyCanonicalVERT013bLayout(layout VERT013bLayoutContract, contract VERT013bClassContract) error {
	claimed := layout.ContentHash
	_, want, err := CanonicalVERT013bLayout(layout, contract)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("VERT-013b layout content hash mismatch")
	}
	return nil
}

func DecodeVERT013bLayout(data []byte, contract VERT013bClassContract) (*VERT013bLayoutContract, error) {
	if len(data) > maxVERT013bLayoutBytes {
		return nil, fmt.Errorf("VERT-013b layout exceeds %d bytes", maxVERT013bLayoutBytes)
	}
	var layout VERT013bLayoutContract
	if err := jsonx.Unmarshal(data, &layout, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	if err := VerifyCanonicalVERT013bLayout(layout, contract); err != nil {
		return nil, err
	}
	return &layout, nil
}
