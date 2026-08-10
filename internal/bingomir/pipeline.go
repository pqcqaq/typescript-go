// Package bingomir implements the checker-free, target-aware first-slice MIR
// pipeline. It consumes only serialized frontend/HIR artifacts and resolved
// target proofs; no live AST or checker value crosses this boundary.
package bingomir

import (
	"bytes"
	"context"
	"fmt"
	"slices"

	"github.com/microsoft/typescript-go/internal/ast2bingo"
	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/buildplan"
	"github.com/microsoft/typescript-go/internal/frontendwire"
	"github.com/microsoft/typescript-go/internal/llvmbackend"
	"github.com/microsoft/typescript-go/internal/targetcontext"
)

type joinedInputs struct {
	typedHIR  ast2bingo.PrimitiveTypedHIRArtifact
	plan      buildplan.Plan
	context   targetcontext.TargetContext
	layout    llvmbackend.DataLayout
	catalog   targetcontext.AvailableCapabilityCatalog
	toolchain llvmbackend.ToolchainManifest
	runtime   targetcontext.RuntimeManifest
}

// ExecuteFirstSliceMIR runs the complete canonical prefix from a validated
// serialized snapshot through final verified number-only MIR.
func ExecuteFirstSliceMIR(
	ctx context.Context,
	snapshot frontendwire.ProgramSnapshot,
	identity bingo.CompilerBuildIdentity,
	plan buildplan.Plan,
	machine *llvmbackend.TargetMachine,
	runtimeManifest []byte,
) (bingo.FirstSliceMIRArtifact, bingo.PassExecution, error) {
	handlers, err := NewFirstSlicePassHandlers(identity, machine)
	if err != nil {
		return bingo.FirstSliceMIRArtifact{}, bingo.PassExecution{}, err
	}
	inputs, err := targetcontext.NewResolverInputArtifacts(plan, machine, runtimeManifest)
	if err != nil {
		return bingo.FirstSliceMIRArtifact{}, bingo.PassExecution{}, err
	}
	envelope, err := bingo.NewPassArtifactEnvelope(inputs...)
	if err != nil {
		return bingo.FirstSliceMIRArtifact{}, bingo.PassExecution{}, err
	}
	snapshotBytes, err := snapshot.CanonicalBytes()
	if err != nil {
		return bingo.FirstSliceMIRArtifact{}, bingo.PassExecution{}, err
	}
	executor, err := bingo.NewPassExecutor(handlers, 0)
	if err != nil {
		return bingo.FirstSliceMIRArtifact{}, bingo.PassExecution{}, err
	}
	initial := bingo.PassState{
		Schema: "snapshot-v2",
		Facts: []string{
			"canonical-build-plan",
			"module-graph",
			"property-facts",
			"runtime-manifest",
			"signatures",
			"symbols",
			"syntax",
			"toolchain-manifest",
			"type-arguments",
			"types",
		},
		Artifact:  snapshotBytes,
		Artifacts: &envelope,
	}
	execution, err := executor.Execute(ctx, initial)
	if err != nil {
		return bingo.FirstSliceMIRArtifact{}, execution, err
	}
	module, err := bingo.DecodeBoundFirstSliceMIR(execution.State.Artifact)
	if err != nil {
		return bingo.FirstSliceMIRArtifact{}, execution, fmt.Errorf("decode final first-slice MIR: %w", err)
	}
	return *module, execution, nil
}

// NewFirstSlicePassHandlers binds every real handler through the final MIR
// verifier. The resolver remains tied to the observed LLVM TargetMachine.
func NewFirstSlicePassHandlers(identity bingo.CompilerBuildIdentity, machine *llvmbackend.TargetMachine) (map[bingo.PassID]bingo.PassHandler, error) {
	handlers := ast2bingo.PrimitiveHIRPassHandlers(identity)
	for _, id := range []bingo.PassID{bingo.PassEvaluationOrder, bingo.PassSpecialization, bingo.PassVarianceAliasing} {
		handlers[id] = newHIRPreservingHandler(id, identity)
	}
	resolver, err := targetcontext.NewResolveTargetPassHandler(machine)
	if err != nil {
		return nil, err
	}
	handlers[bingo.PassResolveTarget] = resolver
	for id, handler := range targetAwareHandlers() {
		handlers[id] = handler
	}
	return handlers, nil
}

