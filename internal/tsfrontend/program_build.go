package tsfrontend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/collections"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	jsonx "github.com/microsoft/typescript-go/internal/json"
	"github.com/microsoft/typescript-go/internal/tsoptions"
	"github.com/microsoft/typescript-go/internal/tspath"
	"github.com/microsoft/typescript-go/internal/vfs"
	"github.com/microsoft/typescript-go/internal/vfs/osvfs"
)

// BuildRequest describes one isolated batch frontend compilation. ConfigPath
// and RootFiles are mutually exclusive; ConfigPath takes precedence when it is
// present. BingoOptionsOverride distinguishes an explicit zero-value override
// from the bingoOptions object in tsconfig. BingoProfileOverride changes only
// the profile after the full config has been loaded.
type BuildRequest struct {
	ConfigPath           string
	RootFiles            []string
	CurrentDirectory     string
	ProjectRoot          string
	BingoOptions         BingoOptions
	BingoOptionsOverride bool
	BingoProfileOverride *Profile
	// FileSystem supplies a hermetic project filesystem to Check and Build.
	// When nil, the operating-system filesystem is used.
	FileSystem vfs.FS
}

type programBuild struct {
	program     *compiler.Program
	config      *tsoptions.ParsedCommandLine
	options     NormalizedOptions
	configPath  string
	projectRoot string
	diagnostics []Diagnostic
}

func buildProgram(ctx context.Context, request BuildRequest) (*programBuild, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cwd := request.CurrentDirectory
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	cwd = tspath.NormalizePath(cwd)
	if request.ConfigPath == "" && len(request.RootFiles) == 0 {
		return nil, errors.New("tsfrontend: ConfigPath or RootFiles is required")
	}
	fs := request.FileSystem
	if fs == nil {
		fs = bundled.WrapFS(osvfs.FS())
	} else {
		// Keep project files hermetic while still exposing the locked bundled
		// lib.*.d.ts set to the compiler host.
		fs = bundled.WrapFS(fs)
	}
	configPath := ""
	var parsed *tsoptions.ParsedCommandLine
	var fatalDiagnostics []*ast.Diagnostic
	if request.ConfigPath != "" {
		configPath = tspath.GetNormalizedAbsolutePath(request.ConfigPath, cwd)
		parseHost := compiler.NewCachedFSCompilerHost(cwd, fs, bundled.LibPath(), nil, nil)
		parsed, fatalDiagnostics = tsoptions.GetParsedCommandLineOfConfigFile(configPath, nil, nil, parseHost, nil)
	} else {
		rootFiles := make([]string, 0, len(request.RootFiles))
		for _, fileName := range request.RootFiles {
			rootFiles = append(rootFiles, tspath.GetNormalizedAbsolutePath(fileName, cwd))
		}
		slices.Sort(rootFiles)
		parsed = tsoptions.NewParsedCommandLine(&core.CompilerOptions{
			Strict:              core.TSTrue,
			StrictNullChecks:    core.TSTrue,
			StrictFunctionTypes: core.TSTrue,
			NoImplicitAny:       core.TSTrue,
		}, rootFiles, tspath.ComparePathsOptions{
			UseCaseSensitiveFileNames: fs.UseCaseSensitiveFileNames(),
			CurrentDirectory:          cwd,
		})
	}

	build := &programBuild{
		config:      parsed,
		configPath:  configPath,
		projectRoot: projectRootForRequest(request, configPath, cwd),
		diagnostics: ConvertTSDiagnostics(fatalDiagnostics, DiagnosticStageConfiguration),
	}
	if parsed == nil {
		return build, nil
	}

	bingoOptions := DefaultBingoOptions()
	if parsed.Raw != nil {
		fromConfig, present, err := extractBingoOptions(parsed.Raw)
		if err != nil {
			diagnostic := NewDiagnostic(
				DiagnosticCodeInvalidOption,
				DiagnosticCategoryBingo,
				DiagnosticStageConfiguration,
				DiagnosticSeverityError,
				SourceSpan{File: configPath},
				"config.invalid_bingo_options",
				err.Error(),
			)
			diagnostic.EntityID = "bingoOptions"
			build.diagnostics = append(build.diagnostics, diagnostic)
		} else if present {
			bingoOptions = fromConfig
		}
	}
	if request.BingoOptionsOverride {
		bingoOptions = request.BingoOptions
	}
	if request.BingoProfileOverride != nil {
		bingoOptions.Profile = *request.BingoProfileOverride
	}

	var optionDiagnostics []Diagnostic
	build.options, optionDiagnostics = normalizeFrontendOptions(bingoOptions, parsed.CompilerOptions())
	build.diagnostics = append(build.diagnostics, optionDiagnostics...)
	parsed.SetCompilerOptions(build.options.CompilerOptionsCopy())
	build.diagnostics = append(build.diagnostics, ConvertTSDiagnostics(parsed.GetConfigFileParsingDiagnostics(), DiagnosticStageConfiguration)...)

	host := compiler.NewCachedFSCompilerHost(cwd, fs, bundled.LibPath(), nil, nil)
	build.program = compiler.NewProgram(compiler.ProgramOptions{Config: parsed, Host: host})
	return build, nil
}

