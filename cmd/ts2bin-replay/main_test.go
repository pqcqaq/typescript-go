package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast2bingo"
	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/tsfrontend"
)

func TestReplayCommandReadsFrontendSnapshotFromDiskInSeparateProcess(t *testing.T) {
	injectedForkCommit := strings.Repeat("a", 40)
	project := t.TempDir()
	configPath := filepath.Join(project, "tsconfig.json")
	if err := os.WriteFile(configPath, []byte(`{"compilerOptions":{"strict":true},"files":["main.ts"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "main.ts"), []byte(`export function add(left: number, right: number): number { return left + right; }`), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, diagnostics := tsfrontend.NewOSFrontend(injectedForkCommit).Build(context.Background(), tsfrontend.BuildRequest{ConfigPath: configPath})
	if snapshot == nil || tsfrontend.DiagnosticsHaveErrors(diagnostics) {
		t.Fatalf("snapshot/diagnostics = %#v / %#v", snapshot, diagnostics)
	}
	frontend, err := tsfrontend.NewFrontendSnapshot(*snapshot)
	if err != nil {
		t.Fatal(err)
	}
	input, err := frontend.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	inputPath := filepath.Join(t.TempDir(), "frontend.snapshot.json")
	outputDir := t.TempDir()
	outputPath := filepath.Join(outputDir, "replay-one.json")
	secondOutputPath := filepath.Join(outputDir, "replay-two.json")
	if err := os.WriteFile(inputPath, input, 0o600); err != nil {
		t.Fatal(err)
	}

	binaryPath := filepath.Join(t.TempDir(), "ts2bin-replay.exe")
	ldflags := strings.Join([]string{
		"-X github.com/microsoft/typescript-go/internal/ast2bingo.injectedUpstreamCommit=" + tsfrontend.TypeScriptGoCommit,
		"-X github.com/microsoft/typescript-go/internal/ast2bingo.injectedForkCommit=" + injectedForkCommit,
		"-X github.com/microsoft/typescript-go/internal/tsfrontend.TypeScriptGoCommit=" + injectedForkCommit,
	}, " ")
	build := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-ldflags", ldflags, "-o", binaryPath, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build replay command: %v\n%s", err, output)
	}
	for _, path := range []string{outputPath, secondOutputPath} {
		command := exec.Command(binaryPath, "--input", inputPath, "--output", path)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("replay process: %v\n%s", err, output)
		}
	}

	encoded, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(secondOutputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, second) {
		t.Fatal("replay command output is not byte-identical across processes")
	}
	var result ast2bingo.SnapshotReplayResult
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatalf("decode replay result: %v\n%s", err, encoded)
	}
	if err := bingo.VerifyHIR(result.HIR); err != nil {
		t.Fatalf("verify replayed HIR: %v", err)
	}
	wantIdentity, err := ast2bingo.NewCompilerBuildIdentity(
		tsfrontend.TypeScriptGoCommit,
		injectedForkCommit,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.CompilerBuildIdentity != wantIdentity || result.HIR.Provenance.CompilerBuildIdentity != wantIdentity {
		t.Fatalf("replay output compiler identity = %#v / %#v, want %#v", result.CompilerBuildIdentity, result.HIR.Provenance.CompilerBuildIdentity, wantIdentity)
	}
	gotKinds := make([]string, len(result.Events))
	for index, event := range result.Events {
		gotKinds[index] = event.Kind
	}
	wantKinds := []string{"function.begin", "parameter", "parameter", "binary.add", "return", "function.end"}
	if !slices.Equal(gotKinds, wantKinds) {
		t.Fatalf("event order = %v, want %v", gotKinds, wantKinds)
	}
}

func TestReplayCommandRejectsRawProgramSnapshot(t *testing.T) {
	originalIdentityLoader := loadCompilerBuildIdentity
	loadCompilerBuildIdentity = func() (bingo.CompilerBuildIdentity, error) {
		return bingo.CompilerBuildIdentity{
			UpstreamCommit: strings.Repeat("1", 40),
			ForkCommit:     strings.Repeat("2", 40),
			LoweringSchema: "bingo-hir-lowering-v2",
			LoweringHash:   strings.Repeat("4", 64),
		}, nil
	}
	t.Cleanup(func() { loadCompilerBuildIdentity = originalIdentityLoader })

	path := filepath.Join(t.TempDir(), "raw.json")
	if err := os.WriteFile(path, []byte(`{"schemaVersion":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{path}, &stdout, &stderr); code != exitFailure {
		t.Fatalf("exit = %d, stdout = %s, stderr = %s", code, &stdout, &stderr)
	}
	if strings.Contains(stderr.String(), "compiler identity") || !strings.Contains(stderr.String(), "invalid frontend snapshot hash") {
		t.Fatalf("raw snapshot rejection was masked: %s", &stderr)
	}
}
