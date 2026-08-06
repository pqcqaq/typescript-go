package tsfrontend

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

func TestCheckMatchesCompilerDiagnosticGate(t *testing.T) {
	t.Parallel()
	request := testBuildRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true,"noEmit":true},"files":["main.ts"]}`,
		"/project/main.ts":       `export const value: string = 1;`,
	})

	result, err := Check(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	build, err := buildProgram(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	raw := compiler.GetDiagnosticsOfAnyProgram(
		context.Background(),
		build.program,
		nil,
		true,
		build.program.GetBindDiagnostics,
		build.program.GetSemanticDiagnostics,
	)
	wantCodes := make([]string, 0, len(raw))
	for _, diagnostic := range ConvertTSDiagnostics(raw, DiagnosticStageSemantic) {
		wantCodes = append(wantCodes, diagnostic.Code)
	}
	slices.Sort(wantCodes)
	gotCodes := make([]string, 0, len(result.Diagnostics))
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Category == DiagnosticCategoryTS {
			gotCodes = append(gotCodes, diagnostic.Code)
		}
	}
	slices.Sort(gotCodes)
	if !slices.Equal(gotCodes, wantCodes) {
		t.Fatalf("diagnostic codes = %v, compiler gate = %v", gotCodes, wantCodes)
	}
	if !DiagnosticsHaveErrors(result.Diagnostics) {
		t.Fatal("type error did not stop the frontend")
	}
}

func TestCollectProgramDiagnosticsPreservesBindStage(t *testing.T) {
	t.Parallel()
	request := testBuildRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true,"noEmit":true},"files":["main.ts"]}`,
		"/project/main.ts": `
			export const duplicate = 1;
			export const duplicate = 2;
			export const checked: string = 1;
		`,
	})
	build, err := buildProgram(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	diagnostics, err := collectProgramDiagnostics(context.Background(), build.program)
	if err != nil {
		t.Fatal(err)
	}

	bindCount := 0
	semanticCount := 0
	for _, diagnostic := range diagnostics {
		switch diagnostic.Code {
		case "TS2451":
			bindCount++
			if diagnostic.Stage != DiagnosticStageBinding {
				t.Fatalf("bind diagnostic stage = %q, want %q: %#v", diagnostic.Stage, DiagnosticStageBinding, diagnostic)
			}
		case "TS2322":
			semanticCount++
			if diagnostic.Stage != DiagnosticStageSemantic {
				t.Fatalf("semantic diagnostic stage = %q, want %q: %#v", diagnostic.Stage, DiagnosticStageSemantic, diagnostic)
			}
		}
	}
	if bindCount == 0 {
		t.Fatalf("binding diagnostics were not preserved: %#v", diagnostics)
	}
	if semanticCount == 0 {
		t.Fatalf("binding diagnostics incorrectly stopped semantic checking: %#v", diagnostics)
	}
}

func TestCollectProgramDiagnosticsDoesNotRestoreSuppressedBindDiagnostics(t *testing.T) {
	t.Parallel()
	request := testBuildRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true,"noEmit":true},"files":["main.ts"]}`,
		"/project/main.ts": `
			const scope = { value: 1 };
			// @ts-ignore: the diagnostic is intentionally suppressed.
			with (scope) {}
		`,
	})
	build, err := buildProgram(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	diagnostics, err := collectProgramDiagnostics(context.Background(), build.program)
	if err != nil {
		t.Fatal(err)
	}
	if slices.ContainsFunc(diagnostics, func(diagnostic Diagnostic) bool { return diagnostic.Code == "TS1101" }) {
		t.Fatalf("suppressed bind diagnostic was restored: %#v", diagnostics)
	}
}

func TestCheckUsesNormalizedStrictDefaults(t *testing.T) {
	t.Parallel()
	request := testBuildRequest(map[string]string{
		"/project/tsconfig.json": `{"files":["main.ts"]}`,
		"/project/main.ts":       `export function identity(value) { return value; }`,
	})
	result, err := Check(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(result.Diagnostics, func(d Diagnostic) bool { return d.Code == "TS7006" }) {
		t.Fatalf("strict default did not produce TS7006: %#v", result.Diagnostics)
	}
}

func TestCheckRejectsUnknownBingoOption(t *testing.T) {
	t.Parallel()
	request := testBuildRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true},"bingoOptions":{"profil":"static"},"files":["main.ts"]}`,
		"/project/main.ts":       `export const value = 1;`,
	})

	result, err := Check(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(result.Diagnostics, func(diagnostic Diagnostic) bool {
		return diagnostic.Code == DiagnosticCodeInvalidOption && diagnostic.MessageKey == "config.invalid_bingo_options"
	})
	if index < 0 {
		t.Fatalf("unknown bingoOptions field diagnostics = %#v", result.Diagnostics)
	}
	diagnostic := result.Diagnostics[index]
	if diagnostic.EntityID != "bingoOptions" || !slices.ContainsFunc(diagnostic.Arguments, func(argument string) bool {
		return strings.Contains(argument, "profil")
	}) {
		t.Fatalf("unknown bingoOptions field context = %#v", diagnostic)
	}
}

func TestCheckMissingConfigIsStableDiagnostic(t *testing.T) {
	t.Parallel()
	request := testBuildRequest(map[string]string{})
	result, err := Check(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Category != DiagnosticCategoryTS {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
}

func TestCheckHonorsCanceledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Check(ctx, testBuildRequest(map[string]string{}))
	if err == nil {
		t.Fatal("canceled Check returned nil error")
	}
}

func testBuildRequest(files map[string]string) BuildRequest {
	return BuildRequest{
		ConfigPath:       "/project/tsconfig.json",
		CurrentDirectory: "/project",
		FileSystem:       vfstest.FromMap(files, true),
	}
}
