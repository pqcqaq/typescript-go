package tsfrontend

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/collections"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/frontendwire"
	jsonx "github.com/microsoft/typescript-go/internal/json"
)

// OptionsSchemaVersion is the canonical configuration schema version emitted by
// the frontend. It must be bumped when the serialized meaning of an option
// changes.
const OptionsSchemaVersion = frontendwire.OptionsSchemaVersion

// Profile selects the source-language boundary that the frontend will enforce.
// Dynamic is intentionally represented so a caller can receive a stable
// capability diagnostic; it is not available in the Phase 1 compiler.
type Profile = frontendwire.Profile

const (
	// ProfileStatic is the default statically representable TypeScript subset.
	ProfileStatic = frontendwire.ProfileStatic
	// ProfileInterop permits explicitly declared DynamicValue/host boundaries.
	ProfileInterop = frontendwire.ProfileInterop
	// ProfileUnsafe permits explicitly declared unsafe intrinsics or FFI edges.
	ProfileUnsafe = frontendwire.ProfileUnsafe
	// ProfileDynamic names the future dynamic runtime product and is unavailable.
	ProfileDynamic = frontendwire.ProfileDynamic
)

// GCMode selects the ownership/collection contract recorded in provenance.
type GCMode = frontendwire.GCMode

const (
	// GCTracing is the general static profile's non-moving tracing collector.
	GCTracing = frontendwire.GCTracing
	// GCArc requests reference counting and requires a later acyclicity proof.
	GCArc = frontendwire.GCArc
	// GCArena requests an explicitly bounded arena lifetime.
	GCArena = frontendwire.GCArena
)

// ExceptionMode selects the exception ABI expected by the runtime manifest.
type ExceptionMode = frontendwire.ExceptionMode

const (
	// ExceptionsNone disables exception handling for the initial Phase 2A slice.
	ExceptionsNone = frontendwire.ExceptionsNone
	// ExceptionsLLVMEH names the future LLVM exception/unwind ABI. It remains
	// unavailable until the native EH implementation is complete.
	ExceptionsLLVMEH = frontendwire.ExceptionsLLVMEH
)

// OverflowMode selects number/overflow semantics. The first profile preserves
// JavaScript's IEEE-754 number behavior rather than integer overflow behavior.
type OverflowMode = frontendwire.OverflowMode

const (
	// OverflowJSNumber preserves JavaScript number conversion and overflow rules.
	OverflowJSNumber = frontendwire.OverflowJSNumber
)

// BoundsCheckMode controls whether generated accesses retain explicit checks.
type BoundsCheckMode = frontendwire.BoundsCheckMode

const (
	// BoundsCheckOn keeps explicit checks in the semantic contract.
	BoundsCheckOn = frontendwire.BoundsCheckOn
	// BoundsCheckOff requests unchecked accesses and is reserved for a later
	// capability/unsafe validation pass.
	BoundsCheckOff = frontendwire.BoundsCheckOff
)

// EmitArtifact names a requested frontend/backend artifact. Phase 1 consumes
// the values for canonical configuration only; it does not emit LLVM yet.
type EmitArtifact = frontendwire.EmitArtifact

const (
	// EmitHIR requests a Bingo HIR artifact.
	EmitHIR = frontendwire.EmitHIR
	// EmitMIR requests a Bingo MIR artifact.
	EmitMIR = frontendwire.EmitMIR
	// EmitLLVM requests an LLVM IR artifact.
	EmitLLVM = frontendwire.EmitLLVM
	// EmitObject requests a target object artifact.
	EmitObject = frontendwire.EmitObject
)

// BingoOptions is the user-controlled ts2bin portion of compiler configuration.
// Zero values are normalized by NormalizeOptions; slices are copied, sorted
// where they denote sets, and never retained by reference.
type BingoOptions = frontendwire.BingoOptions

// NormalizedOptions is an independent, effective frontend configuration. The
// CompilerOptions pointer is a private clone of the input semantics; callers
// should use CompilerOptionsCopy when passing options to a mutable tsgo API.
type NormalizedOptions struct {
	// SchemaVersion identifies the canonical Bingo options schema.
	SchemaVersion int `json:"schemaVersion"`
	// Bingo contains normalized ts2bin options.
	Bingo BingoOptions `json:"bingoOptions"`
	// CompilerOptions contains a cloned, normalized tsgo compiler option set.
	CompilerOptions *core.CompilerOptions `json:"typescript"`
}

