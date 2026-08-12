package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast2bingo"
	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
)

func TestEmitFunctionThunkReplayDeterministicAndFailClosed(t *testing.T) {
	snapshotPath := filepath.Join("..", "..", "testdata", "ts2bin", "functionthunk", "frontend-snapshot.json")
	data, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := ast2bingo.NewCompilerBuildIdentity(frontend.Program.Provenance.TypeScriptGoCommit, strings.Repeat("d", 40))
	if err != nil {
		t.Fatal(err)
	}
	previous := loadCompilerBuildIdentity
	loadCompilerBuildIdentity = func() (bingo.CompilerBuildIdentity, error) { return identity, nil }
	t.Cleanup(func() { loadCompilerBuildIdentity = previous })
	var stdout, stderr bytes.Buffer
	first := filepath.Join(t.TempDir(), "thunk.json")
	if code := runWithEnvironment(context.Background(), []string{"emit-function-thunk-replay", "-o", first, snapshotPath}, &stdout, &stderr, defaultCommandEnvironment()); code != exitSuccess {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	firstBytes, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ast2bingo.DecodeFunctionThunkReplay(bytes.TrimSuffix(firstBytes, []byte{'\n'}))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.CompilerBuildIdentity != identity || !strings.Contains(stdout.String(), decoded.ContentHash) {
		t.Fatal("CLI output does not bind identity/hash")
	}
	second := filepath.Join(t.TempDir(), "thunk.json")
	stdout.Reset()
	stderr.Reset()
	if code := runWithEnvironment(context.Background(), []string{"emit-function-thunk-replay", "--output", second, snapshotPath}, &stdout, &stderr, defaultCommandEnvironment()); code != exitSuccess {
		t.Fatalf("second exit=%d stderr=%s", code, stderr.String())
	}
	secondBytes, _ := os.ReadFile(second)
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("CLI output is nondeterministic")
	}
	stdout.Reset()
	stderr.Reset()
	if code := runWithEnvironment(context.Background(), []string{"emit-function-thunk-replay", "-o", first, snapshotPath}, &stdout, &stderr, defaultCommandEnvironment()); code != exitUsage {
		t.Fatalf("clobber exit=%d", code)
	}
	loadCompilerBuildIdentity = func() (bingo.CompilerBuildIdentity, error) {
		return bingo.CompilerBuildIdentity{}, errors.New("missing")
	}
	missing := filepath.Join(t.TempDir(), "missing.json")
	stdout.Reset()
	stderr.Reset()
	if code := runWithEnvironment(context.Background(), []string{"emit-function-thunk-replay", "-o", missing, snapshotPath}, &stdout, &stderr, defaultCommandEnvironment()); code != exitUsage || !strings.Contains(stderr.String(), "compiler identity") {
		t.Fatalf("missing exit=%d stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("missing identity published output")
	}
	loadCompilerBuildIdentity = func() (bingo.CompilerBuildIdentity, error) { return identity, nil }
	rejected := filepath.Join(t.TempDir(), "rejected.json")
	wrong := filepath.Join("..", "..", "testdata", "ts2bin", "objectview", "frontend-snapshot.json")
	stdout.Reset()
	stderr.Reset()
	if code := runWithEnvironment(context.Background(), []string{"emit-function-thunk-replay", "-o", rejected, wrong}, &stdout, &stderr, defaultCommandEnvironment()); code != exitDiagnostics {
		t.Fatalf("wrong fixture exit=%d stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(rejected); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("wrong fixture published output")
	}
}
