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

// VERT013aLoweringResult preserves the verified class artifacts up to the
// target-bound MIR boundary. LLVM emission is deliberately a later gate.
type VERT013aLoweringResult struct {
	Replay     ast2bingo.VERT013aReplayResult
	Resolution targetcontext.Resolution
	Layout     bingo.ObjectLayoutContract
	MIR        bingo.VERT013aMIRModule
	BoundMIR   bingo.VERT013aBoundMIR
}

type VERT013aPipelineResult struct {
	VERT013aLoweringResult
	Emission llvmbackend.VERT013aEmission
}

// LowerVERT013a runs the canonical base-class snapshot through target binding.
func LowerVERT013a(snapshot frontendwire.ProgramSnapshot, identity bingo.CompilerBuildIdentity, plan buildplan.Plan, machine *llvmbackend.TargetMachine, runtimeManifest []byte) (VERT013aLoweringResult, error) {
	if machine == nil {
		return VERT013aLoweringResult{}, fmt.Errorf("VERT-013a target machine is nil")
	}
	replay, err := ast2bingo.ReplayVERT013aSnapshot(snapshot, identity)
	if err != nil {
		return VERT013aLoweringResult{}, fmt.Errorf("VERT-013a snapshot replay: %w", err)
	}
	resolution, err := targetcontext.ResolveTargetContext(plan, machine, runtimeManifest)
	if err != nil {
		return VERT013aLoweringResult{}, fmt.Errorf("VERT-013a target resolution: %w", err)
	}
	if plan.FrontendHash != replay.FrontendSnapshotHash || resolution.Context.FrontendHash != replay.FrontendSnapshotHash {
		return VERT013aLoweringResult{}, fmt.Errorf("VERT-013a frontend identity does not match build plan and target context")
	}
	target, err := bingo.CanonicalObjectLayoutTarget(resolution.Context.Triple)
	if err != nil {
		return VERT013aLoweringResult{}, err
	}
	layout, err := bingo.PlanObjectLayout(replay.Contract.Classes[0].InstanceTypeKey, target, []bingo.ObjectLayoutPropertyInput{{Key: "value", Kind: bingo.ObjectPropertyData, Representation: "f64"}})
	if err != nil {
		return VERT013aLoweringResult{}, fmt.Errorf("VERT-013a instance layout: %w", err)
	}
	mir, err := bingo.LowerVERT013aMIR(replay.HIR, layout)
	if err != nil {
		return VERT013aLoweringResult{}, fmt.Errorf("VERT-013a MIR lowering: %w", err)
	}
	bound, err := targetcontext.BindVERT013aMIR(mir, resolution.Context, resolution.Catalog)
	if err != nil {
		return VERT013aLoweringResult{}, fmt.Errorf("VERT-013a capability binding: %w", err)
	}
	return VERT013aLoweringResult{Replay: replay, Resolution: resolution, Layout: layout, MIR: mir, BoundMIR: bound}, nil
}

// ExecuteVERT013a extends the verified lowering chain through LLVM object emission.
func ExecuteVERT013a(snapshot frontendwire.ProgramSnapshot, identity bingo.CompilerBuildIdentity, plan buildplan.Plan, machine *llvmbackend.TargetMachine, runtimeManifest []byte) (VERT013aPipelineResult, error) {
	lowered, err := LowerVERT013a(snapshot, identity, plan, machine, runtimeManifest)
	if err != nil {
		return VERT013aPipelineResult{}, err
	}
	emission, err := machine.EmitVERT013aObject(lowered.BoundMIR)
	if err != nil {
		return VERT013aPipelineResult{}, fmt.Errorf("VERT-013a LLVM emission: %w", err)
	}
	return VERT013aPipelineResult{VERT013aLoweringResult: lowered, Emission: emission}, nil
}
