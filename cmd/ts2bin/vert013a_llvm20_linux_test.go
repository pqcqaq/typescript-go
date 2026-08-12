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

func TestEmitVERT013aCommandPublishesVerifiedArtifactSet(t *testing.T) {
	caseDirectory := filepath.Join("..", "..", "testdata", "ts2bin", "classcounter")
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
	outputDirectory := filepath.Join(t.TempDir(), "vert013a-artifacts")
	arguments := []string{"emit-vert013a", "--runtime-manifest", runtimeManifest, "--output-dir", outputDirectory, snapshotPath}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != exitSuccess {
		t.Fatalf("emit-vert013a exit = %d, stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	read := func(name string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(outputDirectory, name))
		if err != nil {
			t.Fatal(err)
		}
		return bytes.TrimSpace(data)
	}
	if _, err := bingo.DecodeClassContract(read("class-contract-v1.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := bingo.DecodeVERT013aClassHIR(read("hir-v12.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := bingo.DecodeObjectLayoutContract(read("instance-layout-v1.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := bingo.DecodeVERT013aMIR(read("mir-v10.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := bingo.DecodeVERT013aBoundMIR(read("bound-mir-v1.json")); err != nil {
		t.Fatal(err)
	}
	object := read("module.o")
	if len(object) < 4 || object[0] != 0x7f || string(object[1:4]) != "ELF" {
		t.Fatal("CLI object artifact is not ELF")
	}
	if _, err := decodeVERT013aArtifactReport(read("report.json")); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(context.Background(), arguments, &stdout, &stderr); code != exitUsage || !strings.Contains(stderr.String(), "already exists") {
		t.Fatalf("second emit exit = %d, stderr=%s", code, stderr.String())
	}
}
