package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVERT012ReportStrictDecodeAndTamperRejection(t *testing.T) {
	digest := strings.Repeat("a", 64)
	report := vert012ArtifactReport{
		SchemaVersion: vert012ReportSchemaVersion, FrontendSnapshotHash: digest, BuildPlanHash: digest,
		TargetContextHash: digest, CapabilityCatalogHash: digest, ClosureContractHash: digest, HIRHash: digest,
		CellLayoutHash: digest, EnvironmentLayoutHash: digest,
		MIRHash: digest, BoundMIRHash: digest, LLVMIRHash: digest, ObjectHash: digest,
	}
	withoutHash, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(withoutHash)
	report.ContentHash = hex.EncodeToString(hash[:])
	encoded, err := report.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeVERT012ArtifactReport(encoded); err != nil {
		t.Fatal(err)
	}
	unknown := bytes.Replace(encoded, []byte(`"schemaVersion":1`), []byte(`"schemaVersion":1,"unknown":true`), 1)
	if _, err := decodeVERT012ArtifactReport(unknown); err == nil {
		t.Fatal("VERT-012 report accepted an unknown member")
	}
	tampered := bytes.Replace(encoded, []byte(digest), []byte(strings.Repeat("b", 64)), 1)
	if _, err := decodeVERT012ArtifactReport(tampered); err == nil {
		t.Fatal("VERT-012 report accepted stale content hash")
	}
}

func TestPublishVERT012OutputsIsNoClobber(t *testing.T) {
	artifacts := make(map[string][]byte)
	for _, name := range []string{"closure-contract-v1.json", "hir-v11.json", "cell-layout-v1.json", "environment-layout-v1.json", "mir-v9.json", "bound-mir-v1.json", "module.ll", "module.o", "report.json"} {
		artifacts[name] = []byte(name)
	}
	directory := filepath.Join(t.TempDir(), "outputs")
	if err := publishVERT012Outputs(directory, artifacts); err != nil {
		t.Fatal(err)
	}
	if err := publishVERT012Outputs(directory, artifacts); err == nil {
		t.Fatal("VERT-012 publisher replaced an existing artifact set")
	}
	for name, want := range artifacts {
		got, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("artifact %s was replaced", name)
		}
	}
}