// canonicalBingoOptions preserves the schema-v1 options encoding. The shared
// frontendwire.BingoOptions DTO uses omitempty so a FrontendSnapshot exposes
// only source-level fields; the user-facing normalized-options contract must
// still emit every backend field explicitly until its schema is bumped.
type canonicalBingoOptions struct {
	Profile      Profile         `json:"profile"`
	Runtime      string          `json:"runtime"`
	LLVMMajor    int             `json:"llvmMajor"`
	TargetTriple string          `json:"targetTriple"`
	CPU          string          `json:"cpu"`
	Features     []string        `json:"features"`
	GC           GCMode          `json:"gc"`
	Exceptions   ExceptionMode   `json:"exceptions"`
	Overflow     OverflowMode    `json:"overflow"`
	BoundsCheck  BoundsCheckMode `json:"boundsCheck"`
	Emit         []EmitArtifact  `json:"emit"`
}

// DefaultBingoOptions returns the Phase 1 defaults. It allocates fresh slices,
// so the returned value can be modified by one compilation without affecting a
// concurrent compilation.
func DefaultBingoOptions() BingoOptions {
	return BingoOptions{
		Profile:     ProfileStatic,
		Runtime:     "core-es2020",
		LLVMMajor:   20,
		CPU:         "generic",
		Features:    []string{},
		GC:          GCTracing,
		Exceptions:  ExceptionsNone,
		Overflow:    OverflowJSNumber,
		BoundsCheck: BoundsCheckOn,
		Emit:        []EmitArtifact{EmitHIR, EmitMIR, EmitLLVM, EmitObject},
	}
}

// NormalizeOptions applies defaults and validates the profile/strictness
// contract. It never mutates either input. A returned configuration is useful
// for diagnostics and inspection even when diagnostics contains errors.
// Dynamic profile requests are retained and reported as a stable capability
// diagnostic because the dynamic runtime is not part of Phase 1.
func NormalizeOptions(input BingoOptions, compilerOptions *core.CompilerOptions) (NormalizedOptions, []Diagnostic) {
	return normalizeOptions(input, compilerOptions, true)
}

// normalizeFrontendOptions applies the source-language profile and TypeScript
// semantic contract while deferring target/runtime validation to BuildPlan.
// The normalized backend values are retained internally so ResolveBuildPlan
// can validate the exact user request after capture.
func normalizeFrontendOptions(input BingoOptions, compilerOptions *core.CompilerOptions) (NormalizedOptions, []Diagnostic) {
	return normalizeOptions(input, compilerOptions, false)
}