func projectRootForRequest(request BuildRequest, configPath, cwd string) string {
	root := request.ProjectRoot
	if root == "" {
		if configPath != "" {
			root = tspath.GetDirectoryPath(configPath)
		} else {
			root = cwd
		}
	}
	return tspath.GetNormalizedAbsolutePath(root, cwd)
}

func extractBingoOptions(raw any) (BingoOptions, bool, error) {
	root, ok := raw.(*collections.OrderedMap[string, any])
	if !ok || root == nil {
		return BingoOptions{}, false, nil
	}
	value, present := root.Get("bingoOptions")
	if !present {
		return BingoOptions{}, false, nil
	}
	encoded, err := jsonx.Marshal(value, jsonx.Deterministic(true))
	if err != nil {
		return BingoOptions{}, true, fmt.Errorf("encode bingoOptions: %w", err)
	}
	var result BingoOptions
	if err := jsonx.Unmarshal(encoded, &result, jsonx.RejectUnknownMembers(true)); err != nil {
		return BingoOptions{}, true, fmt.Errorf("decode bingoOptions: %w", err)
	}
	return result, true, nil
}

// collectProgramDiagnostics follows compiler.GetDiagnosticsOfAnyProgram's
// gating while preserving the producing stage in the stable DTO. Semantic
// diagnostics include the filtered bind results in this tsgo commit, so raw
// bind identities are used only to classify the diagnostics the semantic path
// actually returns.
func collectProgramDiagnostics(ctx context.Context, program *compiler.Program) ([]Diagnostic, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if program == nil {
		return nil, errors.New("tsfrontend: cannot collect diagnostics from a nil program")
	}
	all := ConvertTSDiagnostics(program.GetConfigFileParsingDiagnostics(), DiagnosticStageConfiguration)
	configCount := len(all)
	all = append(all, ConvertTSDiagnostics(program.GetSyntacticDiagnostics(ctx, nil), DiagnosticStageSyntax)...)
	if len(all) == configCount {
		all = append(all, ConvertTSDiagnostics(program.GetProgramDiagnostics(), DiagnosticStageProgram)...)
		bindIdentities := make(map[string]struct{})
		for _, diagnostic := range ConvertTSDiagnostics(program.GetBindDiagnostics(ctx, nil), DiagnosticStageBinding) {
			bindIdentities[diagnosticIdentity(diagnostic)] = struct{}{}
		}
		if program.Options().ListFilesOnly.IsFalseOrUnknown() {
			all = append(all, ConvertTSDiagnostics(program.GetGlobalDiagnostics(ctx), DiagnosticStageGlobal)...)
			if len(all) == configCount {
				semanticDiagnostics := ConvertTSDiagnostics(program.GetSemanticDiagnostics(ctx, nil), DiagnosticStageSemantic)
				for index := range semanticDiagnostics {
					if _, ok := bindIdentities[diagnosticIdentity(semanticDiagnostics[index])]; ok {
						semanticDiagnostics[index].Stage = DiagnosticStageBinding
					}
				}
				all = append(all, semanticDiagnostics...)
				all = append(all, ConvertTSDiagnostics(program.GetGlobalDiagnostics(ctx), DiagnosticStageGlobal)...)
			}
			if program.Options().GetEmitDeclarations() && len(all) == configCount {
				all = append(all, ConvertTSDiagnostics(program.GetDeclarationDiagnostics(ctx, nil), DiagnosticStageSemantic)...)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return SortAndDeduplicateDiagnostics(all), nil
}
