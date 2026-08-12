package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const VERT013bClassContractSchemaVersion uint32 = 2
const maxVERT013bClassContractBytes = 96 << 10

type VERT013bSuperCall struct {
	BaseClassID uint32   `json:"baseClassId"`
	Callee      string   `json:"callee"`
	Arguments   []string `json:"arguments"`
	SourceOrder uint32   `json:"sourceOrder"`
}

type VERT013bClass struct {
	ID              uint32                    `json:"id"`
	SymbolKey       string                    `json:"symbolKey"`
	InstanceTypeKey string                    `json:"instanceTypeKey"`
	BaseClassID     uint32                    `json:"baseClassId,omitempty"`
	Constructor     ClassConstructorContract  `json:"constructor"`
	Fields          []ClassFieldContract      `json:"fields"`
	Methods         []ClassMethodContract     `json:"methods"`
	Super           *VERT013bSuperCall        `json:"super,omitempty"`
	Initialization  []ClassInitializationStep `json:"initialization"`
}

type VERT013bClassContract struct {
	SchemaVersion uint32          `json:"schemaVersion"`
	Classes       []VERT013bClass `json:"classes"`
	ContentHash   string          `json:"contentHash"`
}

func VerifyVERT013bClassContract(contract VERT013bClassContract) error {
	if contract.SchemaVersion != VERT013bClassContractSchemaVersion || len(contract.Classes) != 2 {
		return fmt.Errorf("invalid VERT-013b class contract header")
	}
	base, derived := contract.Classes[0], contract.Classes[1]
	if base.ID != 1 || derived.ID != 2 || base.BaseClassID != 0 || derived.BaseClassID != base.ID || base.SymbolKey == "" || derived.SymbolKey == "" || base.SymbolKey == derived.SymbolKey || !validSHA256Hex(base.InstanceTypeKey) || !validSHA256Hex(derived.InstanceTypeKey) || base.InstanceTypeKey == derived.InstanceTypeKey {
		return fmt.Errorf("invalid VERT-013b nominal class identity")
	}
	if base.Constructor.SymbolKey == "" || base.Constructor.Derived || !base.Constructor.AllocatesReceiver || !base.Constructor.ReturnsOwnReceiver || base.Constructor.Signature != "cdecl(ptr,f64)->void" || derived.Constructor.SymbolKey == "" || !derived.Constructor.Derived || derived.Constructor.AllocatesReceiver || !derived.Constructor.ReturnsOwnReceiver || derived.Constructor.Signature != "cdecl(ptr,f64,f64)->void" {
		return fmt.Errorf("invalid VERT-013b constructor ABI")
	}
	if base.Super != nil || derived.Super == nil || derived.Super.BaseClassID != base.ID || derived.Super.Callee != base.Constructor.SymbolKey || !slices.Equal(derived.Super.Arguments, []string{"start"}) || derived.Super.SourceOrder != 1 {
		return fmt.Errorf("invalid VERT-013b super call")
	}
	if len(base.Fields) != 1 || !validVERT013bField(base.Fields[0], "value", 1) || len(derived.Fields) != 1 || !validVERT013bField(derived.Fields[0], "step", 1) {
		return fmt.Errorf("invalid VERT-013b field set")
	}
	for _, class := range contract.Classes {
		if len(class.Methods) != 1 || class.Methods[0].ID != 1 || class.Methods[0].SymbolKey == "" || class.Methods[0].Name != "increment" || class.Methods[0].Signature != "cdecl(ptr)->f64" || class.Methods[0].Visibility != "public" || !class.Methods[0].RequiresReceiver || class.Methods[0].Static || class.Methods[0].SourceOrder != 3 {
			return fmt.Errorf("invalid VERT-013b method set")
		}
	}
	wantBase := []ClassInitializationStep{{ID: 1, Kind: ClassInitAllocate}, {ID: 2, Kind: ClassInitField, FieldID: 1, SourceOrder: 1}, {ID: 3, Kind: ClassInitBody, SourceOrder: 2}}
	wantDerived := []ClassInitializationStep{{ID: 1, Kind: ClassInitAllocate}, {ID: 2, Kind: ClassInitBody, SourceOrder: 1}, {ID: 3, Kind: ClassInitField, FieldID: 1, SourceOrder: 2}, {ID: 4, Kind: ClassInitBody, SourceOrder: 3}}
	if !slices.Equal(base.Initialization, wantBase) || !slices.Equal(derived.Initialization, wantDerived) {
		return fmt.Errorf("invalid VERT-013b initialization order")
	}
	return nil
}

func validVERT013bField(field ClassFieldContract, name string, order uint32) bool {
	return field.ID == 1 && field.SymbolKey != "" && field.Name == name && field.Type == TypeNumber && field.Visibility == "public" && field.Mutable && !field.Static && field.Storage == ClassFieldInstanceSlot && field.SourceOrder == order
}

func CanonicalVERT013bClassContract(contract VERT013bClassContract) ([]byte, string, error) {
	contract.ContentHash = ""
	if err := VerifyVERT013bClassContract(contract); err != nil {
		return nil, "", err
	}
	encoded, err := jsonx.Marshal(contract)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	contract.ContentHash = hex.EncodeToString(digest[:])
	encoded, err = jsonx.Marshal(contract)
	return encoded, contract.ContentHash, err
}

func VerifyCanonicalVERT013bClassContract(contract VERT013bClassContract) error {
	claimed := contract.ContentHash
	_, want, err := CanonicalVERT013bClassContract(contract)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("VERT-013b class contract content hash mismatch")
	}
	return nil
}

func DecodeVERT013bClassContract(data []byte) (*VERT013bClassContract, error) {
	if len(data) > maxVERT013bClassContractBytes {
		return nil, fmt.Errorf("VERT-013b class contract exceeds %d bytes", maxVERT013bClassContractBytes)
	}
	var contract VERT013bClassContract
	if err := jsonx.Unmarshal(data, &contract, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	if err := VerifyCanonicalVERT013bClassContract(contract); err != nil {
		return nil, err
	}
	return &contract, nil
}
