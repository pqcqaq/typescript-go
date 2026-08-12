package llvmbackend

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	"github.com/microsoft/typescript-go/internal/bingo"
	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const ObjectLayoutCopyBackendPlanSchemaVersion uint32 = 1
const maxObjectLayoutCopyBackendPlanBytes = 5 << 20

type ObjectLayoutCopyBackendPlan struct {
	SchemaVersion    uint32                              `json:"schemaVersion"`
	BoundHash        string                              `json:"boundHash"`
	Bound            bingo.ObjectLayoutCopyBoundArtifact `json:"bound"`
	FunctionName     string                              `json:"functionName"`
	SourceOffset     uint32                              `json:"sourceOffset"`
	TargetOffset     uint32                              `json:"targetOffset"`
	Representation   string                              `json:"representation"`
	TargetLayoutHash string                              `json:"targetLayoutHash"`
	RuntimeCalls     []string                            `json:"runtimeCalls"`
	StatusChecked    bool                                `json:"statusChecked"`
	Allocates        bool                                `json:"allocates"`
	NewIdentity      bool                                `json:"newIdentity"`
	InvokesAccessors bool                                `json:"invokesAccessors"`
	UsesBitcast      bool                                `json:"usesBitcast"`
	ContentHash      string                              `json:"contentHash"`
}

func BuildObjectLayoutCopyBackendPlan(bound bingo.ObjectLayoutCopyBoundArtifact) (ObjectLayoutCopyBackendPlan, error) {
	plan := ObjectLayoutCopyBackendPlan{SchemaVersion: ObjectLayoutCopyBackendPlanSchemaVersion, BoundHash: bound.ContentHash, Bound: bound, FunctionName: "bingo_object_layout_copy_v1", StatusChecked: true, Allocates: true, NewIdentity: true, RuntimeCalls: objectLayoutCopyRuntimeCalls(bound)}
	if len(bound.MIR.HIR.Copy.Mappings) == 1 {
		mapping := bound.MIR.HIR.Copy.Mappings[0]
		plan.SourceOffset, plan.TargetOffset, plan.Representation, plan.TargetLayoutHash = mapping.SourceFieldOffset, mapping.TargetFieldOffset, mapping.TargetRepresentation, bound.MIR.HIR.Copy.TargetLayout.ContentHash
	}
	_, hash, err := CanonicalObjectLayoutCopyBackendPlan(plan)
	plan.ContentHash = hash
	return plan, err
}

func CanonicalObjectLayoutCopyBackendPlan(plan ObjectLayoutCopyBackendPlan) ([]byte, string, error) {
	plan.ContentHash = ""
	if plan.SchemaVersion != ObjectLayoutCopyBackendPlanSchemaVersion || plan.FunctionName != "bingo_object_layout_copy_v1" || !plan.StatusChecked || !plan.Allocates || !plan.NewIdentity || plan.InvokesAccessors || plan.UsesBitcast {
		return nil, "", fmt.Errorf("invalid object layout copy backend header")
	}
	if err := bingo.VerifyCanonicalObjectLayoutCopyBoundArtifact(plan.Bound); err != nil {
		return nil, "", err
	}
	if plan.BoundHash != plan.Bound.ContentHash || len(plan.Bound.MIR.HIR.Copy.Mappings) != 1 {
		return nil, "", fmt.Errorf("object layout copy backend requires one bound mapping")
	}
	mapping := plan.Bound.MIR.HIR.Copy.Mappings[0]
	if mapping.SourceRepresentation != "f64" || mapping.TargetRepresentation != "f64" || plan.SourceOffset != mapping.SourceFieldOffset || plan.TargetOffset != mapping.TargetFieldOffset || plan.Representation != "f64" || plan.TargetLayoutHash != plan.Bound.MIR.HIR.Copy.TargetLayout.ContentHash {
		return nil, "", fmt.Errorf("object layout copy backend mapping mismatch")
	}
	wantedCalls := objectLayoutCopyRuntimeCalls(plan.Bound)
	if !slices.Equal(plan.RuntimeCalls, wantedCalls) {
		return nil, "", fmt.Errorf("object layout copy runtime closure mismatch")
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

func objectLayoutCopyRuntimeCalls(bound bingo.ObjectLayoutCopyBoundArtifact) []string {
	byLogical := make(map[bingo.RuntimeCapabilityID]string, len(bound.Bindings))
	for _, binding := range bound.Bindings {
		byLogical[binding.LogicalName] = binding.SymbolName
	}
	order := []bingo.RuntimeCapabilityID{"rt.gc.frame.link", "rt.gc.root.store", "rt.gc.root.publish", "rt.gc.alloc", "rt.gc.root.reload", "rt.gc.frame.unlink"}
	result := make([]string, len(order))
	for index, logical := range order {
		result[index] = byLogical[logical]
	}
	return result
}

func VerifyCanonicalObjectLayoutCopyBackendPlan(plan ObjectLayoutCopyBackendPlan) error {
	claimed := plan.ContentHash
	_, wanted, err := CanonicalObjectLayoutCopyBackendPlan(plan)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != wanted {
		return fmt.Errorf("object layout copy backend content hash mismatch")
	}
	return nil
}

func DecodeObjectLayoutCopyBackendPlan(data []byte) (*ObjectLayoutCopyBackendPlan, error) {
	if len(data) > maxObjectLayoutCopyBackendPlanBytes {
		return nil, fmt.Errorf("object layout copy backend exceeds %d bytes", maxObjectLayoutCopyBackendPlanBytes)
	}
	var plan ObjectLayoutCopyBackendPlan
	if err := jsonx.Unmarshal(data, &plan, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode object layout copy backend: %w", err)
	}
	if err := VerifyCanonicalObjectLayoutCopyBackendPlan(plan); err != nil {
		return nil, err
	}
	return &plan, nil
}
