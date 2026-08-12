package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const DynamicValueABISchemaVersion uint32 = 1
const maxDynamicValueABIBytes = 64 << 10

const (
	DynamicPropertyLoadCapability  RuntimeCapabilityID = "rt.dynamic.property_load"
	DynamicPropertyLoadSymbol                          = "bingo_dynamic_property_load_v1"
	DynamicValueRepresentation                         = "{tag:u32,reserved:u32,payload:u64}"
	DynamicStringKeyRepresentation                     = "utf16-string-view"
)

type DynamicValueABIContract struct {
	SchemaVersion     uint32              `json:"schemaVersion"`
	Representation    string              `json:"representation"`
	SizeBytes         uint32              `json:"sizeBytes"`
	AlignBytes        uint32              `json:"alignBytes"`
	TagBits           uint32              `json:"tagBits"`
	PayloadBits       uint32              `json:"payloadBits"`
	ObjectPayload     string              `json:"objectPayload"`
	NumberPayload     string              `json:"numberPayload"`
	StringKey         string              `json:"stringKey"`
	Capability        RuntimeCapabilityID `json:"capability"`
	Symbol            string              `json:"symbol"`
	Signature         string              `json:"signature"`
	SignatureHash     string              `json:"signatureHash"`
	StatusChecked     bool                `json:"statusChecked"`
	ExceptionStatus   uint32              `json:"exceptionStatus"`
	ExceptionResult   string              `json:"exceptionResult"`
	ExceptionCarrier  string              `json:"exceptionCarrier"`
	Allocates         bool                `json:"allocates"`
	MayInvokeAccessor bool                `json:"mayInvokeAccessor"`
	MayThrow          bool                `json:"mayThrow"`
	ContentHash       string              `json:"contentHash"`
}

func BuildDynamicValueABIContract() (DynamicValueABIContract, error) {
	signatureHash, err := DynamicPropertyLoadSignatureHash()
	if err != nil {
		return DynamicValueABIContract{}, err
	}
	contract := DynamicValueABIContract{SchemaVersion: DynamicValueABISchemaVersion, Representation: DynamicValueRepresentation, SizeBytes: 16, AlignBytes: 8, TagBits: 32, PayloadBits: 64, ObjectPayload: "opaque-host-handle", NumberPayload: "ieee754-binary64-bits", StringKey: DynamicStringKeyRepresentation, Capability: DynamicPropertyLoadCapability, Symbol: DynamicPropertyLoadSymbol, Signature: "u32(dynamic-value-v1,utf16-string-view,dynamic-value-v1*)", SignatureHash: signatureHash, StatusChecked: true, ExceptionStatus: 6, ExceptionResult: "canonical-undefined", ExceptionCarrier: "none-v1", MayInvokeAccessor: true, MayThrow: true}
	_, hash, err := CanonicalDynamicValueABIContract(contract)
	contract.ContentHash = hash
	return contract, err
}

func CanonicalDynamicValueABIContract(contract DynamicValueABIContract) ([]byte, string, error) {
	contract.ContentHash = ""
	signatureHash, err := DynamicPropertyLoadSignatureHash()
	if err != nil {
		return nil, "", err
	}
	if contract.SchemaVersion != DynamicValueABISchemaVersion || contract.Representation != DynamicValueRepresentation || contract.SizeBytes != 16 || contract.AlignBytes != 8 || contract.TagBits != 32 || contract.PayloadBits != 64 || contract.ObjectPayload != "opaque-host-handle" || contract.NumberPayload != "ieee754-binary64-bits" || contract.StringKey != DynamicStringKeyRepresentation || contract.Capability != DynamicPropertyLoadCapability || contract.Symbol != DynamicPropertyLoadSymbol || contract.Signature != "u32(dynamic-value-v1,utf16-string-view,dynamic-value-v1*)" || contract.SignatureHash != signatureHash || !contract.StatusChecked || contract.ExceptionStatus != 6 || contract.ExceptionResult != "canonical-undefined" || contract.ExceptionCarrier != "none-v1" || contract.Allocates || !contract.MayInvokeAccessor || !contract.MayThrow {
		return nil, "", fmt.Errorf("invalid DynamicValue ABI contract")
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

func DynamicPropertyLoadSignatureHash() (string, error) {
	encoded, err := jsonx.Marshal("u32(dynamic-value-v1,utf16-string-view,dynamic-value-v1*)")
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func VerifyCanonicalDynamicValueABIContract(contract DynamicValueABIContract) error {
	claimed := contract.ContentHash
	_, want, err := CanonicalDynamicValueABIContract(contract)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("DynamicValue ABI content hash mismatch")
	}
	return nil
}

func DecodeDynamicValueABIContract(data []byte) (*DynamicValueABIContract, error) {
	if len(data) > maxDynamicValueABIBytes {
		return nil, fmt.Errorf("DynamicValue ABI exceeds %d bytes", maxDynamicValueABIBytes)
	}
	var contract DynamicValueABIContract
	if err := jsonx.Unmarshal(data, &contract, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode DynamicValue ABI: %w", err)
	}
	if err := VerifyCanonicalDynamicValueABIContract(contract); err != nil {
		return nil, err
	}
	return &contract, nil
}
