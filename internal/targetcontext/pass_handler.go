package targetcontext

import (
	"bytes"
	"context"
	"fmt"
	"slices"

	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/buildplan"
	"github.com/microsoft/typescript-go/internal/llvmbackend"
)

// NewResolverInputArtifacts creates the three typed, immutable inputs consumed
// by PassResolveTarget from a real TargetMachine and locked runtime manifest.
func NewResolverInputArtifacts(plan buildplan.Plan, machine *llvmbackend.TargetMachine, runtimeManifest []byte) ([]bingo.PassArtifact, error) {
	if machine == nil {
		return nil, fmt.Errorf("target machine is nil")
	}
	manifest := machine.Manifest()
	if err := llvmbackend.ValidateToolchainManifest(manifest); err != nil {
		return nil, fmt.Errorf("observe LLVM target machine: %w", err)
	}
	return newResolverInputArtifacts(plan, manifest, runtimeManifest)
}

func newResolverInputArtifacts(plan buildplan.Plan, toolchain llvmbackend.ToolchainManifest, runtimeManifest []byte) ([]bingo.PassArtifact, error) {
	resolution, err := resolveTargetContext(plan, toolchain, runtimeManifest)
	if err != nil {
		return nil, err
	}
	planBytes, err := plan.CanonicalBytes()
	if err != nil {
		return nil, err
	}
	toolchainBytes, err := resolution.Toolchain.CanonicalBytes()
	if err != nil {
		return nil, err
	}
	runtimeBytes, err := resolution.Runtime.CanonicalBytes()
	if err != nil {
		return nil, err
	}
	inputs := []struct {
		name    bingo.PassArtifactName
		schema  string
		payload []byte
	}{
		{bingo.PassArtifactBuildPlan, "build-plan-v1", planBytes},
		{bingo.PassArtifactRuntimeManifest, "runtime-manifest-v1", runtimeBytes},
		{bingo.PassArtifactToolchainManifest, "toolchain-manifest-v1", toolchainBytes},
	}
	artifacts := make([]bingo.PassArtifact, len(inputs))
	for index, input := range inputs {
		artifact, artifactErr := bingo.NewPassArtifact(input.name, input.schema, input.payload)
		if artifactErr != nil {
			return nil, artifactErr
		}
		artifacts[index] = artifact
	}
	return artifacts, nil
}

// NewResolveTargetPassHandler binds the canonical resolver pass to an observed
// LLVM TargetMachine. Unit tests use the private manifest constructor; product
// code cannot instantiate the handler from an unobserved DataLayout.
func NewResolveTargetPassHandler(machine *llvmbackend.TargetMachine) (bingo.PassHandler, error) {
	if machine == nil {
		return bingo.PassHandler{}, fmt.Errorf("target machine is nil")
	}
	manifest := machine.Manifest()
	if err := llvmbackend.ValidateToolchainManifest(manifest); err != nil {
		return bingo.PassHandler{}, fmt.Errorf("observe LLVM target machine: %w", err)
	}
	return newResolveTargetPassHandler(manifest)
}

func newResolveTargetPassHandler(observed llvmbackend.ToolchainManifest) (bingo.PassHandler, error) {
	if err := llvmbackend.ValidateToolchainManifest(observed); err != nil {
		return bingo.PassHandler{}, err
	}
	return bingo.PassHandler{
		PreVerify: func(ctx context.Context, spec bingo.PassSpec, iteration int, input bingo.PassState) error {
			if err := requireResolveTargetPass(ctx, spec, iteration, input); err != nil {
				return err
			}
			_, err := resolvePassInput(input, observed)
			return err
		},
		Run: func(ctx context.Context, spec bingo.PassSpec, iteration int, input bingo.PassState) (bingo.PassResult, error) {
			if err := requireResolveTargetPass(ctx, spec, iteration, input); err != nil {
				return bingo.PassResult{}, err
			}
			resolution, err := resolvePassInput(input, observed)
			if err != nil {
				return bingo.PassResult{}, err
			}
			output, err := resolvedPassState(input, spec, resolution)
			if err != nil {
				return bingo.PassResult{}, err
			}
			return bingo.PassResult{State: output}, nil
		},
		PostVerify: func(ctx context.Context, spec bingo.PassSpec, iteration int, input, output bingo.PassState) (bingo.PassVerification, error) {
			if err := requireResolveTargetPass(ctx, spec, iteration, input); err != nil {
				return bingo.PassVerification{}, err
			}
			resolution, err := resolvePassInput(input, observed)
			if err != nil {
				return bingo.PassVerification{}, err
			}
			if err := verifyResolvedPassState(input, output, spec, resolution); err != nil {
				return bingo.PassVerification{}, err
			}
			return bingo.PassVerification{}, nil
		},
	}, nil
}

