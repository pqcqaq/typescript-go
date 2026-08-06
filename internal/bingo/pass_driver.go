package bingo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
)

const DefaultSpecializationIterationBudget = 64

// PassState is the immutable, serializable boundary passed between lowering
// stages. Artifact is the legacy/current-stage primary JSON value. Artifacts is
// an optional immutable sidecar envelope for independently typed pass inputs and
// outputs. Facts are ordering metadata and never constitute target or
// capability proof.
type PassState struct {
	Schema    string                `json:"schema"`
	Facts     []string              `json:"facts"`
	Artifact  json.RawMessage       `json:"artifact"`
	Artifacts *PassArtifactEnvelope `json:"artifacts,omitempty"`
}

// PassEffects records the effects independently observed in the output
// artifact and the subset newly introduced by this pass. Both sets are sorted,
// unique, and validated against the canonical PassSpec.
type PassEffects struct {
	Input      []PassEffect `json:"input"`
	Artifact   []PassEffect `json:"artifact"`
	Introduced []PassEffect `json:"introduced,omitempty"`
}

// PassResult is returned by a pass handler. Changed is only valid for the
// specialization pass, where it requests another deterministic iteration.
type PassResult struct {
	State   PassState
	Effects PassEffects
	Changed bool
}

// PassVerification is independently derived by a post-verifier from the pass
// input and output. The executor compares it with the handler's claims before
// accepting the result. PendingSpecializations contains canonical work keys
// that still require another specialization iteration.
type PassVerification struct {
	Effects                PassEffects
	PendingSpecializations []string
}

// PassVerifier checks a pass input boundary. Iteration is one-based and is
// greater than one only for the specialization fixed-point stage.
type PassVerifier func(context.Context, PassSpec, int, PassState) error

// PassRunner executes one pass iteration.
type PassRunner func(context.Context, PassSpec, int, PassState) (PassResult, error)

// PassPostVerifier independently checks a pass output and derives the effects
// and specialization worklist used to validate the handler's claims.
type PassPostVerifier func(context.Context, PassSpec, int, PassState, PassState) (PassVerification, error)

// PassHandler binds executable behavior and mandatory pre/post verification to
// one canonical pass.
type PassHandler struct {
	Run        PassRunner
	PreVerify  PassVerifier
	PostVerify PassPostVerifier
}

// PassDump is the deterministic post-pass artifact used by golden and diff
// tooling. ContentHash covers every other field in the record.
type PassDump struct {
	Sequence               int                   `json:"sequence"`
	Pass                   PassID                `json:"pass"`
	Iteration              int                   `json:"iteration"`
	Schema                 string                `json:"schema"`
	Facts                  []string              `json:"facts"`
	Artifact               json.RawMessage       `json:"artifact"`
	Artifacts              *PassArtifactEnvelope `json:"artifacts,omitempty"`
	Effects                PassEffects           `json:"effects"`
	Changed                bool                  `json:"changed"`
	PendingSpecializations []string              `json:"pendingSpecializations,omitempty"`
	ContentHash            string                `json:"contentHash"`
}

// PassExecution contains the final state and every deterministic stage dump.
type PassExecution struct {
	State PassState
	Dumps []PassDump
}

// PassExecutor owns an immutable copy of the complete handler registry.
type PassExecutor struct {
	handlers                      map[PassID]PassHandler
	knownFacts                    map[string]struct{}
	knownArtifacts                map[PassArtifactName]string
	producedArtifacts             map[PassArtifactName]struct{}
	specializationIterationBudget int
	registeredThrough             int
}

// NewPassExecutor validates a complete canonical handler registry. A zero
// budget selects DefaultSpecializationIterationBudget.
func NewPassExecutor(handlers map[PassID]PassHandler, specializationIterationBudget int) (*PassExecutor, error) {
	return newPassExecutorThrough(handlers, PassFinalVerifier, specializationIterationBudget)
}

// NewPassExecutorThrough validates a handler registry for one exact canonical
// prefix. It exists for production slices that have real artifacts only up to
// terminal; handlers after terminal must not be registered as placeholders.
func NewPassExecutorThrough(handlers map[PassID]PassHandler, terminal PassID, specializationIterationBudget int) (*PassExecutor, error) {
	return newPassExecutorThrough(handlers, terminal, specializationIterationBudget)
}