func targetAwareHandlers() map[bingo.PassID]bingo.PassHandler {
	return map[bingo.PassID]bingo.PassHandler{
		bingo.PassRepresentationPlan: representationPlanHandler(),
		bingo.PassMIRCFGSSA:          mirLoweringHandler(),
		bingo.PassCleanupState:       structuralMIRPreservingHandler(bingo.PassCleanupState),
		bingo.PassStructuralVerifier: structuralMIRPreservingHandler(bingo.PassStructuralVerifier),
		bingo.PassCapabilityBinding:  capabilityBindingHandler(),
		bingo.PassOptimizeProvenMIR:  boundMIRPreservingHandler(bingo.PassOptimizeProvenMIR),
		bingo.PassPlaceGCRoots:       boundMIRPreservingHandler(bingo.PassPlaceGCRoots),
		bingo.PassFinalVerifier:      boundMIRPreservingHandler(bingo.PassFinalVerifier),
	}
}

func newHIRPreservingHandler(id bingo.PassID, identity bingo.CompilerBuildIdentity) bingo.PassHandler {
	return bingo.PassHandler{
		PreVerify: func(ctx context.Context, spec bingo.PassSpec, iteration int, input bingo.PassState) error {
			if err := requirePass(ctx, spec, iteration, id); err != nil {
				return err
			}
			_, err := decodeVerifiedTypedHIR(input.Artifact, identity)
			return err
		},
		Run: func(ctx context.Context, spec bingo.PassSpec, iteration int, input bingo.PassState) (bingo.PassResult, error) {
			if err := requirePass(ctx, spec, iteration, id); err != nil {
				return bingo.PassResult{}, err
			}
			if _, err := decodeVerifiedTypedHIR(input.Artifact, identity); err != nil {
				return bingo.PassResult{}, err
			}
			return bingo.PassResult{State: advanceState(input, spec, input.Artifact)}, nil
		},
		PostVerify: func(ctx context.Context, spec bingo.PassSpec, iteration int, input, output bingo.PassState) (bingo.PassVerification, error) {
			if err := requirePass(ctx, spec, iteration, id); err != nil {
				return bingo.PassVerification{}, err
			}
			if _, err := decodeVerifiedTypedHIR(output.Artifact, identity); err != nil {
				return bingo.PassVerification{}, err
			}
			if !bytes.Equal(input.Artifact, output.Artifact) || !slices.Equal(output.Facts, mergedFacts(input.Facts, spec.WritesFacts)) {
				return bingo.PassVerification{}, fmt.Errorf("pass %q did not preserve canonical typed HIR", id)
			}
			if id == bingo.PassVarianceAliasing {
				artifact, err := requiredArtifact(output, bingo.PassArtifactTypedHIR, "hir-v3")
				if err != nil {
					return bingo.PassVerification{}, err
				}
				if !bytes.Equal(artifact.Payload, output.Artifact) {
					return bingo.PassVerification{}, fmt.Errorf("typed HIR sidecar differs from primary artifact")
				}
			}
			return bingo.PassVerification{}, nil
		},
	}
}

