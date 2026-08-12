package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const ClassContractSchemaVersion uint32 = 1
const maxClassContractBytes = 64 << 10

type ClassFieldStorage string
type ClassInitializationKind string

const (
	ClassFieldInstanceSlot ClassFieldStorage       = "instance-slot"
	ClassInitAllocate      ClassInitializationKind = "allocate-receiver"
	ClassInitField         ClassInitializationKind = "initialize-field"
	ClassInitBody          ClassInitializationKind = "constructor-body"
)

type ClassFieldContract struct {
	ID          uint32            `json:"id"`
	SymbolKey   string            `json:"symbolKey"`
	Name        string            `json:"name"`
	Type        TypeKind          `json:"type"`
	Visibility  string            `json:"visibility"`
	Mutable     bool              `json:"mutable"`
	Static      bool              `json:"static"`
	Storage     ClassFieldStorage `json:"storage"`
	SourceOrder uint32            `json:"sourceOrder"`
}

type ClassMethodContract struct {
	ID               uint32 `json:"id"`
	SymbolKey        string `json:"symbolKey"`
	Name             string `json:"name"`
	Signature        string `json:"signature"`
	Visibility       string `json:"visibility"`
	Static           bool   `json:"static"`
	RequiresReceiver bool   `json:"requiresReceiver"`
	SourceOrder      uint32 `json:"sourceOrder"`
}

type ClassConstructorContract struct {
	SymbolKey          string `json:"symbolKey"`
	Signature          string `json:"signature"`
	Derived            bool   `json:"derived"`
	AllocatesReceiver  bool   `json:"allocatesReceiver"`
	ReturnsOwnReceiver bool   `json:"returnsOwnReceiver"`
}

type ClassInitializationStep struct {
	ID          uint32                  `json:"id"`
	Kind        ClassInitializationKind `json:"kind"`
	FieldID     uint32                  `json:"fieldId,omitempty"`
	SourceOrder uint32                  `json:"sourceOrder"`
}

type ClassDeclarationContract struct {
	ID              uint32                    `json:"id"`
	SymbolKey       string                    `json:"symbolKey"`
	InstanceTypeKey string                    `json:"instanceTypeKey"`
	BaseClassID     uint32                    `json:"baseClassId,omitempty"`
	Constructor     ClassConstructorContract  `json:"constructor"`
	Fields          []ClassFieldContract      `json:"fields"`
	Methods         []ClassMethodContract     `json:"methods"`
	Initialization  []ClassInitializationStep `json:"initialization"`
}

type ClassContract struct {
	SchemaVersion uint32                     `json:"schemaVersion"`
	Classes       []ClassDeclarationContract `json:"classes"`
	ContentHash   string                     `json:"contentHash"`
}

func VerifyClassContract(contract ClassContract) error {
	if contract.SchemaVersion != ClassContractSchemaVersion || len(contract.Classes) != 1 {
		return fmt.Errorf("invalid class contract header")
	}
	class := contract.Classes[0]
	if class.ID != 1 || class.SymbolKey == "" || !validSHA256Hex(class.InstanceTypeKey) || class.BaseClassID != 0 {
		return fmt.Errorf("invalid base class identity")
	}
	constructor := class.Constructor
	if constructor.SymbolKey == "" || constructor.Signature != "cdecl(ptr,f64)->void" || constructor.Derived || !constructor.AllocatesReceiver || !constructor.ReturnsOwnReceiver {
		return fmt.Errorf("invalid base class constructor")
	}
	if len(class.Fields) != 1 || len(class.Methods) != 1 || len(class.Initialization) != 3 {
		return fmt.Errorf("unsupported VERT-013a class shape")
	}
	field := class.Fields[0]
	if field.ID != 1 || field.SymbolKey == "" || field.Name != "value" || field.Type != TypeNumber || field.Visibility != "public" || !field.Mutable || field.Static || field.Storage != ClassFieldInstanceSlot || field.SourceOrder != 1 {
		return fmt.Errorf("invalid VERT-013a instance field")
	}
	method := class.Methods[0]
	if method.ID != 1 || method.SymbolKey == "" || method.Name != "increment" || method.Signature != "cdecl(ptr)->f64" || method.Visibility != "public" || method.Static || !method.RequiresReceiver || method.SourceOrder != 3 {
		return fmt.Errorf("invalid VERT-013a instance method")
	}
	want := []ClassInitializationStep{
		{ID: 1, Kind: ClassInitAllocate, SourceOrder: 0},
		{ID: 2, Kind: ClassInitField, FieldID: field.ID, SourceOrder: field.SourceOrder},
		{ID: 3, Kind: ClassInitBody, SourceOrder: 2},
	}
	for index, step := range class.Initialization {
		if step != want[index] {
			return fmt.Errorf("invalid class initialization step %d", index+1)
		}
	}
	return nil
}

func CanonicalClassContract(contract ClassContract) ([]byte, string, error) {
	contract.ContentHash = ""
	if err := VerifyClassContract(contract); err != nil {
		return nil, "", err
	}
	encoded, err := jsonx.Marshal(contract)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	contract.ContentHash = hash
	encoded, err = jsonx.Marshal(contract)
	return encoded, hash, err
}

func DecodeClassContract(data []byte) (*ClassContract, error) {
	if len(data) > maxClassContractBytes {
		return nil, fmt.Errorf("class contract exceeds %d bytes", maxClassContractBytes)
	}
	var contract ClassContract
	if err := jsonx.Unmarshal(data, &contract, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode class contract: %w", err)
	}
	claimed := contract.ContentHash
	_, want, err := CanonicalClassContract(contract)
	if err != nil {
		return nil, err
	}
	if claimed == "" || claimed != want {
		return nil, fmt.Errorf("class contract content hash mismatch")
	}
	return &contract, nil
}
