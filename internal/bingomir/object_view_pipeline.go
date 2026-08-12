package bingomir

import (
	"fmt"

	"github.com/microsoft/typescript-go/internal/ast2bingo"
	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/llvmbackend"
)

// ObjectViewPipelineResult preserves the complete source-to-backend OBJ-005
// chain. Emission is present only after ExecuteObjectView succeeds.
type ObjectViewPipelineResult struct {
	Replay   ast2bingo.ObjectViewReplayResult
	Plan     llvmbackend.ObjectViewBackendPlan
	Emission llvmbackend.ObjectViewEmission
}

func LowerObjectView(snapshot ast2bingo.ProgramSnapshot, identity bingo.CompilerBuildIdentity) (ObjectViewPipelineResult, error) {
	replay, err := ast2bingo.ReplayObjectViewSnapshot(snapshot, identity)
	if err != nil {
		return ObjectViewPipelineResult{}, fmt.Errorf("ObjectView snapshot replay: %w", err)
	}
	plan, err := llvmbackend.BuildObjectViewBackendPlan(replay.MIR)
	if err != nil {
		return ObjectViewPipelineResult{}, fmt.Errorf("ObjectView backend planning: %w", err)
	}
	return ObjectViewPipelineResult{Replay: replay, Plan: plan}, nil
}

func ExecuteObjectView(snapshot ast2bingo.ProgramSnapshot, identity bingo.CompilerBuildIdentity, machine *llvmbackend.TargetMachine) (ObjectViewPipelineResult, error) {
	if machine == nil {
		return ObjectViewPipelineResult{}, fmt.Errorf("ObjectView target machine is nil")
	}
	result, err := LowerObjectView(snapshot, identity)
	if err != nil {
		return ObjectViewPipelineResult{}, err
	}
	emission, err := machine.EmitObjectViewObject(result.Plan)
	if err != nil {
		return ObjectViewPipelineResult{}, fmt.Errorf("ObjectView LLVM emission: %w", err)
	}
	result.Emission = emission
	return result, nil
}