func representationPlanHandler() bingo.PassHandler {
	return bingo.PassHandler{
		PreVerify: func(ctx context.Context, spec bingo.PassSpec, iteration int, input bingo.PassState) error {
			if err := requirePass(ctx, spec, iteration, bingo.PassRepresentationPlan); err != nil {
				return err
			}
			_, err := decodeJoinedInputs(input)
			return err
		},
		Run: func(ctx context.Context, spec bingo.PassSpec, iteration int, input bingo.PassState) (bingo.PassResult, error) {
			if err := requirePass(ctx, spec, iteration, bingo.PassRepresentationPlan); err != nil {
				return bingo.PassResult{}, err
			}
			joined, err := decodeJoinedInputs(input)
			if err != nil {
				return bingo.PassResult{}, err
			}
			plan, err := representationPlan(joined)
			if err != nil {
				return bingo.PassResult{}, err
			}
			encoded, err := plan.CanonicalBytes()
			if err != nil {
				return bingo.PassResult{}, err
			}
			return bingo.PassResult{State: advanceState(input, spec, encoded)}, nil
		},
		PostVerify: func(ctx context.Context, spec bingo.PassSpec, iteration int, input, output bingo.PassState) (bingo.PassVerification, error) {
			if err := requirePass(ctx, spec, iteration, bingo.PassRepresentationPlan); err != nil {
				return bingo.PassVerification{}, err
			}
			joined, err := decodeJoinedInputs(input)
			if err != nil {
				return bingo.PassVerification{}, err
			}
			expected, err := representationPlan(joined)
			if err != nil {
				return bingo.PassVerification{}, err
			}
			expectedBytes, err := expected.CanonicalBytes()
			if err != nil {
				return bingo.PassVerification{}, err
			}
			actual, err := bingo.DecodeRepresentationPlan(output.Artifact)
			if err != nil {
				return bingo.PassVerification{}, err
			}
			if actual.ContentHash != expected.ContentHash {
				return bingo.PassVerification{}, fmt.Errorf("representation plan hash %s does not match verified join %s", actual.ContentHash, expected.ContentHash)
			}
			if !bytes.Equal(output.Artifact, expectedBytes) {
				return bingo.PassVerification{}, fmt.Errorf("representation plan bytes do not match the verified provenance join")
			}
			if !slices.Equal(output.Facts, mergedFacts(input.Facts, spec.WritesFacts)) {
				return bingo.PassVerification{}, fmt.Errorf("representation plan facts %v do not match canonical transition %v", output.Facts, mergedFacts(input.Facts, spec.WritesFacts))
			}
			artifact, err := requiredArtifact(output, bingo.PassArtifactRepresentationPlan, "rep-plan-v1")
			if err != nil {
				return bingo.PassVerification{}, err
			}
			if !bytes.Equal(artifact.Payload, output.Artifact) {
				return bingo.PassVerification{}, fmt.Errorf("representation plan sidecar differs from primary artifact")
			}
			return bingo.PassVerification{}, nil
		},
	}
}

func mirLoweringHandler() bingo.PassHandler {
	return bingo.PassHandler{
		PreVerify: func(ctx context.Context, spec bingo.PassSpec, iteration int, input bingo.PassState) error {
			if err := requirePass(ctx, spec, iteration, bingo.PassMIRCFGSSA); err != nil {
				return err
			}
			_, _, err := decodeMIRLoweringInputs(input)
			return err
		},
		Run: func(ctx context.Context, spec bingo.PassSpec, iteration int, input bingo.PassState) (bingo.PassResult, error) {
			if err := requirePass(ctx, spec, iteration, bingo.PassMIRCFGSSA); err != nil {
				return bingo.PassResult{}, err
			}
			hir, plan, err := decodeMIRLoweringInputs(input)
			if err != nil {
				return bingo.PassResult{}, err
			}
			module, err := bingo.LowerFirstSliceMIR(hir, plan)
			if err != nil {
				return bingo.PassResult{}, err
			}
			encoded, err := module.CanonicalStructuralBytes()
			if err != nil {
				return bingo.PassResult{}, err
			}
			return bingo.PassResult{State: advanceState(input, spec, encoded)}, nil
		},
		PostVerify: func(ctx context.Context, spec bingo.PassSpec, iteration int, input, output bingo.PassState) (bingo.PassVerification, error) {
			if err := requirePass(ctx, spec, iteration, bingo.PassMIRCFGSSA); err != nil {
				return bingo.PassVerification{}, err
			}
			hir, plan, err := decodeMIRLoweringInputs(input)
			if err != nil {
				return bingo.PassVerification{}, err
			}
			expected, err := bingo.LowerFirstSliceMIR(hir, plan)
			if err != nil {
				return bingo.PassVerification{}, err
			}
			expectedBytes, err := expected.CanonicalStructuralBytes()
			if err != nil {
				return bingo.PassVerification{}, err
			}
			actual, err := bingo.DecodeStructuralFirstSliceMIR(output.Artifact)
			if err != nil {
				return bingo.PassVerification{}, err
			}
			if actual.ContentHash != expected.ContentHash || !bytes.Equal(output.Artifact, expectedBytes) ||
				!slices.Equal(output.Facts, mergedFacts(input.Facts, spec.WritesFacts)) {
				return bingo.PassVerification{}, fmt.Errorf("MIR output does not match verified HIR and representation inputs")
			}
			return bingo.PassVerification{}, nil
		},
	}
}