func newPassExecutorThrough(handlers map[PassID]PassHandler, terminal PassID, specializationIterationBudget int) (*PassExecutor, error) {
	if specializationIterationBudget < 0 {
		return nil, fmt.Errorf("specialization iteration budget must not be negative")
	}
	if specializationIterationBudget == 0 {
		specializationIterationBudget = DefaultSpecializationIterationBudget
	}
	terminalIndex, ok := canonicalPassIndex(terminal)
	if !ok {
		return nil, fmt.Errorf("unknown terminal pass %q", terminal)
	}
	known := make(map[PassID]struct{}, terminalIndex+1)
	knownFacts := make(map[string]struct{})
	knownArtifacts := make(map[PassArtifactName]string)
	producedArtifacts := make(map[PassArtifactName]struct{})
	cloned := make(map[PassID]PassHandler, len(handlers))
	for _, spec := range canonicalPassSpecs[:terminalIndex+1] {
		if err := validateCanonicalPassSpec(spec); err != nil {
			return nil, err
		}
		known[spec.ID] = struct{}{}
		for _, fact := range spec.ReadsFacts {
			knownFacts[fact] = struct{}{}
		}
		for _, fact := range spec.WritesFacts {
			knownFacts[fact] = struct{}{}
		}
		for _, requirement := range spec.ReadsArtifacts {
			if err := registerPassArtifactSchema(knownArtifacts, requirement.Name, requirement.Schema); err != nil {
				return nil, fmt.Errorf("pass %q read artifact: %w", spec.ID, err)
			}
		}
		for _, write := range spec.WritesArtifacts {
			if err := registerPassArtifactSchema(knownArtifacts, write.Name, write.Schema); err != nil {
				return nil, fmt.Errorf("pass %q write artifact: %w", spec.ID, err)
			}
			if _, duplicate := producedArtifacts[write.Name]; duplicate {
				return nil, fmt.Errorf("pass artifact %q has more than one producer", write.Name)
			}
			producedArtifacts[write.Name] = struct{}{}
		}
		handler, ok := handlers[spec.ID]
		if !ok {
			return nil, fmt.Errorf("pass %q has no registered handler", spec.ID)
		}
		if handler.Run == nil || handler.PreVerify == nil || handler.PostVerify == nil {
			return nil, fmt.Errorf("pass %q requires run, pre-verifier, and post-verifier hooks", spec.ID)
		}
		cloned[spec.ID] = handler
	}
	for id := range handlers {
		if _, ok := known[id]; !ok {
			if index, canonical := canonicalPassIndex(id); canonical && index > terminalIndex {
				return nil, fmt.Errorf("handler registry contains pass %q after terminal %q", id, terminal)
			}
			return nil, fmt.Errorf("handler registry contains unknown pass %q", id)
		}
	}
	return &PassExecutor{
		handlers:                      cloned,
		knownFacts:                    knownFacts,
		knownArtifacts:                knownArtifacts,
		producedArtifacts:             producedArtifacts,
		specializationIterationBudget: specializationIterationBudget,
		registeredThrough:             terminalIndex,
	}, nil
}

// Execute runs every canonical pass in order and fails closed on schema, fact,
// effect, verification, fixed-point, or handler determinism violations. On a
// pass failure, the returned execution retains every previously accepted dump.
func (e *PassExecutor) Execute(ctx context.Context, initial PassState) (PassExecution, error) {
	if e == nil {
		return PassExecution{}, fmt.Errorf("pass executor is nil")
	}
	if e.registeredThrough != len(canonicalPassSpecs)-1 {
		return PassExecution{}, fmt.Errorf(
			"pass executor is registered through %q; use ExecuteThrough for an explicit canonical prefix",
			canonicalPassSpecs[e.registeredThrough].ID,
		)
	}
	return e.executeThrough(ctx, initial, e.registeredThrough)
}

// ExecuteThrough runs the exact canonical prefix ending at terminal. The
// terminal must be covered by the registry used to construct the executor.
func (e *PassExecutor) ExecuteThrough(ctx context.Context, initial PassState, terminal PassID) (PassExecution, error) {
	if e == nil {
		return PassExecution{}, fmt.Errorf("pass executor is nil")
	}
	terminalIndex, ok := canonicalPassIndex(terminal)
	if !ok {
		return PassExecution{}, fmt.Errorf("unknown terminal pass %q", terminal)
	}
	if terminalIndex > e.registeredThrough {
		return PassExecution{}, fmt.Errorf(
			"terminal pass %q is after registered terminal %q",
			terminal, canonicalPassSpecs[e.registeredThrough].ID,
		)
	}
	return e.executeThrough(ctx, initial, terminalIndex)
}

