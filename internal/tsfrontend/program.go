package tsfrontend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"runtime"
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/core"
	jsonx "github.com/microsoft/typescript-go/internal/json"
	"github.com/microsoft/typescript-go/internal/tspath"
	"github.com/microsoft/typescript-go/internal/vfs"
	"github.com/microsoft/typescript-go/internal/vfs/osvfs"
)

// Frontend owns filesystem and provenance inputs for independent Program
// builds. A Frontend has no mutable Program/checker cache and can therefore be
// reused concurrently.
type Frontend struct {
	fs                  vfs.FS
	defaultLibraryPath  string
	typeScriptGoCommit  string
	standardLibraryHash string
	caseSensitivePaths  bool
}

// NewFrontend creates a frontend over an explicit filesystem. The commit and
// stdlib hash are provenance inputs and must come from the repository lock or a
// compatibility test fixture. If standardLibraryHash is empty, Build computes
// it from defaultLibraryPath.
func NewFrontend(fs vfs.FS, defaultLibraryPath, typeScriptGoCommit, standardLibraryHash string) *Frontend {
	caseSensitivePaths := runtime.GOOS != "windows"
	if fs != nil {
		caseSensitivePaths = fs.UseCaseSensitiveFileNames()
	}
	return &Frontend{
		fs:                  fs,
		defaultLibraryPath:  defaultLibraryPath,
		typeScriptGoCommit:  typeScriptGoCommit,
		standardLibraryHash: standardLibraryHash,
		caseSensitivePaths:  caseSensitivePaths,
	}
}

// NewOSFrontend creates a frontend backed by the local filesystem and tsgo's
// bundled standard library.
func NewOSFrontend(typeScriptGoCommit string) *Frontend {
	return NewFrontend(bundled.WrapFS(osvfs.FS()), bundled.LibPath(), typeScriptGoCommit, StandardLibraryHash)
}

