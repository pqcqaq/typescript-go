package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast2bingo"
	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
)

func propertyAccessUnboundReportFixture(t testing.TB) propertyAccessUnboundReport {
	t.Helper()
	report := propertyAccessUnboundReport{SchemaVersion: propertyAccessUnboundReportSchemaVersion, Stage: "property-access-unbound", FrontendSnapshotHash: strings.Repeat("1", 64), BuildPlanHash: strings.Repeat("2", 64), ReplayHash: strings.Repeat("3", 64), HIRHash: strings.Repeat("4", 64), MIRHash: strings.Repeat("5", 64), TargetTriple: "x86_64-unknown-linux-gnu", DataLayoutHash: strings.Repeat("6", 64)}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encoded)
	report.ContentHash = hex.EncodeToString(digest[:])
	return report
}

func TestPropertyAccessUnboundReportIsStrict(t *testing.T) {
	report := propertyAccessUnboundReportFixture(t)
	encoded, err := report.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodePropertyAccessUnboundReport(encoded); err != nil {
		t.Fatal(err)
	}
	unknown := bytes.Replace(encoded, []byte(`"contentHash":`), []byte(`"boundMirHash":"`+strings.Repeat("7", 64)+`","contentHash":`), 1)
	if _, err := decodePropertyAccessUnboundReport(unknown); err == nil {
		t.Fatal("property-access unbound report accepted a bound artifact field")
	}
	stale := report
	stale.MIRHash = strings.Repeat("8", 64)
	staleBytes, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodePropertyAccessUnboundReport(staleBytes); err == nil {
		t.Fatal("property-access unbound report accepted a stale content hash")
	}
}

func TestPublishPropertyAccessUnboundOutputsIsNoClobberAndClosed(t *testing.T) {
	artifacts := map[string][]byte{}
	for _, name := range propertyAccessUnboundArtifactNames {
		artifacts[name] = []byte(name)
	}
	directory := filepath.Join(t.TempDir(), "unbound")
	if err := publishPropertyAccessUnboundOutputs(directory, artifacts); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(propertyAccessUnboundArtifactNames) {
		t.Fatalf("published entries = %v", entries)
	}
	for _, forbidden := range []string{"bound-mir-v1.json", "backend-plan-v1.json", "module.ll", "module.o"} {
		if _, err := os.Stat(filepath.Join(directory, forbidden)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unbound publisher emitted forbidden artifact %s", forbidden)
		}
	}
	if err := publishPropertyAccessUnboundOutputs(directory, artifacts); err == nil {
		t.Fatal("property-access unbound publisher accepted an existing directory")
	}
	missing := mapsCloneBytes(artifacts)
	delete(missing, "unbound-mir-v1.json")
	if err := publishPropertyAccessUnboundOutputs(filepath.Join(t.TempDir(), "missing"), missing); err == nil {
		t.Fatal("property-access unbound publisher accepted an incomplete set")
	}
}

func mapsCloneBytes(input map[string][]byte) map[string][]byte {
	output := make(map[string][]byte, len(input))
	for key, value := range input {
		output[key] = bytes.Clone(value)
	}
	return output
}

func TestEmitPropertyAccessReplayDeterministicAndFailClosed(t *testing.T) {
	snapshotPath := filepath.Join("..", "..", "testdata", "ts2bin", "propertyaccessadmission", "frontend-snapshot.json")
	data, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := ast2bingo.NewCompilerBuildIdentity(frontend.Program.Provenance.TypeScriptGoCommit, strings.Repeat("e", 40))
	if err != nil {
		t.Fatal(err)
	}
	previous := loadCompilerBuildIdentity
	loadCompilerBuildIdentity = func() (bingo.CompilerBuildIdentity, error) { return identity, nil }
	t.Cleanup(func() { loadCompilerBuildIdentity = previous })

	var stdout, stderr bytes.Buffer
	firstPath := filepath.Join(t.TempDir(), "property-access-replay.json")
	if code := runWithEnvironment(context.Background(), []string{"emit-property-access-replay", "-o", firstPath, snapshotPath}, &stdout, &stderr, defaultCommandEnvironment()); code != exitSuccess {
		t.Fatalf("first exit=%d stderr=%s", code, stderr.String())
	}
	first, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ast2bingo.DecodePropertyAccessAdmissionReplay(first)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.CompilerBuildIdentity != identity || !strings.Contains(stdout.String(), decoded.ContentHash) {
		t.Fatal("CLI output does not bind replay identity")
	}

	secondPath := filepath.Join(t.TempDir(), "property-access-replay.json")
	stdout.Reset()
	stderr.Reset()
	if code := runWithEnvironment(context.Background(), []string{"emit-property-access-replay", "--output", secondPath, snapshotPath}, &stdout, &stderr, defaultCommandEnvironment()); code != exitSuccess {
		t.Fatalf("second exit=%d stderr=%s", code, stderr.String())
	}
	second, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("property access replay output is nondeterministic")
	}

	stdout.Reset()
	stderr.Reset()
	if code := runWithEnvironment(context.Background(), []string{"emit-property-access-replay", "-o", firstPath, snapshotPath}, &stdout, &stderr, defaultCommandEnvironment()); code != exitUsage {
		t.Fatalf("clobber exit=%d stderr=%s", code, stderr.String())
	}
	if got, err := os.ReadFile(firstPath); err != nil || !bytes.Equal(got, first) {
		t.Fatal("existing replay was changed")
	}

	loadCompilerBuildIdentity = func() (bingo.CompilerBuildIdentity, error) {
		return bingo.CompilerBuildIdentity{}, errors.New("missing")
	}
	missing := filepath.Join(t.TempDir(), "missing.json")
	stdout.Reset()
	stderr.Reset()
	if code := runWithEnvironment(context.Background(), []string{"emit-property-access-replay", "-o", missing, snapshotPath}, &stdout, &stderr, defaultCommandEnvironment()); code != exitUsage || !strings.Contains(stderr.String(), "compiler identity") {
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
	if code := runWithEnvironment(context.Background(), []string{"emit-property-access-replay", "-o", rejected, wrong}, &stdout, &stderr, defaultCommandEnvironment()); code != exitDiagnostics {
		t.Fatalf("wrong profile exit=%d stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(rejected); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("wrong profile published output")
	}
}