func (e *PassExecutor) executeThrough(ctx context.Context, initial PassState, terminalIndex int) (PassExecution, error) {
	if ctx == nil {
		return PassExecution{}, fmt.Errorf("pass context is nil")
	}
	state, err := normalizePassState(initial)
	if err != nil {
		return PassExecution{}, fmt.Errorf("normalize initial pass state: %w", err)
	}
	if err := validateKnownFacts(state.Facts, e.knownFacts); err != nil {
		return PassExecution{State: clonePassState(state), Dumps: []PassDump{}}, fmt.Errorf("initial pass state: %w", err)
	}
	if err := validateInitialPassArtifacts(state.Artifacts, e.knownArtifacts, e.producedArtifacts); err != nil {
		return PassExecution{State: clonePassState(state), Dumps: []PassDump{}}, fmt.Errorf("initial pass state: %w", err)
	}
	dumps := make([]PassDump, 0, terminalIndex+1)
	verifiedEffects := []PassEffect{}
	for sequence, spec := range canonicalPassSpecs[:terminalIndex+1] {
		if state.Schema != spec.InputSchema {
			return failedPassExecution(state, dumps, fmt.Errorf("pass %q input schema is %q, want %q", spec.ID, state.Schema, spec.InputSchema))
		}
		if err := requireFacts(state.Facts, spec.ReadsFacts); err != nil {
			return failedPassExecution(state, dumps, fmt.Errorf("pass %q input facts: %w", spec.ID, err))
		}
		if err := requirePassArtifacts(state.Artifacts, spec.ReadsArtifacts); err != nil {
			return failedPassExecution(state, dumps, fmt.Errorf("pass %q input artifacts: %w", spec.ID, err))
		}
		iterations := 1
		if spec.ID == PassSpecialization {
			iterations = e.specializationIterationBudget
		}
		for iteration := 1; iteration <= iterations; iteration++ {
			if err := ctx.Err(); err != nil {
				return failedPassExecution(state, dumps, fmt.Errorf("pass %q canceled: %w", spec.ID, err))
			}
			handler := e.handlers[spec.ID]
			input := clonePassState(state)
			if err := handler.PreVerify(ctx, clonePassSpec(spec), iteration, clonePassState(input)); err != nil {
				return failedPassExecution(state, dumps, fmt.Errorf("pass %q iteration %d pre-verifier: %w", spec.ID, iteration, err))
			}
			if err := ctx.Err(); err != nil {
				return failedPassExecution(state, dumps, fmt.Errorf("pass %q iteration %d canceled after pre-verifier: %w", spec.ID, iteration, err))
			}
			result, err := handler.Run(ctx, clonePassSpec(spec), iteration, clonePassState(input))
			if err != nil {
				return failedPassExecution(state, dumps, fmt.Errorf("pass %q iteration %d: %w", spec.ID, iteration, err))
			}
			if err := ctx.Err(); err != nil {
				return failedPassExecution(state, dumps, fmt.Errorf("pass %q iteration %d canceled after runner: %w", spec.ID, iteration, err))
			}
			result, err = normalizePassResultForSpec(spec, result)
			if err != nil {
				return failedPassExecution(state, dumps, fmt.Errorf("pass %q iteration %d output: %w", spec.ID, iteration, err))
			}
			replayed, err := handler.Run(ctx, clonePassSpec(spec), iteration, clonePassState(input))
			if err != nil {
				return failedPassExecution(state, dumps, fmt.Errorf("pass %q iteration %d deterministic replay: %w", spec.ID, iteration, err))
			}
			if err := ctx.Err(); err != nil {
				return failedPassExecution(state, dumps, fmt.Errorf("pass %q iteration %d canceled after deterministic replay: %w", spec.ID, iteration, err))
			}
			replayed, err = normalizePassResultForSpec(spec, replayed)
			if err != nil {
				return failedPassExecution(state, dumps, fmt.Errorf("pass %q iteration %d deterministic replay output: %w", spec.ID, iteration, err))
			}
			if !equalPassResult(result, replayed) {
				return failedPassExecution(state, dumps, fmt.Errorf("pass %q iteration %d produced a nondeterministic result", spec.ID, iteration))
			}
			if spec.ID != PassSpecialization && result.Changed {
				return failedPassExecution(state, dumps, fmt.Errorf("pass %q requested fixed-point iteration outside specialization", spec.ID))
			}
			output := result.State
			if output.Schema != spec.OutputSchema {
				return failedPassExecution(state, dumps, fmt.Errorf("pass %q iteration %d output schema is %q, want %q", spec.ID, iteration, output.Schema, spec.OutputSchema))
			}
			if err := validateKnownFacts(output.Facts, e.knownFacts); err != nil {
				return failedPassExecution(state, dumps, fmt.Errorf("pass %q iteration %d facts: %w", spec.ID, iteration, err))
			}
			if err := validateKnownPassArtifacts(output.Artifacts, e.knownArtifacts); err != nil {
				return failedPassExecution(state, dumps, fmt.Errorf("pass %q iteration %d artifacts: %w", spec.ID, iteration, err))
			}
			verification, err := handler.PostVerify(ctx, clonePassSpec(spec), iteration, clonePassState(input), clonePassState(output))
			if err != nil {
				return failedPassExecution(state, dumps, fmt.Errorf("pass %q iteration %d post-verifier: %w", spec.ID, iteration, err))
			}
			if err := ctx.Err(); err != nil {
				return failedPassExecution(state, dumps, fmt.Errorf("pass %q iteration %d canceled after post-verifier: %w", spec.ID, iteration, err))
			}
			verification, err = normalizePassVerification(verification)
			if err != nil {
				return failedPassExecution(state, dumps, fmt.Errorf("pass %q iteration %d verification: %w", spec.ID, iteration, err))
			}
			replayedVerification, err := handler.PostVerify(ctx, clonePassSpec(spec), iteration, clonePassState(input), clonePassState(output))
			if err != nil {
				return failedPassExecution(state, dumps, fmt.Errorf("pass %q iteration %d post-verifier deterministic replay: %w", spec.ID, iteration, err))
			}
			if err := ctx.Err(); err != nil {
				return failedPassExecution(state, dumps, fmt.Errorf("pass %q iteration %d canceled after post-verifier deterministic replay: %w", spec.ID, iteration, err))
			}
			replayedVerification, err = normalizePassVerification(replayedVerification)
			if err != nil {
				return failedPassExecution(state, dumps, fmt.Errorf("pass %q iteration %d post-verifier deterministic replay proof: %w", spec.ID, iteration, err))
			}
			if !equalPassVerification(verification, replayedVerification) {
				return failedPassExecution(state, dumps, fmt.Errorf("pass %q iteration %d produced a nondeterministic verifier proof", spec.ID, iteration))
			}
			if !equalPassEffects(result.Effects, verification.Effects) {
				return failedPassExecution(state, dumps, fmt.Errorf("pass %q iteration %d effect claim does not match verifier proof", spec.ID, iteration))
			}
			if !slices.Equal(verification.Effects.Input, verifiedEffects) {
				return failedPassExecution(state, dumps, fmt.Errorf(
					"pass %q iteration %d input effects %v do not match previous verified artifact effects %v",
					spec.ID, iteration, verification.Effects.Input, verifiedEffects,
				))
			}
			if err := validatePassEffects(spec, verification.Effects); err != nil {
				return failedPassExecution(state, dumps, fmt.Errorf("pass %q iteration %d effects: %w", spec.ID, iteration, err))
			}
			if err := validateSpecializationProof(spec, result.Changed, verification.PendingSpecializations); err != nil {
				return failedPassExecution(state, dumps, fmt.Errorf("pass %q iteration %d specialization proof: %w", spec.ID, iteration, err))
			}
			if err := validateFactTransition(input.Facts, output.Facts, spec, result.Changed); err != nil {
				return failedPassExecution(state, dumps, fmt.Errorf("pass %q iteration %d facts: %w", spec.ID, iteration, err))
			}
			if err := validatePassArtifactTransition(input, output, spec); err != nil {
				return failedPassExecution(state, dumps, fmt.Errorf("pass %q iteration %d artifacts: %w", spec.ID, iteration, err))
			}
			dump, err := makePassDump(sequence, spec.ID, iteration, output, verification, result.Changed)
			if err != nil {
				return failedPassExecution(state, dumps, fmt.Errorf("pass %q iteration %d dump: %w", spec.ID, iteration, err))
			}
			dumps = append(dumps, dump)
			state = output
			verifiedEffects = slices.Clone(verification.Effects.Artifact)
			if spec.ID != PassSpecialization || !result.Changed {
				break
			}
			if iteration == iterations {
				return failedPassExecution(state, dumps, fmt.Errorf("pass %q exceeded specialization iteration budget %d", spec.ID, iterations))
			}
		}
	}
	return PassExecution{State: clonePassState(state), Dumps: clonePassDumps(dumps)}, nil
}