// Build creates a Program, emits the complete TypeScript diagnostic set, then
// captures and gates an immutable snapshot. TypeScript errors stop before any
// checker lease is acquired for snapshot capture. Infrastructure failures are
// represented as an internal structured diagnostic so CLI callers retain one
// stable result shape.
func (f *Frontend) Build(ctx context.Context, request BuildRequest) (*ProgramSnapshot, []Diagnostic) {
	if f == nil {
		f = NewOSFrontend(TypeScriptGoCommit)
	}
	if request.FileSystem == nil {
		request.FileSystem = f.fs
	}
	if request.FileSystem != nil {
		// A caller may reuse an OS frontend with a hermetic VFS. The VFS policy,
		// rather than the host process, owns path identity for this build.
		copy := *f
		copy.caseSensitivePaths = request.FileSystem.UseCaseSensitiveFileNames()
		f = &copy
	}
	build, err := buildProgram(ctx, request)
	if err != nil {
		diagnostic := NewRegisteredDiagnostic(DiagnosticCodeInternalFailure, SourceSpan{}, err.Error())
		diagnostic.Stage = DiagnosticStageSnapshot
		diagnostic.EntityID = "program-build"
		return nil, []Diagnostic{diagnostic}
	}
	if build == nil {
		return nil, []Diagnostic{NewRegisteredDiagnostic(DiagnosticCodeInternalFailure, SourceSpan{}, "nil program build")}
	}
	diagnostics := slices.Clone(build.diagnostics)
	if build.program == nil {
		if len(diagnostics) == 0 {
			diagnostic := NewRegisteredDiagnostic(DiagnosticCodeInternalFailure, SourceSpan{}, "program was not constructed")
			diagnostic.Stage = DiagnosticStageSnapshot
			diagnostic.EntityID = "program-build"
			diagnostics = append(diagnostics, diagnostic)
		}
		return nil, normalizeDiagnosticPaths(diagnostics, build.projectRoot, f.defaultLibraryPath, f.caseSensitivePaths)
	}
	programDiagnostics, err := collectProgramDiagnostics(ctx, build.program)
	if err != nil {
		diagnostic := NewRegisteredDiagnostic(DiagnosticCodeInternalFailure, SourceSpan{}, err.Error())
		diagnostic.Stage = DiagnosticStageSnapshot
		diagnostic.EntityID = "program-diagnostics"
		return nil, SortAndDeduplicateDiagnostics(append(diagnostics, diagnostic))
	}
	diagnostics = append(diagnostics, programDiagnostics...)
	diagnostics = normalizeDiagnosticPaths(diagnostics, build.projectRoot, f.defaultLibraryPath, f.caseSensitivePaths)
	if DiagnosticsHaveErrors(diagnostics) {
		return nil, diagnostics
	}

	// Snapshot capture is the sole place where checker leases are acquired.
	snapshot, snapshotDiagnostics := build.captureSnapshot(ctx, f)
	diagnostics = append(diagnostics, snapshotDiagnostics...)
	if snapshot == nil {
		return nil, SortAndDeduplicateDiagnostics(diagnostics)
	}
	diagnostics = SortAndDeduplicateDiagnostics(diagnostics)
	snapshot.Diagnostics = cloneDiagnostics(diagnostics)
	if err := finalizeSnapshot(snapshot); err != nil {
		diagnostic := NewRegisteredDiagnostic(DiagnosticCodeInternalFailure, SourceSpan{}, err.Error())
		diagnostic.Stage = DiagnosticStageSnapshot
		diagnostic.EntityID = "program-snapshot"
		return nil, SortAndDeduplicateDiagnostics(append(diagnostics, diagnostic))
	}
	validated, err := newValidatedProgramSnapshot(*snapshot)
	if err != nil {
		diagnostic := NewRegisteredDiagnostic(DiagnosticCodeInternalFailure, SourceSpan{}, err.Error())
		diagnostic.Stage = DiagnosticStageSnapshot
		diagnostic.EntityID = "program-snapshot-validation"
		return nil, SortAndDeduplicateDiagnostics(append(diagnostics, diagnostic))
	}

	diagnostics = append(diagnostics, runSubsetGate(validated)...)
	diagnostics = SortAndDeduplicateDiagnostics(diagnostics)
	snapshot.Diagnostics = cloneDiagnostics(diagnostics)
	if err := finalizeSnapshot(snapshot); err != nil {
		diagnostic := NewRegisteredDiagnostic(DiagnosticCodeInternalFailure, SourceSpan{}, err.Error())
		diagnostic.Stage = DiagnosticStageSnapshot
		diagnostic.EntityID = "program-snapshot"
		return nil, SortAndDeduplicateDiagnostics(append(diagnostics, diagnostic))
	}
	sealed, err := newValidatedProgramSnapshot(*snapshot)
	if err != nil {
		diagnostic := NewRegisteredDiagnostic(DiagnosticCodeInternalFailure, SourceSpan{}, err.Error())
		diagnostic.Stage = DiagnosticStageSnapshot
		diagnostic.EntityID = "program-snapshot-validation"
		return nil, SortAndDeduplicateDiagnostics(append(diagnostics, diagnostic))
	}
	return &sealed.snapshot, cloneDiagnostics(diagnostics)
}

func normalizeDiagnosticPaths(input []Diagnostic, projectRoot, defaultLibraryPath string, caseSensitive ...bool) []Diagnostic {
	result := slices.Clone(input)
	for i := range result {
		result[i].PrimarySpan.File = logicalPath(result[i].PrimarySpan.File, projectRoot, defaultLibraryPath, caseSensitive...)
		for j := range result[i].RelatedSpans {
			result[i].RelatedSpans[j].Span.File = logicalPath(result[i].RelatedSpans[j].Span.File, projectRoot, defaultLibraryPath, caseSensitive...)
		}
	}
	return SortAndDeduplicateDiagnostics(result)
}

// logicalPath removes machine-specific project roots while retaining a stable
// stdlib namespace. It is also used by stable FileId and diagnostic ordering.
func logicalPath(fileName, projectRoot, defaultLibraryPath string, caseSensitive ...bool) string {
	if fileName == "" {
		return ""
	}
	fileName = tspath.NormalizePath(fileName)
	projectRoot = tspath.NormalizePath(projectRoot)
	defaultLibraryPath = tspath.NormalizePath(defaultLibraryPath)
	useCaseSensitivePaths := runtime.GOOS != "windows"
	if len(caseSensitive) != 0 {
		useCaseSensitivePaths = caseSensitive[0]
	}
	compareOptions := tspath.ComparePathsOptions{UseCaseSensitiveFileNames: useCaseSensitivePaths, CurrentDirectory: projectRoot}
	if defaultLibraryPath != "" && tspath.ContainsPath(defaultLibraryPath, fileName, compareOptions) {
		relative := tspath.GetRelativePathFromDirectory(defaultLibraryPath, fileName, compareOptions)
		return canonicalPathCase("@stdlib/"+strings.TrimPrefix(tspath.NormalizeSlashes(relative), "./"), useCaseSensitivePaths)
	}
	relative := tspath.GetRelativePathFromDirectory(projectRoot, fileName, compareOptions)
	return canonicalPathCase(strings.TrimPrefix(tspath.NormalizeSlashes(relative), "./"), useCaseSensitivePaths)
}

