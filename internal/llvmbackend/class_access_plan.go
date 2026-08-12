package llvmbackend

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	"github.com/microsoft/typescript-go/internal/bingo"
	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const ClassAccessBackendPlanSchemaVersion uint32 = 1
const maxClassAccessBackendPlanBytes = 512 << 10

type ClassAccessBackendAccess struct {
	ID              uint32                `json:"id"`
	AuthorizationID uint32                `json:"authorizationId"`
	FunctionID      bingo.FunctionID      `json:"functionId"`
	InstructionID   bingo.ValueID         `json:"instructionId"`
	Operation       string                `json:"operation"`
	Representation  bingo.VERT013aRepType `json:"representation"`
	ReceiverClassID uint32                `json:"receiverClassId"`
	MemberSymbolKey string                `json:"memberSymbolKey"`
	FieldOffset     uint32                `json:"fieldOffset,omitempty"`
	CalleeSymbolKey string                `json:"calleeSymbolKey,omitempty"`
	Origin          bingo.Origin          `json:"origin"`
}

type ClassAccessBackendPlan struct {
	SchemaVersion uint32                          `json:"schemaVersion"`
	LayoutHash    string                          `json:"layoutHash"`
	Layout        bingo.ClassAccessLayoutContract `json:"layout"`
	Accesses      []ClassAccessBackendAccess      `json:"accesses"`
	ContentHash   string                          `json:"contentHash"`
}

func PlanClassAccessBackend(layout bingo.ClassAccessLayoutContract) (ClassAccessBackendPlan, error) {
	if err := bingo.VerifyCanonicalClassAccessLayout(layout); err != nil {
		return ClassAccessBackendPlan{}, err
	}
	plan := ClassAccessBackendPlan{SchemaVersion: ClassAccessBackendPlanSchemaVersion, LayoutHash: layout.ContentHash, Layout: layout, Accesses: fixedClassAccessBackendAccesses(layout)}
	_, hash, err := CanonicalClassAccessBackendPlan(plan)
	if err != nil {
		return ClassAccessBackendPlan{}, err
	}
	plan.ContentHash = hash
	return plan, nil
}

func fixedClassAccessBackendAccesses(layout bingo.ClassAccessLayoutContract) []ClassAccessBackendAccess {
	result := make([]ClassAccessBackendAccess, 0, len(layout.MIR.Authorizations))
	for _, function := range layout.MIR.Functions {
		for _, instruction := range function.Instructions {
			if instruction.AuthorizationID == 0 {
				continue
			}
			authorization := layout.MIR.Authorizations[instruction.AuthorizationID-1]
			access := ClassAccessBackendAccess{
				ID: uint32(len(result) + 1), AuthorizationID: authorization.ID, FunctionID: function.ID, InstructionID: instruction.ID, Operation: authorization.Operation,
				Representation: authorization.Representation, ReceiverClassID: authorization.Request.ReceiverClassID,
				MemberSymbolKey: authorization.MemberSymbolKey, Origin: authorization.Origin,
			}
			if authorization.MemberKind == bingo.ClassAccessField {
				physical := layout.Base
				if authorization.Request.ReceiverClassID == layout.DerivedClassID {
					physical = layout.Derived
				}
				for _, property := range physical.Properties {
					if property.Key == authorization.MemberSymbolKey {
						access.FieldOffset = property.FieldOffset
						break
					}
				}
			} else {
				access.CalleeSymbolKey = authorization.MemberSymbolKey
			}
			result = append(result, access)
		}
	}
	return result
}

func VerifyClassAccessBackendPlan(plan ClassAccessBackendPlan) error {
	if plan.SchemaVersion != ClassAccessBackendPlanSchemaVersion || plan.LayoutHash != plan.Layout.ContentHash || len(plan.Accesses) != 4 {
		return fmt.Errorf("invalid OBJ-003b backend plan envelope")
	}
	if err := bingo.VerifyCanonicalClassAccessLayout(plan.Layout); err != nil {
		return err
	}
	want := fixedClassAccessBackendAccesses(plan.Layout)
	left, err := jsonx.Marshal(plan.Accesses)
	if err != nil {
		return err
	}
	right, err := jsonx.Marshal(want)
	if err != nil {
		return err
	}
	if !slices.Equal(left, right) {
		return fmt.Errorf("OBJ-003b backend access lowering mismatch")
	}
	for index, access := range plan.Accesses {
		authorization := plan.Layout.MIR.Authorizations[access.AuthorizationID-1]
		if access.ID != uint32(index+1) || access.AuthorizationID != authorization.ID || access.Representation != bingo.VERT013aRepF64 {
			return fmt.Errorf("OBJ-003b backend access identity mismatch")
		}
		if authorization.MemberKind == bingo.ClassAccessField {
			if access.Operation != "class.field.load.authorized" || access.FieldOffset == 0 || access.CalleeSymbolKey != "" {
				return fmt.Errorf("OBJ-003b backend field access mismatch")
			}
		} else if access.Operation != "class.method.call.authorized" || access.FieldOffset != 0 || access.CalleeSymbolKey != access.MemberSymbolKey {
			return fmt.Errorf("OBJ-003b backend method access mismatch")
		}
	}
	return nil
}

func CanonicalClassAccessBackendPlan(plan ClassAccessBackendPlan) ([]byte, string, error) {
	plan.ContentHash = ""
	if err := VerifyClassAccessBackendPlan(plan); err != nil {
		return nil, "", err
	}
	encoded, err := jsonx.Marshal(plan)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	plan.ContentHash = hex.EncodeToString(digest[:])
	encoded, err = jsonx.Marshal(plan)
	return encoded, plan.ContentHash, err
}

func VerifyCanonicalClassAccessBackendPlan(plan ClassAccessBackendPlan) error {
	claimed := plan.ContentHash
	_, want, err := CanonicalClassAccessBackendPlan(plan)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("OBJ-003b backend plan content hash mismatch")
	}
	return nil
}

func DecodeClassAccessBackendPlan(data []byte) (*ClassAccessBackendPlan, error) {
	if len(data) > maxClassAccessBackendPlanBytes {
		return nil, fmt.Errorf("OBJ-003b backend plan exceeds %d bytes", maxClassAccessBackendPlanBytes)
	}
	var plan ClassAccessBackendPlan
	if err := jsonx.Unmarshal(data, &plan, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	if err := VerifyCanonicalClassAccessBackendPlan(plan); err != nil {
		return nil, err
	}
	return &plan, nil
}