func structuralMIRPreservingHandler(id bingo.PassID) bingo.PassHandler {
	return preservingMIRHandler(id, false)
}

func boundMIRPreservingHandler(id bingo.PassID) bingo.PassHandler {
	return preservingMIRHandler(id, true)
}

func preservingMIRHandler(id bingo.PassID, bound bool) bingo.PassHandler {
	verify := func(state bingo.PassState) error {
		return verifyMIRState(state, bound)
	}
	return bingo.PassHandler{
		PreVerify: func(ctx context.Context, spec bingo.PassSpec, iteration int, input bingo.PassState) error {
			if err := requirePass(ctx, spec, iteration, id); err != nil {
				return err
			}
			return verify(input)
		},
		Run: func(ctx context.Context, spec bingo.PassSpec, iteration int, input bingo.PassState) (bingo.PassResult, error) {
			if err := requirePass(ctx, spec, iteration, id); err != nil {
				return bingo.PassResult{}, err
			}
			if err := verify(input); err != nil {
				return bingo.PassResult{}, err
			}
			return bingo.PassResult{State: advanceState(input, spec, input.Artifact)}, nil
		},
		PostVerify: func(ctx context.Context, spec bingo.PassSpec, iteration int, input, output bingo.PassState) (bingo.PassVerification, error) {
			if err := requirePass(ctx, spec, iteration, id); err != nil {
				return bingo.PassVerification{}, err
			}
			if err := verify(output); err != nil {
				return bingo.PassVerification{}, err
			}
			if !bytes.Equal(input.Artifact, output.Artifact) || !slices.Equal(output.Facts, mergedFacts(input.Facts, spec.WritesFacts)) {
				return bingo.PassVerification{}, fmt.Errorf("pass %q changed verified first-slice MIR", id)
			}
			return bingo.PassVerification{}, nil
		},
	}
}

func capabilityBindingHandler() bingo.PassHandler {
	return bingo.PassHandler{
		PreVerify: func(ctx context.Context, spec bingo.PassSpec, iteration int, input bingo.PassState) error {
			if err := requirePass(ctx, spec, iteration, bingo.PassCapabilityBinding); err != nil {
				return err
			}
			return verifyCapabilityCatalogState(input, false)
		},
		Run: func(ctx context.Context, spec bingo.PassSpec, iteration int, input bingo.PassState) (bingo.PassResult, error) {
			if err := requirePass(ctx, spec, iteration, bingo.PassCapabilityBinding); err != nil {
				return bingo.PassResult{}, err
			}
			module, err := structuralMIRFromState(input)
			if err != nil {
				return bingo.PassResult{}, err
			}
			if err := verifyCapabilityCatalogState(input, false); err != nil {
				return bingo.PassResult{}, err
			}
			bound, err := bingo.BindFirstSliceCapabilities(module)
			if err != nil {
				return bingo.PassResult{}, err
			}
			encoded, err := bound.CanonicalBoundBytes()
			if err != nil {
				return bingo.PassResult{}, err
			}
			return bingo.PassResult{State: advanceState(input, spec, encoded)}, nil
		},
		PostVerify: func(ctx context.Context, spec bingo.PassSpec, iteration int, input, output bingo.PassState) (bingo.PassVerification, error) {
			if err := requirePass(ctx, spec, iteration, bingo.PassCapabilityBinding); err != nil {
				return bingo.PassVerification{}, err
			}
			structural, err := structuralMIRFromState(input)
			if err != nil {
				return bingo.PassVerification{}, err
			}
			expected, err := bingo.BindFirstSliceCapabilities(structural)
			if err != nil {
				return bingo.PassVerification{}, err
			}
			expectedBytes, err := expected.CanonicalBoundBytes()
			if err != nil {
				return bingo.PassVerification{}, err
			}
			actual, err := bingo.DecodeBoundFirstSliceMIR(output.Artifact)
			if err != nil {
				return bingo.PassVerification{}, err
			}
			if actual.ContentHash != expected.ContentHash || !bytes.Equal(output.Artifact, expectedBytes) ||
				!slices.Equal(output.Facts, mergedFacts(input.Facts, spec.WritesFacts)) {
				return bingo.PassVerification{}, fmt.Errorf("bound capability closure is not the MIR-derived closure")
			}
			if err := verifyCapabilityCatalogState(output, true); err != nil {
				return bingo.PassVerification{}, err
			}
			return bingo.PassVerification{}, nil
		},
	}
}

