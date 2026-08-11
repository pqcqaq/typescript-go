package bingo

import "fmt"

// PassID names one stage in the target-independent to target-aware lowering
// contract. The sequence is part of the IR provenance and is not configurable
// by a caller.
type PassID string

// PassEffect is one verifier-derived observable effect. RootPublication is a
// structural GC effect introduced after exact runtime effects are frozen; it
// is deliberately distinct from creating a safepoint.
type PassEffect string

const (
	PassEffectAllocate         PassEffect = "allocate"
	PassEffectBlock            PassEffect = "block"
	PassEffectCall             PassEffect = "call"
	PassEffectDynamic          PassEffect = "dynamic"
	PassEffectFFI              PassEffect = "ffi"
	PassEffectHost             PassEffect = "host"
	PassEffectIO               PassEffect = "io"
	PassEffectNondeterministic PassEffect = "nondeterministic"
	PassEffectRead             PassEffect = "read"
	PassEffectRetainRelease    PassEffect = "retain-release"
	PassEffectRootPublication  PassEffect = "root-publication"
	PassEffectSafepoint        PassEffect = "safepoint"
	PassEffectSuspend          PassEffect = "suspend"
	PassEffectThrow            PassEffect = "throw"
	PassEffectWrite            PassEffect = "write"
)

const (
	PassValidateSnapshot   PassID = "validate-snapshot-source-type-plan"
	PassTypedHIR           PassID = "typed-hir"
	PassEvaluationOrder    PassID = "evaluation-order-and-semantic-sugar"
	PassSpecialization     PassID = "specialization-fixed-point"
	PassVarianceAliasing   PassID = "variance-aliasing-and-conversions"
	PassResolveTarget      PassID = "resolve-target-context"
	PassRepresentationPlan PassID = "target-representation-plan"
	PassMIRCFGSSA          PassID = "mir-cfg-ssa"
	PassCleanupState       PassID = "cleanup-exception-async-state"
	PassStructuralVerifier PassID = "structural-mir-verifier"
	PassCapabilityBinding  PassID = "capability-binding-effect-freeze"
	PassOptimizeProvenMIR  PassID = "optimize-proven-mir"
	PassPlaceGCRoots       PassID = "place-gc-roots-freeze-cleanup"
	PassFinalVerifier      PassID = "final-verifier"
)

// PassSpec records the schema/fact/effect boundary of one pass. The first
// vertical slice only executes the validation and typed-HIR stages, but every
// later stage is named here so a consumer cannot silently invent a different
// order.
type PassSpec struct {
	ID                       PassID                    `json:"id"`
	InputSchema              string                    `json:"inputSchema"`
	OutputSchema             string                    `json:"outputSchema"`
	ReadsFacts               []string                  `json:"readsFacts"`
	WritesFacts              []string                  `json:"writesFacts"`
	ReadsArtifacts           []PassArtifactRequirement `json:"readsArtifacts,omitempty"`
	WritesArtifacts          []PassArtifactWrite       `json:"writesArtifacts,omitempty"`
	MayIntroduceEffects      []PassEffect              `json:"mayIntroduceEffects"`
	PreservesEvaluationOrder bool                      `json:"preservesEvaluationOrder"`
}

