package llvmbackend

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/microsoft/typescript-go/internal/bingo"
	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const PropertyAccessBackendPlanSchemaVersion uint32 = 1
const maxPropertyAccessBackendPlanBytes = 4 << 20

type PropertyAccessBackendPlan struct {
	SchemaVersion        uint32                       `json:"schemaVersion"`
	BoundMIRHash         string                       `json:"boundMirHash"`
	BoundMIR             bingo.PropertyAccessBoundMIR `json:"boundMir"`
	DynamicValueLLVM     string                       `json:"dynamicValueLlvm"`
	UTF16ViewLLVM        string                       `json:"utf16ViewLlvm"`
	RuntimeSymbol        string                       `json:"runtimeSymbol"`
	EntrySymbol          string                       `json:"entrySymbol"`
	StatusRepresentation string                       `json:"statusRepresentation"`
	SuccessStatus        uint32                       `json:"successStatus"`
	ExceptionStatus      uint32                       `json:"exceptionStatus"`
	ChecksStatus         bool                         `json:"checksStatus"`
	ClearsFailureResult  bool                         `json:"clearsFailureResult"`
	ExceptionResult      string                       `json:"exceptionResult"`
	Allocates            bool                         `json:"allocates"`
	ContentHash          string                       `json:"contentHash"`
}

func BuildPropertyAccessBackendPlan(bound bingo.PropertyAccessBoundMIR) (PropertyAccessBackendPlan, error) {
	plan := PropertyAccessBackendPlan{
		SchemaVersion: PropertyAccessBackendPlanSchemaVersion, BoundMIRHash: bound.ContentHash,
		BoundMIR: bound, DynamicValueLLVM: "{i32,i32,i64}", UTF16ViewLLVM: "{ptr,i64}",
		RuntimeSymbol: bingo.DynamicPropertyLoadSymbol, StatusRepresentation: "i32",
		EntrySymbol:     "bingo_property_access_dynamic_v1",
		ExceptionStatus: 6, ChecksStatus: true, ClearsFailureResult: true, ExceptionResult: "canonical-undefined",
	}
	_, hash, err := CanonicalPropertyAccessBackendPlan(plan)
	plan.ContentHash = hash
	return plan, err
}

func CanonicalPropertyAccessBackendPlan(plan PropertyAccessBackendPlan) ([]byte, string, error) {
	plan.ContentHash = ""
	if plan.SchemaVersion != PropertyAccessBackendPlanSchemaVersion || plan.BoundMIRHash != plan.BoundMIR.ContentHash ||
		plan.DynamicValueLLVM != "{i32,i32,i64}" || plan.UTF16ViewLLVM != "{ptr,i64}" ||
		plan.RuntimeSymbol != bingo.DynamicPropertyLoadSymbol || plan.StatusRepresentation != "i32" ||
		plan.EntrySymbol != "bingo_property_access_dynamic_v1" ||
		plan.SuccessStatus != 0 || plan.ExceptionStatus != 6 || !plan.ChecksStatus || !plan.ClearsFailureResult ||
		plan.ExceptionResult != "canonical-undefined" || plan.Allocates {
		return nil, "", fmt.Errorf("invalid property access backend plan")
	}
	if err := bingo.VerifyCanonicalPropertyAccessBoundMIR(plan.BoundMIR); err != nil {
		return nil, "", err
	}
	if plan.BoundMIR.MIR.DynamicABI.ExceptionStatus != plan.ExceptionStatus ||
		plan.BoundMIR.MIR.DynamicABI.ExceptionResult != plan.ExceptionResult ||
		plan.BoundMIR.Binding.SymbolName != plan.RuntimeSymbol {
		return nil, "", fmt.Errorf("property access backend ABI does not match bound MIR")
	}
	encoded, err := jsonx.Marshal(plan)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	plan.ContentHash = hash
	encoded, err = jsonx.Marshal(plan)
	return encoded, hash, err
}

func VerifyCanonicalPropertyAccessBackendPlan(plan PropertyAccessBackendPlan) error {
	claimed := plan.ContentHash
	_, want, err := CanonicalPropertyAccessBackendPlan(plan)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("property access backend plan content hash mismatch")
	}
	return nil
}

func DecodePropertyAccessBackendPlan(data []byte) (*PropertyAccessBackendPlan, error) {
	if len(data) > maxPropertyAccessBackendPlanBytes {
		return nil, fmt.Errorf("property access backend plan exceeds %d bytes", maxPropertyAccessBackendPlanBytes)
	}
	var plan PropertyAccessBackendPlan
	if err := jsonx.Unmarshal(data, &plan, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode property access backend plan: %w", err)
	}
	if err := VerifyCanonicalPropertyAccessBackendPlan(plan); err != nil {
		return nil, err
	}
	return &plan, nil
}
