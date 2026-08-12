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
	"github.com/microsoft/typescript-go/internal/llvmbackend"
)

func TestEmitClassAccessCommandPublishesVerifiedArtifactSet(t *testing.T) {
	caseDirectory := filepath.Join("..", "..", "testdata", "ts2bin", "classaccess")
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
	previous := loadCompilerBuildIdentity
	loadCompilerBuildIdentity = func() (bingo.CompilerBuildIdentity, error) { return identity, nil }
	t.Cleanup(func() { loadCompilerBuildIdentity = previous })
	runtimeManifest := filepath.Join("..", "..", "internal", "targetcontext", "testdata", "runtime-manifest.json")
	outputDirectory := filepath.Join(t.TempDir(), "classaccess-artifacts")
	arguments := []string{"emit-classaccess", "--runtime-manifest", runtimeManifest, "--output-dir", outputDirectory, snapshotPath}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), arguments, &stdout, &stderr); code != exitSuccess {
		t.Fatalf("emit-classaccess exit=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	read := func(name string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(outputDirectory, name))
		if err != nil {
			t.Fatal(err)
		}
		return bytes.TrimSpace(data)
	}
	if _, err := bingo.DecodeClassAccessContract(read("access-contract-v1.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := bingo.DecodeClassAccessExecution(read("execution-v1.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := bingo.DecodeClassAccessHIR(read("hir-v15.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := bingo.DecodeClassAccessMIR(read("mir-v13.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := bingo.DecodeClassAccessLayout(read("layout-v1.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := bingo.DecodeClassAccessBoundMIR(read("bound-mir-v1.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := llvmbackend.DecodeClassAccessBackendPlan(read("backend-plan-v1.json")); err != nil {
		t.Fatal(err)
	}
	object := read("module.o")
	if len(object) < 4 || object[0] != 0x7f || string(object[1:4]) != "ELF" {
		t.Fatal("CLI object artifact is not ELF")
	}
	if _, err := decodeClassAccessArtifactReport(read("report.json")); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(context.Background(), arguments, &stdout, &stderr); code != exitUsage || !strings.Contains(stderr.String(), "already exists") {
		t.Fatalf("second emit exit=%d stderr=%s", code, stderr.String())
	}
}
