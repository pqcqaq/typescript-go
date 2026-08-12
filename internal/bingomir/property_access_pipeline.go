package bingomir

import (
	"fmt"

	"github.com/microsoft/typescript-go/internal/ast2bingo"
	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/buildplan"
	"github.com/microsoft/typescript-go/internal/frontendwire"
	"github.com/microsoft/typescript-go/internal/llvmbackend"
	"github.com/microsoft/typescript-go/internal/targetcontext"
)

type PropertyAccessLoweringResult struct {
	Replay ast2bingo.PropertyAccessAdmissionReplay
	HIR    bingo.PropertyAccessHIRArtifact
	MIR    bingo.PropertyAccessMIRArtifact
}

type PropertyAccessBindingResult struct {
	PropertyAccessLoweringResult
	BoundMIR    bingo.PropertyAccessBoundMIR
	BackendPlan llvmbackend.PropertyAccessBackendPlan
}

func LowerPropertyAccess(snapshot frontendwire.ProgramSnapshot, identity bingo.CompilerBuildIdentity, plan buildplan.Plan, machine *llvmbackend.TargetMachine) (PropertyAccessLoweringResult, error) {
	replay, err := ast2bingo.ReplayPropertyAccessAdmissionSnapshot(snapshot, identity)
	if err != nil {
		return PropertyAccessLoweringResult{}, fmt.Errorf("property access snapshot replay: %w", err)
	}
	return lowerPropertyAccessReplay(replay, identity, plan, machine)
}

func LowerPropertyAccessReplay(replayBytes []byte, identity bingo.CompilerBuildIdentity, plan buildplan.Plan, machine *llvmbackend.TargetMachine) (PropertyAccessLoweringResult, error) {
	replay, err := ast2bingo.DecodePropertyAccessAdmissionReplay(replayBytes)
	if err != nil {
		return PropertyAccessLoweringResult{}, fmt.Errorf("property access replay artifact: %w", err)
	}
	return lowerPropertyAccessReplay(*replay, identity, plan, machine)
}

func lowerPropertyAccessReplay(replay ast2bingo.PropertyAccessAdmissionReplay, identity bingo.CompilerBuildIdentity, plan buildplan.Plan, machine *llvmbackend.TargetMachine) (PropertyAccessLoweringResult, error) {
	if err := bingo.ValidateCompilerBuildIdentity(identity); err != nil {
		return PropertyAccessLoweringResult{}, fmt.Errorf("property access compiler identity: %w", err)
	}
	if replay.CompilerBuildIdentity != identity {
		return PropertyAccessLoweringResult{}, fmt.Errorf("property access replay does not bind current compiler identity")
	}
	if err := buildplan.Validate(plan); err != nil {
		return PropertyAccessLoweringResult{}, fmt.Errorf("property access BuildPlan: %w", err)
	}
	if plan.FrontendHash != replay.FrontendSnapshotHash {
		return PropertyAccessLoweringResult{}, fmt.Errorf("property access BuildPlan does not bind frontend snapshot")
	}
	if plan.Profile != frontendwire.ProfileInterop {
		return PropertyAccessLoweringResult{}, fmt.Errorf("property access requires the interop profile")
	}
	if machine == nil {
		return PropertyAccessLoweringResult{}, fmt.Errorf("property access target machine is nil")
	}
	manifest := machine.Manifest()
	if err := llvmbackend.ValidateToolchainManifest(manifest); err != nil {
		return PropertyAccessLoweringResult{}, fmt.Errorf("property access observed target: %w", err)
	}
	hir, err := ast2bingo.LowerPropertyAccessAdmissionHIR(replay)
	if err != nil {
		return PropertyAccessLoweringResult{}, fmt.Errorf("property access HIR: %w", err)
	}
	abi, err := bingo.BuildDynamicValueABIContract()
	if err != nil {
		return PropertyAccessLoweringResult{}, fmt.Errorf("property access DynamicValue ABI: %w", err)
	}
	mir, err := bingo.LowerPropertyAccessMIR(hir, manifest.TargetTriple, manifest.DataLayout.ContentHash, abi)
	if err != nil {
		return PropertyAccessLoweringResult{}, fmt.Errorf("property access MIR: %w", err)
	}
	return PropertyAccessLoweringResult{Replay: replay, HIR: hir, MIR: mir}, nil
}

func BindPropertyAccess(lowered PropertyAccessLoweringResult, context targetcontext.TargetContext, catalog targetcontext.AvailableCapabilityCatalog) (PropertyAccessBindingResult, error) {
	bound, err := targetcontext.BindPropertyAccessMIR(lowered.MIR, context, catalog)
	if err != nil {
		return PropertyAccessBindingResult{}, fmt.Errorf("property access runtime binding: %w", err)
	}
	plan, err := llvmbackend.BuildPropertyAccessBackendPlan(bound)
	if err != nil {
		return PropertyAccessBindingResult{}, fmt.Errorf("property access backend planning: %w", err)
	}
	return PropertyAccessBindingResult{PropertyAccessLoweringResult: lowered, BoundMIR: bound, BackendPlan: plan}, nil
}

// ResolveAndBindPropertyAccess is the authoritative production binding path.
// The runtime manifest is deliberately explicit: a caller cannot smuggle a
// hand-built context or capability catalog into the backend.
func ResolveAndBindPropertyAccess(lowered PropertyAccessLoweringResult, plan buildplan.Plan, machine *llvmbackend.TargetMachine, runtimeManifest []byte) (PropertyAccessBindingResult, error) {
	if err := buildplan.Validate(plan); err != nil {
		return PropertyAccessBindingResult{}, fmt.Errorf("property access BuildPlan: %w", err)
	}
	if plan.Profile != frontendwire.ProfileInterop {
		return PropertyAccessBindingResult{}, fmt.Errorf("property access requires the interop profile")
	}
	if lowered.Replay.FrontendSnapshotHash != plan.FrontendHash {
		return PropertyAccessBindingResult{}, fmt.Errorf("property access BuildPlan does not bind lowered replay")
	}
	resolution, err := targetcontext.ResolveTargetContext(plan, machine, runtimeManifest)
	if err != nil {
		return PropertyAccessBindingResult{}, fmt.Errorf("property access target resolution: %w", err)
	}
	if resolution.Context.FrontendHash != lowered.Replay.FrontendSnapshotHash {
		return PropertyAccessBindingResult{}, fmt.Errorf("property access TargetContext does not bind lowered replay")
	}
	return BindPropertyAccess(lowered, resolution.Context, resolution.Catalog)
}
