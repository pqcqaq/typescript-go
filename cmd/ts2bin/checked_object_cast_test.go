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

func TestEmitCheckedCastReplayPublishesCanonicalDeterministicArtifact(t *testing.T) {
	snapshotPath := filepath.Join("..", "..", "testdata", "ts2bin", "checkedobjectcast", "frontend-snapshot.json")
	snapshotBytes, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(snapshotBytes)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := ast2bingo.NewCompilerBuildIdentity(frontend.Program.Provenance.TypeScriptGoCommit, strings.Repeat("a", 40))
	if err != nil {
		t.Fatal(err)
	}
	previous := loadCompilerBuildIdentity
	loadCompilerBuildIdentity = func() (bingo.CompilerBuildIdentity, error) { return identity, nil }
	t.Cleanup(func() { loadCompilerBuildIdentity = previous })

	var firstStdout, firstStderr bytes.Buffer
	firstPath := filepath.Join(t.TempDir(), "checked-cast-replay.json")
	if code := runWithEnvironment(context.Background(), []string{"emit-checked-cast-replay", "--output", firstPath, snapshotPath}, &firstStdout, &firstStderr, defaultCommandEnvironment()); code != exitSuccess {
		t.Fatalf("first emit exit = %d, stderr = %s", code, firstStderr.String())
	}
	first, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ast2bingo.DecodeCheckedObjectCastReplay(bytes.TrimSuffix(first, []byte{'\n'}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(firstStdout.String(), decoded.ContentHash) || decoded.CompilerBuildIdentity != identity {
		t.Fatalf("output does not bind replay identity: %s", firstStdout.String())
	}

	var secondStdout, secondStderr bytes.Buffer
	secondPath := filepath.Join(t.TempDir(), "checked-cast-replay.json")
	if code := runWithEnvironment(context.Background(), []string{"emit-checked-cast-replay", "-o", secondPath, snapshotPath}, &secondStdout, &secondStderr, defaultCommandEnvironment()); code != exitSuccess {
		t.Fatalf("second emit exit = %d, stderr = %s", code, secondStderr.String())
	}
	second, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("checked-cast replay CLI output is not deterministic")
	}
}

func TestEmitCheckedCastReplayFailsClosed(t *testing.T) {
	snapshotPath := filepath.Join("..", "..", "testdata", "ts2bin", "checkedobjectcast", "frontend-snapshot.json")
	snapshotBytes, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(snapshotBytes)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := ast2bingo.NewCompilerBuildIdentity(frontend.Program.Provenance.TypeScriptGoCommit, strings.Repeat("b", 40))
	if err != nil {
		t.Fatal(err)
	}
	previous := loadCompilerBuildIdentity
	t.Cleanup(func() { loadCompilerBuildIdentity = previous })

	output := filepath.Join(t.TempDir(), "existing.json")
	if err := os.WriteFile(output, []byte("owner"), 0o600); err != nil {
		t.Fatal(err)
	}
	loadCompilerBuildIdentity = func() (bingo.CompilerBuildIdentity, error) { return identity, nil }
	var stdout, stderr bytes.Buffer
	if code := runWithEnvironment(context.Background(), []string{"emit-checked-cast-replay", "-o", output, snapshotPath}, &stdout, &stderr, defaultCommandEnvironment()); code != exitUsage {
		t.Fatalf("existing output exit = %d, stderr = %s", code, stderr.String())
	}
	if got, err := os.ReadFile(output); err != nil || string(got) != "owner" {
		t.Fatalf("existing output was changed: %q, %v", got, err)
	}

	loadCompilerBuildIdentity = func() (bingo.CompilerBuildIdentity, error) {
		return bingo.CompilerBuildIdentity{}, errors.New("missing identity")
	}
	stdout.Reset()
	stderr.Reset()
	missing := filepath.Join(t.TempDir(), "missing.json")
	if code := runWithEnvironment(context.Background(), []string{"emit-checked-cast-replay", "-o", missing, snapshotPath}, &stdout, &stderr, defaultCommandEnvironment()); code != exitUsage || !strings.Contains(stderr.String(), "compiler identity") {
		t.Fatalf("missing identity exit = %d, stderr = %s", code, stderr.String())
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing identity published output: %v", err)
	}

	loadCompilerBuildIdentity = func() (bingo.CompilerBuildIdentity, error) { return identity, nil }
	stdout.Reset()
	stderr.Reset()
	staticSnapshot := filepath.Join("..", "..", "testdata", "ts2bin", "objectview", "frontend-snapshot.json")
	rejected := filepath.Join(t.TempDir(), "rejected.json")
	if code := runWithEnvironment(context.Background(), []string{"emit-checked-cast-replay", "-o", rejected, staticSnapshot}, &stdout, &stderr, defaultCommandEnvironment()); code != exitDiagnostics {
		t.Fatalf("static snapshot exit = %d, stderr = %s", code, stderr.String())
	}
	if _, err := os.Stat(rejected); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected snapshot published output: %v", err)
	}
}
