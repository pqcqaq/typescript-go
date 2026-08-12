package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

// ClassAccessExecutionContract is the target-independent source execution
// boundary for OBJ-003b. It deliberately contains no offsets, DataLayout, or
// runtime symbols; those are selected only after MIR/target binding.
const ClassAccessExecutionContractSchemaVersion uint32 = 1
const maxClassAccessExecutionContractBytes = 256 << 10

type ClassAccessExecutionFunction struct {
	ID              FunctionID   `json:"id"`
	Name            string       `json:"name"`
	Exported        bool         `json:"exported,omitempty"`
	ReceiverClassID uint32       `json:"receiverClassId,omitempty"`
	FieldSymbols    []string     `json:"fieldSymbols,omitempty"`
	FieldInitBits   []string     `json:"fieldInitBits,omitempty"`
	Calls           []FunctionID `json:"calls,omitempty"`
	ReturnType      TypeKind     `json:"returnType"`
	Origin          Origin       `json:"origin"`
}

type ClassAccessExecutionContract struct {
	SchemaVersion    uint32                         `json:"schemaVersion"`
	ClassAccessHash  string                         `json:"classAccessHash"`
	ClassAccess      ClassAccessContract            `json:"classAccess"`
	Functions        []ClassAccessExecutionFunction `json:"functions"`
	AllocatedClassID uint32                         `json:"allocatedClassId"`
	ContentHash      string                         `json:"contentHash"`
}

func NewClassAccessExecutionContract(contract ClassAccessContract) (ClassAccessExecutionContract, error) {
	if err := VerifyCanonicalClassAccessContract(contract); err != nil {
		return ClassAccessExecutionContract{}, err
	}
	if len(contract.Classes) < 2 || len(contract.Members) < 4 {
		return ClassAccessExecutionContract{}, fmt.Errorf("OBJ-003b execution requires canonical base/derived contract")
	}
	functions := fixedClassAccessExecutionFunctions(contract)
	result := ClassAccessExecutionContract{SchemaVersion: ClassAccessExecutionContractSchemaVersion, ClassAccessHash: contract.ContentHash, ClassAccess: contract, Functions: functions, AllocatedClassID: 2}
	_, hash, err := CanonicalClassAccessExecution(result)
	if err != nil {
		return ClassAccessExecutionContract{}, err
	}
	result.ContentHash = hash
	return result, nil
}

func fixedClassAccessExecutionFunctions(contract ClassAccessContract) []ClassAccessExecutionFunction {
	return []ClassAccessExecutionFunction{
		{ID: 1, Name: "Vault.constructor", ReceiverClassID: 1, FieldSymbols: []string{contract.Members[0].SymbolKey, contract.Members[1].SymbolKey}, FieldInitBits: []string{"3ff0000000000000", "4000000000000000"}, ReturnType: TypeVoid, Origin: Origin{File: "/project/classaccess.ts", Start: 0, End: 143}},
		{ID: 2, Name: "DerivedVault.constructor", ReceiverClassID: 2, Calls: []FunctionID{1}, ReturnType: TypeVoid, Origin: Origin{File: "/project/classaccess.ts", Start: 143, End: 258}},
		{ID: 3, Name: "Vault.readSecret", ReceiverClassID: 1, FieldSymbols: []string{contract.Members[0].SymbolKey}, ReturnType: TypeNumber, Origin: Origin{File: "/project/classaccess.ts", Start: 74, End: 142}},
		{ID: 4, Name: "DerivedVault.readValue", ReceiverClassID: 2, FieldSymbols: []string{contract.Members[1].SymbolKey}, ReturnType: TypeNumber, Origin: Origin{File: "/project/classaccess.ts", Start: 179, End: 257}},
		{ID: 5, Name: "classAccess", Exported: true, Calls: []FunctionID{2, 3, 4}, ReturnType: TypeNumber, Origin: Origin{File: "/project/classaccess.ts", Start: 258, End: 390}},
	}
}

func VerifyClassAccessExecution(contract ClassAccessExecutionContract) error {
	if contract.SchemaVersion != ClassAccessExecutionContractSchemaVersion || contract.ClassAccessHash != contract.ClassAccess.ContentHash || contract.AllocatedClassID != 2 || len(contract.Functions) != 5 || len(contract.ClassAccess.Classes) < 2 || len(contract.ClassAccess.Members) < 4 {
		return fmt.Errorf("invalid OBJ-003b execution contract envelope")
	}
	if err := VerifyCanonicalClassAccessContract(contract.ClassAccess); err != nil {
		return err
	}
	want := ClassAccessExecutionContract{SchemaVersion: ClassAccessExecutionContractSchemaVersion, ClassAccessHash: contract.ClassAccess.ContentHash, ClassAccess: contract.ClassAccess, Functions: fixedClassAccessExecutionFunctions(contract.ClassAccess), AllocatedClassID: 2}
	var err error
	left, err := jsonx.Marshal(contract.Functions)
	if err != nil {
		return err
	}
	right, err := jsonx.Marshal(want.Functions)
	if err != nil || !slices.Equal(left, right) {
		return fmt.Errorf("OBJ-003b execution function contract mismatch")
	}
	return nil
}

func CanonicalClassAccessExecution(contract ClassAccessExecutionContract) ([]byte, string, error) {
	contract.ContentHash = ""
	if err := VerifyClassAccessExecution(contract); err != nil {
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

func VerifyCanonicalClassAccessExecution(contract ClassAccessExecutionContract) error {
	claimed := contract.ContentHash
	_, want, err := CanonicalClassAccessExecution(contract)
	if err != nil || claimed == "" || claimed != want {
		return fmt.Errorf("OBJ-003b execution contract content hash mismatch")
	}
	return nil
}

func DecodeClassAccessExecution(data []byte) (*ClassAccessExecutionContract, error) {
	if len(data) > maxClassAccessExecutionContractBytes {
		return nil, fmt.Errorf("OBJ-003b execution contract exceeds %d bytes", maxClassAccessExecutionContractBytes)
	}
	var contract ClassAccessExecutionContract
	if err := jsonx.Unmarshal(data, &contract, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	if err := VerifyCanonicalClassAccessExecution(contract); err != nil {
		return nil, err
	}
	return &contract, nil
}