func decodeJoinedInputs(state bingo.PassState) (joinedInputs, error) {
	typedArtifact, err := requiredArtifact(state, bingo.PassArtifactTypedHIR, "hir-v3")
	if err != nil {
		return joinedInputs{}, err
	}
	planArtifact, err := requiredArtifact(state, bingo.PassArtifactBuildPlan, "build-plan-v1")
	if err != nil {
		return joinedInputs{}, err
	}
	contextArtifact, err := requiredArtifact(state, bingo.PassArtifactTargetContext, "target-context-v1")
	if err != nil {
		return joinedInputs{}, err
	}
	layoutArtifact, err := requiredArtifact(state, bingo.PassArtifactDataLayout, "data-layout-v1")
	if err != nil {
		return joinedInputs{}, err
	}
	catalogArtifact, err := requiredArtifact(state, bingo.PassArtifactAvailableCapabilityCatalog, "available-capability-catalog-v1")
	if err != nil {
		return joinedInputs{}, err
	}
	toolchainArtifact, err := requiredArtifact(state, bingo.PassArtifactToolchainManifest, "toolchain-manifest-v1")
	if err != nil {
		return joinedInputs{}, err
	}
	runtimeArtifact, err := requiredArtifact(state, bingo.PassArtifactRuntimeManifest, "runtime-manifest-v1")
	if err != nil {
		return joinedInputs{}, err
	}

	typed, err := ast2bingo.DecodePrimitiveTypedHIRArtifact(typedArtifact.Payload)
	if err != nil {
		return joinedInputs{}, err
	}
	if _, err := decodeVerifiedTypedHIR(typedArtifact.Payload, typed.CompilerBuildIdentity); err != nil {
		return joinedInputs{}, err
	}
	plan, err := buildplan.Decode(planArtifact.Payload)
	if err != nil {
		return joinedInputs{}, err
	}
	target, err := targetcontext.DecodeTargetContext(contextArtifact.Payload)
	if err != nil {
		return joinedInputs{}, err
	}
	if !bytes.Equal(contextArtifact.Payload, state.Artifact) {
		return joinedInputs{}, fmt.Errorf("target context primary and sidecar differ")
	}
	layout, err := llvmbackend.DecodeDataLayout(layoutArtifact.Payload)
	if err != nil {
		return joinedInputs{}, err
	}
	catalog, err := targetcontext.DecodeAvailableCapabilityCatalog(catalogArtifact.Payload)
	if err != nil {
		return joinedInputs{}, err
	}
	toolchain, err := llvmbackend.DecodeToolchainManifest(toolchainArtifact.Payload)
	if err != nil {
		return joinedInputs{}, err
	}
	runtime, err := targetcontext.DecodeRuntimeManifest(runtimeArtifact.Payload)
	if err != nil {
		return joinedInputs{}, err
	}

	joined := joinedInputs{typedHIR: typed, plan: *plan, context: *target, layout: *layout, catalog: *catalog, toolchain: *toolchain, runtime: *runtime}
	if err := verifyJoinedInputs(joined); err != nil {
		return joinedInputs{}, err
	}
	return joined, nil
}

