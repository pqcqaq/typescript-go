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

// VERT010PipelineResult preserves every independently verified artifact in the
// first owned-object source-to-object pipeline.
type VERT010PipelineResult struct {
	Replay     ast2bingo.VERT010ReplayResult
	Resolution targetcontext.Resolution
	Layout     bingo.ObjectLayoutContract
	MIR        bingo.VERT010MIRModule
	BoundMIR   bingo.VERT010BoundMIR
	Emission   llvmbackend.VERT010Emission
}

// ExecuteVERT010 lowers one validated objectAlias snapshot through the
// observed target machine without admitting HIR v9 or MIR v7 to primitive
// artifact readers.
func ExecuteVERT010(
	snapshot frontendwire.ProgramSnapshot,
	identity bingo.CompilerBuildIdentity,
	plan buildplan.Plan,
	machine *llvmbackend.TargetMachine,
	runtimeManifest []byte,
) (VERT010PipelineResult, error) {
	if machine == nil {
		return VERT010PipelineResult{}, fmt.Errorf("VERT-010 target machine is nil")
	}
	replay, err := ast2bingo.ReplayVERT010Snapshot(snapshot, identity)
	if err != nil {
		return VERT010PipelineResult{}, fmt.Errorf("VERT-010 snapshot replay: %w", err)
	}
	resolution, err := targetcontext.ResolveTargetContext(plan, machine, runtimeManifest)
	if err != nil {
		return VERT010PipelineResult{}, fmt.Errorf("VERT-010 target resolution: %w", err)
	}
	if plan.FrontendHash != replay.FrontendSnapshotHash || resolution.Context.FrontendHash != replay.FrontendSnapshotHash {
		return VERT010PipelineResult{}, fmt.Errorf("VERT-010 frontend identity does not match build plan and target context")
	}
	target, err := bingo.CanonicalObjectLayoutTarget(resolution.Context.Triple)
	if err != nil {
		return VERT010PipelineResult{}, err
	}
	objectType := replay.HIR.ObjectTypes[0]
	layout, err := bingo.PlanObjectLayout(objectType.TypeKey, target, []bingo.ObjectLayoutPropertyInput{{Key: "value", Kind: bingo.ObjectPropertyData, Representation: "f64"}})
	if err != nil {
		return VERT010PipelineResult{}, fmt.Errorf("VERT-010 object layout: %w", err)
	}
	mir, err := bingo.LowerVERT010MIR(replay.HIR, layout)
	if err != nil {
		return VERT010PipelineResult{}, fmt.Errorf("VERT-010 MIR lowering: %w", err)
	}
	bound, err := targetcontext.BindVERT010MIR(mir, resolution.Context, resolution.Catalog)
	if err != nil {
		return VERT010PipelineResult{}, fmt.Errorf("VERT-010 capability binding: %w", err)
	}
	emission, err := machine.EmitVERT010Object(bound)
	if err != nil {
		return VERT010PipelineResult{}, fmt.Errorf("VERT-010 LLVM emission: %w", err)
	}
	return VERT010PipelineResult{Replay: replay, Resolution: resolution, Layout: layout, MIR: mir, BoundMIR: bound, Emission: emission}, nil
}
