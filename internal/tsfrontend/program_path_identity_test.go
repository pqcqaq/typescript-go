package tsfrontend

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

// A frontend cache key must describe the project, not the machine directory
// that happened to host it. Exercise both path spellings used by the Windows
// and WSL toolchains.
func TestFrontendSnapshotPathOptionsAreProjectRelative(t *testing.T) {
	t.Parallel()
	type buildResult struct {
		snapshot *ProgramSnapshot
		bytes    []byte
	}
	build := func(root string, caseSensitive bool) buildResult {
		t.Helper()
		files := map[string]string{
			root + "/tsconfig.json":    `{"compilerOptions":{"strict":true,"noEmit":true,"paths":{"@app/*":["./src/*"]},"rootDirs":["./src","./generated"],"typeRoots":["./types"]},"files":["src/main.ts","src/value.ts"]}`,
			root + "/src/main.ts":      `import { value } from "@app/value"; export const result: number = value;`,
			root + "/src/value.ts":     `export const value: number = 1;`,
			root + "/types/empty.d.ts": `declare const __ts2bin_path_identity: unique symbol;`,
		}
		request := BuildRequest{
			ConfigPath:           root + "/tsconfig.json",
			CurrentDirectory:     root,
			ProjectRoot:          root,
			FileSystem:           vfstest.FromMap(files, caseSensitive),
			BingoOptions:         DefaultBingoOptions(),
			BingoOptionsOverride: true,
		}
		frontend := NewFrontend(request.FileSystem, bundled.LibPath(), TypeScriptGoCommit, StandardLibraryHash)
		snapshot, diagnostics := frontend.Build(context.Background(), request)
		if DiagnosticsHaveErrors(diagnostics) || snapshot == nil {
			t.Fatalf("build at %q failed: snapshot=%t diagnostics=%v", root, snapshot != nil, diagnostics)
		}
		if got, want := snapshot.Config.TypeScript.RootDirs, []string{"src", "generated"}; !equalStringSlices(got, want) {
			t.Fatalf("rootDirs at %q = %#v, want %#v", root, got, want)
		}
		if got, want := snapshot.Config.TypeScript.TypeRoots, []string{"types"}; !equalStringSlices(got, want) {
			t.Fatalf("typeRoots at %q = %#v, want %#v", root, got, want)
		}
		encoded, err := snapshot.CanonicalBytes()
		if err != nil {
			t.Fatal(err)
		}
		return buildResult{snapshot: snapshot, bytes: encoded}
	}

	windows := build(`C:/ts2bin/path-identity`, false)
	wsl := build(`/mnt/c/ts2bin/path-identity`, true)
	if windows.snapshot.Config.TypeScriptDigest != wsl.snapshot.Config.TypeScriptDigest {
		t.Fatalf("TypeScript digest changed across physical roots: windows=%s wsl=%s", windows.snapshot.Config.TypeScriptDigest, wsl.snapshot.Config.TypeScriptDigest)
	}
	if windows.snapshot.ContentHash != wsl.snapshot.ContentHash {
		t.Fatalf("frontend content hash changed across physical roots: windows=%s wsl=%s", windows.snapshot.ContentHash, wsl.snapshot.ContentHash)
	}
	if !bytes.Equal(windows.bytes, wsl.bytes) {
		t.Fatal("canonical snapshots changed across Windows and WSL physical roots")
	}
}

func TestSnapshotTypeScriptOptionsProjectPathCanonicalization(t *testing.T) {
	t.Parallel()
	got := snapshotTypeScriptOptionsForProject(&core.CompilerOptions{
		BaseUrl:   `C:\project`,
		RootDirs:  []string{`C:\project\src`, `C:\project\generated`},
		TypeRoots: []string{`C:\project\types`},
	}, `C:\project`, false)
	if got.BaseURL != "." || !equalStringSlices(got.RootDirs, []string{"src", "generated"}) || !equalStringSlices(got.TypeRoots, []string{"types"}) {
		t.Fatalf("project-relative options = %#v", got)
	}
}

func TestFrontendSnapshotRejectsCrossRootTypeScriptOptionPath(t *testing.T) {
	t.Parallel()
	const root = "C:/ts2bin/cross-root-option"
	files := map[string]string{
		root + "/tsconfig.json": `{"compilerOptions":{"strict":true,"rootDirs":["./src","D:/machine-specific/generated"]},"files":["main.ts"]}`,
		root + "/main.ts":       `export const value: number = 1;`,
	}
	fs := vfstest.FromMap(files, false)
	frontend := NewFrontend(fs, bundled.LibPath(), TypeScriptGoCommit, StandardLibraryHash)
	snapshot, diagnostics := frontend.Build(context.Background(), BuildRequest{
		ConfigPath:           root + "/tsconfig.json",
		CurrentDirectory:     root,
		ProjectRoot:          root,
		FileSystem:           fs,
		BingoOptions:         DefaultBingoOptions(),
		BingoOptionsOverride: true,
	})
	if snapshot != nil {
		t.Fatal("cross-root TypeScript option path produced a frontend snapshot")
	}
	for _, diagnostic := range diagnostics {
		for _, argument := range diagnostic.Arguments {
			if strings.Contains(argument, "rootDirs[1]") && strings.Contains(argument, "rooted disk path") {
				return
			}
		}
	}
	t.Fatalf("cross-root option path diagnostics = %v", diagnostics)
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
