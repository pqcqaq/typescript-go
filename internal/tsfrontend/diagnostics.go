package tsfrontend

import (
	"slices"
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	tsdiagnostics "github.com/microsoft/typescript-go/internal/diagnostics"
	"github.com/microsoft/typescript-go/internal/frontendwire"
	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const (
	// DiagnosticCodeUnclassifiedASTKind reports a source AST Kind missing from
	// the checked-in support manifest.
	DiagnosticCodeUnclassifiedASTKind = "BINGO1000_UNCLASSIFIED_AST_KIND"
	// DiagnosticCodeUnsupportedSyntax reports a classified syntax construct that
	// the active Phase 1 subset rejects.
	DiagnosticCodeUnsupportedSyntax = "BINGO1001_UNSUPPORTED_SYNTAX"
	// DiagnosticCodeParserRecoveryNode reports a synthetic/error-recovery node
	// that cannot be copied into a reliable snapshot.
	DiagnosticCodeParserRecoveryNode = "BINGO1002_PARSER_RECOVERY_NODE"
	// DiagnosticCodeInvalidProfile reports a profile name outside the published
	// static, interop, unsafe, and reserved dynamic names.
	DiagnosticCodeInvalidProfile = "BINGO1008_INVALID_PROFILE"
	// DiagnosticCodeStrictOptionRequired reports an effectively disabled tsgo
	// strictness option required for sound representation selection.
	DiagnosticCodeStrictOptionRequired = "BINGO1009_STRICT_OPTION_REQUIRED"
	// DiagnosticCodeInvalidOption reports a malformed non-profile Bingo option.
	DiagnosticCodeInvalidOption = "BINGO1010_INVALID_OPTION"
	// DiagnosticCodeSourceNotAssignable reports a source-level assignment that
	// the frontend cannot accept.
	DiagnosticCodeSourceNotAssignable = "BINGO1101_SOURCE_NOT_ASSIGNABLE"
	// DiagnosticCodeRepresentationMismatch reports TypeScript assignability that
	// does not preserve the selected runtime representation.
	DiagnosticCodeRepresentationMismatch = "BINGO1201_REPRESENTATION_MISMATCH"
	// DiagnosticCodeMutableVariance reports a mutable container edge that would
	// violate invariance.
	DiagnosticCodeMutableVariance = "BINGO1202_MUTABLE_VARIANCE"
	// DiagnosticCodeFunctionVariance reports an ABI-incompatible parameter or
	// return variance edge.
	DiagnosticCodeFunctionVariance = "BINGO1203_FUNCTION_VARIANCE"
	// DiagnosticCodeVarianceUnreliable reports a variance proof that cannot be
	// established from the captured semantic facts.
	DiagnosticCodeVarianceUnreliable = "BINGO1204_VARIANCE_UNRELIABLE"
	// DiagnosticCodeUnsafeAssertionChain reports an assertion path that crosses
	// any/unknown without a representation proof or runtime check.
	DiagnosticCodeUnsafeAssertionChain = "BINGO1301_UNSAFE_ASSERTION_CHAIN"
	// DiagnosticCodeUnprovenNonNullAssertion reports a non-null assertion without
	// flow proof or an enabled checked policy.
	DiagnosticCodeUnprovenNonNullAssertion = "BINGO1302_UNPROVEN_NON_NULL_ASSERTION"
	// DiagnosticCodeUnresolvedGeneric reports a generic instance that still
	// contains unresolved type parameters at the representation boundary.
	DiagnosticCodeUnresolvedGeneric = "BINGO1401_UNRESOLVED_GENERIC"
	// DiagnosticCodeSpecializationBudget reports deterministic specialization
	// budget exhaustion.
	DiagnosticCodeSpecializationBudget = "BINGO1402_SPECIALIZATION_BUDGET"
	// DiagnosticCodeDescriptorCapability reports a generic descriptor that lacks
	// a required runtime operation.
	DiagnosticCodeDescriptorCapability = "BINGO1403_DESCRIPTOR_CAPABILITY"
	// DiagnosticCodeExternalBodyMissing reports an ambient value declaration with
	// no runtime or FFI implementation.
	DiagnosticCodeExternalBodyMissing = "BINGO3001_EXTERNAL_BODY_MISSING"
	// DiagnosticCodeDynamicProfileUnavailable reports the missing dynamic runtime;
	// it is also the generic published runtime-capability-missing code.
	DiagnosticCodeDynamicProfileUnavailable = "BINGO3002_RUNTIME_CAPABILITY_MISSING"
	// DiagnosticCodeHostAPIUnbound reports a host API without a matching manifest
	// ABI binding.
	DiagnosticCodeHostAPIUnbound = "BINGO3003_HOST_API_UNBOUND"
	// DiagnosticCodeInternalFailure reports a frontend invariant failure and is
	// never suppressible as a normal user error.
	DiagnosticCodeInternalFailure = "BINGO9000_INTERNAL_FAILURE"
)

// DiagnosticSeverity states whether compilation must stop or whether a
// permitted boundary is being annotated. Unsupported/unsafe rejection is
// always an error; it is never downgraded by a release profile.
type DiagnosticSeverity = frontendwire.DiagnosticSeverity

const (
	// DiagnosticSeverityError prevents a semantically valid artifact.
	DiagnosticSeverityError = frontendwire.DiagnosticSeverityError
	// DiagnosticSeverityWarning records an allowed interop/unsafe risk or cost.
	DiagnosticSeverityWarning = frontendwire.DiagnosticSeverityWarning
	// DiagnosticSeverityNote records proof, related declaration, or remediation
	// context and does not independently stop compilation.
	DiagnosticSeverityNote = frontendwire.DiagnosticSeverityNote
)

// DiagnosticCategory identifies the stable public diagnostic namespace.
type DiagnosticCategory = frontendwire.DiagnosticCategory

const (
	// DiagnosticCategoryTS contains diagnostics originating in typescript-go.
	DiagnosticCategoryTS = frontendwire.DiagnosticCategoryTS
	// DiagnosticCategoryBingo contains unsupported, capability, and ordinary
	// representation diagnostics produced by ts2bin.
	DiagnosticCategoryBingo = frontendwire.DiagnosticCategoryBingo
	// DiagnosticCategoryBingoUnsafe contains assertion, dynamic-boundary, and
	// variance diagnostics that require explicit safety review.
	DiagnosticCategoryBingoUnsafe = frontendwire.DiagnosticCategoryBingoUnsafe
	// DiagnosticCategoryLLVM contains backend/verifier failures, which are
	// compiler defects rather than ordinary source rejection.
	DiagnosticCategoryLLVM = frontendwire.DiagnosticCategoryLLVM
)

// DiagnosticStage identifies the pipeline phase that produced a diagnostic.
// The values also define the stable phase ordering within a category.
type DiagnosticStage = frontendwire.DiagnosticStage

const (
	// DiagnosticStageConfiguration is tsconfig/bingoOptions parsing and checking.
	DiagnosticStageConfiguration = frontendwire.DiagnosticStageConfiguration
	// DiagnosticStageSyntax is TypeScript scanning/parsing.
	DiagnosticStageSyntax = frontendwire.DiagnosticStageSyntax
	// DiagnosticStageBinding is TypeScript declaration binding.
	DiagnosticStageBinding = frontendwire.DiagnosticStageBinding
	// DiagnosticStageProgram is Program-wide TypeScript validation.
	DiagnosticStageProgram = frontendwire.DiagnosticStageProgram
	// DiagnosticStageGlobal is TypeScript global-environment validation.
	DiagnosticStageGlobal = frontendwire.DiagnosticStageGlobal
	// DiagnosticStageSemantic is TypeScript checker validation.
	DiagnosticStageSemantic = frontendwire.DiagnosticStageSemantic
	// DiagnosticStageSnapshot is immutable frontend snapshot capture.
	DiagnosticStageSnapshot = frontendwire.DiagnosticStageSnapshot
	// DiagnosticStageSubset is AST/type semantic subset validation.
	DiagnosticStageSubset = frontendwire.DiagnosticStageSubset
	// DiagnosticStageRepresentation is type/representation/variance planning.
	DiagnosticStageRepresentation = frontendwire.DiagnosticStageRepresentation
	// DiagnosticStageCapability is runtime, stdlib, host, or target binding.
	DiagnosticStageCapability = frontendwire.DiagnosticStageCapability
	// DiagnosticStageBackend is HIR/MIR/LLVM verification or object generation.
	DiagnosticStageBackend = frontendwire.DiagnosticStageBackend
)

// SourceSpan is a half-open UTF-8 byte-offset range in a canonical source path.
// Start and End are zero for a global/config diagnostic without a source range.
type SourceSpan = frontendwire.SourceSpan

// RelatedSpan carries stable related-location context without retaining an AST
// or diagnostic pointer.
type RelatedSpan = frontendwire.RelatedSpan

// Diagnostic is the pointer-free, localizable diagnostic protocol shared by
// the CLI, snapshot golden tests, and later compiler stages. Identity and
// deduplication use Code, PrimarySpan, EntityID, and ProofPath; display text is
// deliberately not part of the schema.
type Diagnostic = frontendwire.Diagnostic

// DiagnosticDefinition is one immutable entry in the built-in code registry.
// Definitions freeze code meaning; display localization remains external.
type DiagnosticDefinition struct {
	// Code is the globally stable published identifier.
	Code string `json:"code"`
	// Category is the public TS/BINGO/BINGO-UNSAFE/LLVM namespace.
	Category DiagnosticCategory `json:"category"`
	// Stage is the owning compiler phase.
	Stage DiagnosticStage `json:"stage"`
	// Severity is the definition's default severity.
	Severity DiagnosticSeverity `json:"severity"`
	// MessageKey is the stable default localization key.
	MessageKey string `json:"messageKey"`
}

var builtinDiagnosticRegistry = []DiagnosticDefinition{
	{DiagnosticCodeUnclassifiedASTKind, DiagnosticCategoryBingo, DiagnosticStageSnapshot, DiagnosticSeverityError, "snapshot.unclassified_ast_kind"},
	{DiagnosticCodeUnsupportedSyntax, DiagnosticCategoryBingo, DiagnosticStageSubset, DiagnosticSeverityError, "subset.unsupported_syntax"},
	{DiagnosticCodeParserRecoveryNode, DiagnosticCategoryBingo, DiagnosticStageSnapshot, DiagnosticSeverityError, "snapshot.parser_recovery_node"},
	{DiagnosticCodeInvalidProfile, DiagnosticCategoryBingo, DiagnosticStageConfiguration, DiagnosticSeverityError, "config.invalid_profile"},
	{DiagnosticCodeStrictOptionRequired, DiagnosticCategoryBingo, DiagnosticStageConfiguration, DiagnosticSeverityError, "config.strict_option_required"},
	{DiagnosticCodeInvalidOption, DiagnosticCategoryBingo, DiagnosticStageConfiguration, DiagnosticSeverityError, "config.invalid_option"},
	{DiagnosticCodeSourceNotAssignable, DiagnosticCategoryBingo, DiagnosticStageRepresentation, DiagnosticSeverityError, "type.source_not_assignable"},
	{DiagnosticCodeRepresentationMismatch, DiagnosticCategoryBingo, DiagnosticStageRepresentation, DiagnosticSeverityError, "representation.mismatch"},
	{DiagnosticCodeMutableVariance, DiagnosticCategoryBingoUnsafe, DiagnosticStageRepresentation, DiagnosticSeverityError, "variance.mutable_requires_invariance"},
	{DiagnosticCodeFunctionVariance, DiagnosticCategoryBingoUnsafe, DiagnosticStageRepresentation, DiagnosticSeverityError, "variance.function_abi_mismatch"},
	{DiagnosticCodeVarianceUnreliable, DiagnosticCategoryBingoUnsafe, DiagnosticStageRepresentation, DiagnosticSeverityError, "variance.unreliable"},
	{DiagnosticCodeUnsafeAssertionChain, DiagnosticCategoryBingoUnsafe, DiagnosticStageSubset, DiagnosticSeverityError, "assertion.unsafe_chain"},
	{DiagnosticCodeUnprovenNonNullAssertion, DiagnosticCategoryBingoUnsafe, DiagnosticStageSubset, DiagnosticSeverityError, "assertion.non_null_unproven"},
	{DiagnosticCodeUnresolvedGeneric, DiagnosticCategoryBingo, DiagnosticStageRepresentation, DiagnosticSeverityError, "generic.unresolved"},
	{DiagnosticCodeSpecializationBudget, DiagnosticCategoryBingo, DiagnosticStageRepresentation, DiagnosticSeverityError, "generic.specialization_budget"},
	{DiagnosticCodeDescriptorCapability, DiagnosticCategoryBingo, DiagnosticStageCapability, DiagnosticSeverityError, "generic.descriptor_capability"},
	{DiagnosticCodeExternalBodyMissing, DiagnosticCategoryBingo, DiagnosticStageCapability, DiagnosticSeverityError, "capability.external_body_missing"},
	{DiagnosticCodeDynamicProfileUnavailable, DiagnosticCategoryBingo, DiagnosticStageCapability, DiagnosticSeverityError, "capability.runtime_missing"},
	{DiagnosticCodeHostAPIUnbound, DiagnosticCategoryBingo, DiagnosticStageCapability, DiagnosticSeverityError, "capability.host_api_unbound"},
	{DiagnosticCodeInternalFailure, DiagnosticCategoryBingo, DiagnosticStageBackend, DiagnosticSeverityError, "internal.failure"},
}

// NewDiagnostic constructs a detached structured diagnostic. It normalizes the
// path/range and clones arguments; it does not validate registry membership so
// an upgrade audit can represent newly introduced upstream codes.
func NewDiagnostic(
	code string,
	category DiagnosticCategory,
	stage DiagnosticStage,
	severity DiagnosticSeverity,
	span SourceSpan,
	messageKey string,
	arguments ...string,
) Diagnostic {
	return Diagnostic{
		Code:         code,
		Severity:     severity,
		Category:     category,
		Stage:        stage,
		PrimarySpan:  canonicalSpan(span),
		MessageKey:   messageKey,
		Arguments:    cloneStrings(arguments),
		RelatedSpans: []RelatedSpan{},
		ProofPath:    []string{},
	}
}

// NewRegisteredDiagnostic constructs a diagnostic from the built-in registry.
// Unknown codes are still represented deterministically: their category is
// inferred from the prefix, severity is error, and MessageKey equals the code.
func NewRegisteredDiagnostic(code string, span SourceSpan, arguments ...string) Diagnostic {
	if definition, ok := LookupDiagnosticDefinition(code); ok {
		return NewDiagnostic(code, definition.Category, definition.Stage, definition.Severity, span, definition.MessageKey, arguments...)
	}
	return NewDiagnostic(code, ClassifyDiagnosticCode(code), DiagnosticStageSubset, DiagnosticSeverityError, span, code, arguments...)
}

// ConvertTSDiagnostic copies one tsgo diagnostic into the stable protocol. The
// caller supplies the collection phase because ast.Diagnostic does not retain
// whether Program returned it as config, syntax, bind, global, or semantic.
// Message-chain entries are flattened into RelatedSpans in their original
// order. The result does not retain SourceFile or ast.Diagnostic pointers. A
// nil input returns the zero Diagnostic; ConvertTSDiagnostics skips nil entries.
func ConvertTSDiagnostic(input *ast.Diagnostic, stage DiagnosticStage) Diagnostic {
	if input == nil {
		return Diagnostic{}
	}
	span := SourceSpan{Start: input.Pos(), End: input.End()}
	if input.File() != nil {
		span.File = input.File().FileName()
	}
	result := NewDiagnostic(
		"TS"+strconv.FormatInt(int64(input.Code()), 10),
		DiagnosticCategoryTS,
		stage,
		severityFromTS(input.Category()),
		span,
		string(input.MessageKey()),
		input.MessageArgs()...,
	)
	result.RelatedSpans = make([]RelatedSpan, 0, len(input.RelatedInformation())+len(input.MessageChain()))
	chainSeen := map[*ast.Diagnostic]struct{}{input: {}}
	appendMessageChain(&result.RelatedSpans, input.MessageChain(), chainSeen)
	for _, related := range input.RelatedInformation() {
		if related == nil {
			continue
		}
		relatedSpan := SourceSpan{Start: related.Pos(), End: related.End()}
		if related.File() != nil {
			relatedSpan.File = related.File().FileName()
		}
		result.RelatedSpans = append(result.RelatedSpans, RelatedSpan{
			Code:       "TS" + strconv.FormatInt(int64(related.Code()), 10),
			Span:       canonicalSpan(relatedSpan),
			MessageKey: string(related.MessageKey()),
			Arguments:  cloneStrings(related.MessageArgs()),
		})
	}
	return result
}

func appendMessageChain(output *[]RelatedSpan, chain []*ast.Diagnostic, seen map[*ast.Diagnostic]struct{}) {
	for _, entry := range chain {
		if entry == nil {
			continue
		}
		if _, exists := seen[entry]; exists {
			continue
		}
		seen[entry] = struct{}{}
		span := SourceSpan{Start: entry.Pos(), End: entry.End()}
		if entry.File() != nil {
			span.File = entry.File().FileName()
		}
		*output = append(*output, RelatedSpan{
			Code:       "TS" + strconv.FormatInt(int64(entry.Code()), 10),
			Span:       canonicalSpan(span),
			MessageKey: string(entry.MessageKey()),
			Arguments:  cloneStrings(entry.MessageArgs()),
		})
		appendMessageChain(output, entry.MessageChain(), seen)
	}
}

// ConvertTSDiagnostics copies a collection and applies stable sorting and
// deduplication. It is safe to call concurrently when callers do not mutate the
// input ast.Diagnostics during the call.
func ConvertTSDiagnostics(input []*ast.Diagnostic, stage DiagnosticStage) []Diagnostic {
	result := make([]Diagnostic, 0, len(input))
	for _, diagnostic := range input {
		if diagnostic != nil {
			result = append(result, ConvertTSDiagnostic(diagnostic, stage))
		}
	}
	return SortAndDeduplicateDiagnostics(result)
}

// DiagnosticRegistry returns an independent code-sorted copy of the immutable
// built-in registry. Mutating the returned slice cannot affect another build.
func DiagnosticRegistry() []DiagnosticDefinition {
	return slices.Clone(builtinDiagnosticRegistry)
}

// LookupDiagnosticDefinition returns the immutable definition for code. The
// returned value is a copy and remains valid for the caller's lifetime.
func LookupDiagnosticDefinition(code string) (DiagnosticDefinition, bool) {
	index, ok := slices.BinarySearchFunc(builtinDiagnosticRegistry, code, func(entry DiagnosticDefinition, code string) int {
		return strings.Compare(entry.Code, code)
	})
	if !ok {
		return DiagnosticDefinition{}, false
	}
	return builtinDiagnosticRegistry[index], true
}

// ClassifyDiagnosticCode returns the public namespace implied by a code. Known
// registry entries take precedence; unknown prefixes return an empty category.
func ClassifyDiagnosticCode(code string) DiagnosticCategory {
	if definition, ok := LookupDiagnosticDefinition(code); ok {
		return definition.Category
	}
	upper := strings.ToUpper(code)
	switch {
	case strings.HasPrefix(upper, "TS"):
		return DiagnosticCategoryTS
	case strings.HasPrefix(upper, "LLVM"):
		return DiagnosticCategoryLLVM
	case strings.HasPrefix(upper, "BINGO-UNSAFE"):
		return DiagnosticCategoryBingoUnsafe
	case strings.HasPrefix(upper, "BINGO"):
		number := diagnosticCodeNumber(upper)
		if (number >= 1300 && number <= 1399) || (number >= 1202 && number <= 1204) {
			return DiagnosticCategoryBingoUnsafe
		}
		return DiagnosticCategoryBingo
	default:
		return ""
	}
}

// SortAndDeduplicateDiagnostics returns a deep-copied stable sequence. It sorts
// TS before Bingo before LLVM, respects TS phase order, then uses canonical file
// path, start, end, numeric code, entity, and proof path. Deduplication follows
// the published identity contract and never mutates the input slice.
func SortAndDeduplicateDiagnostics(input []Diagnostic) []Diagnostic {
	if len(input) == 0 {
		return []Diagnostic{}
	}
	result := make([]Diagnostic, len(input))
	for i := range input {
		result[i] = cloneDiagnostic(input[i])
	}
	slices.SortStableFunc(result, compareDiagnostics)
	seen := make(map[string]struct{}, len(result))
	write := 0
	for read := range result {
		identity := diagnosticIdentity(result[read])
		if _, duplicate := seen[identity]; duplicate {
			continue
		}
		seen[identity] = struct{}{}
		result[write] = result[read]
		write++
	}
	return slices.Clip(result[:write])
}

// CanonicalDiagnosticsJSON serializes the stable sorted/deduplicated diagnostic
// array without a trailing newline. It does not mutate the input.
func CanonicalDiagnosticsJSON(input []Diagnostic) ([]byte, error) {
	return jsonx.Marshal(SortAndDeduplicateDiagnostics(input), jsonx.Deterministic(true))
}

// DiagnosticsHaveErrors reports whether any diagnostic prevents artifact
// generation. It is a read-only operation and is safe for immutable slices.
func DiagnosticsHaveErrors(input []Diagnostic) bool {
	return slices.ContainsFunc(input, func(d Diagnostic) bool {
		return d.Severity == DiagnosticSeverityError
	})
}

func severityFromTS(category tsdiagnostics.Category) DiagnosticSeverity {
	switch category {
	case tsdiagnostics.CategoryError:
		return DiagnosticSeverityError
	case tsdiagnostics.CategoryWarning:
		return DiagnosticSeverityWarning
	case tsdiagnostics.CategorySuggestion, tsdiagnostics.CategoryMessage:
		return DiagnosticSeverityNote
	default:
		return DiagnosticSeverityError
	}
}

func canonicalSpan(span SourceSpan) SourceSpan {
	span.File = strings.ReplaceAll(strings.TrimSpace(span.File), "\\", "/")
	if span.Start < 0 {
		span.Start = 0
	}
	if span.End < span.Start {
		span.End = span.Start
	}
	return span
}

func cloneStrings(input []string) []string {
	if len(input) == 0 {
		return []string{}
	}
	return slices.Clone(input)
}

func cloneDiagnostic(input Diagnostic) Diagnostic {
	result := input
	result.PrimarySpan = canonicalSpan(input.PrimarySpan)
	result.Arguments = cloneStrings(input.Arguments)
	result.ProofPath = cloneStrings(input.ProofPath)
	result.RelatedSpans = make([]RelatedSpan, len(input.RelatedSpans))
	for i := range input.RelatedSpans {
		result.RelatedSpans[i] = input.RelatedSpans[i]
		result.RelatedSpans[i].Span = canonicalSpan(input.RelatedSpans[i].Span)
		result.RelatedSpans[i].Arguments = cloneStrings(input.RelatedSpans[i].Arguments)
	}
	return result
}

func cloneDiagnostics(input []Diagnostic) []Diagnostic {
	result := make([]Diagnostic, len(input))
	for index := range input {
		result[index] = cloneDiagnostic(input[index])
	}
	return result
}

func compareDiagnostics(a, b Diagnostic) int {
	if cmp := diagnosticLayerRank(a.Category) - diagnosticLayerRank(b.Category); cmp != 0 {
		return cmp
	}
	if a.Category == DiagnosticCategoryTS && b.Category == DiagnosticCategoryTS {
		if cmp := diagnosticStageRank(a.Stage) - diagnosticStageRank(b.Stage); cmp != 0 {
			return cmp
		}
	}
	if cmp := strings.Compare(a.PrimarySpan.File, b.PrimarySpan.File); cmp != 0 {
		return cmp
	}
	if cmp := a.PrimarySpan.Start - b.PrimarySpan.Start; cmp != 0 {
		return cmp
	}
	if cmp := a.PrimarySpan.End - b.PrimarySpan.End; cmp != 0 {
		return cmp
	}
	if cmp := diagnosticCodeNumber(a.Code) - diagnosticCodeNumber(b.Code); cmp != 0 {
		return cmp
	}
	if cmp := strings.Compare(a.Code, b.Code); cmp != 0 {
		return cmp
	}
	if cmp := strings.Compare(a.EntityID, b.EntityID); cmp != 0 {
		return cmp
	}
	if cmp := slices.Compare(a.ProofPath, b.ProofPath); cmp != 0 {
		return cmp
	}
	if cmp := diagnosticStageRank(a.Stage) - diagnosticStageRank(b.Stage); cmp != 0 {
		return cmp
	}
	if cmp := strings.Compare(string(a.Category), string(b.Category)); cmp != 0 {
		return cmp
	}
	if cmp := strings.Compare(string(a.Severity), string(b.Severity)); cmp != 0 {
		return cmp
	}
	if cmp := strings.Compare(a.MessageKey, b.MessageKey); cmp != 0 {
		return cmp
	}
	if cmp := slices.Compare(a.Arguments, b.Arguments); cmp != 0 {
		return cmp
	}
	return compareRelatedSpans(a.RelatedSpans, b.RelatedSpans)
}

func compareRelatedSpans(a, b []RelatedSpan) int {
	if cmp := len(a) - len(b); cmp != 0 {
		return cmp
	}
	for i := range a {
		if cmp := strings.Compare(a[i].Span.File, b[i].Span.File); cmp != 0 {
			return cmp
		}
		if cmp := a[i].Span.Start - b[i].Span.Start; cmp != 0 {
			return cmp
		}
		if cmp := a[i].Span.End - b[i].Span.End; cmp != 0 {
			return cmp
		}
		if cmp := strings.Compare(a[i].Code, b[i].Code); cmp != 0 {
			return cmp
		}
		if cmp := strings.Compare(a[i].MessageKey, b[i].MessageKey); cmp != 0 {
			return cmp
		}
		if cmp := slices.Compare(a[i].Arguments, b[i].Arguments); cmp != 0 {
			return cmp
		}
	}
	return 0
}

func diagnosticIdentity(diagnostic Diagnostic) string {
	var identity strings.Builder
	writeIdentityPart := func(value string) {
		identity.WriteString(strconv.Itoa(len(value)))
		identity.WriteByte(':')
		identity.WriteString(value)
	}
	writeIdentityPart(diagnostic.Code)
	writeIdentityPart(diagnostic.PrimarySpan.File)
	writeIdentityPart(strconv.Itoa(diagnostic.PrimarySpan.Start))
	writeIdentityPart(strconv.Itoa(diagnostic.PrimarySpan.End))
	writeIdentityPart(diagnostic.EntityID)
	for _, proof := range diagnostic.ProofPath {
		writeIdentityPart(proof)
	}
	return identity.String()
}

func diagnosticCodeNumber(code string) int {
	start := -1
	for i := range len(code) {
		if code[i] >= '0' && code[i] <= '9' {
			start = i
			break
		}
	}
	if start == -1 {
		return 0
	}
	end := start
	for end < len(code) && code[end] >= '0' && code[end] <= '9' {
		end++
	}
	number, err := strconv.Atoi(code[start:end])
	if err != nil {
		return 0
	}
	return number
}

func diagnosticLayerRank(category DiagnosticCategory) int {
	switch category {
	case DiagnosticCategoryTS:
		return 0
	case DiagnosticCategoryBingo, DiagnosticCategoryBingoUnsafe:
		return 1
	case DiagnosticCategoryLLVM:
		return 2
	default:
		return 3
	}
}

func diagnosticStageRank(stage DiagnosticStage) int {
	switch stage {
	case DiagnosticStageConfiguration:
		return 0
	case DiagnosticStageSyntax:
		return 1
	case DiagnosticStageBinding:
		return 2
	case DiagnosticStageProgram:
		return 3
	case DiagnosticStageGlobal:
		return 4
	case DiagnosticStageSemantic:
		return 5
	case DiagnosticStageSnapshot:
		return 6
	case DiagnosticStageSubset:
		return 7
	case DiagnosticStageRepresentation:
		return 8
	case DiagnosticStageCapability:
		return 9
	case DiagnosticStageBackend:
		return 10
	default:
		return 11
	}
}
