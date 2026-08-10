package tsfrontend

import (
	"fmt"
	"slices"

	"github.com/microsoft/typescript-go/internal/buildplan"
	"github.com/microsoft/typescript-go/internal/frontendwire"
)

// BuildPlanSchemaVersion is the wire/hash version for target-dependent plans.
// A schema bump invalidates all plans even when their individual fields are
// unchanged.
const BuildPlanSchemaVersion = buildplan.SchemaVersion

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
type BackendRequest = buildplan.BackendRequest

// BuildPlan is an immutable target-dependent request. FrontendHash links the
// request to the exact serialized frontend snapshot it consumes. A backend
// must bind it to a validated TargetContext before representation planning or
// MIR lowering; BuildPlan validation alone does not make it executable.
type BuildPlan = buildplan.Plan

// DecodeBuildPlan strictly decodes and validates a target-dependent plan.
// Unknown fields are rejected so future or misspelled backend choices cannot
// be silently discarded before artifact-key validation.
func DecodeBuildPlan(data []byte) (*BuildPlan, error) {
	return buildplan.Decode(data)
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
	plan, err := buildplan.New(frontend.ContentHash, normalized.Profile, backend)
	if err != nil {
		return BuildPlan{}, fmt.Errorf("construct build plan: %w", err)
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
	if err := buildplan.Validate(plan); err != nil {
		return err
	}
	options := buildPlanOptions(plan)
	canonical := normalizeBingoOptions(options)
	if canonical.Profile != plan.Profile || !buildplan.EqualBackendRequest(backendRequestFromOptions(canonical), plan.Backend) {
		return fmt.Errorf("build plan backend request is not canonical")
	}
	return validateBuildPlanOptions(canonical)
}

func buildPlanContentHash(plan BuildPlan) (string, error) {
	return buildplan.ContentHash(plan)
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
	return buildplan.EqualBackendRequest(left, right)
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
