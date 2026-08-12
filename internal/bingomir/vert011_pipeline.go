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

// VERT011PipelineResult preserves each verified property-place artifact.
type VERT011PipelineResult struct {
	Replay     ast2bingo.VERT011ReplayResult
	Resolution targetcontext.Resolution
	Layout     bingo.ObjectLayoutContract
	MIR        bingo.VERT011MIRModule
	BoundMIR   bingo.VERT011BoundMIR
	Emission   llvmbackend.VERT011Emission
}

// ExecuteVERT011 lowers one validated propertyNullishAssign snapshot through
// HIR v10, MIR v8, target binding, and LLVM object emission.
func ExecuteVERT011(snapshot frontendwire.ProgramSnapshot, identity bingo.CompilerBuildIdentity, plan buildplan.Plan, machine *llvmbackend.TargetMachine, runtimeManifest []byte) (VERT011PipelineResult, error) {
	if machine == nil {
		return VERT011PipelineResult{}, fmt.Errorf("VERT-011 target machine is nil")
	}
	replay, err := ast2bingo.ReplayVERT011Snapshot(snapshot, identity)
	if err != nil {
		return VERT011PipelineResult{}, fmt.Errorf("VERT-011 snapshot replay: %w", err)
	}
	resolution, err := targetcontext.ResolveTargetContext(plan, machine, runtimeManifest)
	if err != nil {
		return VERT011PipelineResult{}, fmt.Errorf("VERT-011 target resolution: %w", err)
	}
	if plan.FrontendHash != replay.FrontendSnapshotHash || resolution.Context.FrontendHash != replay.FrontendSnapshotHash {
		return VERT011PipelineResult{}, fmt.Errorf("VERT-011 frontend identity does not match build plan and target context")
	}
	target, err := bingo.CanonicalObjectLayoutTarget(resolution.Context.Triple)
	if err != nil {
		return VERT011PipelineResult{}, err
	}
	place := replay.HIR.PlaceRefs.Places[0]
	layout, err := bingo.PlanObjectLayout(place.ObjectTypeKey, target, []bingo.ObjectLayoutPropertyInput{
		{Key: place.BackingPropertyKey, Kind: bingo.ObjectPropertyData, Representation: "nullable-f64"},
		{Key: place.PropertyKey, Kind: bingo.ObjectPropertyAccessor},
	})
	if err != nil {
		return VERT011PipelineResult{}, fmt.Errorf("VERT-011 object layout: %w", err)
	}
	mir, err := bingo.LowerVERT011MIR(replay.HIR, layout)
	if err != nil {
		return VERT011PipelineResult{}, fmt.Errorf("VERT-011 MIR lowering: %w", err)
	}
	bound, err := targetcontext.BindVERT011MIR(mir, resolution.Context, resolution.Catalog)
	if err != nil {
		return VERT011PipelineResult{}, fmt.Errorf("VERT-011 capability binding: %w", err)
	}
	emission, err := machine.EmitVERT011Object(bound)
	if err != nil {
		return VERT011PipelineResult{}, fmt.Errorf("VERT-011 LLVM emission: %w", err)
	}
	return VERT011PipelineResult{Replay: replay, Resolution: resolution, Layout: layout, MIR: mir, BoundMIR: bound, Emission: emission}, nil
}
