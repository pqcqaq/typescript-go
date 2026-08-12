package llvmbackend

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/microsoft/typescript-go/internal/bingo"
	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const CheckedObjectCastBackendPlanSchemaVersion uint32 = 1
const maxCheckedObjectCastBackendPlanBytes = 1 << 20

type CheckedObjectCastBackendPlan struct {
	SchemaVersion            uint32                               `json:"schemaVersion"`
	BoundHash                string                               `json:"boundHash"`
	Bound                    bingo.CheckedObjectCastBoundContract `json:"bound"`
	FunctionName             string                               `json:"functionName"`
	TargetSemanticHash       string                               `json:"targetSemanticHash"`
	TargetLayoutContentHash  string                               `json:"targetLayoutContentHash"`
	TargetPhysicalLayoutHash string                               `json:"targetPhysicalLayoutHash"`
	PropertyCount            uint32                               `json:"propertyCount"`
	StatusChecked            bool                                 `json:"statusChecked"`
	MatchDomain              string                               `json:"matchDomain"`
	SuccessValue             string                               `json:"successValue"`
	FailureValue             string                               `json:"failureValue"`
	PreservesIdentity        bool                                 `json:"preservesIdentity"`
	Allocates                bool                                 `json:"allocates"`
	Copies                   bool                                 `json:"copies"`
	InvokesAccessors         bool                                 `json:"invokesAccessors"`
	RuntimeCalls             []string                             `json:"runtimeCalls"`
	ContentHash              string                               `json:"contentHash"`
}

func BuildCheckedObjectCastBackendPlan(bound bingo.CheckedObjectCastBoundContract) (CheckedObjectCastBackendPlan, error) {
	plan := CheckedObjectCastBackendPlan{SchemaVersion: CheckedObjectCastBackendPlanSchemaVersion, BoundHash: bound.ContentHash, Bound: bound, FunctionName: bingo.CheckedObjectCastSymbol, TargetSemanticHash: bound.Cast.Target.ContentHash, TargetLayoutContentHash: bound.Cast.TargetLayout.ContentHash, TargetPhysicalLayoutHash: bound.Cast.TargetLayout.LayoutHash, PropertyCount: uint32(len(bound.Cast.Properties)), StatusChecked: true, MatchDomain: "0|1", SuccessValue: "source-reference", FailureValue: "none", PreservesIdentity: true, RuntimeCalls: []string{bingo.CheckedObjectCastSymbol}}
	_, hash, err := CanonicalCheckedObjectCastBackendPlan(plan)
	plan.ContentHash = hash
	return plan, err
}

func CanonicalCheckedObjectCastBackendPlan(plan CheckedObjectCastBackendPlan) ([]byte, string, error) {
	plan.ContentHash = ""
	if plan.SchemaVersion != CheckedObjectCastBackendPlanSchemaVersion || !plan.StatusChecked || plan.MatchDomain != "0|1" || plan.SuccessValue != "source-reference" || plan.FailureValue != "none" || !plan.PreservesIdentity || plan.Allocates || plan.Copies || plan.InvokesAccessors {
		return nil, "", fmt.Errorf("invalid checked object cast backend plan header")
	}
	if err := bingo.VerifyCanonicalCheckedObjectCastBound(plan.Bound); err != nil {
		return nil, "", err
	}
	if plan.BoundHash != plan.Bound.ContentHash || plan.FunctionName != bingo.CheckedObjectCastSymbol || len(plan.RuntimeCalls) != 1 || plan.RuntimeCalls[0] != bingo.CheckedObjectCastSymbol || plan.Bound.Binding.SymbolName != plan.FunctionName {
		return nil, "", fmt.Errorf("checked object cast runtime call mismatch")
	}
	cast := plan.Bound.Cast
	if plan.TargetSemanticHash != cast.Target.ContentHash || plan.TargetLayoutContentHash != cast.TargetLayout.ContentHash || plan.TargetPhysicalLayoutHash != cast.TargetLayout.LayoutHash || plan.PropertyCount != uint32(len(cast.Properties)) || plan.PropertyCount == 0 {
		return nil, "", fmt.Errorf("checked object cast target shape mismatch")
	}
	b, err := jsonx.Marshal(plan)
	if err != nil {
		return nil, "", err
	}
	d := sha256.Sum256(b)
	h := hex.EncodeToString(d[:])
	plan.ContentHash = h
	b, err = jsonx.Marshal(plan)
	return b, h, err
}

func VerifyCanonicalCheckedObjectCastBackendPlan(plan CheckedObjectCastBackendPlan) error {
	claimed := plan.ContentHash
	_, want, err := CanonicalCheckedObjectCastBackendPlan(plan)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("checked object cast backend plan content hash mismatch")
	}
	return nil
}

func DecodeCheckedObjectCastBackendPlan(data []byte) (*CheckedObjectCastBackendPlan, error) {
	if len(data) > maxCheckedObjectCastBackendPlanBytes {
		return nil, fmt.Errorf("checked object cast backend plan exceeds %d bytes", maxCheckedObjectCastBackendPlanBytes)
	}
	var plan CheckedObjectCastBackendPlan
	if err := jsonx.Unmarshal(data, &plan, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode checked object cast backend plan: %w", err)
	}
	if err := VerifyCanonicalCheckedObjectCastBackendPlan(plan); err != nil {
		return nil, err
	}
	return &plan, nil
}