func normalizePassResult(result PassResult) (PassResult, error) {
	state, err := normalizePassState(result.State)
	if err != nil {
		return PassResult{}, err
	}
	result.State = state
	result.Effects, err = normalizePassEffects(result.Effects)
	if err != nil {
		return PassResult{}, fmt.Errorf("effects: %w", err)
	}
	return result, nil
}

func normalizePassResultForSpec(spec PassSpec, result PassResult) (PassResult, error) {
	result, err := normalizePassResult(result)
	if err != nil {
		return PassResult{}, err
	}
	state, err := materializePrimaryPassArtifacts(spec, result.State)
	if err != nil {
		return PassResult{}, err
	}
	result.State = state
	return result, nil
}

func materializePrimaryPassArtifacts(spec PassSpec, state PassState) (PassState, error) {
	result := state
	for _, write := range spec.WritesArtifacts {
		if !write.FromPrimary {
			continue
		}
		artifact, err := NewPassArtifact(write.Name, write.Schema, state.Artifact)
		if err != nil {
			return PassState{}, fmt.Errorf("bind primary artifact %q: %w", write.Name, err)
		}
		if existing, ok := result.NamedArtifact(write.Name); ok {
			if !equalPassArtifact(existing, artifact) {
				return PassState{}, fmt.Errorf("primary artifact %q does not match handler-provided artifact", write.Name)
			}
			continue
		}
		result, err = result.WithNamedArtifacts(artifact)
		if err != nil {
			return PassState{}, fmt.Errorf("bind primary artifact %q: %w", write.Name, err)
		}
	}
	return result, nil
}

