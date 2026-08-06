package tsfrontend

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/microsoft/typescript-go/internal/frontendwire"
	jsonx "github.com/microsoft/typescript-go/internal/json"
)

// BuildPlanSchemaVersion is the wire/hash version for target-dependent plans.
// A schema bump invalidates all plans even when their individual fields are
// unchanged.
const BuildPlanSchemaVersion uint32 = 1

// FrontendSnapshot is the target-independent handoff used by cache clients.
// Program contains the serialized checker facts; ContentHash is derived only
// from source/frontend semantics and provenance. The wrapper is deliberately
// small so the existing ProgramSnapshot wire format remains readable during
// the schema-v2 migration.
type FrontendSnapshot = frontendwire.FrontendSnapshot

// DecodeFrontendSnapshot strictly decodes the wrapper used by disk replay.
// Unknown fields are rejected so a backend request cannot be silently dropped
// while crossing the frontend-only process boundary.
func DecodeFrontendSnapshot(data []byte) (*FrontendSnapshot, error) {
	return frontendwire.DecodeFrontendSnapshot(data)
}

// BackendRequest contains canonical target/runtime choices that must not
// affect frontend capture or its cache key. It is an unresolved request, not
// proof that a matching toolchain or runtime capability is installed.
type BackendRequest struct {
	Target      string          `json:"target"`
	CPU         string          `json:"cpu"`
	Features    []string        `json:"features,omitempty"`
	Runtime     string          `json:"runtime"`
	GC          GCMode          `json:"gc"`
	Exceptions  ExceptionMode   `json:"exceptions"`
	Overflow    OverflowMode    `json:"overflow"`
	BoundsCheck BoundsCheckMode `json:"boundsCheck"`
	Emit        []EmitArtifact  `json:"emit"`
	LLVMMajor   int             `json:"llvmMajor"`
}

// BuildPlan is an immutable target-dependent request. FrontendHash links the
// request to the exact serialized frontend snapshot it consumes. A backend
// must bind it to a validated TargetContext before representation planning or
// MIR lowering; BuildPlan validation alone does not make it executable.
type BuildPlan struct {
	SchemaVersion uint32 `json:"schemaVersion"`
	FrontendHash  string `json:"frontendHash"`
	// Profile is repeated as plan provenance because profile-specific lowering
	// and runtime capability choices are target-dependent, while the same value
	// is also retained in the target-independent frontend projection.
	Profile     Profile        `json:"profile"`
	Backend     BackendRequest `json:"backend"`
	ContentHash string         `json:"contentHash"`
}

type buildPlanHashInput struct {
	SchemaVersion uint32         `json:"schemaVersion"`
	FrontendHash  string         `json:"frontendHash"`
	Profile       Profile        `json:"profile"`
	Backend       BackendRequest `json:"backend"`
}

// CanonicalBytes returns the validated deterministic disk representation of a
// target-dependent plan.
func (p BuildPlan) CanonicalBytes() ([]byte, error) {
	if err := ValidateBuildPlan(p); err != nil {
		return nil, err
	}
	return json.MarshalIndent(p, "", "  ")
}