func normalizeOptions(input BingoOptions, compilerOptions *core.CompilerOptions, validateBackend bool) (NormalizedOptions, []Diagnostic) {
	options := normalizeBingoOptions(input)
	compiler := cloneCompilerOptions(compilerOptions)
	diagnostics := make([]Diagnostic, 0, 8)
	if compiler.Strict == core.TSUnknown {
		compiler.Strict = core.TSTrue
	}

	// GetStrictOptionValue is the tsgo definition of effective strictness. Make
	// each required option explicit after evaluating that definition, so
	// {strict:true} and an equivalent fully-expanded config share one digest.
	type strictOption struct {
		name  string
		value *core.Tristate
	}
	strict := []strictOption{
		{name: "strict", value: &compiler.Strict},
		{name: "strictNullChecks", value: &compiler.StrictNullChecks},
		{name: "strictFunctionTypes", value: &compiler.StrictFunctionTypes},
		{name: "noImplicitAny", value: &compiler.NoImplicitAny},
	}
	for i := range strict {
		item := &strict[i]
		effective := compiler.GetStrictOptionValue(*item.value)
		*item.value = core.BoolToTristate(effective)
		if !effective {
			// Keep the field name in EntityID so separate invalid options are not
			// accidentally deduplicated when they share the global config span.
			d := NewDiagnostic(
				DiagnosticCodeStrictOptionRequired,
				DiagnosticCategoryBingo,
				DiagnosticStageConfiguration,
				DiagnosticSeverityError,
				SourceSpan{},
				"config.strict_option_required",
				item.name,
			)
			d.EntityID = "typescript." + item.name
			d.Profile = options.Profile
			d.RemediationKind = "enable-compiler-option"
			diagnostics = append(diagnostics, d)
		}
	}

	switch options.Profile {
	case ProfileStatic, ProfileInterop, ProfileUnsafe:
		// Available profiles are accepted in Phase 1. Capability checks for their
		// boundaries happen after a Program snapshot is available.
	case ProfileDynamic:
		d := NewRegisteredDiagnostic(DiagnosticCodeDynamicProfileUnavailable, SourceSpan{}, "runtime.dynamic")
		d.Profile = options.Profile
		d.RequiredCapability = "runtime.dynamic"
		d.EntityID = "bingoOptions.profile"
		d.RemediationKind = "select-static-interop-or-unsafe"
		diagnostics = append(diagnostics, d)
	default:
		d := NewDiagnostic(
			DiagnosticCodeInvalidProfile,
			DiagnosticCategoryBingo,
			DiagnosticStageConfiguration,
			DiagnosticSeverityError,
			SourceSpan{},
			"config.invalid_profile",
			string(options.Profile),
		)
		d.Profile = options.Profile
		d.EntityID = "bingoOptions.profile"
		d.RemediationKind = "select-static-interop-or-unsafe"
		diagnostics = append(diagnostics, d)
	}

	if validateBackend {
		// Direct NormalizeOptions callers still receive eager validation. Source
		// capture uses normalizeFrontendOptions and defers these choices to the
		// target-dependent BuildPlan boundary.
		type optionValue struct {
			name    string
			value   string
			invalid bool
		}
		invalidOptions := []optionValue{
			{name: "llvmMajor", value: strconv.Itoa(options.LLVMMajor), invalid: options.LLVMMajor != 20},
			{name: "gc", value: string(options.GC), invalid: options.GC != GCTracing && options.GC != GCArc && options.GC != GCArena},
			{name: "exceptions", value: string(options.Exceptions), invalid: options.Exceptions != ExceptionsNone},
			{name: "overflow", value: string(options.Overflow), invalid: options.Overflow != OverflowJSNumber},
			{name: "boundsCheck", value: string(options.BoundsCheck), invalid: options.BoundsCheck != BoundsCheckOn && options.BoundsCheck != BoundsCheckOff},
		}
		for _, option := range invalidOptions {
			if option.invalid {
				diagnostics = append(diagnostics, invalidOptionDiagnostic(options.Profile, option.name, option.value))
			}
		}
		for _, artifact := range options.Emit {
			if artifact != EmitHIR && artifact != EmitMIR && artifact != EmitLLVM && artifact != EmitObject {
				d := invalidOptionDiagnostic(options.Profile, "emit", string(artifact))
				d.EntityID += ":" + string(artifact)
				diagnostics = append(diagnostics, d)
			}
		}
	}

	return NormalizedOptions{
		SchemaVersion:   OptionsSchemaVersion,
		Bingo:           options,
		CompilerOptions: compiler,
	}, SortAndDeduplicateDiagnostics(diagnostics)
}

func invalidOptionDiagnostic(profile Profile, name, value string) Diagnostic {
	d := NewDiagnostic(
		DiagnosticCodeInvalidOption,
		DiagnosticCategoryBingo,
		DiagnosticStageConfiguration,
		DiagnosticSeverityError,
		SourceSpan{},
		"config.invalid_option",
		name,
		value,
	)
	d.Profile = profile
	d.EntityID = "bingoOptions." + name
	d.RemediationKind = "select-supported-option"
	return d
}

// CompilerOptionsCopy returns a detached, non-nil clone suitable for passing to
// APIs that may mutate compiler options. A zero-value NormalizedOptions yields
// an empty core.CompilerOptions.
func (o NormalizedOptions) CompilerOptionsCopy() *core.CompilerOptions {
	return cloneCompilerOptions(o.CompilerOptions)
}

