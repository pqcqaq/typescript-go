//go:build llvm20 && cgo && linux

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast2bingo"
	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
)

func TestEmitPropertyAccessUnboundPublishesVerifiedBundle(t *testing.T) {
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
	first := filepath.Join(t.TempDir(), "unbound")
	var stdout, stderr bytes.Buffer
	if code := runWithEnvironment(context.Background(), []string{"emit-property-access-unbound", "--output-dir", first, snapshotPath}, &stdout, &stderr, defaultCommandEnvironment()); code != exitSuccess {
		t.Fatalf("first exit=%d stderr=%s", code, stderr.String())
	}
	for _, name := range propertyAccessUnboundArtifactNames {
		if _, err := os.Stat(filepath.Join(first, name)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := decodePropertyAccessUnboundReport(mustReadFile(t, filepath.Join(first, "report.json"))); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"bound-mir-v1.json", "backend-plan-v1.json", "module.ll", "module.o"} {
		if _, err := os.Stat(filepath.Join(first, forbidden)); !os.IsNotExist(err) {
			t.Fatalf("forbidden artifact %s exists", forbidden)
		}
	}
	if code := runWithEnvironment(context.Background(), []string{"emit-property-access-unbound", "--output-dir", first, snapshotPath}, &stdout, &stderr, defaultCommandEnvironment()); code != exitUsage {
		t.Fatalf("no-clobber exit=%d stderr=%s", code, stderr.String())
	}
}

func mustReadFile(t testing.TB, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
