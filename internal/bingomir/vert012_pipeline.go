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

type VERT012PipelineResult struct {
	Replay            ast2bingo.VERT012ReplayResult
	Resolution        targetcontext.Resolution
	CellLayout        bingo.ObjectLayoutContract
	EnvironmentLayout bingo.ObjectLayoutContract
	MIR               bingo.VERT012MIRModule
	BoundMIR          bingo.VERT012BoundMIR
	Emission          llvmbackend.VERT012Emission
}

// ExecuteVERT012 runs the canonical closure snapshot through verified ELF object emission.
func ExecuteVERT012(snapshot frontendwire.ProgramSnapshot, identity bingo.CompilerBuildIdentity, plan buildplan.Plan, machine *llvmbackend.TargetMachine, runtimeManifest []byte) (VERT012PipelineResult, error) {
	if machine == nil {
		return VERT012PipelineResult{}, fmt.Errorf("VERT-012 target machine is nil")
	}
	replay, err := ast2bingo.ReplayVERT012Snapshot(snapshot, identity)
	if err != nil {
		return VERT012PipelineResult{}, fmt.Errorf("VERT-012 snapshot replay: %w", err)
	}
	resolution, err := targetcontext.ResolveTargetContext(plan, machine, runtimeManifest)
	if err != nil {
		return VERT012PipelineResult{}, fmt.Errorf("VERT-012 target resolution: %w", err)
	}
	if plan.FrontendHash != replay.FrontendSnapshotHash || resolution.Context.FrontendHash != replay.FrontendSnapshotHash {
		return VERT012PipelineResult{}, fmt.Errorf("VERT-012 frontend identity does not match build plan and target context")
	}
	target, err := bingo.CanonicalObjectLayoutTarget(resolution.Context.Triple)
	if err != nil {
		return VERT012PipelineResult{}, err
	}
	cellKey, environmentKey := bingo.VERT012LayoutTypeKeys(replay.Contract.ContentHash)
	cell, err := bingo.PlanObjectLayout(cellKey, target, []bingo.ObjectLayoutPropertyInput{{Key: "value", Kind: bingo.ObjectPropertyData, Representation: "f64"}})
	if err != nil {
		return VERT012PipelineResult{}, fmt.Errorf("VERT-012 cell layout: %w", err)
	}
	environment, err := bingo.PlanObjectLayout(environmentKey, target, []bingo.ObjectLayoutPropertyInput{{Key: "cell", Kind: bingo.ObjectPropertyData, Representation: "gc-ref", Reference: true}})
	if err != nil {
		return VERT012PipelineResult{}, fmt.Errorf("VERT-012 environment layout: %w", err)
	}
	mir, err := bingo.LowerVERT012MIR(replay.HIR, cell, environment)
	if err != nil {
		return VERT012PipelineResult{}, fmt.Errorf("VERT-012 MIR lowering: %w", err)
	}
	bound, err := targetcontext.BindVERT012MIR(mir, resolution.Context, resolution.Catalog)
	if err != nil {
		return VERT012PipelineResult{}, fmt.Errorf("VERT-012 capability binding: %w", err)
	}
	emission, err := machine.EmitVERT012Object(bound)
	if err != nil {
		return VERT012PipelineResult{}, fmt.Errorf("VERT-012 LLVM emission: %w", err)
	}
	return VERT012PipelineResult{Replay: replay, Resolution: resolution, CellLayout: cell, EnvironmentLayout: environment, MIR: mir, BoundMIR: bound, Emission: emission}, nil
}
