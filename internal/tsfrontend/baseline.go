package tsfrontend

import (
	"runtime"

	"github.com/microsoft/typescript-go/internal/core"
	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const (
	// TypeScriptGoUpstreamCommit is the upstream baseline merged into the fork.
	TypeScriptGoUpstreamCommit = "86cc4767d4ebadb9b7845d0ab8eb2b05785c3fee"
	// TypeScriptGoCommit remains the snapshot wire name for the audited upstream
	// baseline. The exact fork commit enters typed-HIR compiler identity instead.
	TypeScriptGoCommit = TypeScriptGoUpstreamCommit
	// StandardLibraryHash is the canonical hash of internal/bundled/libs. The
	// stream format is sorted relative-path NUL file-content NUL, repeated.
	StandardLibraryHash = "b82676463eea69ca2b7e4a6db078098999eae73b0e426cca8b8d1a7ebfc08967"
	// StandardLibraryFileCount is the audited number of bundled declaration files.
	StandardLibraryFileCount = 108
	// StandardLibraryHashAlgorithm names the reproducible hashing contract.
	StandardLibraryHashAlgorithm = "sha256-path-nul-content-nul-v1"
	// LockedLLVMMajor is the backend ABI/toolchain major selected by Phase 1.
	LockedLLVMMajor = 20
	// LockedLLVMVersion is the exact LLVM toolchain release selected by the
	// repository lock. The major remains a separate compatibility boundary.
	LockedLLVMVersion = "20.1.8"
	// LockedLLDVersion is the exact linker release selected by the repository
	// lock and is reported independently from the LLVM library ABI.
	LockedLLDVersion = "20.1.8"
	// DiagnosticSchemaVersion is the public structured diagnostic schema major.
	DiagnosticSchemaVersion = 1
)

// BuildInfo is the deterministic toolchain and schema provenance printed by
// ts2bin version. Runtime Go patch/build metadata is reported for inspection
// but is not folded into snapshot identity beyond the locked version contract.
type BuildInfo struct {
	Name                         string `json:"name"`
	TypeScriptVersion            string `json:"typescriptVersion"`
	TypeScriptGoCommit           string `json:"typescriptGoCommit"`
	GoVersion                    string `json:"goVersion"`
	StandardLibraryHash          string `json:"standardLibraryHash"`
	StandardLibraryHashAlgorithm string `json:"standardLibraryHashAlgorithm"`
	StandardLibraryFileCount     int    `json:"standardLibraryFileCount"`
	SnapshotSchemaVersion        uint32 `json:"snapshotSchemaVersion"`
	OptionsSchemaVersion         int    `json:"optionsSchemaVersion"`
	DiagnosticSchemaVersion      int    `json:"diagnosticSchemaVersion"`
	LLVMMajor                    int    `json:"llvmMajor"`
	LLVMVersion                  string `json:"llvmVersion"`
	LLDVersion                   string `json:"lldVersion"`
}

// CurrentBuildInfo returns a fresh value containing the locked frontend
// provenance and the Go runtime version of the current binary.
func CurrentBuildInfo() BuildInfo {
	return BuildInfo{
		Name:                         "ts2bin",
		TypeScriptVersion:            core.Version(),
		TypeScriptGoCommit:           TypeScriptGoCommit,
		GoVersion:                    runtime.Version(),
		StandardLibraryHash:          StandardLibraryHash,
		StandardLibraryHashAlgorithm: StandardLibraryHashAlgorithm,
		StandardLibraryFileCount:     StandardLibraryFileCount,
		SnapshotSchemaVersion:        SnapshotSchemaVersion,
		OptionsSchemaVersion:         OptionsSchemaVersion,
		DiagnosticSchemaVersion:      DiagnosticSchemaVersion,
		LLVMMajor:                    LockedLLVMMajor,
		LLVMVersion:                  LockedLLVMVersion,
		LLDVersion:                   LockedLLDVersion,
	}
}

// CanonicalBuildInfoJSON returns deterministic compact JSON without a trailing
// newline, suitable for lock/provenance checks.
func CanonicalBuildInfoJSON() ([]byte, error) {
	return jsonx.Marshal(CurrentBuildInfo(), jsonx.Deterministic(true))
}
