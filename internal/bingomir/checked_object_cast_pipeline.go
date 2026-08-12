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

// CheckedObjectCastLoweringResult preserves the complete source-to-backend
// planning chain. Binding fails closed until the authoritative runtime
// manifest publishes rt.object.shape_matches.
type CheckedObjectCastLoweringResult struct {
	Replay      ast2bingo.CheckedObjectCastReplayResult
	Resolution  targetcontext.Resolution
	Bound       bingo.CheckedObjectCastBoundContract
	BackendPlan llvmbackend.CheckedObjectCastBackendPlan
}

type CheckedObjectCastPipelineResult struct {
	CheckedObjectCastLoweringResult
	Emission llvmbackend.CheckedObjectCastEmission
}

func LowerCheckedObjectCast(snapshot frontendwire.ProgramSnapshot, identity bingo.CompilerBuildIdentity, plan buildplan.Plan, machine *llvmbackend.TargetMachine, runtimeManifest []byte) (CheckedObjectCastLoweringResult, error) {
	replay, err := ast2bingo.ReplayCheckedObjectCastSnapshot(snapshot, identity)
	if err != nil {
		return CheckedObjectCastLoweringResult{}, fmt.Errorf("checked object cast snapshot replay: %w", err)
	}
	return lowerCheckedObjectCastReplay(replay, identity, plan, machine, runtimeManifest)
}

// LowerCheckedObjectCastReplay consumes the strict, self-contained replay
// artifact produced by emit-checked-cast-replay. The expected identity binds
// the artifact to the currently executing compiler, not merely its producer.
func LowerCheckedObjectCastReplay(replayBytes []byte, identity bingo.CompilerBuildIdentity, plan buildplan.Plan, machine *llvmbackend.TargetMachine, runtimeManifest []byte) (CheckedObjectCastLoweringResult, error) {
	replay, err := ast2bingo.DecodeCheckedObjectCastReplay(replayBytes)
	if err != nil {
		return CheckedObjectCastLoweringResult{}, fmt.Errorf("checked object cast replay artifact: %w", err)
	}
	return lowerCheckedObjectCastReplay(*replay, identity, plan, machine, runtimeManifest)
}

func lowerCheckedObjectCastReplay(replay ast2bingo.CheckedObjectCastReplayResult, identity bingo.CompilerBuildIdentity, plan buildplan.Plan, machine *llvmbackend.TargetMachine, runtimeManifest []byte) (CheckedObjectCastLoweringResult, error) {
	if err := bingo.ValidateCompilerBuildIdentity(identity); err != nil {
		return CheckedObjectCastLoweringResult{}, fmt.Errorf("checked object cast compiler identity: %w", err)
	}
	if replay.CompilerBuildIdentity != identity {
		return CheckedObjectCastLoweringResult{}, fmt.Errorf("checked object cast replay does not bind current compiler identity")
	}
	if plan.FrontendHash != replay.FrontendSnapshotHash {
		return CheckedObjectCastLoweringResult{}, fmt.Errorf("checked object cast BuildPlan does not bind frontend snapshot")
	}
	if plan.Profile != frontendwire.ProfileInterop {
		return CheckedObjectCastLoweringResult{}, fmt.Errorf("checked object cast requires the interop profile")
	}
	resolution, err := targetcontext.ResolveTargetContext(plan, machine, runtimeManifest)
	if err != nil {
		return CheckedObjectCastLoweringResult{}, fmt.Errorf("checked object cast target resolution: %w", err)
	}
	if resolution.Context.FrontendHash != replay.FrontendSnapshotHash {
		return CheckedObjectCastLoweringResult{}, fmt.Errorf("checked object cast TargetContext does not bind frontend snapshot")
	}
	bound, err := targetcontext.BindCheckedObjectCast(replay.Cast, resolution.Context, resolution.Catalog)
	if err != nil {
		return CheckedObjectCastLoweringResult{}, fmt.Errorf("checked object cast runtime binding: %w", err)
	}
	backendPlan, err := llvmbackend.BuildCheckedObjectCastBackendPlan(bound)
	if err != nil {
		return CheckedObjectCastLoweringResult{}, fmt.Errorf("checked object cast backend planning: %w", err)
	}
	return CheckedObjectCastLoweringResult{Replay: replay, Resolution: resolution, Bound: bound, BackendPlan: backendPlan}, nil
}

func ExecuteCheckedObjectCast(snapshot frontendwire.ProgramSnapshot, identity bingo.CompilerBuildIdentity, plan buildplan.Plan, machine *llvmbackend.TargetMachine, runtimeManifest []byte) (CheckedObjectCastPipelineResult, error) {
	lowered, err := LowerCheckedObjectCast(snapshot, identity, plan, machine, runtimeManifest)
	if err != nil {
		return CheckedObjectCastPipelineResult{}, err
	}
	emission, err := machine.EmitCheckedObjectCastObject(lowered.BackendPlan)
	if err != nil {
		return CheckedObjectCastPipelineResult{}, fmt.Errorf("checked object cast LLVM emission: %w", err)
	}
	return CheckedObjectCastPipelineResult{CheckedObjectCastLoweringResult: lowered, Emission: emission}, nil
}

func ExecuteCheckedObjectCastReplay(replayBytes []byte, identity bingo.CompilerBuildIdentity, plan buildplan.Plan, machine *llvmbackend.TargetMachine, runtimeManifest []byte) (CheckedObjectCastPipelineResult, error) {
	lowered, err := LowerCheckedObjectCastReplay(replayBytes, identity, plan, machine, runtimeManifest)
	if err != nil {
		return CheckedObjectCastPipelineResult{}, err
	}
	emission, err := machine.EmitCheckedObjectCastObject(lowered.BackendPlan)
	if err != nil {
		return CheckedObjectCastPipelineResult{}, fmt.Errorf("checked object cast LLVM emission: %w", err)
	}
	return CheckedObjectCastPipelineResult{CheckedObjectCastLoweringResult: lowered, Emission: emission}, nil
}