func equalPassResult(left, right PassResult) bool {
	return left.Changed == right.Changed && equalPassEffects(left.Effects, right.Effects) &&
		left.State.Schema == right.State.Schema && slices.Equal(left.State.Facts, right.State.Facts) &&
		bytes.Equal(left.State.Artifact, right.State.Artifact) &&
		equalPassArtifactEnvelopes(left.State.Artifacts, right.State.Artifacts)
}

func normalizePassVerification(verification PassVerification) (PassVerification, error) {
	effects, err := normalizePassEffects(verification.Effects)
	if err != nil {
		return PassVerification{}, fmt.Errorf("effects: %w", err)
	}
	verification.Effects = effects
	pending := slices.Clone(verification.PendingSpecializations)
	for index, key := range pending {
		pending[index] = strings.TrimSpace(key)
		if pending[index] == "" {
			return PassVerification{}, fmt.Errorf("pending specialization %d is empty", index)
		}
	}
	slices.Sort(pending)
	for index := 1; index < len(pending); index++ {
		if pending[index] == pending[index-1] {
			return PassVerification{}, fmt.Errorf("duplicate pending specialization %q", pending[index])
		}
	}
	verification.PendingSpecializations = pending
	return verification, nil
}

func equalPassVerification(left, right PassVerification) bool {
	return equalPassEffects(left.Effects, right.Effects) &&
		slices.Equal(left.PendingSpecializations, right.PendingSpecializations)
}

func normalizePassEffects(effects PassEffects) (PassEffects, error) {
	var err error
	effects.Input, err = normalizeEffectSet(effects.Input)
	if err != nil {
		return PassEffects{}, fmt.Errorf("input effects: %w", err)
	}
	effects.Artifact, err = normalizeEffectSet(effects.Artifact)
	if err != nil {
		return PassEffects{}, fmt.Errorf("artifact effects: %w", err)
	}
	effects.Introduced, err = normalizeEffectSet(effects.Introduced)
	if err != nil {
		return PassEffects{}, fmt.Errorf("introduced effects: %w", err)
	}
	return effects, nil
}

func normalizeEffectSet(input []PassEffect) ([]PassEffect, error) {
	result := append([]PassEffect{}, input...)
	for index, effect := range result {
		if !validPassEffect(effect) {
			return nil, fmt.Errorf("invalid effect %q", effect)
		}
		if index > 0 && result[index-1] == effect {
			return nil, fmt.Errorf("duplicate effect %q", effect)
		}
	}
	slices.Sort(result)
	for index := 1; index < len(result); index++ {
		if result[index] == result[index-1] {
			return nil, fmt.Errorf("duplicate effect %q", result[index])
		}
	}
	return result, nil
}

func validateCanonicalPassSpec(spec PassSpec) error {
	if strings.TrimSpace(string(spec.ID)) == "" || strings.TrimSpace(spec.InputSchema) == "" || strings.TrimSpace(spec.OutputSchema) == "" {
		return fmt.Errorf("canonical pass has an empty ID or schema")
	}
	if !spec.PreservesEvaluationOrder {
		return fmt.Errorf("canonical pass %q does not preserve evaluation order", spec.ID)
	}
	factSets := []struct {
		name  string
		facts []string
	}{{name: "read", facts: spec.ReadsFacts}, {name: "write", facts: spec.WritesFacts}}
	for _, factSet := range factSets {
		name, facts := factSet.name, factSet.facts
		seen := make(map[string]struct{}, len(facts))
		for _, fact := range facts {
			if strings.TrimSpace(fact) == "" {
				return fmt.Errorf("canonical pass %q has an empty %s fact", spec.ID, name)
			}
			if _, duplicate := seen[fact]; duplicate {
				return fmt.Errorf("canonical pass %q has duplicate %s fact %q", spec.ID, name, fact)
			}
			seen[fact] = struct{}{}
		}
	}
	artifactSets := []struct {
		name         string
		requirements []PassArtifactRequirement
	}{
		{name: "read", requirements: spec.ReadsArtifacts},
	}
	for _, artifactSet := range artifactSets {
		seen := make(map[PassArtifactName]struct{}, len(artifactSet.requirements))
		for _, requirement := range artifactSet.requirements {
			if strings.TrimSpace(string(requirement.Name)) == "" || strings.TrimSpace(requirement.Schema) == "" {
				return fmt.Errorf("canonical pass %q has an empty %s artifact name or schema", spec.ID, artifactSet.name)
			}
			if _, duplicate := seen[requirement.Name]; duplicate {
				return fmt.Errorf("canonical pass %q has duplicate %s artifact %q", spec.ID, artifactSet.name, requirement.Name)
			}
			seen[requirement.Name] = struct{}{}
		}
	}
	readArtifacts := make(map[PassArtifactName]struct{}, len(spec.ReadsArtifacts))
	for _, requirement := range spec.ReadsArtifacts {
		readArtifacts[requirement.Name] = struct{}{}
	}
	writtenArtifacts := make(map[PassArtifactName]struct{}, len(spec.WritesArtifacts))
	for _, write := range spec.WritesArtifacts {
		if strings.TrimSpace(string(write.Name)) == "" || strings.TrimSpace(write.Schema) == "" {
			return fmt.Errorf("canonical pass %q has an empty write artifact name or schema", spec.ID)
		}
		if _, duplicate := writtenArtifacts[write.Name]; duplicate {
			return fmt.Errorf("canonical pass %q has duplicate write artifact %q", spec.ID, write.Name)
		}
		if _, alsoRead := readArtifacts[write.Name]; alsoRead {
			return fmt.Errorf("canonical pass %q both reads and writes immutable artifact %q", spec.ID, write.Name)
		}
		if write.FromPrimary && write.Schema != spec.OutputSchema {
			return fmt.Errorf("canonical pass %q primary artifact %q schema is %q, want output schema %q", spec.ID, write.Name, write.Schema, spec.OutputSchema)
		}
		writtenArtifacts[write.Name] = struct{}{}
	}
	effects, err := normalizeEffectSet(spec.MayIntroduceEffects)
	if err != nil {
		return fmt.Errorf("canonical pass %q effects: %w", spec.ID, err)
	}
	if !slices.Equal(effects, spec.MayIntroduceEffects) {
		return fmt.Errorf("canonical pass %q effects are not in canonical order", spec.ID)
	}
	return nil
}