func verifyJoinedInputs(joined joinedInputs) error {
	hir := joined.typedHIR.HIR
	if joined.typedHIR.FrontendSnapshotHash != hir.Provenance.FrontendSnapshotHash ||
		joined.typedHIR.CompilerBuildIdentity != hir.Provenance.CompilerBuildIdentity {
		return fmt.Errorf("typed HIR wrapper provenance does not match canonical HIR")
	}
	if joined.plan.FrontendHash != hir.Provenance.FrontendSnapshotHash ||
		joined.context.RequestHash != joined.plan.ContentHash || joined.context.FrontendHash != joined.plan.FrontendHash {
		return fmt.Errorf("HIR, BuildPlan, and TargetContext frontend/request provenance do not join")
	}
	if joined.context.DataLayoutHash != joined.layout.ContentHash || joined.context.LLVMDataLayout != joined.layout.LayoutString ||
		joined.toolchain.DataLayout.ContentHash != joined.layout.ContentHash {
		return fmt.Errorf("TargetContext, toolchain, and DataLayout identities do not join")
	}
	if joined.context.AvailableCapabilityCatalogHash != joined.catalog.ContentHash || joined.catalog.RequestHash != joined.plan.ContentHash {
		return fmt.Errorf("TargetContext and available capability catalog do not join")
	}
	if joined.context.ToolchainManifestHash != joined.toolchain.ContentHash || joined.catalog.ToolchainManifestHash != joined.toolchain.ContentHash {
		return fmt.Errorf("toolchain manifest identity does not join")
	}
	if joined.context.RuntimeManifestHash != joined.runtime.ContentHash || joined.catalog.RuntimeManifestHash != joined.runtime.ContentHash {
		return fmt.Errorf("runtime manifest identity does not join")
	}
	return nil
}

func representationPlan(joined joinedInputs) (bingo.RepresentationPlan, error) {
	provenance := bingo.TargetProvenance{
		HIRHash:                        joined.typedHIR.HIR.ContentHash,
		FrontendSnapshotHash:           joined.plan.FrontendHash,
		BuildPlanHash:                  joined.plan.ContentHash,
		CompilerBuildIdentity:          joined.typedHIR.CompilerBuildIdentity,
		TargetContextHash:              joined.context.ContentHash,
		DataLayoutHash:                 joined.layout.ContentHash,
		AvailableCapabilityCatalogHash: joined.catalog.ContentHash,
		ToolchainManifestHash:          joined.toolchain.ContentHash,
		RuntimeManifestHash:            joined.runtime.ContentHash,
	}
	return bingo.NewRepresentationPlanForHIR(provenance, joined.typedHIR.HIR)
}

func decodeMIRLoweringInputs(state bingo.PassState) (bingo.HIRModule, bingo.RepresentationPlan, error) {
	plan, err := bingo.DecodeRepresentationPlan(state.Artifact)
	if err != nil {
		return bingo.HIRModule{}, bingo.RepresentationPlan{}, err
	}
	planArtifact, err := requiredArtifact(state, bingo.PassArtifactRepresentationPlan, "rep-plan-v1")
	if err != nil {
		return bingo.HIRModule{}, bingo.RepresentationPlan{}, err
	}
	if !bytes.Equal(planArtifact.Payload, state.Artifact) {
		return bingo.HIRModule{}, bingo.RepresentationPlan{}, fmt.Errorf("representation plan primary and sidecar differ")
	}
	typedArtifact, err := requiredArtifact(state, bingo.PassArtifactTypedHIR, "hir-v3")
	if err != nil {
		return bingo.HIRModule{}, bingo.RepresentationPlan{}, err
	}
	typed, err := ast2bingo.DecodePrimitiveTypedHIRArtifact(typedArtifact.Payload)
	if err != nil {
		return bingo.HIRModule{}, bingo.RepresentationPlan{}, err
	}
	if _, err := decodeVerifiedTypedHIR(typedArtifact.Payload, typed.CompilerBuildIdentity); err != nil {
		return bingo.HIRModule{}, bingo.RepresentationPlan{}, err
	}
	if plan.Provenance.HIRHash != typed.HIR.ContentHash || plan.Provenance.FrontendSnapshotHash != typed.FrontendSnapshotHash ||
		plan.Provenance.CompilerBuildIdentity != typed.CompilerBuildIdentity {
		return bingo.HIRModule{}, bingo.RepresentationPlan{}, fmt.Errorf("representation plan does not join its typed HIR sidecar")
	}
	return typed.HIR, *plan, nil
}

func verifyMIRState(state bingo.PassState, bound bool) error {
	planArtifact, err := requiredArtifact(state, bingo.PassArtifactRepresentationPlan, "rep-plan-v1")
	if err != nil {
		return err
	}
	plan, err := bingo.DecodeRepresentationPlan(planArtifact.Payload)
	if err != nil {
		return err
	}
	var module *bingo.FirstSliceMIRArtifact
	if bound {
		module, err = bingo.DecodeBoundFirstSliceMIR(state.Artifact)
	} else {
		module, err = bingo.DecodeStructuralFirstSliceMIR(state.Artifact)
	}
	if err != nil {
		return err
	}
	if module.Provenance.TargetProvenance != plan.Provenance || module.Provenance.RepresentationPlanHash != plan.ContentHash {
		return fmt.Errorf("first-slice MIR provenance does not match its representation plan")
	}
	return nil
}