// CanonicalJSON serializes normalized options with fixed field order and
// deterministic map handling. The returned bytes contain no trailing newline.
func (o NormalizedOptions) CanonicalJSON() ([]byte, error) {
	bingo := normalizeBingoOptions(o.Bingo)
	compiler := canonicalCompilerOptions(cloneCompilerOptions(o.CompilerOptions))
	canonical := struct {
		SchemaVersion   int                   `json:"schemaVersion"`
		Bingo           canonicalBingoOptions `json:"bingoOptions"`
		CompilerOptions *core.CompilerOptions `json:"typescript"`
	}{
		SchemaVersion: o.SchemaVersion,
		Bingo: canonicalBingoOptions{
			Profile:      bingo.Profile,
			Runtime:      bingo.Runtime,
			LLVMMajor:    bingo.LLVMMajor,
			TargetTriple: bingo.TargetTriple,
			CPU:          bingo.CPU,
			Features:     slices.Clone(bingo.Features),
			GC:           bingo.GC,
			Exceptions:   bingo.Exceptions,
			Overflow:     bingo.Overflow,
			BoundsCheck:  bingo.BoundsCheck,
			Emit:         slices.Clone(bingo.Emit),
		},
		CompilerOptions: compiler,
	}
	return jsonx.Marshal(canonical, jsonx.Deterministic(true))
}

