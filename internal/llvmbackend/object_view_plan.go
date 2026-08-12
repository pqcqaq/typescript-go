package llvmbackend

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/microsoft/typescript-go/internal/bingo"
	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const ObjectViewBackendPlanSchemaVersion uint32 = 2
const maxObjectViewBackendPlanBytes = 2 << 20

type ObjectViewBackendPlan struct {
	SchemaVersion     uint32                    `json:"schemaVersion"`
	MIRHash           string                    `json:"mirHash"`
	MIR               bingo.ObjectViewMIRModule `json:"mir"`
	FunctionName      string                    `json:"functionName"`
	SourceOffset      uint32                    `json:"sourceOffset"`
	Accessor          bool                      `json:"accessor"`
	GetterSymbolKey   string                    `json:"getterSymbolKey,omitempty"`
	BackingOffset     uint32                    `json:"backingOffset,omitempty"`
	Representation    string                    `json:"representation"`
	PreservesIdentity bool                      `json:"preservesIdentity"`
	Allocates         bool                      `json:"allocates"`
	RuntimeCalls      []string                  `json:"runtimeCalls"`
	ContentHash       string                    `json:"contentHash"`
}

func BuildObjectViewBackendPlan(mir bingo.ObjectViewMIRModule) (ObjectViewBackendPlan, error) {
	plan := ObjectViewBackendPlan{SchemaVersion: ObjectViewBackendPlanSchemaVersion, MIRHash: mir.ContentHash, MIR: mir, FunctionName: "bingo_object_view_read_v1", PreservesIdentity: true, RuntimeCalls: []string{}}
	if len(mir.Reads) == 1 {
		read := mir.Reads[0]
		plan.SourceOffset, plan.Representation = read.SourceFieldOffset, read.Representation
		if read.Kind == bingo.ObjectPropertyAccessor {
			place := mir.HIR.Gate.HIR.PlaceRefs.Places[0]
			for _, property := range mir.HIR.Operation.View.SourceLayout.Properties {
				if property.Key == place.BackingPropertyKey {
					plan.BackingOffset = property.FieldOffset
				}
			}
			plan.Accessor, plan.GetterSymbolKey, plan.FunctionName = true, read.GetterSymbolKey, "bingo_object_view_read_accessor_v1"
		}
	}
	_, hash, err := CanonicalObjectViewBackendPlan(plan)
	plan.ContentHash = hash
	return plan, err
}

func CanonicalObjectViewBackendPlan(plan ObjectViewBackendPlan) ([]byte, string, error) {
	plan.ContentHash = ""
	if plan.SchemaVersion != ObjectViewBackendPlanSchemaVersion || !plan.PreservesIdentity || plan.Allocates || len(plan.RuntimeCalls) != 0 {
		return nil, "", fmt.Errorf("invalid ObjectView backend plan header")
	}
	if err := bingo.VerifyCanonicalObjectViewMIR(plan.MIR); err != nil {
		return nil, "", err
	}
	if plan.MIRHash != plan.MIR.ContentHash || len(plan.MIR.Reads) != 1 {
		return nil, "", fmt.Errorf("ObjectView backend requires exactly one verified read")
	}
	read := plan.MIR.Reads[0]
	if plan.Accessor {
		place := plan.MIR.HIR.Gate.HIR.PlaceRefs.Places[0]
		backingIndex := -1
		for index, property := range plan.MIR.HIR.Operation.View.SourceLayout.Properties {
			if property.Key == place.BackingPropertyKey {
				if backingIndex >= 0 {
					return nil, "", fmt.Errorf("ObjectView accessor backing layout is ambiguous")
				}
				backingIndex = index
			}
		}
		if backingIndex < 0 {
			return nil, "", fmt.Errorf("ObjectView accessor backing layout is missing")
		}
		backing := plan.MIR.HIR.Operation.View.SourceLayout.Properties[backingIndex]
		if read.Kind != bingo.ObjectPropertyAccessor || read.Representation != string(bingo.VERT011RepNullableF64) || plan.Representation != read.Representation || plan.FunctionName != "bingo_object_view_read_accessor_v1" || plan.GetterSymbolKey != read.GetterSymbolKey || plan.GetterSymbolKey != place.GetterSymbolKey || plan.GetterSymbolKey == "" || backing.Kind != bingo.ObjectPropertyData || backing.Representation != string(bingo.VERT011RepNullableF64) || plan.BackingOffset != backing.FieldOffset || plan.BackingOffset == 0 || plan.SourceOffset != 0 {
			return nil, "", fmt.Errorf("ObjectView accessor backend mapping mismatch")
		}
	} else if read.Kind != bingo.ObjectPropertyData || read.Representation != "f64" || plan.FunctionName != "bingo_object_view_read_v1" || plan.Representation != read.Representation || plan.SourceOffset != read.SourceFieldOffset || plan.GetterSymbolKey != "" || plan.BackingOffset != 0 {
		return nil, "", fmt.Errorf("ObjectView data backend read mapping mismatch")
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

func VerifyCanonicalObjectViewBackendPlan(plan ObjectViewBackendPlan) error {
	claimed := plan.ContentHash
	_, want, err := CanonicalObjectViewBackendPlan(plan)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("ObjectView backend plan content hash mismatch")
	}
	return nil
}
func DecodeObjectViewBackendPlan(data []byte) (*ObjectViewBackendPlan, error) {
	if len(data) > maxObjectViewBackendPlanBytes {
		return nil, fmt.Errorf("ObjectView backend plan exceeds %d bytes", maxObjectViewBackendPlanBytes)
	}
	var plan ObjectViewBackendPlan
	if err := jsonx.Unmarshal(data, &plan, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode ObjectView backend plan: %w", err)
	}
	if err := VerifyCanonicalObjectViewBackendPlan(plan); err != nil {
		return nil, err
	}
	return &plan, nil
}