func (s *programBuild) logicalPath(fileName string, frontend *Frontend) string {
	return frontend.logicalPath(fileName, s.projectRoot)
}

func (f *Frontend) logicalPath(fileName, projectRoot string) string {
	if f == nil {
		return logicalPath(fileName, projectRoot, "")
	}
	return logicalPath(fileName, projectRoot, f.defaultLibraryPath, f.caseSensitivePaths)
}

func canonicalPathCase(path string, caseSensitive bool) string {
	if caseSensitive {
		return path
	}
	return strings.ToLower(path)
}

func pathCasePolicy(explicit []bool) bool {
	if len(explicit) != 0 {
		return explicit[0]
	}
	return runtime.GOOS != "windows"
}

func snapshotTypeScriptOptions(options *core.CompilerOptions) TypeScriptOptions {
	return snapshotTypeScriptOptionsForProject(options, "", true)
}

// snapshotTypeScriptOptionsForProject projects compiler path options into the
// logical source tree before they enter the frontend cache key. The checker
// resolves these options to absolute paths, so retaining them verbatim would
// make an otherwise identical project hash differently after relocation (or
// when the same tree is built from Windows and WSL).
func snapshotTypeScriptOptionsForProject(options *core.CompilerOptions, projectRoot string, caseSensitive bool) TypeScriptOptions {
	if options == nil {
		return TypeScriptOptions{}
	}

	// Capture effective values rather than the tristate spelling supplied by a
	// tsconfig. For example, `strict: true` and an explicitly expanded set of
	// strict flags must produce the same frontend identity.
	strict := func(value core.Tristate) bool { return options.GetStrictOptionValue(value) }
	result := TypeScriptOptions{
		Strict:                       strict(options.Strict),
		StrictNullChecks:             strict(options.StrictNullChecks),
		StrictFunctionTypes:          strict(options.StrictFunctionTypes),
		NoImplicitAny:                strict(options.NoImplicitAny),
		NoImplicitThis:               strict(options.NoImplicitThis),
		StrictBindCallApply:          strict(options.StrictBindCallApply),
		StrictBuiltinIteratorReturn:  strict(options.StrictBuiltinIteratorReturn),
		StrictPropertyInitialization: strict(options.StrictPropertyInitialization),
		UseUnknownInCatchVariables:   strict(options.UseUnknownInCatchVariables),
		AlwaysStrict:                 strict(options.AlwaysStrict),

		ExactOptionalPropertyTypes:   options.ExactOptionalPropertyTypes.IsTrue(),
		NoUncheckedIndexedAccess:     options.NoUncheckedIndexedAccess.IsTrue(),
		NoPropertyAccessFromIndexSig: options.NoPropertyAccessFromIndexSignature.IsTrue(),
		NoImplicitReturns:            options.NoImplicitReturns.IsTrue(),
		NoFallthroughCasesInSwitch:   options.NoFallthroughCasesInSwitch.IsTrue(),
		NoUnusedLocals:               options.NoUnusedLocals.IsTrue(),
		NoUnusedParameters:           options.NoUnusedParameters.IsTrue(),
		AllowUnreachableCode:         options.AllowUnreachableCode.IsTrue(),
		AllowUnusedLabels:            options.AllowUnusedLabels.IsTrue(),

		AllowJS:                                   options.GetAllowJS(),
		CheckJS:                                   options.CheckJs.IsTrue(),
		AllowArbitraryExtensions:                  options.AllowArbitraryExtensions.IsTrue(),
		AllowNonTsExtensions:                      options.AllowNonTsExtensions.IsTrue(),
		AllowImportingTSExtensions:                options.GetAllowImportingTsExtensions(),
		RewriteRelativeImportExtensions:           options.RewriteRelativeImportExtensions.IsTrue(),
		AllowUmdGlobalAccess:                      options.AllowUmdGlobalAccess.IsTrue(),
		UseDefineForClassFields:                   options.GetUseDefineForClassFields(),
		VerbatimModuleSyntax:                      options.VerbatimModuleSyntax.IsTrue(),
		ExperimentalDecorators:                    options.ExperimentalDecorators.IsTrue(),
		EmitDecoratorMetadata:                     options.EmitDecoratorMetadata.IsTrue(),
		ESModuleInterop:                           options.ESModuleInterop.IsTrue(),
		AllowSyntheticDefaultImports:              options.AllowSyntheticDefaultImports.IsTrue(),
		IsolatedModules:                           options.GetIsolatedModules(),
		IsolatedDeclarations:                      options.IsolatedDeclarations.IsTrue(),
		ErasableSyntaxOnly:                        options.ErasableSyntaxOnly.IsTrue(),
		NoCheck:                                   options.NoCheck.IsTrue(),
		DisableSizeLimit:                          options.DisableSizeLimit.IsTrue(),
		NoErrorTruncation:                         options.NoErrorTruncation.IsTrue(),
		AssumeChangesOnlyAffectDirectDependencies: options.AssumeChangesOnlyAffectDirectDependencies.IsTrue(),
		NoImplicitOverride:                        options.NoImplicitOverride.IsTrue(),
		ForceConsistentCasing:                     options.ForceConsistentCasingInFileNames.IsTrue(),
		NoResolve:                                 options.NoResolve.IsTrue(),
		NoDTSResolution:                           options.NoDtsResolution.IsTrue(),
		NoLib:                                     options.NoLib.IsTrue(),
		NoUncheckedSideEffectImports:              options.NoUncheckedSideEffectImports.IsTrue(),
		PreserveSymlinks:                          options.PreserveSymlinks.IsTrue(),
		SkipLibCheck:                              options.SkipLibCheck.IsTrue(),
		SkipDefaultLibCheck:                       options.SkipDefaultLibCheck.IsTrue(),
		DeduplicatePackages:                       options.DeduplicatePackages.IsTrueOrUnknown(),
		LibReplacement:                            options.LibReplacement.IsTrue(),
		StableTypeOrdering:                        options.StableTypeOrdering.IsTrueOrUnknown(),
		PreserveConstEnums:                        options.ShouldPreserveConstEnums(),
		MaxNodeModuleJSDepth:                      optionInt(options.MaxNodeModuleJsDepth),

		Target:                    options.GetEmitScriptTarget().String(),
		Module:                    options.GetEmitModuleKind().String(),
		ModuleResolution:          options.GetModuleResolutionKind().String(),
		ModuleDetection:           moduleDetectionString(options.GetEmitModuleDetectionKind()),
		ModuleSuffixes:            slices.Clone(options.ModuleSuffixes),
		BaseURL:                   canonicalProjectOptionPath(options.BaseUrl, projectRoot, caseSensitive),
		RootDirs:                  canonicalProjectOptionPaths(options.RootDirs, projectRoot, caseSensitive),
		TypeRoots:                 canonicalProjectOptionPaths(options.TypeRoots, projectRoot, caseSensitive),
		Types:                     slices.Clone(options.Types),
		JSX:                       jsxOptionString(options.Jsx),
		JSXFactory:                options.JsxFactory,
		JSXFragmentFactory:        options.JsxFragmentFactory,
		JSXImportSource:           options.JsxImportSource,
		ReactNamespace:            options.ReactNamespace,
		Lib:                       slices.Clone(options.Lib),
		CustomConditions:          slices.Clone(options.CustomConditions),
		ResolvePackageJSONExports: options.GetResolvePackageJsonExports(),
		ResolvePackageJSONImports: options.GetResolvePackageJsonImports(),
		ResolveJSONModule:         options.GetResolveJsonModule(),
	}
	if options.Paths != nil {
		result.Paths = make([]TypeScriptPathMapping, 0, options.Paths.Size())
		for index := 0; index < options.Paths.Size(); index++ {
			pattern, substitutions, ok := options.Paths.EntryAt(index)
			if !ok {
				continue
			}
			result.Paths = append(result.Paths, TypeScriptPathMapping{
				Pattern:       canonicalOptionPath(pattern),
				Substitutions: canonicalProjectOptionPaths(substitutions, projectRoot, caseSensitive),
			})
		}
		slices.SortFunc(result.Paths, func(left, right TypeScriptPathMapping) int {
			return strings.Compare(left.Pattern, right.Pattern)
		})
	}
	return result
}

