package bingo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestPassExecutorRunsCanonicalDAGWithDeterministicDumps(t *testing.T) {
	firstEvents := []string{}
	first := newTestPassExecutor(t, 8, 1, &firstEvents)
	firstRun, err := first.Execute(context.Background(), testInitialPassState())
	if err != nil {
		t.Fatal(err)
	}
	secondEvents := []string{}
	second := newTestPassExecutor(t, 8, 1, &secondEvents)
	secondRun, err := second.Execute(context.Background(), testInitialPassState())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(firstRun, secondRun) {
		t.Fatalf("pass execution is not deterministic:\nfirst=%#v\nsecond=%#v", firstRun, secondRun)
	}
	if firstRun.State.Schema != "verified-mir-v3" || !slices.Contains(firstRun.State.Facts, "final-mir") {
		t.Fatalf("final state = %#v", firstRun.State)
	}
	if got, want := len(firstRun.Dumps), len(canonicalPassSpecs)+1; got != want {
		t.Fatalf("dump count = %d, want %d", got, want)
	}
	if got, want := len(firstEvents), len(firstRun.Dumps)*3; got != want {
		t.Fatalf("verifier event count = %d, want %d", got, want)
	}
	for index, dump := range firstRun.Dumps {
		if len(dump.ContentHash) != 64 || !json.Valid(dump.Artifact) {
			t.Fatalf("dump %d = %#v", index, dump)
		}
		if string(dump.Artifact) != `{"a":2,"z":1}` {
			t.Fatalf("dump %d artifact is not canonical: %s", index, dump.Artifact)
		}
	}
	specialization := slices.DeleteFunc(slices.Clone(firstRun.Dumps), func(dump PassDump) bool {
		return dump.Pass != PassSpecialization
	})
	if len(specialization) != 2 || slices.Contains(specialization[0].Facts, "specialization-fixed-point") ||
		!slices.Contains(specialization[1].Facts, "specialization-fixed-point") {
		t.Fatalf("specialization fixed-point publication = %#v", specialization)
	}
	if !specialization[0].Changed || !slices.Equal(specialization[0].PendingSpecializations, []string{"pending-1"}) ||
		specialization[1].Changed || len(specialization[1].PendingSpecializations) != 0 {
		t.Fatalf("specialization proof dumps = %#v", specialization)
	}
}