func requireResolveTargetPass(ctx context.Context, spec bingo.PassSpec, iteration int, input bingo.PassState) error {
	if ctx == nil {
		return fmt.Errorf("pass context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if spec.ID != bingo.PassResolveTarget || spec.InputSchema != "hir-v6" || spec.OutputSchema != "target-context-v1" || iteration != 1 {
		return fmt.Errorf("unexpected resolve-target pass invocation: id=%s input=%s output=%s iteration=%d", spec.ID, spec.InputSchema, spec.OutputSchema, iteration)
	}
	if input.Schema != spec.InputSchema {
		return fmt.Errorf("resolve-target input schema is %q, want %q", input.Schema, spec.InputSchema)
	}
	return nil
}

func resolvePassInput(input bingo.PassState, observed llvmbackend.ToolchainManifest) (Resolution, error) {
	planArtifact, err := requiredArtifact(input, bingo.PassArtifactBuildPlan, "build-plan-v1")
	if err != nil {
		return Resolution{}, err
	}
	runtimeArtifact, err := requiredArtifact(input, bingo.PassArtifactRuntimeManifest, "runtime-manifest-v1")
	if err != nil {
		return Resolution{}, err
	}
	toolchainArtifact, err := requiredArtifact(input, bingo.PassArtifactToolchainManifest, "toolchain-manifest-v1")
	if err != nil {
		return Resolution{}, err
	}
	plan, err := buildplan.Decode(planArtifact.Payload)
	if err != nil {
		return Resolution{}, err
	}
	toolchain, err := llvmbackend.DecodeToolchainManifest(toolchainArtifact.Payload)
	if err != nil {
		return Resolution{}, err
	}
	if !equalCanonical(*toolchain, observed) {
		return Resolution{}, fmt.Errorf("toolchain manifest artifact does not match observed LLVM TargetMachine")
	}
	return resolveTargetContext(*plan, observed, runtimeArtifact.Payload)
}

func requiredArtifact(input bingo.PassState, name bingo.PassArtifactName, schema string) (bingo.PassArtifact, error) {
	artifact, ok := input.NamedArtifact(name)
	if !ok {
		return bingo.PassArtifact{}, fmt.Errorf("required pass artifact %q is missing", name)
	}
	if artifact.Schema != schema {
		return bingo.PassArtifact{}, fmt.Errorf("pass artifact %q schema is %q, want %q", name, artifact.Schema, schema)
	}
	if _, err := artifact.CanonicalBytes(); err != nil {
		return bingo.PassArtifact{}, err
	}
	return artifact, nil
}

func resolvedPassState(input bingo.PassState, spec bingo.PassSpec, resolution Resolution) (bingo.PassState, error) {
	contextBytes, err := resolution.Context.CanonicalBytes()
	if err != nil {
		return bingo.PassState{}, err
	}
	layoutBytes, err := resolution.DataLayout.CanonicalBytes()
	if err != nil {
		return bingo.PassState{}, err
	}
	catalogBytes, err := resolution.Catalog.CanonicalBytes()
	if err != nil {
		return bingo.PassState{}, err
	}
	writes := []struct {
		name    bingo.PassArtifactName
		schema  string
		payload []byte
	}{
		{bingo.PassArtifactTargetContext, "target-context-v1", contextBytes},
		{bingo.PassArtifactDataLayout, "data-layout-v1", layoutBytes},
		{bingo.PassArtifactAvailableCapabilityCatalog, "available-capability-catalog-v1", catalogBytes},
	}
	artifacts := make([]bingo.PassArtifact, len(writes))
	for index, write := range writes {
		artifact, artifactErr := bingo.NewPassArtifact(write.name, write.schema, write.payload)
		if artifactErr != nil {
			return bingo.PassState{}, artifactErr
		}
		artifacts[index] = artifact
	}
	output := bingo.PassState{Schema: spec.OutputSchema, Facts: mergeFacts(input.Facts, spec.WritesFacts), Artifact: contextBytes, Artifacts: input.Artifacts}
	return output.WithNamedArtifacts(artifacts...)
}

func verifyResolvedPassState(input, output bingo.PassState, spec bingo.PassSpec, resolution Resolution) error {
	if output.Schema != spec.OutputSchema {
		return fmt.Errorf("resolved output schema is %q, want %q", output.Schema, spec.OutputSchema)
	}
	if !slices.Equal(output.Facts, mergeFacts(input.Facts, spec.WritesFacts)) {
		return fmt.Errorf("resolved output facts do not match canonical transition")
	}
	expected, err := resolvedPassState(input, spec, resolution)
	if err != nil {
		return err
	}
	if !bytes.Equal(output.Artifact, expected.Artifact) {
		return fmt.Errorf("target context primary artifact does not match resolver output")
	}
	if output.Artifacts == nil || expected.Artifacts == nil || output.Artifacts.Digest != expected.Artifacts.Digest {
		return fmt.Errorf("resolved artifact envelope does not match resolver output")
	}
	for _, name := range []bingo.PassArtifactName{bingo.PassArtifactTargetContext, bingo.PassArtifactDataLayout, bingo.PassArtifactAvailableCapabilityCatalog} {
		actual, actualOK := output.NamedArtifact(name)
		want, wantOK := expected.NamedArtifact(name)
		if !actualOK || !wantOK || actual.Digest != want.Digest || actual.Schema != want.Schema || !bytes.Equal(actual.Payload, want.Payload) {
			return fmt.Errorf("resolved artifact %q does not match resolver output", name)
		}
	}
	return nil
}

func mergeFacts(existing, writes []string) []string {
	result := append(slices.Clone(existing), writes...)
	slices.Sort(result)
	return slices.Compact(result)
}
