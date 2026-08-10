package bingo

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestPassArtifactEnvelopeCanonicalizesAndBindsRoleSchemaAndPayload(t *testing.T) {
	hir, err := NewPassArtifact(PassArtifactTypedHIR, "hir-v5", json.RawMessage(`{ "z": 1, "a": 2 }`))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := NewPassArtifact(PassArtifactBuildPlan, "build-plan-v1", json.RawMessage(`{"target":"x86_64-unknown-linux-gnu"}`))
	if err != nil {
		t.Fatal(err)
	}
	first, err := NewPassArtifactEnvelope(hir, plan)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewPassArtifactEnvelope(plan, hir)
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, err := first.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := second.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatalf("envelope order is not canonical:\nfirst=%s\nsecond=%s", firstBytes, secondBytes)
	}
	if len(first.Digest) != 64 || len(hir.Digest) != 64 || len(plan.Digest) != 64 {
		t.Fatalf("incomplete digests: envelope=%q HIR=%q plan=%q", first.Digest, hir.Digest, plan.Digest)
	}
	storedHIR, ok := first.Artifact(PassArtifactTypedHIR)
	if !ok || string(storedHIR.Payload) != `{"a":2,"z":1}` {
		t.Fatalf("canonical HIR artifact = %#v", storedHIR)
	}

	otherRole, err := NewPassArtifact(PassArtifactRepresentationPlan, "hir-v5", hir.Payload)
	if err != nil {
		t.Fatal(err)
	}
	otherSchema, err := NewPassArtifact(PassArtifactTypedHIR, "hir-v3", hir.Payload)
	if err != nil {
		t.Fatal(err)
	}
	if hir.Digest == otherRole.Digest || hir.Digest == otherSchema.Digest {
		t.Fatal("artifact digest does not bind semantic name and schema")
	}
}

func TestPassArtifactEnvelopeRejectsTamperingAndDuplicateNames(t *testing.T) {
	artifact, err := NewPassArtifact(PassArtifactBuildPlan, "build-plan-v1", json.RawMessage(`{"target":"linux"}`))
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := NewPassArtifactEnvelope(artifact)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("payload", func(t *testing.T) {
		tampered := *clonePassArtifactEnvelope(&envelope)
		tampered.Artifacts[0].Payload = json.RawMessage(`{"target":"windows"}`)
		if _, err := tampered.CanonicalBytes(); err == nil || !strings.Contains(err.Error(), "artifact") || !strings.Contains(err.Error(), "digest mismatch") {
			t.Fatalf("payload tamper error = %v", err)
		}
	})

	t.Run("schema", func(t *testing.T) {
		tampered := *clonePassArtifactEnvelope(&envelope)
		tampered.Artifacts[0].Schema = "build-plan-v2"
		if _, err := tampered.CanonicalBytes(); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
			t.Fatalf("schema tamper error = %v", err)
		}
	})

	t.Run("envelope digest", func(t *testing.T) {
		tampered := *clonePassArtifactEnvelope(&envelope)
		tampered.Digest = strings.Repeat("0", 64)
		if _, err := tampered.CanonicalBytes(); err == nil || !strings.Contains(err.Error(), "envelope digest mismatch") {
			t.Fatalf("envelope tamper error = %v", err)
		}
	})

	t.Run("duplicate name", func(t *testing.T) {
		duplicate, err := NewPassArtifact(PassArtifactBuildPlan, "build-plan-v2", json.RawMessage(`{"target":"linux"}`))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := NewPassArtifactEnvelope(artifact, duplicate); err == nil || !strings.Contains(err.Error(), "duplicate pass artifact") {
			t.Fatalf("duplicate error = %v", err)
		}
	})

	if _, err := NewPassArtifact(PassArtifactBuildPlan, "build-plan-v1", json.RawMessage(`{`)); err == nil {
		t.Fatal("invalid JSON artifact was accepted")
	}
}

