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

// ClassAccessLoweringResult is the production OBJ-003b authorization chain.
// It deliberately stops at a verified backend plan until the structural MIR
// is followed by a target-bound GC/capability execution artifact.
type ClassAccessLoweringResult struct {
	Replay      ast2bingo.ClassAccessReplayResult
	Resolution  targetcontext.Resolution
	MIR         bingo.ClassAccessMIRModule
	Layout      bingo.ClassAccessLayoutContract
	BoundMIR    bingo.ClassAccessBoundMIR
	BackendPlan llvmbackend.ClassAccessBackendPlan
}

type ClassAccessPipelineResult struct {
	ClassAccessLoweringResult
	Emission llvmbackend.ClassAccessEmission
}

func LowerClassAccess(snapshot frontendwire.ProgramSnapshot, identity bingo.CompilerBuildIdentity, plan buildplan.Plan, machine *llvmbackend.TargetMachine, runtimeManifest []byte) (ClassAccessLoweringResult, error) {
	replay, err := ast2bingo.ReplayClassAccessSnapshot(snapshot, identity)
	if err != nil {
		return ClassAccessLoweringResult{}, err
	}
	resolution, err := targetcontext.ResolveTargetContext(plan, machine, runtimeManifest)
	if err != nil {
		return ClassAccessLoweringResult{}, err
	}
	mir, err := targetcontext.LowerClassAccessMIR(replay.HIR, resolution.Context)
	if err != nil {
		return ClassAccessLoweringResult{}, err
	}
	layout, err := targetcontext.PlanClassAccessLayout(mir, resolution.Context)
	if err != nil {
		return ClassAccessLoweringResult{}, err
	}
	bound, err := targetcontext.BindClassAccessMIR(layout, resolution.Context, resolution.Catalog)
	if err != nil {
		return ClassAccessLoweringResult{}, err
	}
	backendPlan, err := llvmbackend.PlanClassAccessBackend(layout)
	if err != nil {
		return ClassAccessLoweringResult{}, err
	}
	return ClassAccessLoweringResult{Replay: replay, Resolution: resolution, MIR: mir, Layout: layout, BoundMIR: bound, BackendPlan: backendPlan}, nil
}

func ExecuteClassAccess(snapshot frontendwire.ProgramSnapshot, identity bingo.CompilerBuildIdentity, plan buildplan.Plan, machine *llvmbackend.TargetMachine, runtimeManifest []byte) (ClassAccessPipelineResult, error) {
	lowered, err := LowerClassAccess(snapshot, identity, plan, machine, runtimeManifest)
	if err != nil {
		return ClassAccessPipelineResult{}, err
	}
	emission, err := machine.EmitClassAccessObject(lowered.BoundMIR)
	if err != nil {
		return ClassAccessPipelineResult{}, fmt.Errorf("OBJ-003b LLVM emission: %w", err)
	}
	return ClassAccessPipelineResult{ClassAccessLoweringResult: lowered, Emission: emission}, nil
}
