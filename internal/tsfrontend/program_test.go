package tsfrontend

import (
	"reflect"
	"testing"

	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/collections"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/vfs/osvfs"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

func TestBundledStandardLibraryHashMatchesLock(t *testing.T) {
	t.Parallel()
	frontend := NewFrontend(bundled.WrapFS(osvfs.FS()), bundled.LibPath(), TypeScriptGoCommit, "")
	got, err := frontend.stdlibHash()
	if err != nil {
		t.Fatal(err)
	}
	if got != StandardLibraryHash {
		t.Fatalf("stdlib hash = %s, want %s", got, StandardLibraryHash)
	}
}

func TestLogicalPathUsesFilesystemCasePolicy(t *testing.T) {
	t.Parallel()
	files := map[string]string{"/project/A.ts": "", "/project/a.ts": ""}
	caseSensitive := NewFrontend(vfstest.FromMap(files, true), "", "test", "test")
	upper := caseSensitive.logicalPath("/project/A.ts", "/project")
	lower := caseSensitive.logicalPath("/project/a.ts", "/project")
	if upper == lower {
		t.Fatalf("case-sensitive paths collided: %q", upper)
	}

	caseInsensitive := NewFrontend(vfstest.FromMap(map[string]string{"/project/A.ts": ""}, false), "", "test", "test")
	upper = caseInsensitive.logicalPath("/project/A.ts", "/project")
	lower = caseInsensitive.logicalPath("/project/a.ts", "/project")
	if upper != lower {
		t.Fatalf("case-insensitive aliases differ: %q vs %q", upper, lower)
	}
}

func TestLogicalPathNormalizesWindowsAndWSLSeparators(t *testing.T) {
	t.Parallel()
	if got := logicalPath(`C:\project\src\main.ts`, `C:\project`, "", false); got != "src/main.ts" {
		t.Fatalf("Windows logical path = %q", got)
	}
	if got := logicalPath("/mnt/c/project/src/main.ts", "/mnt/c/project", "", true); got != "src/main.ts" {
		t.Fatalf("WSL logical path = %q", got)
	}
}

func TestSnapshotTypeScriptOptionsCapturesSemanticOptions(t *testing.T) {
	t.Parallel()
	paths := collections.NewOrderedMapFromList([]collections.MapEntry[string, []string]{
		{Key: `z\*`, Value: []string{`.\z\first\*`, `.\z\second\*`}},
		{Key: `a\*`, Value: []string{`.\a\first\*`, `.\a\second\*`}},
	})
	got := snapshotTypeScriptOptions(&core.CompilerOptions{
		Strict:                          core.TSTrue,
		NoImplicitThis:                  core.TSFalse,
		CheckJs:                         core.TSTrue,
		AllowImportingTsExtensions:      core.TSFalse,
		RewriteRelativeImportExtensions: core.TSTrue,
		VerbatimModuleSyntax:            core.TSTrue,
		Target:                          core.ScriptTargetES2022,
		Module:                          core.ModuleKindNodeNext,
		ModuleResolution:                core.ModuleResolutionKindNodeNext,
		ModuleDetection:                 core.ModuleDetectionKindLegacy,
		ModuleSuffixes:                  []string{".native", ""},
		BaseUrl:                         `C:\project\src\..\base`,
		Paths:                           paths,
		RootDirs:                        []string{`C:\project\src`, `C:\project\generated`},
		TypeRoots:                       []string{`C:\project\types`},
		Types:                           []string{"node", "vitest"},
		AllowArbitraryExtensions:        core.TSTrue,
		AllowNonTsExtensions:            core.TSTrue,
		NoCheck:                         core.TSTrue,
		DisableSizeLimit:                core.TSTrue,
		NoErrorTruncation:               core.TSTrue,
		AssumeChangesOnlyAffectDirectDependencies: core.TSTrue,
		NoImplicitOverride:                        core.TSTrue,
		DeduplicatePackages:                       core.TSFalse,
		LibReplacement:                            core.TSTrue,
		StableTypeOrdering:                        core.TSFalse,
		PreserveConstEnums:                        core.TSTrue,
		MaxNodeModuleJsDepth:                      intPointer(3),
		ReactNamespace:                            "ReactCompat",
		NoDtsResolution:                           core.TSTrue,
		NoUncheckedSideEffectImports:              core.TSTrue,
		ResolvePackageJsonExports:                 core.TSFalse,
		ResolvePackageJsonImports:                 core.TSTrue,
		ResolveJsonModule:                         core.TSTrue,
		NoPropertyAccessFromIndexSignature:        core.TSTrue,
	})

	if !got.Strict || !got.StrictNullChecks || !got.StrictFunctionTypes || !got.NoImplicitAny {
		t.Fatalf("strict umbrella was not expanded: %#v", got)
	}
	if got.NoImplicitThis {
		t.Fatal("explicit strict option override was not retained")
	}
	if !got.AllowJS || !got.CheckJS || !got.AllowImportingTSExtensions {
		t.Fatalf("effective JavaScript/import options were not captured: %#v", got)
	}
	if !got.AllowNonTsExtensions || !got.RewriteRelativeImportExtensions || !got.NoCheck ||
		!got.NoImplicitOverride || got.DeduplicatePackages || !got.LibReplacement || got.StableTypeOrdering ||
		!got.PreserveConstEnums || got.MaxNodeModuleJSDepth != 3 || got.ReactNamespace != "ReactCompat" {
		t.Fatalf("additional semantic options were not captured: %#v", got)
	}
	if !got.DisableSizeLimit || !got.NoErrorTruncation || !got.AssumeChangesOnlyAffectDirectDependencies {
		t.Fatalf("diagnostic/cache semantic options were not captured: %#v", got)
	}
	if !got.IsolatedModules || !got.UseDefineForClassFields {
		t.Fatalf("derived checker options were not captured: %#v", got)
	}
	if got.ModuleDetection != "legacy" || got.BaseURL != "C:/project/base" {
		t.Fatalf("module/path options were not canonicalized: %#v", got)
	}
	wantPaths := []TypeScriptPathMapping{
		{Pattern: "a/*", Substitutions: []string{"a/first/*", "a/second/*"}},
		{Pattern: "z/*", Substitutions: []string{"z/first/*", "z/second/*"}},
	}
	if !reflect.DeepEqual(got.Paths, wantPaths) {
		t.Fatalf("paths = %#v, want %#v", got.Paths, wantPaths)
	}
	if !reflect.DeepEqual(got.ModuleSuffixes, []string{".native", ""}) ||
		!reflect.DeepEqual(got.RootDirs, []string{"C:/project/src", "C:/project/generated"}) ||
		!reflect.DeepEqual(got.TypeRoots, []string{"C:/project/types"}) ||
		!reflect.DeepEqual(got.Types, []string{"node", "vitest"}) {
		t.Fatalf("ordered semantic lists were not retained: %#v", got)
	}
	if got.ResolvePackageJSONExports || !got.ResolvePackageJSONImports || !got.ResolveJSONModule ||
		!got.NoDTSResolution || !got.NoUncheckedSideEffectImports || !got.NoPropertyAccessFromIndexSig {
		t.Fatalf("resolution/checker flags were not captured: %#v", got)
	}
}

func TestSnapshotTypeScriptOptionsDigestChangesWithSemanticInputs(t *testing.T) {
	t.Parallel()
	base := &core.CompilerOptions{
		Strict:           core.TSTrue,
		Target:           core.ScriptTargetES2022,
		Module:           core.ModuleKindESNext,
		ModuleResolution: core.ModuleResolutionKindBundler,
		ModuleDetection:  core.ModuleDetectionKindAuto,
	}
	baseDigest, err := hashCanonical(snapshotTypeScriptOptions(base))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*core.CompilerOptions)
	}{
		{name: "strict flag", mutate: func(options *core.CompilerOptions) { options.NoImplicitThis = core.TSFalse }},
		{name: "checkJs", mutate: func(options *core.CompilerOptions) { options.CheckJs = core.TSTrue }},
		{name: "module detection", mutate: func(options *core.CompilerOptions) { options.ModuleDetection = core.ModuleDetectionKindForce }},
		{name: "module suffixes", mutate: func(options *core.CompilerOptions) { options.ModuleSuffixes = []string{".native", ""} }},
		{name: "baseUrl", mutate: func(options *core.CompilerOptions) { options.BaseUrl = "/project/src" }},
		{name: "paths", mutate: func(options *core.CompilerOptions) {
			options.Paths = collections.NewOrderedMapFromList([]collections.MapEntry[string, []string]{
				{Key: "@app/*", Value: []string{"src/*"}},
			})
		}},
		{name: "rootDirs", mutate: func(options *core.CompilerOptions) { options.RootDirs = []string{"/project/src"} }},
		{name: "typeRoots", mutate: func(options *core.CompilerOptions) { options.TypeRoots = []string{"/project/types"} }},
		{name: "types", mutate: func(options *core.CompilerOptions) { options.Types = []string{"node"} }},
		{name: "resolution flag", mutate: func(options *core.CompilerOptions) { options.NoResolve = core.TSTrue }},
		{name: "noImplicitOverride", mutate: func(options *core.CompilerOptions) { options.NoImplicitOverride = core.TSTrue }},
		{name: "stableTypeOrdering", mutate: func(options *core.CompilerOptions) { options.StableTypeOrdering = core.TSFalse }},
		{name: "libReplacement", mutate: func(options *core.CompilerOptions) { options.LibReplacement = core.TSTrue }},
		{name: "preserveConstEnums", mutate: func(options *core.CompilerOptions) { options.PreserveConstEnums = core.TSTrue }},
		{name: "module JS depth", mutate: func(options *core.CompilerOptions) { options.MaxNodeModuleJsDepth = intPointer(2) }},
		{name: "disable size limit", mutate: func(options *core.CompilerOptions) { options.DisableSizeLimit = core.TSTrue }},
		{name: "no error truncation", mutate: func(options *core.CompilerOptions) { options.NoErrorTruncation = core.TSTrue }},
		{name: "direct dependency assumption", mutate: func(options *core.CompilerOptions) { options.AssumeChangesOnlyAffectDirectDependencies = core.TSTrue }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := base.Clone()
			test.mutate(changed)
			digest, err := hashCanonical(snapshotTypeScriptOptions(changed))
			if err != nil {
				t.Fatal(err)
			}
			if digest == baseDigest {
				t.Fatalf("semantic option change did not invalidate digest: %s", digest)
			}
		})
	}
}

func intPointer(value int) *int {
	return &value
}
