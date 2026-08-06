package frontendwire

// OptionsSchemaVersion is the canonical configuration schema version emitted
// by the frontend. It is part of the serialized contract and hash input.
const OptionsSchemaVersion = 1

// Profile selects the source-language boundary enforced by the frontend.
type Profile string

const (
	ProfileStatic  Profile = "static"
	ProfileInterop Profile = "interop"
	ProfileUnsafe  Profile = "unsafe"
	ProfileDynamic Profile = "dynamic"
)

type GCMode string

const (
	GCTracing GCMode = "tracing"
	GCArc     GCMode = "arc"
	GCArena   GCMode = "arena"
)

type ExceptionMode string

const (
	// ExceptionsNone disables exception handling for the initial Phase 2A slice.
	ExceptionsNone ExceptionMode = "none"
	// ExceptionsLLVMEH is reserved for the future native LLVM EH implementation.
	// It is not currently a supported build capability.
	ExceptionsLLVMEH ExceptionMode = "llvm-eh"
)

type OverflowMode string

const (
	OverflowJSNumber OverflowMode = "js-number"
)

type BoundsCheckMode string

const (
	BoundsCheckOn  BoundsCheckMode = "on"
	BoundsCheckOff BoundsCheckMode = "off"
)

type EmitArtifact string

const (
	EmitHIR    EmitArtifact = "hir"
	EmitMIR    EmitArtifact = "mir"
	EmitLLVM   EmitArtifact = "llvm"
	EmitObject EmitArtifact = "obj"
)

// BingoOptions is the target-independent subset of compiler options retained
// in a frontend snapshot. Backend-only choices are intentionally present as
// zero-valued fields for source compatibility, but validation rejects them.
type BingoOptions struct {
	Profile      Profile         `json:"profile"`
	Runtime      string          `json:"runtime,omitempty"`
	LLVMMajor    int             `json:"llvmMajor,omitempty"`
	TargetTriple string          `json:"targetTriple,omitempty"`
	CPU          string          `json:"cpu,omitempty"`
	Features     []string        `json:"features,omitempty"`
	GC           GCMode          `json:"gc,omitempty"`
	Exceptions   ExceptionMode   `json:"exceptions,omitempty"`
	Overflow     OverflowMode    `json:"overflow,omitempty"`
	BoundsCheck  BoundsCheckMode `json:"boundsCheck,omitempty"`
	Emit         []EmitArtifact  `json:"emit,omitempty"`
}