func TestTargetPassMetadataRequiresTypedArtifacts(t *testing.T) {
	specs := PassSpecs()
	resolve := specs[slices.IndexFunc(specs, func(spec PassSpec) bool { return spec.ID == PassResolveTarget })]
	representation := specs[slices.IndexFunc(specs, func(spec PassSpec) bool { return spec.ID == PassRepresentationPlan })]
	mir := specs[slices.IndexFunc(specs, func(spec PassSpec) bool { return spec.ID == PassMIRCFGSSA })]
	capability := specs[slices.IndexFunc(specs, func(spec PassSpec) bool { return spec.ID == PassCapabilityBinding })]

	assertRequirements := func(t *testing.T, got []PassArtifactRequirement, want map[PassArtifactName]string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("artifact requirements = %#v, want %#v", got, want)
		}
		for _, requirement := range got {
			if schema, ok := want[requirement.Name]; !ok || schema != requirement.Schema {
				t.Fatalf("unexpected artifact requirement %#v in %#v", requirement, got)
			}
		}
	}
	assertRequirements(t, resolve.ReadsArtifacts, map[PassArtifactName]string{
		PassArtifactBuildPlan:         "build-plan-v1",
		PassArtifactRuntimeManifest:   "runtime-manifest-v1",
		PassArtifactToolchainManifest: "toolchain-manifest-v1",
	})
	assertRequirements(t, representation.ReadsArtifacts, map[PassArtifactName]string{
		PassArtifactTypedHIR:                   "hir-v5",
		PassArtifactBuildPlan:                  "build-plan-v1",
		PassArtifactRuntimeManifest:            "runtime-manifest-v1",
		PassArtifactToolchainManifest:          "toolchain-manifest-v1",
		PassArtifactTargetContext:              "target-context-v1",
		PassArtifactDataLayout:                 "data-layout-v1",
		PassArtifactAvailableCapabilityCatalog: "available-capability-catalog-v1",
	})
	assertRequirements(t, mir.ReadsArtifacts, map[PassArtifactName]string{
		PassArtifactTypedHIR:           "hir-v5",
		PassArtifactRepresentationPlan: "rep-plan-v2",
	})
	assertRequirements(t, capability.ReadsArtifacts, map[PassArtifactName]string{
		PassArtifactRepresentationPlan:         "rep-plan-v2",
		PassArtifactAvailableCapabilityCatalog: "available-capability-catalog-v1",
	})
}

func TestPassExecutorRejectsResolverFactsWithoutTypedArtifacts(t *testing.T) {
	executor := newTestPassExecutor(t, 2, 0, nil)
	initial := testInitialPassState()
	initial.Artifacts = nil
	execution, err := executor.Execute(context.Background(), initial)
	if err == nil || !strings.Contains(err.Error(), `pass "resolve-target-context" input artifacts`) ||
		!strings.Contains(err.Error(), `missing required artifact "canonical-build-plan"`) {
		t.Fatalf("resolver artifact error = %v", err)
	}
	if len(execution.Dumps) == 0 || execution.Dumps[len(execution.Dumps)-1].Pass != PassVarianceAliasing {
		t.Fatalf("resolver accepted before typed HIR publication: %#v", execution.Dumps)
	}
}

