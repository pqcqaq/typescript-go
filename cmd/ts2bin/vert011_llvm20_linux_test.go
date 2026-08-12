//go:build llvm20 && cgo && linux

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast2bingo"
	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
)

func TestEmitVERT011CommandPublishesVerifiedArtifactSet(t *testing.T) {
	caseDirectory := filepath.Join("..", "..", "testdata", "ts2bin", "propertynullishassign")
	snapshotPath := filepath.Join(caseDirectory, "frontend-snapshot.json")
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
	previousIdentity := loadCompilerBuildIdentity
	loadCompilerBuildIdentity = func() (bingo.CompilerBuildIdentity, error) { return identity, nil }
	t.Cleanup(func() { loadCompilerBuildIdentity = previousIdentity })
	runtimeManifest := filepath.Join("..", "..", "internal", "targetcontext", "testdata", "runtime-manifest.json")
	outputDirectory := filepath.Join(t.TempDir(), "vert011-artifacts")
	arguments := []string{"emit-vert011", "--runtime-manifest", runtimeManifest, "--output-dir", outputDirectory, snapshotPath}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != exitSuccess {
		t.Fatalf("emit-vert011 exit = %d, stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	read := func(name string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(outputDirectory, name))
		if err != nil {
			t.Fatal(err)
		}
		return bytes.TrimSpace(data)
	}
	if _, err := bingo.DecodeVERT011PlaceHIR(read("hir-v10.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := bingo.DecodeObjectLayoutContract(read("object-layout-v1.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := bingo.DecodeVERT011MIR(read("mir-v8.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := bingo.DecodeVERT011BoundMIR(read("bound-mir-v1.json")); err != nil {
		t.Fatal(err)
	}
	object := read("module.o")
	if len(object) < 4 || object[0] != 0x7f || string(object[1:4]) != "ELF" {
		t.Fatal("CLI object artifact is not ELF")
	}
	report, err := decodeVERT011ArtifactReport(read("report.json"))
	if err != nil {
		t.Fatal(err)
	}
	claimed := report.ContentHash
	copy := *report
	copy.ContentHash = ""
	encoded, err := json.Marshal(copy)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encoded)
	if claimed != hex.EncodeToString(digest[:]) {
		t.Fatal("CLI VERT-011 report hash mismatch")
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(context.Background(), arguments, &stdout, &stderr); code != exitUsage || !strings.Contains(stderr.String(), "already exists") {
		t.Fatalf("second emit exit = %d, stderr=%s", code, stderr.String())
	}
}