func validPassEffect(effect PassEffect) bool {
	switch effect {
	case PassEffectAllocate, PassEffectBlock, PassEffectCall, PassEffectDynamic,
		PassEffectFFI, PassEffectHost, PassEffectIO, PassEffectNondeterministic,
		PassEffectRead, PassEffectRetainRelease, PassEffectRootPublication,
		PassEffectSafepoint, PassEffectSuspend, PassEffectThrow, PassEffectWrite:
		return true
	default:
		return false
	}
}

func equalPassEffects(left, right PassEffects) bool {
	return slices.Equal(left.Input, right.Input) && slices.Equal(left.Artifact, right.Artifact) &&
		slices.Equal(left.Introduced, right.Introduced)
}

func validateSpecializationProof(spec PassSpec, changed bool, pending []string) error {
	if spec.ID != PassSpecialization {
		if len(pending) != 0 {
			return fmt.Errorf("reported pending specialization work outside specialization")
		}
		return nil
	}
	if changed != (len(pending) != 0) {
		return fmt.Errorf("changed=%t but verifier found %d pending specialization keys", changed, len(pending))
	}
	return nil
}

func failedPassExecution(state PassState, dumps []PassDump, err error) (PassExecution, error) {
	return PassExecution{State: clonePassState(state), Dumps: clonePassDumps(dumps)}, err
}

func normalizePassState(state PassState) (PassState, error) {
	state.Schema = strings.TrimSpace(state.Schema)
	if state.Schema == "" {
		return PassState{}, fmt.Errorf("schema is empty")
	}
	facts := slices.Clone(state.Facts)
	for index, fact := range facts {
		facts[index] = strings.TrimSpace(fact)
		if facts[index] == "" {
			return PassState{}, fmt.Errorf("fact %d is empty", index)
		}
	}
	slices.Sort(facts)
	for index := 1; index < len(facts); index++ {
		if facts[index] == facts[index-1] {
			return PassState{}, fmt.Errorf("duplicate fact %q", facts[index])
		}
	}
	artifact, err := canonicalJSON(state.Artifact)
	if err != nil {
		return PassState{}, fmt.Errorf("artifact: %w", err)
	}
	artifacts, err := normalizePassArtifactEnvelopePointer(state.Artifacts)
	if err != nil {
		return PassState{}, fmt.Errorf("artifacts: %w", err)
	}
	return PassState{Schema: state.Schema, Facts: facts, Artifact: artifact, Artifacts: artifacts}, nil
}

func canonicalJSON(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("JSON is empty")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("JSON contains multiple values")
		}
		return err
	}
	return nil
}

func requireFacts(have []string, required []string) error {
	set := make(map[string]struct{}, len(have))
	for _, fact := range have {
		set[fact] = struct{}{}
	}
	for _, fact := range required {
		if _, ok := set[fact]; !ok {
			return fmt.Errorf("missing required fact %q", fact)
		}
	}
	return nil
}

func validateKnownFacts(facts []string, known map[string]struct{}) error {
	for _, fact := range facts {
		if _, ok := known[fact]; !ok {
			return fmt.Errorf("unknown fact %q", fact)
		}
	}
	return nil
}

func registerPassArtifactSchema(known map[PassArtifactName]string, name PassArtifactName, schema string) error {
	if existing, ok := known[name]; ok && existing != schema {
		return fmt.Errorf("artifact %q uses schemas %q and %q", name, existing, schema)
	}
	known[name] = schema
	return nil
}