func TestPassExecutorRetainsResolverInputsAndOutputsThroughRepresentation(t *testing.T) {
	handlers := testPassHandlers(0, nil)
	for _, spec := range PassSpecs() {
		if index, _ := canonicalPassIndex(spec.ID); index > 6 {
			delete(handlers, spec.ID)
		}
	}
	executor, err := NewPassExecutorThrough(handlers, PassRepresentationPlan, 2)
	if err != nil {
		t.Fatal(err)
	}
	initial := testInitialPassState()
	execution, err := executor.ExecuteThrough(context.Background(), initial, PassRepresentationPlan)
	if err != nil {
		t.Fatal(err)
	}
	if execution.State.Artifacts == nil {
		t.Fatal("representation state has no typed artifact envelope")
	}
	if _, err := execution.State.Artifacts.CanonicalBytes(); err != nil {
		t.Fatalf("final envelope is not canonical: %v", err)
	}
	want := map[PassArtifactName]string{
		PassArtifactTypedHIR:                   "hir-v5",
		PassArtifactBuildPlan:                  "build-plan-v1",
		PassArtifactRuntimeManifest:            "runtime-manifest-v1",
		PassArtifactToolchainManifest:          "toolchain-manifest-v1",
		PassArtifactTargetContext:              "target-context-v1",
		PassArtifactDataLayout:                 "data-layout-v1",
		PassArtifactAvailableCapabilityCatalog: "available-capability-catalog-v1",
		PassArtifactRepresentationPlan:         "rep-plan-v2",
	}
	if len(execution.State.Artifacts.Artifacts) != len(want) {
		t.Fatalf("final artifacts = %#v", execution.State.Artifacts.Artifacts)
	}
	for name, schema := range want {
		artifact, ok := execution.State.NamedArtifact(name)
		if !ok || artifact.Schema != schema || len(artifact.Digest) != 64 {
			t.Errorf("artifact %q = %#v, want schema %q", name, artifact, schema)
		}
	}
	for _, input := range initial.Artifacts.Artifacts {
		preserved, ok := execution.State.NamedArtifact(input.Name)
		if !ok || !equalPassArtifact(input, preserved) {
			t.Errorf("external input %q was not retained exactly", input.Name)
		}
	}

	resolveIndex := slices.IndexFunc(execution.Dumps, func(dump PassDump) bool { return dump.Pass == PassResolveTarget })
	if resolveIndex < 0 || execution.Dumps[resolveIndex].Artifacts == nil || len(execution.Dumps[resolveIndex].Artifacts.Artifacts) != 7 {
		t.Fatalf("resolver dump does not retain seven joined artifacts: %#v", execution.Dumps)
	}
}

func TestPassExecutorRejectsArtifactInjectionReplacementAndMissingResolverOutput(t *testing.T) {
	t.Run("initial produced artifact", func(t *testing.T) {
		executor := newTestPassExecutor(t, 2, 0, nil)
		initial := testInitialPassState()
		injected, err := NewPassArtifact(PassArtifactTargetContext, "target-context-v1", json.RawMessage(`{"target":"forged"}`))
		if err != nil {
			t.Fatal(err)
		}
		initial, err = initial.WithNamedArtifacts(injected)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := executor.Execute(context.Background(), initial); err == nil || !strings.Contains(err.Error(), "must be produced by its canonical pass") {
			t.Fatalf("initial injection error = %v", err)
		}
	})

	t.Run("validly rehashed input replacement", func(t *testing.T) {
		handlers := testPassHandlers(0, nil)
		handler := handlers[PassResolveTarget]
		original := handler.Run
		handler.Run = func(ctx context.Context, spec PassSpec, iteration int, state PassState) (PassResult, error) {
			result, err := original(ctx, spec, iteration, state)
			if err != nil {
				return PassResult{}, err
			}
			replacement, err := NewPassArtifact(PassArtifactBuildPlan, "build-plan-v1", json.RawMessage(`{"fixture":"forged"}`))
			if err != nil {
				return PassResult{}, err
			}
			artifacts := slices.Clone(result.State.Artifacts.Artifacts)
			for index := range artifacts {
				if artifacts[index].Name == PassArtifactBuildPlan {
					artifacts[index] = replacement
				}
			}
			envelope, err := NewPassArtifactEnvelope(artifacts...)
			if err != nil {
				return PassResult{}, err
			}
			result.State.Artifacts = &envelope
			return result, nil
		}
		handlers[PassResolveTarget] = handler
		executor, err := NewPassExecutor(handlers, 2)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := executor.Execute(context.Background(), testInitialPassState()); err == nil || !strings.Contains(err.Error(), `changed immutable input artifact "canonical-build-plan"`) {
			t.Fatalf("replacement error = %v", err)
		}
	})

	t.Run("missing resolver output", func(t *testing.T) {
		handlers := testPassHandlers(0, nil)
		handler := handlers[PassResolveTarget]
		handler.Run = func(_ context.Context, spec PassSpec, _ int, state PassState) (PassResult, error) {
			state.Schema = spec.OutputSchema
			state.Facts = appendDeclaredFacts(state.Facts, spec.WritesFacts)
			return PassResult{State: state}, nil
		}
		handlers[PassResolveTarget] = handler
		executor, err := NewPassExecutor(handlers, 2)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := executor.Execute(context.Background(), testInitialPassState()); err == nil || !strings.Contains(err.Error(), `missing declared write artifact "data-layout"`) {
			t.Fatalf("missing output error = %v", err)
		}
	})
}
