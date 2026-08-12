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

// ObjectLayoutCopyLoweringResult binds explicit-copy MIR to one observed
// target and its locked static runtime capability catalog.
type ObjectLayoutCopyLoweringResult struct {
	Replay      ast2bingo.ObjectLayoutCopyReplayResult
	Resolution  targetcontext.Resolution
	Bound       bingo.ObjectLayoutCopyBoundArtifact
	BackendPlan llvmbackend.ObjectLayoutCopyBackendPlan
}

type ObjectLayoutCopyPipelineResult struct {
	ObjectLayoutCopyLoweringResult
	Emission llvmbackend.ObjectLayoutCopyEmission
}

func LowerObjectLayoutCopy(snapshot frontendwire.ProgramSnapshot, identity bingo.CompilerBuildIdentity, plan buildplan.Plan, machine *llvmbackend.TargetMachine, runtimeManifest []byte) (ObjectLayoutCopyLoweringResult, error) {
	replay, err := ast2bingo.ReplayObjectLayoutCopySnapshot(snapshot, identity)
	if err != nil {
		return ObjectLayoutCopyLoweringResult{}, fmt.Errorf("object layout copy snapshot replay: %w", err)
	}
	return lowerObjectLayoutCopyReplay(replay, identity, plan, machine, runtimeManifest)
}

func LowerObjectLayoutCopyReplay(replayBytes []byte, identity bingo.CompilerBuildIdentity, plan buildplan.Plan, machine *llvmbackend.TargetMachine, runtimeManifest []byte) (ObjectLayoutCopyLoweringResult, error) {
	replay, err := ast2bingo.DecodeObjectLayoutCopyReplay(replayBytes)
	if err != nil {
		return ObjectLayoutCopyLoweringResult{}, fmt.Errorf("object layout copy replay artifact: %w", err)
	}
	return lowerObjectLayoutCopyReplay(*replay, identity, plan, machine, runtimeManifest)
}

func lowerObjectLayoutCopyReplay(replay ast2bingo.ObjectLayoutCopyReplayResult, identity bingo.CompilerBuildIdentity, plan buildplan.Plan, machine *llvmbackend.TargetMachine, runtimeManifest []byte) (ObjectLayoutCopyLoweringResult, error) {
	if err := bingo.ValidateCompilerBuildIdentity(identity); err != nil {
		return ObjectLayoutCopyLoweringResult{}, err
	}
	if replay.CompilerBuildIdentity != identity {
		return ObjectLayoutCopyLoweringResult{}, fmt.Errorf("object layout copy replay does not bind current compiler identity")
	}
	if plan.FrontendHash != replay.FrontendSnapshotHash {
		return ObjectLayoutCopyLoweringResult{}, fmt.Errorf("object layout copy BuildPlan does not bind frontend snapshot")
	}
	if plan.Profile != frontendwire.ProfileStatic {
		return ObjectLayoutCopyLoweringResult{}, fmt.Errorf("object layout copy requires the static profile")
	}
	if machine == nil {
		return ObjectLayoutCopyLoweringResult{}, fmt.Errorf("object layout copy target machine is nil")
	}
	resolution, err := targetcontext.ResolveTargetContext(plan, machine, runtimeManifest)
	if err != nil {
		return ObjectLayoutCopyLoweringResult{}, fmt.Errorf("object layout copy target resolution: %w", err)
	}
	if resolution.Context.FrontendHash != replay.FrontendSnapshotHash {
		return ObjectLayoutCopyLoweringResult{}, fmt.Errorf("object layout copy TargetContext does not bind frontend snapshot")
	}
	bound, err := targetcontext.BindObjectLayoutCopy(replay.MIR, resolution.Context, resolution.Catalog)
	if err != nil {
		return ObjectLayoutCopyLoweringResult{}, fmt.Errorf("object layout copy runtime binding: %w", err)
	}
	backendPlan, err := llvmbackend.BuildObjectLayoutCopyBackendPlan(bound)
	if err != nil {
		return ObjectLayoutCopyLoweringResult{}, fmt.Errorf("object layout copy backend planning: %w", err)
	}
	return ObjectLayoutCopyLoweringResult{replay, resolution, bound, backendPlan}, nil
}

func ExecuteObjectLayoutCopy(snapshot frontendwire.ProgramSnapshot, identity bingo.CompilerBuildIdentity, plan buildplan.Plan, machine *llvmbackend.TargetMachine, runtimeManifest []byte) (ObjectLayoutCopyPipelineResult, error) {
	lowered, err := LowerObjectLayoutCopy(snapshot, identity, plan, machine, runtimeManifest)
	if err != nil {
		return ObjectLayoutCopyPipelineResult{}, err
	}
	emission, err := machine.EmitObjectLayoutCopyObject(lowered.BackendPlan)
	if err != nil {
		return ObjectLayoutCopyPipelineResult{}, fmt.Errorf("object layout copy LLVM emission: %w", err)
	}
	return ObjectLayoutCopyPipelineResult{lowered, emission}, nil
}