func validateInitialPassArtifacts(
	envelope *PassArtifactEnvelope,
	known map[PassArtifactName]string,
	produced map[PassArtifactName]struct{},
) error {
	if err := validateKnownPassArtifacts(envelope, known); err != nil {
		return err
	}
	if envelope == nil {
		return nil
	}
	for _, artifact := range envelope.Artifacts {
		if _, generated := produced[artifact.Name]; generated {
			return fmt.Errorf("artifact %q must be produced by its canonical pass, not injected initially", artifact.Name)
		}
	}
	return nil
}

func validateKnownPassArtifacts(envelope *PassArtifactEnvelope, known map[PassArtifactName]string) error {
	if envelope == nil {
		return nil
	}
	for _, artifact := range envelope.Artifacts {
		schema, ok := known[artifact.Name]
		if !ok {
			return fmt.Errorf("unknown artifact %q", artifact.Name)
		}
		if artifact.Schema != schema {
			return fmt.Errorf("artifact %q schema is %q, want %q", artifact.Name, artifact.Schema, schema)
		}
	}
	return nil
}

func requirePassArtifacts(envelope *PassArtifactEnvelope, required []PassArtifactRequirement) error {
	available := make(map[PassArtifactName]PassArtifact)
	if envelope != nil {
		for _, artifact := range envelope.Artifacts {
			available[artifact.Name] = artifact
		}
	}
	for _, requirement := range required {
		artifact, ok := available[requirement.Name]
		if !ok {
			return fmt.Errorf("missing required artifact %q with schema %q", requirement.Name, requirement.Schema)
		}
		if artifact.Schema != requirement.Schema {
			return fmt.Errorf("required artifact %q has schema %q, want %q", requirement.Name, artifact.Schema, requirement.Schema)
		}
	}
	return nil
}

func validatePassArtifactTransition(input, output PassState, spec PassSpec) error {
	inputArtifacts := passArtifactMap(input.Artifacts)
	outputArtifacts := passArtifactMap(output.Artifacts)
	writes := make(map[PassArtifactName]PassArtifactWrite, len(spec.WritesArtifacts))
	for _, write := range spec.WritesArtifacts {
		writes[write.Name] = write
		if _, exists := inputArtifacts[write.Name]; exists {
			return fmt.Errorf("declared write artifact %q already exists before the pass", write.Name)
		}
		artifact, exists := outputArtifacts[write.Name]
		if !exists {
			return fmt.Errorf("missing declared write artifact %q", write.Name)
		}
		if artifact.Schema != write.Schema {
			return fmt.Errorf("write artifact %q has schema %q, want %q", write.Name, artifact.Schema, write.Schema)
		}
		if write.FromPrimary {
			primary, err := NewPassArtifact(write.Name, write.Schema, output.Artifact)
			if err != nil {
				return fmt.Errorf("verify primary artifact %q: %w", write.Name, err)
			}
			if !equalPassArtifact(artifact, primary) {
				return fmt.Errorf("write artifact %q is not bound to the primary output", write.Name)
			}
		}
	}
	for name, artifact := range inputArtifacts {
		preserved, exists := outputArtifacts[name]
		if !exists {
			return fmt.Errorf("removed input artifact %q", name)
		}
		if !equalPassArtifact(artifact, preserved) {
			return fmt.Errorf("changed immutable input artifact %q", name)
		}
	}
	for name := range outputArtifacts {
		if _, exists := inputArtifacts[name]; exists {
			continue
		}
		if _, declared := writes[name]; !declared {
			return fmt.Errorf("added undeclared artifact %q", name)
		}
	}
	return nil
}

func passArtifactMap(envelope *PassArtifactEnvelope) map[PassArtifactName]PassArtifact {
	result := make(map[PassArtifactName]PassArtifact)
	if envelope == nil {
		return result
	}
	for _, artifact := range envelope.Artifacts {
		result[artifact.Name] = artifact
	}
	return result
}

func validateFactTransition(input, output []string, spec PassSpec, changed bool) error {
	inputSet := make(map[string]struct{}, len(input))
	outputSet := make(map[string]struct{}, len(output))
	writeSet := make(map[string]struct{}, len(spec.WritesFacts))
	for _, fact := range input {
		inputSet[fact] = struct{}{}
	}
	for _, fact := range output {
		outputSet[fact] = struct{}{}
	}
	for _, fact := range spec.WritesFacts {
		writeSet[fact] = struct{}{}
		if _, exists := inputSet[fact]; exists {
			return fmt.Errorf("declared write fact %q already exists before the pass", fact)
		}
		_, exists := outputSet[fact]
		if spec.ID == PassSpecialization && changed {
			if exists {
				return fmt.Errorf("fixed-point fact %q was published while specialization work remains", fact)
			}
			continue
		}
		if !exists {
			return fmt.Errorf("missing declared write fact %q", fact)
		}
	}
	for fact := range inputSet {
		if _, exists := outputSet[fact]; !exists {
			return fmt.Errorf("removed input fact %q", fact)
		}
	}
	for fact := range outputSet {
		if _, exists := inputSet[fact]; exists {
			continue
		}
		if _, declared := writeSet[fact]; !declared {
			return fmt.Errorf("added undeclared fact %q", fact)
		}
	}
	return nil
}