func TestPassExecutorMatchesGoldenDumps(t *testing.T) {
	executor := newTestPassExecutor(t, 8, 1, nil)
	execution, err := executor.Execute(context.Background(), testInitialPassState())
	if err != nil {
		t.Fatal(err)
	}
	type goldenDump struct {
		Sequence    int    `json:"sequence"`
		Pass        PassID `json:"pass"`
		Iteration   int    `json:"iteration"`
		Schema      string `json:"schema"`
		ContentHash string `json:"contentHash"`
	}
	projection := make([]goldenDump, 0, len(execution.Dumps))
	for _, dump := range execution.Dumps {
		projection = append(projection, goldenDump{
			Sequence: dump.Sequence, Pass: dump.Pass, Iteration: dump.Iteration,
			Schema: dump.Schema, ContentHash: dump.ContentHash,
		})
	}
	got, err := json.MarshalIndent(projection, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	want, err := os.ReadFile("testdata/pass_driver.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("pass dumps differ from golden:\n%s", got)
	}
}

func TestPassSpecsMatchGolden(t *testing.T) {
	type goldenSpec struct {
		ID          PassID `json:"id"`
		ContentHash string `json:"contentHash"`
	}
	projection := make([]goldenSpec, 0, len(PassSpecs()))
	for _, spec := range PassSpecs() {
		encoded, err := json.Marshal(spec)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(encoded)
		projection = append(projection, goldenSpec{ID: spec.ID, ContentHash: fmt.Sprintf("%x", digest)})
	}
	got, err := json.MarshalIndent(projection, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	want, err := os.ReadFile("testdata/pass_specs.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("pass specs differ from golden:\n%s", got)
	}
}

func TestNewPassExecutorRejectsIncompleteOrUnknownRegistry(t *testing.T) {
	handlers := testPassHandlers(0, nil)
	delete(handlers, PassTypedHIR)
	if _, err := NewPassExecutor(handlers, 1); err == nil || !strings.Contains(err.Error(), "no registered handler") {
		t.Fatalf("missing handler error = %v", err)
	}
	handlers = testPassHandlers(0, nil)
	handlers[PassID("invented")] = handlers[PassTypedHIR]
	if _, err := NewPassExecutor(handlers, 1); err == nil || !strings.Contains(err.Error(), "unknown pass") {
		t.Fatalf("unknown handler error = %v", err)
	}
	handlers = testPassHandlers(0, nil)
	handler := handlers[PassTypedHIR]
	handler.PostVerify = nil
	handlers[PassTypedHIR] = handler
	if _, err := NewPassExecutor(handlers, 1); err == nil || !strings.Contains(err.Error(), "post-verifier") {
		t.Fatalf("missing verifier error = %v", err)
	}
}

func TestPassExecutorRejectsSpecializationBudgetExhaustion(t *testing.T) {
	runs := 0
	handlers := testPassHandlers(0, nil)
	handler := handlers[PassSpecialization]
	handler.Run = func(_ context.Context, spec PassSpec, _ int, state PassState) (PassResult, error) {
		runs++
		state.Schema = spec.OutputSchema
		return PassResult{State: state, Changed: true}, nil
	}
	handler.PostVerify = func(context.Context, PassSpec, int, PassState, PassState) (PassVerification, error) {
		return PassVerification{PendingSpecializations: []string{"still-pending"}}, nil
	}
	handlers[PassSpecialization] = handler
	executor, err := NewPassExecutor(handlers, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(context.Background(), testInitialPassState()); err == nil || !strings.Contains(err.Error(), "iteration budget 2") {
		t.Fatalf("budget error = %v", err)
	}
	if runs != 4 {
		t.Fatalf("specialization runs = %d, want 4 including deterministic replay", runs)
	}
}

func TestPassExecutorRejectsSchemaFactAndEffectViolations(t *testing.T) {
	tests := []struct {
		name string
		edit func(map[PassID]PassHandler)
		want string
	}{
		{
			name: "schema",
			edit: func(handlers map[PassID]PassHandler) {
				handler := handlers[PassValidateSnapshot]
				handler.Run = func(_ context.Context, _ PassSpec, _ int, state PassState) (PassResult, error) {
					state.Schema = "wrong-v1"
					state.Facts = append(state.Facts, "source-type-plan")
					return PassResult{State: state}, nil
				}
				handlers[PassValidateSnapshot] = handler
			},
			want: "output schema",
		},
		{
			name: "fact",
			edit: func(handlers map[PassID]PassHandler) {
				handler := handlers[PassValidateSnapshot]
				handler.Run = func(_ context.Context, spec PassSpec, _ int, state PassState) (PassResult, error) {
					state.Schema = spec.OutputSchema
					return PassResult{State: state}, nil
				}
				handlers[PassValidateSnapshot] = handler
			},
			want: "missing declared write fact",
		},
		{
			name: "effect",
			edit: func(handlers map[PassID]PassHandler) {
				handler := handlers[PassValidateSnapshot]
				original := handler.Run
				handler.Run = func(ctx context.Context, spec PassSpec, iteration int, state PassState) (PassResult, error) {
					result, err := original(ctx, spec, iteration, state)
					result.Effects = PassEffects{Artifact: []PassEffect{PassEffectCall}, Introduced: []PassEffect{PassEffectCall}}
					return result, err
				}
				handler.PostVerify = func(context.Context, PassSpec, int, PassState, PassState) (PassVerification, error) {
					return PassVerification{Effects: PassEffects{Artifact: []PassEffect{PassEffectCall}, Introduced: []PassEffect{PassEffectCall}}}, nil
				}
				handlers[PassValidateSnapshot] = handler
			},
			want: "undeclared call effect",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handlers := testPassHandlers(0, nil)
			test.edit(handlers)
			executor, err := NewPassExecutor(handlers, 2)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := executor.Execute(context.Background(), testInitialPassState()); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestPassExecutorPropagatesVerifierFailureAndCancellation(t *testing.T) {
	handlers := testPassHandlers(0, nil)
	handler := handlers[PassTypedHIR]
	handler.PreVerify = func(context.Context, PassSpec, int, PassState) error { return errors.New("broken input") }
	handlers[PassTypedHIR] = handler
	executor, err := NewPassExecutor(handlers, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(context.Background(), testInitialPassState()); err == nil || !strings.Contains(err.Error(), "pre-verifier: broken input") {
		t.Fatalf("verifier error = %v", err)
	}

	executor = newTestPassExecutor(t, 2, 0, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := executor.Execute(ctx, testInitialPassState()); err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("cancellation error = %v", err)
	}
}

func TestPassExecutorRejectsNondeterministicHandler(t *testing.T) {
	handlers := testPassHandlers(0, nil)
	handler := handlers[PassValidateSnapshot]
	calls := 0
	handler.Run = func(_ context.Context, spec PassSpec, _ int, state PassState) (PassResult, error) {
		calls++
		state.Schema = spec.OutputSchema
		state.Facts = appendDeclaredFacts(state.Facts, spec.WritesFacts)
		state.Artifact = json.RawMessage(`{"call":` + strconv.Itoa(calls) + `}`)
		return PassResult{State: state}, nil
	}
	handlers[PassValidateSnapshot] = handler
	executor, err := NewPassExecutor(handlers, 2)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := executor.Execute(context.Background(), testInitialPassState())
	if err == nil || !strings.Contains(err.Error(), "nondeterministic result") {
		t.Fatalf("determinism error = %v", err)
	}
	if len(execution.Dumps) != 0 {
		t.Fatalf("failed pass produced accepted dumps: %#v", execution.Dumps)
	}
}

func TestPassExecutorRejectsNondeterministicVerifierProof(t *testing.T) {
	handlers := testPassHandlers(0, nil)
	handler := handlers[PassValidateSnapshot]
	calls := 0
	handler.PostVerify = func(context.Context, PassSpec, int, PassState, PassState) (PassVerification, error) {
		calls++
		if calls%2 == 0 {
			return PassVerification{PendingSpecializations: []string{"unstable"}}, nil
		}
		return PassVerification{}, nil
	}
	handlers[PassValidateSnapshot] = handler
	executor, err := NewPassExecutor(handlers, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(context.Background(), testInitialPassState()); err == nil || !strings.Contains(err.Error(), "nondeterministic verifier proof") {
		t.Fatalf("verifier determinism error = %v", err)
	}
}

func TestPassEffectContractSeparatesSafepointsAndRootPublication(t *testing.T) {
	specs := PassSpecs()
	rootIndex := slices.IndexFunc(specs, func(spec PassSpec) bool { return spec.ID == PassPlaceGCRoots })
	if rootIndex < 0 {
		t.Fatal("root-placement pass is missing")
	}
	rootSpec := specs[rootIndex]
	valid := PassEffects{
		Input:      []PassEffect{PassEffectSafepoint},
		Artifact:   []PassEffect{PassEffectRootPublication, PassEffectSafepoint},
		Introduced: []PassEffect{PassEffectRootPublication},
	}
	valid, err := normalizePassEffects(valid)
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePassEffects(rootSpec, valid); err != nil {
		t.Fatalf("valid root publication proof: %v", err)
	}

	newSafepoint := PassEffects{Artifact: []PassEffect{PassEffectSafepoint}, Introduced: []PassEffect{PassEffectSafepoint}}
	newSafepoint, err = normalizePassEffects(newSafepoint)
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePassEffects(rootSpec, newSafepoint); err == nil || !strings.Contains(err.Error(), "undeclared safepoint") {
		t.Fatalf("root placement safepoint error = %v", err)
	}

	missingSafepoint := PassEffects{Artifact: []PassEffect{PassEffectRootPublication}, Introduced: []PassEffect{PassEffectRootPublication}}
	missingSafepoint, err = normalizePassEffects(missingSafepoint)
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePassEffects(rootSpec, missingSafepoint); err == nil || !strings.Contains(err.Error(), "existing safepoint") {
		t.Fatalf("root publication without safepoint error = %v", err)
	}

	hiddenDelta := PassEffects{Artifact: []PassEffect{PassEffectCall}}
	hiddenDelta, err = normalizePassEffects(hiddenDelta)
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePassEffects(rootSpec, hiddenDelta); err == nil || !strings.Contains(err.Error(), "output-minus-input") {
		t.Fatalf("hidden effect delta error = %v", err)
	}
}

func TestPassExecutorBindsInputEffectsToPreviousVerifiedArtifact(t *testing.T) {
	handlers := testPassHandlers(0, nil)
	cleanup := handlers[PassCleanupState]
	cleanup.Run = func(_ context.Context, spec PassSpec, _ int, state PassState) (PassResult, error) {
		state.Schema = spec.OutputSchema
		state.Facts = appendDeclaredFacts(state.Facts, spec.WritesFacts)
		effects := PassEffects{
			Artifact:   []PassEffect{PassEffectCall},
			Introduced: []PassEffect{PassEffectCall},
		}
		return PassResult{State: state, Effects: effects}, nil
	}
	cleanup.PostVerify = func(context.Context, PassSpec, int, PassState, PassState) (PassVerification, error) {
		return PassVerification{Effects: PassEffects{
			Artifact:   []PassEffect{PassEffectCall},
			Introduced: []PassEffect{PassEffectCall},
		}}, nil
	}
	handlers[PassCleanupState] = cleanup

	// This proof is internally self-consistent, but it lies about the effect set
	// accepted from the preceding pass. The executor must reject the discontinuity.
	structural := handlers[PassStructuralVerifier]
	structural.PostVerify = func(context.Context, PassSpec, int, PassState, PassState) (PassVerification, error) {
		return PassVerification{}, nil
	}
	handlers[PassStructuralVerifier] = structural

	executor, err := NewPassExecutor(handlers, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(context.Background(), testInitialPassState()); err == nil || !strings.Contains(err.Error(), "do not match previous verified artifact effects") {
		t.Fatalf("effect continuity error = %v", err)
	}
}

func TestPassExecutorRequiresIndependentEffectAndFixedPointProof(t *testing.T) {
	t.Run("effect claim", func(t *testing.T) {
		handlers := testPassHandlers(0, nil)
		handler := handlers[PassCleanupState]
		handler.PostVerify = func(context.Context, PassSpec, int, PassState, PassState) (PassVerification, error) {
			return PassVerification{Effects: PassEffects{Artifact: []PassEffect{PassEffectCall}, Introduced: []PassEffect{PassEffectCall}}}, nil
		}
		handlers[PassCleanupState] = handler
		executor, err := NewPassExecutor(handlers, 2)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := executor.Execute(context.Background(), testInitialPassState()); err == nil || !strings.Contains(err.Error(), "effect claim") {
			t.Fatalf("effect proof error = %v", err)
		}
	})

	t.Run("pending specialization", func(t *testing.T) {
		handlers := testPassHandlers(0, nil)
		handler := handlers[PassSpecialization]
		handler.PostVerify = func(context.Context, PassSpec, int, PassState, PassState) (PassVerification, error) {
			return PassVerification{PendingSpecializations: []string{"new-instantiation"}}, nil
		}
		handlers[PassSpecialization] = handler
		executor, err := NewPassExecutor(handlers, 2)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := executor.Execute(context.Background(), testInitialPassState()); err == nil || !strings.Contains(err.Error(), "changed=false") {
			t.Fatalf("fixed-point proof error = %v", err)
		}
	})
}

func TestPassExecutorRejectsUnknownFactsAndReturnsPartialDumps(t *testing.T) {
	handlers := testPassHandlers(0, nil)
	handler := handlers[PassTypedHIR]
	handler.PreVerify = func(context.Context, PassSpec, int, PassState) error { return errors.New("broken HIR input") }
	handlers[PassTypedHIR] = handler
	executor, err := NewPassExecutor(handlers, 2)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := executor.Execute(context.Background(), testInitialPassState())
	if err == nil || !strings.Contains(err.Error(), "broken HIR input") {
		t.Fatalf("partial failure = %v", err)
	}
	if len(execution.Dumps) != 1 || execution.State.Schema != "source-type-plan-v2" {
		t.Fatalf("partial execution = %#v", execution)
	}

	initial := testInitialPassState()
	initial.Facts = append(initial.Facts, "injected")
	if _, err := executor.Execute(context.Background(), initial); err == nil || !strings.Contains(err.Error(), `unknown fact "injected"`) {
		t.Fatalf("unknown fact error = %v", err)
	}
}

func TestPassExecutorObservesCancellationDuringHooks(t *testing.T) {
	handlers := testPassHandlers(0, nil)
	ctx, cancel := context.WithCancel(context.Background())
	handler := handlers[PassValidateSnapshot]
	original := handler.Run
	handler.Run = func(ctx context.Context, spec PassSpec, iteration int, state PassState) (PassResult, error) {
		result, err := original(ctx, spec, iteration, state)
		cancel()
		return result, err
	}
	handlers[PassValidateSnapshot] = handler
	executor, err := NewPassExecutor(handlers, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(ctx, testInitialPassState()); err == nil || !strings.Contains(err.Error(), "canceled after runner") {
		t.Fatalf("in-flight cancellation error = %v", err)
	}
	if _, err := executor.Execute(nil, testInitialPassState()); err == nil || !strings.Contains(err.Error(), "context is nil") {
		t.Fatalf("nil context error = %v", err)
	}
}

func newTestPassExecutor(t *testing.T, budget, specializationChanges int, events *[]string) *PassExecutor {
	t.Helper()
	executor, err := NewPassExecutor(testPassHandlers(specializationChanges, events), budget)
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

func testPassHandlers(specializationChanges int, events *[]string) map[PassID]PassHandler {
	handlers := make(map[PassID]PassHandler, len(canonicalPassSpecs))
	for _, spec := range canonicalPassSpecs {
		spec := spec
		handlers[spec.ID] = PassHandler{
			PreVerify: func(_ context.Context, current PassSpec, iteration int, _ PassState) error {
				if events != nil {
					*events = append(*events, "pre:"+string(current.ID)+":"+strconv.Itoa(iteration))
				}
				return nil
			},
			Run: func(_ context.Context, current PassSpec, iteration int, state PassState) (PassResult, error) {
				state.Schema = current.OutputSchema
				changed := current.ID == PassSpecialization && iteration <= specializationChanges
				if !changed {
					state.Facts = appendDeclaredFacts(state.Facts, current.WritesFacts)
					var err error
					state, err = appendTestPassArtifacts(state, current)
					if err != nil {
						return PassResult{}, err
					}
				}
				return PassResult{State: state, Changed: changed}, nil
			},
			PostVerify: func(_ context.Context, current PassSpec, iteration int, _, _ PassState) (PassVerification, error) {
				if events != nil {
					*events = append(*events, "post:"+string(current.ID)+":"+strconv.Itoa(iteration))
				}
				verification := PassVerification{}
				if current.ID == PassSpecialization && iteration <= specializationChanges {
					verification.PendingSpecializations = []string{"pending-" + strconv.Itoa(iteration)}
				}
				return verification, nil
			},
		}
	}
	return handlers
}

func testInitialPassState() PassState {
	written := map[string]struct{}{}
	for _, spec := range canonicalPassSpecs {
		for _, fact := range spec.WritesFacts {
			written[fact] = struct{}{}
		}
	}
	initial := []string{}
	seen := map[string]struct{}{}
	for _, spec := range canonicalPassSpecs {
		for _, fact := range spec.ReadsFacts {
			if _, produced := written[fact]; produced {
				continue
			}
			if _, duplicate := seen[fact]; duplicate {
				continue
			}
			seen[fact] = struct{}{}
			initial = append(initial, fact)
		}
	}
	state := PassState{Schema: "snapshot-v2", Facts: initial, Artifact: json.RawMessage(`{ "z": 1, "a": 2 }`)}
	for _, input := range []struct {
		name   PassArtifactName
		schema string
	}{
		{name: PassArtifactBuildPlan, schema: "build-plan-v1"},
		{name: PassArtifactRuntimeManifest, schema: "runtime-manifest-v1"},
		{name: PassArtifactToolchainManifest, schema: "toolchain-manifest-v1"},
	} {
		artifact, err := NewPassArtifact(input.name, input.schema, json.RawMessage(`{"fixture":true}`))
		if err != nil {
			panic(err)
		}
		state, err = state.WithNamedArtifacts(artifact)
		if err != nil {
			panic(err)
		}
	}
	return state
}

func appendTestPassArtifacts(state PassState, spec PassSpec) (PassState, error) {
	result := state
	for _, write := range spec.WritesArtifacts {
		if write.FromPrimary {
			continue
		}
		payload, err := json.Marshal(struct {
			Pass PassID           `json:"pass"`
			Name PassArtifactName `json:"name"`
		}{Pass: spec.ID, Name: write.Name})
		if err != nil {
			return PassState{}, err
		}
		artifact, err := NewPassArtifact(write.Name, write.Schema, payload)
		if err != nil {
			return PassState{}, err
		}
		result, err = result.WithNamedArtifacts(artifact)
		if err != nil {
			return PassState{}, err
		}
	}
	return result, nil
}

func appendDeclaredFacts(existing, writes []string) []string {
	result := slices.Clone(existing)
	for _, fact := range writes {
		if !slices.Contains(result, fact) {
			result = append(result, fact)
		}
	}
	return result
}