var canonicalPassSpecs = [...]PassSpec{
	{ID: PassValidateSnapshot, InputSchema: "snapshot-v2", OutputSchema: "source-type-plan-v2", ReadsFacts: []string{"syntax", "types", "symbols", "signatures", "module-graph"}, WritesFacts: []string{"source-type-plan"}, PreservesEvaluationOrder: true},
	{ID: PassTypedHIR, InputSchema: "source-type-plan-v2", OutputSchema: "hir-v8", ReadsFacts: []string{"source-type-plan"}, WritesFacts: []string{"typed-hir", "effect-proof"}, PreservesEvaluationOrder: true},
	{ID: PassEvaluationOrder, InputSchema: "hir-v8", OutputSchema: "hir-v8", ReadsFacts: []string{"typed-hir"}, WritesFacts: []string{"evaluation-order"}, PreservesEvaluationOrder: true},
	{ID: PassSpecialization, InputSchema: "hir-v8", OutputSchema: "hir-v8", ReadsFacts: []string{"typed-hir", "type-arguments"}, WritesFacts: []string{"specialization-fixed-point"}, PreservesEvaluationOrder: true},
	{
		ID: PassVarianceAliasing, InputSchema: "hir-v8", OutputSchema: "hir-v8",
		ReadsFacts: []string{"specialization-fixed-point", "property-facts"}, WritesFacts: []string{"variance-proof", "conversion-plan"},
		WritesArtifacts:          []PassArtifactWrite{{Name: PassArtifactTypedHIR, Schema: "hir-v8", FromPrimary: true}},
		PreservesEvaluationOrder: true,
	},
	{
		ID: PassResolveTarget, InputSchema: "hir-v8", OutputSchema: "target-context-v1",
		ReadsFacts: []string{"canonical-build-plan", "runtime-manifest", "toolchain-manifest"}, WritesFacts: []string{"target-context", "data-layout", "available-capability-catalog"},
		ReadsArtifacts: []PassArtifactRequirement{
			{Name: PassArtifactBuildPlan, Schema: "build-plan-v1"},
			{Name: PassArtifactRuntimeManifest, Schema: "runtime-manifest-v1"},
			{Name: PassArtifactToolchainManifest, Schema: "toolchain-manifest-v1"},
		},
		WritesArtifacts: []PassArtifactWrite{
			{Name: PassArtifactTargetContext, Schema: "target-context-v1", FromPrimary: true},
			{Name: PassArtifactDataLayout, Schema: "data-layout-v1"},
			{Name: PassArtifactAvailableCapabilityCatalog, Schema: "available-capability-catalog-v1"},
		},
		PreservesEvaluationOrder: true,
	},
	{
		ID: PassRepresentationPlan, InputSchema: "target-context-v1", OutputSchema: "rep-plan-v2",
		ReadsFacts: []string{"available-capability-catalog", "canonical-build-plan", "conversion-plan", "data-layout", "target-context", "typed-hir"}, WritesFacts: []string{"representation-plan"},
		ReadsArtifacts: []PassArtifactRequirement{
			{Name: PassArtifactTypedHIR, Schema: "hir-v8"},
			{Name: PassArtifactBuildPlan, Schema: "build-plan-v1"},
			{Name: PassArtifactRuntimeManifest, Schema: "runtime-manifest-v1"},
			{Name: PassArtifactToolchainManifest, Schema: "toolchain-manifest-v1"},
			{Name: PassArtifactTargetContext, Schema: "target-context-v1"},
			{Name: PassArtifactDataLayout, Schema: "data-layout-v1"},
			{Name: PassArtifactAvailableCapabilityCatalog, Schema: "available-capability-catalog-v1"},
		},
		WritesArtifacts:          []PassArtifactWrite{{Name: PassArtifactRepresentationPlan, Schema: "rep-plan-v2", FromPrimary: true}},
		PreservesEvaluationOrder: true,
	},
	{
		ID: PassMIRCFGSSA, InputSchema: "rep-plan-v2", OutputSchema: "mir-v3",
		ReadsFacts: []string{"representation-plan", "evaluation-order"}, WritesFacts: []string{"mir-cfg", "ssa"},
		ReadsArtifacts: []PassArtifactRequirement{
			{Name: PassArtifactTypedHIR, Schema: "hir-v8"},
			{Name: PassArtifactRepresentationPlan, Schema: "rep-plan-v2"},
		},
		PreservesEvaluationOrder: true,
	},
	{
		ID: PassCleanupState, InputSchema: "mir-v3", OutputSchema: "mir-v3",
		ReadsFacts: []string{"effect-proof", "mir-cfg"}, WritesFacts: []string{"cleanup-state"},
		ReadsArtifacts:           []PassArtifactRequirement{{Name: PassArtifactRepresentationPlan, Schema: "rep-plan-v2"}},
		MayIntroduceEffects:      []PassEffect{PassEffectAllocate, PassEffectCall, PassEffectRetainRelease, PassEffectSafepoint, PassEffectSuspend, PassEffectThrow},
		PreservesEvaluationOrder: true,
	},
	{
		ID: PassStructuralVerifier, InputSchema: "mir-v3", OutputSchema: "mir-v3",
		ReadsFacts: []string{"mir-cfg", "ssa", "cleanup-state"}, WritesFacts: []string{"verified-structural-mir"},
		ReadsArtifacts:           []PassArtifactRequirement{{Name: PassArtifactRepresentationPlan, Schema: "rep-plan-v2"}},
		PreservesEvaluationOrder: true,
	},
	{
		ID: PassCapabilityBinding, InputSchema: "mir-v3", OutputSchema: "mir-v3",
		ReadsFacts: []string{"available-capability-catalog", "verified-structural-mir"}, WritesFacts: []string{"bound-capability-closure", "frozen-effects"},
		ReadsArtifacts: []PassArtifactRequirement{
			{Name: PassArtifactRepresentationPlan, Schema: "rep-plan-v2"},
			{Name: PassArtifactAvailableCapabilityCatalog, Schema: "available-capability-catalog-v1"},
		},
		MayIntroduceEffects:      []PassEffect{PassEffectAllocate, PassEffectBlock, PassEffectCall, PassEffectDynamic, PassEffectFFI, PassEffectHost, PassEffectIO, PassEffectNondeterministic, PassEffectRead, PassEffectRetainRelease, PassEffectSafepoint, PassEffectSuspend, PassEffectThrow, PassEffectWrite},
		PreservesEvaluationOrder: true,
	},
	{
		ID: PassOptimizeProvenMIR, InputSchema: "mir-v3", OutputSchema: "mir-v3",
		ReadsFacts: []string{"frozen-effects", "verified-structural-mir"}, WritesFacts: []string{"optimized-mir"},
		ReadsArtifacts:           []PassArtifactRequirement{{Name: PassArtifactRepresentationPlan, Schema: "rep-plan-v2"}},
		PreservesEvaluationOrder: true,
	},
	{
		ID: PassPlaceGCRoots, InputSchema: "mir-v3", OutputSchema: "mir-v3",
		ReadsFacts: []string{"optimized-mir", "frozen-effects"}, WritesFacts: []string{"root-map", "frozen-cleanup"},
		ReadsArtifacts:           []PassArtifactRequirement{{Name: PassArtifactRepresentationPlan, Schema: "rep-plan-v2"}},
		MayIntroduceEffects:      []PassEffect{PassEffectRootPublication},
		PreservesEvaluationOrder: true,
	},
	{
		ID: PassFinalVerifier, InputSchema: "mir-v3", OutputSchema: "verified-mir-v3",
		ReadsFacts: []string{"bound-capability-closure", "root-map", "frozen-cleanup", "frozen-effects"}, WritesFacts: []string{"final-mir"},
		ReadsArtifacts:           []PassArtifactRequirement{{Name: PassArtifactRepresentationPlan, Schema: "rep-plan-v2"}},
		PreservesEvaluationOrder: true,
	},
}