func validatePassEffects(spec PassSpec, effects PassEffects) error {
	delta := make([]PassEffect, 0, len(effects.Artifact))
	for _, effect := range effects.Artifact {
		if !slices.Contains(effects.Input, effect) {
			delta = append(delta, effect)
		}
	}
	if !slices.Equal(delta, effects.Introduced) {
		return fmt.Errorf("introduced effects %v do not equal output-minus-input delta %v", effects.Introduced, delta)
	}
	for _, effect := range effects.Introduced {
		if !slices.Contains(spec.MayIntroduceEffects, effect) {
			return fmt.Errorf("introduced undeclared %s effect", effect)
		}
		if !slices.Contains(effects.Artifact, effect) {
			return fmt.Errorf("introduced effect %q is absent from the output artifact effect set", effect)
		}
	}
	if slices.Contains(effects.Introduced, PassEffectRootPublication) {
		if spec.ID != PassPlaceGCRoots {
			return fmt.Errorf("root publication may only be introduced by %q", PassPlaceGCRoots)
		}
		if !slices.Contains(effects.Input, PassEffectSafepoint) {
			return fmt.Errorf("root publication requires an existing safepoint effect")
		}
	}
	return nil
}

func makePassDump(sequence int, id PassID, iteration int, state PassState, verification PassVerification, changed bool) (PassDump, error) {
	payload := struct {
		Sequence               int                   `json:"sequence"`
		Pass                   PassID                `json:"pass"`
		Iteration              int                   `json:"iteration"`
		Schema                 string                `json:"schema"`
		Facts                  []string              `json:"facts"`
		Artifact               json.RawMessage       `json:"artifact"`
		Artifacts              *PassArtifactEnvelope `json:"artifacts,omitempty"`
		Effects                PassEffects           `json:"effects"`
		Changed                bool                  `json:"changed"`
		PendingSpecializations []string              `json:"pendingSpecializations,omitempty"`
	}{
		Sequence: sequence, Pass: id, Iteration: iteration, Schema: state.Schema,
		Facts: slices.Clone(state.Facts), Artifact: slices.Clone(state.Artifact),
		Artifacts: clonePassArtifactEnvelope(state.Artifacts),
		Effects:   clonePassEffects(verification.Effects), Changed: changed,
		PendingSpecializations: slices.Clone(verification.PendingSpecializations),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return PassDump{}, err
	}
	digest := sha256.Sum256(encoded)
	return PassDump{
		Sequence: sequence, Pass: id, Iteration: iteration, Schema: state.Schema,
		Facts: slices.Clone(state.Facts), Artifact: slices.Clone(state.Artifact),
		Artifacts: clonePassArtifactEnvelope(state.Artifacts),
		Effects:   clonePassEffects(verification.Effects), Changed: changed,
		PendingSpecializations: slices.Clone(verification.PendingSpecializations),
		ContentHash:            hex.EncodeToString(digest[:]),
	}, nil
}

func clonePassEffects(effects PassEffects) PassEffects {
	return PassEffects{
		Input:      append([]PassEffect{}, effects.Input...),
		Artifact:   append([]PassEffect{}, effects.Artifact...),
		Introduced: append([]PassEffect{}, effects.Introduced...),
	}
}

func clonePassState(state PassState) PassState {
	return PassState{
		Schema: state.Schema, Facts: slices.Clone(state.Facts), Artifact: slices.Clone(state.Artifact),
		Artifacts: clonePassArtifactEnvelope(state.Artifacts),
	}
}

func clonePassSpec(spec PassSpec) PassSpec {
	spec.ReadsFacts = slices.Clone(spec.ReadsFacts)
	spec.WritesFacts = slices.Clone(spec.WritesFacts)
	spec.ReadsArtifacts = slices.Clone(spec.ReadsArtifacts)
	spec.WritesArtifacts = slices.Clone(spec.WritesArtifacts)
	spec.MayIntroduceEffects = slices.Clone(spec.MayIntroduceEffects)
	return spec
}

func clonePassDumps(dumps []PassDump) []PassDump {
	result := make([]PassDump, len(dumps))
	for index, dump := range dumps {
		result[index] = dump
		result[index].Facts = slices.Clone(dump.Facts)
		result[index].Artifact = slices.Clone(dump.Artifact)
		result[index].Artifacts = clonePassArtifactEnvelope(dump.Artifacts)
		result[index].Effects = clonePassEffects(dump.Effects)
		result[index].PendingSpecializations = slices.Clone(dump.PendingSpecializations)
	}
	return result
}
