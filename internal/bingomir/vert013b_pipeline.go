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

type VERT013bLoweringResult struct {
	Replay     ast2bingo.VERT013bReplayResult
	Resolution targetcontext.Resolution
	Layout     bingo.VERT013bLayoutContract
	MIR        bingo.VERT013bMIRModule
	BoundMIR   bingo.VERT013bBoundMIR
}

type VERT013bPipelineResult struct {
	VERT013bLoweringResult
	Emission llvmbackend.VERT013bEmission
}

// LowerVERT013b runs the canonical derived-class snapshot through exact target binding.
func LowerVERT013b(snapshot frontendwire.ProgramSnapshot, identity bingo.CompilerBuildIdentity, plan buildplan.Plan, machine *llvmbackend.TargetMachine, runtimeManifest []byte) (VERT013bLoweringResult, error) {
	if machine == nil {
		return VERT013bLoweringResult{}, fmt.Errorf("VERT-013b target machine is nil")
	}
	replay, err := ast2bingo.ReplayVERT013bSnapshot(snapshot, identity)
	if err != nil {
		return VERT013bLoweringResult{}, fmt.Errorf("VERT-013b snapshot replay: %w", err)
	}
	resolution, err := targetcontext.ResolveTargetContext(plan, machine, runtimeManifest)
	if err != nil {
		return VERT013bLoweringResult{}, fmt.Errorf("VERT-013b target resolution: %w", err)
	}
	if plan.FrontendHash != replay.FrontendSnapshotHash || resolution.Context.FrontendHash != replay.FrontendSnapshotHash {
		return VERT013bLoweringResult{}, fmt.Errorf("VERT-013b frontend identity does not match build plan and target context")
	}
	target, err := bingo.CanonicalObjectLayoutTarget(resolution.Context.Triple)
	if err != nil {
		return VERT013bLoweringResult{}, err
	}
	layout, err := bingo.PlanVERT013bLayout(replay.Contract, target)
	if err != nil {
		return VERT013bLoweringResult{}, fmt.Errorf("VERT-013b instance layout: %w", err)
	}
	mir, err := bingo.LowerVERT013bMIR(replay.HIR, layout)
	if err != nil {
		return VERT013bLoweringResult{}, fmt.Errorf("VERT-013b MIR lowering: %w", err)
	}
	bound, err := targetcontext.BindVERT013bMIR(mir, resolution.Context, resolution.Catalog)
	if err != nil {
		return VERT013bLoweringResult{}, fmt.Errorf("VERT-013b capability binding: %w", err)
	}
	return VERT013bLoweringResult{Replay: replay, Resolution: resolution, Layout: layout, MIR: mir, BoundMIR: bound}, nil
}

func ExecuteVERT013b(snapshot frontendwire.ProgramSnapshot, identity bingo.CompilerBuildIdentity, plan buildplan.Plan, machine *llvmbackend.TargetMachine, runtimeManifest []byte) (VERT013bPipelineResult, error) {
	lowered, err := LowerVERT013b(snapshot, identity, plan, machine, runtimeManifest)
	if err != nil {
		return VERT013bPipelineResult{}, err
	}
	emission, err := machine.EmitVERT013bObject(lowered.BoundMIR)
	if err != nil {
		return VERT013bPipelineResult{}, fmt.Errorf("VERT-013b LLVM emission: %w", err)
	}
	return VERT013bPipelineResult{VERT013bLoweringResult: lowered, Emission: emission}, nil
}
