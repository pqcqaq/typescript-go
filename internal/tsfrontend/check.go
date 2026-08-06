package tsfrontend

import (
	"context"
	"errors"
	"slices"

	"github.com/microsoft/typescript-go/internal/bundled"
)

// CheckResult is the pointer-free result of TypeScript configuration, parsing,
// binding, and semantic checking. A caller must not proceed to lowering when
// Diagnostics contains an error.
type CheckResult struct {
	Options         NormalizedOptions `json:"options"`
	ConfigPath      string            `json:"configPath"`
	ProjectRoot     string            `json:"projectRoot"`
	SourceFileCount int               `json:"sourceFileCount"`
	Diagnostics     []Diagnostic      `json:"diagnostics"`
}

// Check constructs a batch Program and returns stable TypeScript and ts2bin
// configuration diagnostics. It never exposes the live Program or checker.
func Check(ctx context.Context, request BuildRequest) (CheckResult, error) {
	if err := ctx.Err(); err != nil {
		return CheckResult{}, err
	}
	frontend := NewOSFrontend(TypeScriptGoCommit)
	if request.FileSystem != nil {
		frontend = NewFrontend(bundled.WrapFS(request.FileSystem), bundled.LibPath(), TypeScriptGoCommit, StandardLibraryHash)
	}
	if request.FileSystem == nil {
		request.FileSystem = frontend.fs
	}
	session, err := buildProgram(ctx, request)
	if err != nil {
		return CheckResult{}, err
	}
	if session == nil {
		return CheckResult{}, nil
	}
	diagnostics := slices.Clone(session.diagnostics)
	if session.program == nil {
		diagnostics = normalizeDiagnosticPaths(diagnostics, session.projectRoot, frontend.defaultLibraryPath, frontend.caseSensitivePaths)
		return CheckResult{
			Options:         session.options,
			ConfigPath:      session.configPath,
			ProjectRoot:     session.projectRoot,
			SourceFileCount: 0,
			Diagnostics:     SortAndDeduplicateDiagnostics(diagnostics),
		}, nil
	}
	if err := ctx.Err(); err != nil {
		return CheckResult{}, err
	}
	programDiagnostics, err := collectProgramDiagnostics(ctx, session.program)
	if err != nil {
		return CheckResult{}, err
	}
	diagnostics = append(diagnostics, programDiagnostics...)
	diagnostics = normalizeDiagnosticPaths(diagnostics, session.projectRoot, frontend.defaultLibraryPath, frontend.caseSensitivePaths)
	diagnostics = SortAndDeduplicateDiagnostics(diagnostics)
	return CheckResult{
		Options:         session.options,
		ConfigPath:      session.configPath,
		ProjectRoot:     session.projectRoot,
		SourceFileCount: len(session.program.SourceFiles()),
		Diagnostics:     slices.Clone(diagnostics),
	}, nil
}

// CheckRequest validates that the request has enough information to construct
// a Program. It is kept separate so CLI and tests can report usage errors before
// touching the compiler's filesystem or checker pool.
func CheckRequest(request BuildRequest) error {
	if request.ConfigPath == "" && len(request.RootFiles) == 0 {
		return errors.New("tsfrontend: ConfigPath or RootFiles is required")
	}
	return nil
}
