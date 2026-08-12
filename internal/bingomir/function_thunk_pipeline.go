package bingomir

import (
	"fmt"

	"github.com/microsoft/typescript-go/internal/ast2bingo"
	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/buildplan"
	"github.com/microsoft/typescript-go/internal/frontendwire"
	"github.com/microsoft/typescript-go/internal/llvmbackend"
)

type FunctionThunkLoweringResult struct {
	Replay      ast2bingo.FunctionThunkReplayResult
	HIR         bingo.FunctionThunkHIRArtifact
	MIR         bingo.FunctionThunkMIRArtifact
	BackendPlan llvmbackend.FunctionThunkBackendPlan
}

type FunctionThunkPipelineResult struct {
	FunctionThunkLoweringResult
	Emission llvmbackend.FunctionThunkEmission
}

func LowerFunctionThunk(snapshot frontendwire.ProgramSnapshot, identity bingo.CompilerBuildIdentity, plan buildplan.Plan, machine *llvmbackend.TargetMachine) (FunctionThunkLoweringResult, error) {
	replay, err := ast2bingo.ReplayFunctionThunkSnapshot(snapshot, identity)
	if err != nil {
		return FunctionThunkLoweringResult{}, fmt.Errorf("function thunk snapshot replay: %w", err)
	}
	return lowerFunctionThunkReplay(replay, identity, plan, machine)
}

func LowerFunctionThunkReplay(replayBytes []byte, identity bingo.CompilerBuildIdentity, plan buildplan.Plan, machine *llvmbackend.TargetMachine) (FunctionThunkLoweringResult, error) {
	replay, err := ast2bingo.DecodeFunctionThunkReplay(replayBytes)
	if err != nil {
		return FunctionThunkLoweringResult{}, fmt.Errorf("function thunk replay artifact: %w", err)
	}
	return lowerFunctionThunkReplay(*replay, identity, plan, machine)
}

func lowerFunctionThunkReplay(replay ast2bingo.FunctionThunkReplayResult, identity bingo.CompilerBuildIdentity, plan buildplan.Plan, machine *llvmbackend.TargetMachine) (FunctionThunkLoweringResult, error) {
	if err := bingo.ValidateCompilerBuildIdentity(identity); err != nil {
		return FunctionThunkLoweringResult{}, fmt.Errorf("function thunk compiler identity: %w", err)
	}
	if replay.CompilerBuildIdentity != identity {
		return FunctionThunkLoweringResult{}, fmt.Errorf("function thunk replay does not bind current compiler identity")
	}
	if err := buildplan.Validate(plan); err != nil {
		return FunctionThunkLoweringResult{}, fmt.Errorf("function thunk BuildPlan: %w", err)
	}
	if plan.FrontendHash != replay.FrontendSnapshotHash {
		return FunctionThunkLoweringResult{}, fmt.Errorf("function thunk BuildPlan does not bind frontend snapshot")
	}
	if plan.Profile != frontendwire.ProfileStatic {
		return FunctionThunkLoweringResult{}, fmt.Errorf("function thunk requires the static profile")
	}
	if machine == nil {
		return FunctionThunkLoweringResult{}, fmt.Errorf("function thunk target machine is nil")
	}
	manifest := machine.Manifest()
	if err := llvmbackend.ValidateToolchainManifest(manifest); err != nil {
		return FunctionThunkLoweringResult{}, fmt.Errorf("function thunk observed target: %w", err)
	}
	hir, err := ast2bingo.LowerFunctionThunkHIR(replay)
	if err != nil {
		return FunctionThunkLoweringResult{}, fmt.Errorf("function thunk HIR: %w", err)
	}
	mir, err := bingo.LowerFunctionThunkMIR(hir, manifest.TargetTriple, manifest.DataLayout.ContentHash)
	if err != nil {
		return FunctionThunkLoweringResult{}, fmt.Errorf("function thunk MIR: %w", err)
	}
	backendPlan, err := llvmbackend.BuildFunctionThunkBackendPlan(mir)
	if err != nil {
		return FunctionThunkLoweringResult{}, fmt.Errorf("function thunk backend planning: %w", err)
	}
	return FunctionThunkLoweringResult{Replay: replay, HIR: hir, MIR: mir, BackendPlan: backendPlan}, nil
}

func ExecuteFunctionThunk(snapshot frontendwire.ProgramSnapshot, identity bingo.CompilerBuildIdentity, plan buildplan.Plan, machine *llvmbackend.TargetMachine) (FunctionThunkPipelineResult, error) {
	lowered, err := LowerFunctionThunk(snapshot, identity, plan, machine)
	if err != nil {
		return FunctionThunkPipelineResult{}, err
	}
	emission, err := machine.EmitFunctionThunkObject(lowered.BackendPlan)
	if err != nil {
		return FunctionThunkPipelineResult{}, fmt.Errorf("function thunk LLVM emission: %w", err)
	}
	return FunctionThunkPipelineResult{FunctionThunkLoweringResult: lowered, Emission: emission}, nil
}

func ExecuteFunctionThunkReplay(replayBytes []byte, identity bingo.CompilerBuildIdentity, plan buildplan.Plan, machine *llvmbackend.TargetMachine) (FunctionThunkPipelineResult, error) {
	lowered, err := LowerFunctionThunkReplay(replayBytes, identity, plan, machine)
	if err != nil {
		return FunctionThunkPipelineResult{}, err
	}
	emission, err := machine.EmitFunctionThunkObject(lowered.BackendPlan)
	if err != nil {
		return FunctionThunkPipelineResult{}, fmt.Errorf("function thunk LLVM emission: %w", err)
	}
	return FunctionThunkPipelineResult{FunctionThunkLoweringResult: lowered, Emission: emission}, nil
}