// PassDAG is a detached copy retained for callers that need to serialize the
// declared sequence. Validation always compares against canonicalPassSpecs so
// mutation of this compatibility variable cannot weaken the contract.
var PassDAG = passIDs()

func passIDs() []PassID {
	result := make([]PassID, len(canonicalPassSpecs))
	for index, spec := range canonicalPassSpecs {
		result[index] = spec.ID
	}
	return result
}

// PassSpecs returns a detached copy of the canonical pass metadata.
func PassSpecs() []PassSpec {
	result := make([]PassSpec, len(canonicalPassSpecs))
	for index, spec := range canonicalPassSpecs {
		result[index] = spec
		result[index].ReadsFacts = append([]string(nil), spec.ReadsFacts...)
		result[index].WritesFacts = append([]string(nil), spec.WritesFacts...)
		result[index].ReadsArtifacts = append([]PassArtifactRequirement(nil), spec.ReadsArtifacts...)
		result[index].WritesArtifacts = append([]PassArtifactWrite(nil), spec.WritesArtifacts...)
		result[index].MayIntroduceEffects = append([]PassEffect{}, spec.MayIntroduceEffects...)
	}
	return result
}

// ValidatePassPrefix rejects duplicates, omissions, out-of-order execution,
// truncation before terminal, or an unknown terminal. A prefix is the only
// supported partial execution shape; callers cannot select arbitrary passes.
func ValidatePassPrefix(sequence []PassID, terminal PassID) error {
	terminalIndex, ok := canonicalPassIndex(terminal)
	if !ok {
		return fmt.Errorf("unknown terminal pass %q", terminal)
	}
	want := passIDs()[:terminalIndex+1]
	if len(sequence) != len(want) {
		return fmt.Errorf("pass prefix through %q has %d passes, want %d", terminal, len(sequence), len(want))
	}
	for index, pass := range sequence {
		if pass != want[index] {
			return fmt.Errorf("pass %d is %q, want %q", index, pass, want[index])
		}
	}
	return nil
}

// ValidatePassSequence validates the complete canonical sequence.
func ValidatePassSequence(sequence []PassID) error {
	return ValidatePassPrefix(sequence, PassFinalVerifier)
}

func canonicalPassIndex(id PassID) (int, bool) {
	for index, spec := range canonicalPassSpecs {
		if spec.ID == id {
			return index, true
		}
	}
	return 0, false
}