// DecodeBuildPlan strictly decodes and validates a target-dependent plan.
// Unknown fields are rejected so future or misspelled backend choices cannot
// be silently discarded before artifact-key validation.
func DecodeBuildPlan(data []byte) (*BuildPlan, error) {
	var plan BuildPlan
	if err := jsonx.Unmarshal(data, &plan, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode build plan: %w", err)
	}
	if err := ValidateBuildPlan(plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

// NewFrontendSnapshot wraps an already target-independent ProgramSnapshot.
// Backend fields are rejected by ValidateProgramSnapshot rather than silently
// projected away here, so raw capture regressions cannot be hidden.
func NewFrontendSnapshot(snapshot ProgramSnapshot) (FrontendSnapshot, error) {
	return frontendwire.NewFrontendSnapshot(snapshot)
}

// ValidateFrontendSnapshot proves that the wrapper and its nested program are
// the exact target-independent projection produced by NewFrontendSnapshot.
// Callers cannot bind a BuildPlan to a digest copied from another project.
func ValidateFrontendSnapshot(frontend FrontendSnapshot) error {
	return frontendwire.ValidateFrontendSnapshot(frontend)
}

// FrontendSnapshotKey computes the target-independent key for a snapshot.
func FrontendSnapshotKey(snapshot ProgramSnapshot) (string, error) {
	frontend, err := NewFrontendSnapshot(snapshot)
	if err != nil {
		return "", err
	}
	return frontend.ContentHash, nil
}

// ResolveBuildPlan resolves defaults, canonicalizes backend choices, and binds
// them to a validated target-independent frontend snapshot. It deliberately
// does not resolve toolchain, target, data-layout, or runtime capabilities;
// Phase 2A capability binding must happen before any backend consumes it.
func ResolveBuildPlan(frontend FrontendSnapshot, options BingoOptions) (BuildPlan, error) {
	if err := ValidateFrontendSnapshot(frontend); err != nil {
		return BuildPlan{}, err
	}
	normalized := normalizeBingoOptions(options)
	if err := validateBuildPlanOptions(normalized); err != nil {
		return BuildPlan{}, err
	}
	if normalized.Profile != frontend.Program.Config.Bingo.Profile {
		return BuildPlan{}, fmt.Errorf(
			"build plan profile %q does not match frontend snapshot profile %q",
			normalized.Profile, frontend.Program.Config.Bingo.Profile,
		)
	}
	backend := backendRequestFromOptions(normalized)
	contentHash, err := buildPlanContentHash(BuildPlan{
		SchemaVersion: BuildPlanSchemaVersion,
		FrontendHash:  frontend.ContentHash,
		Profile:       normalized.Profile,
		Backend:       backend,
	})
	if err != nil {
		return BuildPlan{}, fmt.Errorf("hash build plan key: %w", err)
	}
	plan := BuildPlan{
		SchemaVersion: BuildPlanSchemaVersion,
		FrontendHash:  frontend.ContentHash,
		Profile:       normalized.Profile,
		Backend:       backend,
		ContentHash:   contentHash,
	}
	if err := ValidateBuildPlan(plan); err != nil {
		return BuildPlan{}, fmt.Errorf("validate resolved build plan: %w", err)
	}
	return plan, nil
}

// ValidateBuildPlan verifies canonical backend choices and recomputes the plan
// hash. It proves the plan is internally intact; a consumer must additionally
// compare FrontendHash with the validated FrontendSnapshot it is lowering.
func ValidateBuildPlan(plan BuildPlan) error {
	if plan.SchemaVersion != BuildPlanSchemaVersion {
		return fmt.Errorf("unsupported build plan schema %d", plan.SchemaVersion)
	}
	if !isDigest(plan.FrontendHash) {
		return fmt.Errorf("invalid build plan frontend hash %q", plan.FrontendHash)
	}
	if !isDigest(plan.ContentHash) {
		return fmt.Errorf("invalid build plan content hash %q", plan.ContentHash)
	}

	options := buildPlanOptions(plan)
	if err := validateBuildPlanOptions(options); err != nil {
		return err
	}
	canonical := normalizeBingoOptions(options)
	if canonical.Profile != plan.Profile || !equalBackendRequest(backendRequestFromOptions(canonical), plan.Backend) {
		return fmt.Errorf("build plan backend request is not canonical")
	}

	want, err := buildPlanContentHash(plan)
	if err != nil {
		return fmt.Errorf("hash build plan key: %w", err)
	}
	if plan.ContentHash != want {
		return fmt.Errorf("build plan content hash mismatch: got %s, want %s", plan.ContentHash, want)
	}
	return nil
}

func buildPlanContentHash(plan BuildPlan) (string, error) {
	return hashCanonical(buildPlanHashInput{
		SchemaVersion: plan.SchemaVersion,
		FrontendHash:  plan.FrontendHash,
		Profile:       plan.Profile,
		Backend:       plan.Backend,
	})
}

func backendRequestFromOptions(options BingoOptions) BackendRequest {
	return BackendRequest{
		Target:      options.TargetTriple,
		CPU:         options.CPU,
		Features:    slices.Clone(options.Features),
		Runtime:     options.Runtime,
		GC:          options.GC,
		Exceptions:  options.Exceptions,
		Overflow:    options.Overflow,
		BoundsCheck: options.BoundsCheck,
		Emit:        slices.Clone(options.Emit),
		LLVMMajor:   options.LLVMMajor,
	}
}

func buildPlanOptions(plan BuildPlan) BingoOptions {
	return BingoOptions{
		Profile:      plan.Profile,
		Runtime:      plan.Backend.Runtime,
		LLVMMajor:    plan.Backend.LLVMMajor,
		TargetTriple: plan.Backend.Target,
		CPU:          plan.Backend.CPU,
		Features:     slices.Clone(plan.Backend.Features),
		GC:           plan.Backend.GC,
		Exceptions:   plan.Backend.Exceptions,
		Overflow:     plan.Backend.Overflow,
		BoundsCheck:  plan.Backend.BoundsCheck,
		Emit:         slices.Clone(plan.Backend.Emit),
	}
}

func equalBackendRequest(left, right BackendRequest) bool {
	return left.Target == right.Target &&
		left.CPU == right.CPU &&
		slices.Equal(left.Features, right.Features) &&
		left.Runtime == right.Runtime &&
		left.GC == right.GC &&
		left.Exceptions == right.Exceptions &&
		left.Overflow == right.Overflow &&
		left.BoundsCheck == right.BoundsCheck &&
		slices.Equal(left.Emit, right.Emit) &&
		left.LLVMMajor == right.LLVMMajor
}

func validateBuildPlanOptions(options BingoOptions) error {
	switch options.Profile {
	case ProfileStatic, ProfileInterop, ProfileUnsafe:
	case ProfileDynamic:
		return fmt.Errorf("build plan profile %q is unavailable", options.Profile)
	default:
		return fmt.Errorf("build plan profile %q is invalid", options.Profile)
	}
	if options.LLVMMajor != 20 {
		return fmt.Errorf("build plan LLVM major %d is unsupported", options.LLVMMajor)
	}
	switch options.GC {
	case GCTracing, GCArc, GCArena:
	default:
		return fmt.Errorf("build plan GC mode %q is invalid", options.GC)
	}
	switch options.Exceptions {
	case ExceptionsNone:
	case ExceptionsLLVMEH:
		return fmt.Errorf("build plan exception mode %q is unavailable", options.Exceptions)
	default:
		return fmt.Errorf("build plan exception mode %q is invalid", options.Exceptions)
	}
	if options.Overflow != OverflowJSNumber {
		return fmt.Errorf("build plan overflow mode %q is invalid", options.Overflow)
	}
	switch options.BoundsCheck {
	case BoundsCheckOn, BoundsCheckOff:
	default:
		return fmt.Errorf("build plan bounds-check mode %q is invalid", options.BoundsCheck)
	}
	for _, artifact := range options.Emit {
		switch artifact {
		case EmitHIR, EmitMIR, EmitLLVM, EmitObject:
		default:
			return fmt.Errorf("build plan emit artifact %q is invalid", artifact)
		}
	}
	return nil
}

func frontendBingoOptions(input BingoOptions) BingoOptions {
	// Profile is a source boundary. Everything else is selected by the backend
	// planner and must be erased before frontend hashing.
	return BingoOptions{Profile: normalizeBingoOptions(input).Profile}
}
