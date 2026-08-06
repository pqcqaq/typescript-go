package frontendwire

// DiagnosticSeverity is the stable severity shown to downstream consumers.
type DiagnosticSeverity string

const (
	DiagnosticSeverityError   DiagnosticSeverity = "error"
	DiagnosticSeverityWarning DiagnosticSeverity = "warning"
	DiagnosticSeverityNote    DiagnosticSeverity = "note"
)

// DiagnosticCategory distinguishes errors that block lowering from capability
// and informational diagnostics.
type DiagnosticCategory string

const (
	DiagnosticCategoryTS          DiagnosticCategory = "TS"
	DiagnosticCategoryBingo       DiagnosticCategory = "BINGO"
	DiagnosticCategoryBingoUnsafe DiagnosticCategory = "BINGO-UNSAFE"
	DiagnosticCategoryLLVM        DiagnosticCategory = "LLVM"
)

// DiagnosticStage identifies the pipeline stage that produced a diagnostic.
type DiagnosticStage string

const (
	DiagnosticStageConfiguration  DiagnosticStage = "configuration"
	DiagnosticStageSyntax         DiagnosticStage = "syntax"
	DiagnosticStageBinding        DiagnosticStage = "binding"
	DiagnosticStageProgram        DiagnosticStage = "program"
	DiagnosticStageGlobal         DiagnosticStage = "global"
	DiagnosticStageSemantic       DiagnosticStage = "semantic"
	DiagnosticStageSnapshot       DiagnosticStage = "snapshot"
	DiagnosticStageSubset         DiagnosticStage = "subset"
	DiagnosticStageRepresentation DiagnosticStage = "representation"
	DiagnosticStageCapability     DiagnosticStage = "capability"
	DiagnosticStageBackend        DiagnosticStage = "backend"
)

type SourceSpan struct {
	File  string `json:"file,omitempty"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

type RelatedSpan struct {
	Code       string     `json:"code,omitempty"`
	Span       SourceSpan `json:"span"`
	MessageKey string     `json:"messageKey,omitempty"`
	Arguments  []string   `json:"arguments"`
}

// Diagnostic is deliberately pointer-free so it can cross a process boundary.
type Diagnostic struct {
	Code               string             `json:"code"`
	Severity           DiagnosticSeverity `json:"severity"`
	Category           DiagnosticCategory `json:"category"`
	Stage              DiagnosticStage    `json:"stage"`
	PrimarySpan        SourceSpan         `json:"primarySpan"`
	MessageKey         string             `json:"messageKey"`
	Arguments          []string           `json:"arguments"`
	RelatedSpans       []RelatedSpan      `json:"relatedSpans"`
	NodeKind           string             `json:"nodeKind,omitempty"`
	EntityID           string             `json:"entityId,omitempty"`
	SourceType         string             `json:"sourceType,omitempty"`
	TargetType         string             `json:"targetType,omitempty"`
	SourceRep          string             `json:"sourceRep,omitempty"`
	TargetRep          string             `json:"targetRep,omitempty"`
	Profile            Profile            `json:"profile,omitempty"`
	RequiredCapability string             `json:"requiredCapability,omitempty"`
	ProofPath          []string           `json:"proofPath"`
	RemediationKind    string             `json:"remediationKind,omitempty"`
}

// DiagnosticsHaveErrors reports whether any diagnostic blocks compilation.
func DiagnosticsHaveErrors(input []Diagnostic) bool {
	for _, diagnostic := range input {
		if diagnostic.Severity == DiagnosticSeverityError {
			return true
		}
	}
	return false
}