// Digest returns the lower-case SHA-256 hex digest of CanonicalJSON.
func (o NormalizedOptions) Digest() (string, error) {
	b, err := o.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// normalizeBingoOptions canonicalizes user-facing Bingo values and set-like
// slices. It intentionally leaves unknown values in place so a later checker
// can report the exact requested value instead of silently changing semantics.
func normalizeBingoOptions(input BingoOptions) BingoOptions {
	defaults := DefaultBingoOptions()
	output := input
	if strings.TrimSpace(string(output.Profile)) == "" {
		output.Profile = defaults.Profile
	} else {
		output.Profile = Profile(strings.ToLower(strings.TrimSpace(string(output.Profile))))
	}
	if strings.TrimSpace(output.Runtime) == "" {
		output.Runtime = defaults.Runtime
	} else {
		output.Runtime = strings.TrimSpace(output.Runtime)
	}
	if output.LLVMMajor == 0 {
		output.LLVMMajor = defaults.LLVMMajor
	}
	output.TargetTriple = strings.ToLower(strings.TrimSpace(output.TargetTriple))
	if strings.TrimSpace(output.CPU) == "" {
		output.CPU = defaults.CPU
	} else {
		output.CPU = strings.ToLower(strings.TrimSpace(output.CPU))
	}
	if strings.TrimSpace(string(output.GC)) == "" {
		output.GC = defaults.GC
	} else {
		output.GC = GCMode(strings.ToLower(strings.TrimSpace(string(output.GC))))
	}
	if strings.TrimSpace(string(output.Exceptions)) == "" {
		output.Exceptions = defaults.Exceptions
	} else {
		output.Exceptions = ExceptionMode(strings.ToLower(strings.TrimSpace(string(output.Exceptions))))
	}
	if strings.TrimSpace(string(output.Overflow)) == "" {
		output.Overflow = defaults.Overflow
	} else {
		output.Overflow = OverflowMode(strings.ToLower(strings.TrimSpace(string(output.Overflow))))
	}
	if strings.TrimSpace(string(output.BoundsCheck)) == "" {
		output.BoundsCheck = defaults.BoundsCheck
	} else {
		output.BoundsCheck = BoundsCheckMode(strings.ToLower(strings.TrimSpace(string(output.BoundsCheck))))
	}
	output.Features = normalizeFeatureSet(output.Features)
	if output.Emit == nil {
		output.Emit = slices.Clone(defaults.Emit)
	} else {
		output.Emit = normalizeEmitSet(output.Emit)
	}
	return output
}

func normalizeFeatureSet(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}

func normalizeEmitSet(values []EmitArtifact) []EmitArtifact {
	seen := make(map[EmitArtifact]struct{}, len(values))
	result := make([]EmitArtifact, 0, len(values))
	for _, value := range values {
		value = EmitArtifact(strings.ToLower(strings.TrimSpace(string(value))))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	order := map[EmitArtifact]int{EmitHIR: 0, EmitMIR: 1, EmitLLVM: 2, EmitObject: 3}
	slices.SortStableFunc(result, func(a, b EmitArtifact) int {
		ia, oka := order[a]
		ib, okb := order[b]
		if oka && okb {
			return ia - ib
		}
		if oka {
			return -1
		}
		if okb {
			return 1
		}
		return strings.Compare(string(a), string(b))
	})
	return result
}

func cloneCompilerOptions(input *core.CompilerOptions) *core.CompilerOptions {
	if input == nil {
		return &core.CompilerOptions{}
	}
	output := input.Clone()
	output.CustomConditions = slices.Clone(input.CustomConditions)
	output.Lib = slices.Clone(input.Lib)
	output.ModuleSuffixes = slices.Clone(input.ModuleSuffixes)
	output.RootDirs = slices.Clone(input.RootDirs)
	output.TypeRoots = slices.Clone(input.TypeRoots)
	output.Types = slices.Clone(input.Types)
	if input.MaxNodeModuleJsDepth != nil {
		value := *input.MaxNodeModuleJsDepth
		output.MaxNodeModuleJsDepth = &value
	}
	if input.Checkers != nil {
		value := *input.Checkers
		output.Checkers = &value
	}
	if input.Paths != nil {
		entries := make([]collections.MapEntry[string, []string], 0, input.Paths.Size())
		for key, value := range input.Paths.Entries() {
			entries = append(entries, collections.MapEntry[string, []string]{Key: key, Value: slices.Clone(value)})
		}
		output.Paths = collections.NewOrderedMapFromList(entries)
	}
	return output
}

func canonicalCompilerOptions(input *core.CompilerOptions) *core.CompilerOptions {
	if input == nil {
		return nil
	}
	output := cloneCompilerOptions(input)
	// Compiler-option slices can encode resolution/overload precedence. Preserve
	// their order; only set-like Bingo features and artifact requests are sorted.
	// RootDirs are ordered resolution roots; preserve their order while making
	// separators deterministic.
	output.BaseUrl = canonicalConfigPath(output.BaseUrl)
	output.ConfigFilePath = canonicalConfigPath(output.ConfigFilePath)
	output.DeclarationDir = canonicalConfigPath(output.DeclarationDir)
	output.MapRoot = canonicalConfigPath(output.MapRoot)
	output.OutDir = canonicalConfigPath(output.OutDir)
	output.PathsBasePath = canonicalConfigPath(output.PathsBasePath)
	output.Project = canonicalConfigPath(output.Project)
	output.RootDir = canonicalConfigPath(output.RootDir)
	output.SourceRoot = canonicalConfigPath(output.SourceRoot)
	output.TsBuildInfoFile = canonicalConfigPath(output.TsBuildInfoFile)
	for i := range output.Lib {
		output.Lib[i] = canonicalConfigPath(output.Lib[i])
	}
	for i := range output.RootDirs {
		output.RootDirs[i] = canonicalConfigPath(output.RootDirs[i])
	}
	for i := range output.TypeRoots {
		output.TypeRoots[i] = canonicalConfigPath(output.TypeRoots[i])
	}
	if output.Paths != nil {
		if output.Paths.Size() == 0 {
			output.Paths = nil
			return output
		}
		entries := make([]collections.MapEntry[string, []string], 0, output.Paths.Size())
		for key, value := range output.Paths.Entries() {
			key = canonicalConfigPath(key)
			values := slices.Clone(value)
			for i := range values {
				values[i] = strings.ReplaceAll(values[i], "\\", "/")
			}
			entries = append(entries, collections.MapEntry[string, []string]{Key: key, Value: values})
		}
		slices.SortStableFunc(entries, func(a, b collections.MapEntry[string, []string]) int {
			return strings.Compare(a.Key, b.Key)
		})
		output.Paths = collections.NewOrderedMapFromList(entries)
	}
	return output
}

func canonicalConfigPath(value string) string {
	return strings.ReplaceAll(value, "\\", "/")
}