func canonicalOptionPath(value string) string {
	if value == "" {
		return ""
	}
	return tspath.NormalizePath(value)
}

func canonicalOptionPaths(values []string) []string {
	if values == nil {
		return nil
	}
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = canonicalOptionPath(value)
	}
	return result
}

func canonicalProjectOptionPath(value, projectRoot string, caseSensitive bool) string {
	if value == "" {
		return ""
	}
	normalized := tspath.NormalizePath(value)
	if projectRoot == "" || !tspath.IsRootedDiskPath(normalized) {
		return canonicalOptionPath(normalized)
	}
	logical := logicalPath(normalized, projectRoot, "", caseSensitive)
	if logical == "" {
		return "."
	}
	return logical
}

func canonicalProjectOptionPaths(values []string, projectRoot string, caseSensitive bool) []string {
	if values == nil {
		return nil
	}
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = canonicalProjectOptionPath(value, projectRoot, caseSensitive)
	}
	return result
}

func optionInt(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func moduleDetectionString(value core.ModuleDetectionKind) string {
	switch value {
	case core.ModuleDetectionKindAuto:
		return "auto"
	case core.ModuleDetectionKindLegacy:
		return "legacy"
	case core.ModuleDetectionKindForce:
		return "force"
	default:
		return "none"
	}
}

func jsxOptionString(value core.JsxEmit) string {
	if value == core.JsxEmitNone {
		return "none"
	}
	return value.String()
}

func hashCanonical(value any) (string, error) {
	encoded, err := jsonx.Marshal(value, jsonx.Deterministic(true))
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (f *Frontend) stdlibHash() (string, error) {
	if f.standardLibraryHash != "" {
		return f.standardLibraryHash, nil
	}
	type libraryFile struct {
		path     string
		contents string
	}
	entries := make([]libraryFile, 0, 128)
	err := f.fs.WalkDir(f.defaultLibraryPath, func(path string, entry vfs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		contents, ok := f.fs.ReadFile(path)
		if !ok {
			return fmt.Errorf("read bundled declaration %q", path)
		}
		relative := tspath.GetRelativePathFromDirectory(f.defaultLibraryPath, path, tspath.ComparePathsOptions{
			UseCaseSensitiveFileNames: f.fs.UseCaseSensitiveFileNames(),
			CurrentDirectory:          f.defaultLibraryPath,
		})
		entries = append(entries, libraryFile{
			path:     strings.TrimPrefix(tspath.NormalizeSlashes(relative), "./"),
			contents: contents,
		})
		return nil
	})
	if err != nil {
		return "", err
	}
	slices.SortFunc(entries, func(left, right libraryFile) int {
		return strings.Compare(left.path, right.path)
	})
	digest := sha256.New()
	for _, entry := range entries {
		_, _ = digest.Write([]byte(entry.path))
		_, _ = digest.Write([]byte{0})
		_, _ = digest.Write([]byte(entry.contents))
		_, _ = digest.Write([]byte{0})
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func finalizeSnapshot(snapshot *ProgramSnapshot) error {
	snapshot.ContentHash = ""
	encoded, err := jsonx.Marshal(snapshot, jsonx.Deterministic(true))
	if err != nil {
		return fmt.Errorf("encode canonical snapshot: %w", err)
	}
	digest := sha256.Sum256(encoded)
	snapshot.ContentHash = hex.EncodeToString(digest[:])
	return nil
}

func provenanceSnapshot(frontend *Frontend, stdlibHash, kindManifestHash string) ProvenanceSnapshot {
	return ProvenanceSnapshot{
		TypeScriptGoCommit:  frontend.typeScriptGoCommit,
		TypeScriptVersion:   core.Version(),
		GoVersion:           runtime.Version(),
		StandardLibraryHash: stdlibHash,
		KindManifestHash:    kindManifestHash,
	}
}

// Keep compiler and AST imports at this boundary explicit for API audits.
var _ = (*compiler.Program)(nil)
var _ = (*ast.SourceFile)(nil)