func verifyCapabilityCatalogState(state bingo.PassState, bound bool) error {
	if err := verifyMIRState(state, bound); err != nil {
		return err
	}
	catalogArtifact, err := requiredArtifact(state, bingo.PassArtifactAvailableCapabilityCatalog, "available-capability-catalog-v1")
	if err != nil {
		return err
	}
	catalog, err := targetcontext.DecodeAvailableCapabilityCatalog(catalogArtifact.Payload)
	if err != nil {
		return err
	}
	var module *bingo.FirstSliceMIRArtifact
	if bound {
		module, err = bingo.DecodeBoundFirstSliceMIR(state.Artifact)
	} else {
		module, err = bingo.DecodeStructuralFirstSliceMIR(state.Artifact)
	}
	if err != nil {
		return err
	}
	if module.Provenance.AvailableCapabilityCatalogHash != catalog.ContentHash {
		return fmt.Errorf("first-slice MIR provenance does not match the available capability catalog")
	}
	return nil
}

func structuralMIRFromState(state bingo.PassState) (bingo.FirstSliceMIRArtifact, error) {
	if err := verifyMIRState(state, false); err != nil {
		return bingo.FirstSliceMIRArtifact{}, err
	}
	module, err := bingo.DecodeStructuralFirstSliceMIR(state.Artifact)
	if err != nil {
		return bingo.FirstSliceMIRArtifact{}, err
	}
	return *module, nil
}

func decodeVerifiedTypedHIR(raw []byte, identity bingo.CompilerBuildIdentity) (ast2bingo.PrimitiveTypedHIRArtifact, error) {
	typed, err := ast2bingo.DecodePrimitiveTypedHIRArtifact(raw)
	if err != nil {
		return ast2bingo.PrimitiveTypedHIRArtifact{}, err
	}
	if typed.SchemaVersion != ast2bingo.PrimitiveTypedHIRSchemaVersion || typed.FrontendSnapshotHash != typed.HIR.Provenance.FrontendSnapshotHash ||
		typed.CompilerBuildIdentity != identity || typed.CompilerBuildIdentity != typed.HIR.Provenance.CompilerBuildIdentity {
		return ast2bingo.PrimitiveTypedHIRArtifact{}, fmt.Errorf("typed HIR wrapper provenance is invalid")
	}
	if err := verifyCanonicalPrimitiveHIR(typed.HIR); err != nil {
		return ast2bingo.PrimitiveTypedHIRArtifact{}, fmt.Errorf("verify canonical typed HIR: %w", err)
	}
	return typed, nil
}

func verifyCanonicalPrimitiveHIR(hir bingo.HIRModule) error {
	if len(hir.Functions) > 1 || (len(hir.Functions) == 1 && len(hir.Functions[0].Blocks) > 1) {
		return bingo.VerifyCanonicalPhase2HIR(hir)
	}
	return bingo.VerifyCanonicalHIR(hir)
}

func requiredArtifact(state bingo.PassState, name bingo.PassArtifactName, schema string) (bingo.PassArtifact, error) {
	artifact, ok := state.NamedArtifact(name)
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

func requirePass(ctx context.Context, spec bingo.PassSpec, iteration int, id bingo.PassID) error {
	if ctx == nil {
		return fmt.Errorf("pass context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if spec.ID != id || iteration != 1 {
		return fmt.Errorf("unexpected pass invocation: got %q iteration %d, want %q iteration 1", spec.ID, iteration, id)
	}
	return nil
}

func advanceState(input bingo.PassState, spec bingo.PassSpec, artifact []byte) bingo.PassState {
	return bingo.PassState{
		Schema:    spec.OutputSchema,
		Facts:     mergedFacts(input.Facts, spec.WritesFacts),
		Artifact:  slices.Clone(artifact),
		Artifacts: input.Artifacts,
	}
}

func mergedFacts(existing, writes []string) []string {
	result := append(slices.Clone(existing), writes...)
	slices.Sort(result)
	return slices.Compact(result)
}
