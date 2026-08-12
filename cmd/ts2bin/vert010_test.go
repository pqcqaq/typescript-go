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

func TestVERT010ReportStrictDecodeAndTamperRejection(t *testing.T) {
	digest := strings.Repeat("a", 64)
	report := vert010ArtifactReport{
		SchemaVersion: vert010ReportSchemaVersion, FrontendSnapshotHash: digest, BuildPlanHash: digest, TargetContextHash: digest,
		CapabilityCatalogHash: digest, HIRHash: digest, LayoutHash: digest, MIRHash: digest, BoundMIRHash: digest, LLVMIRHash: digest, ObjectHash: digest,
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
	if _, err := decodeVERT010ArtifactReport(encoded); err != nil {
		t.Fatal(err)
	}
	unknown := bytes.Replace(encoded, []byte(`"schemaVersion":1`), []byte(`"schemaVersion":1,"unknown":true`), 1)
	if _, err := decodeVERT010ArtifactReport(unknown); err == nil {
		t.Fatal("VERT-010 report accepted an unknown member")
	}
	tampered := bytes.Replace(encoded, []byte(digest), []byte(strings.Repeat("b", 64)), 1)
	if _, err := decodeVERT010ArtifactReport(tampered); err == nil {
		t.Fatal("VERT-010 report accepted stale content hash")
	}
}

func TestPublishVERT010OutputsIsNoClobber(t *testing.T) {
	artifacts := make(map[string][]byte)
	for _, name := range []string{"hir-v9.json", "object-layout-v1.json", "mir-v7.json", "bound-mir-v1.json", "module.ll", "module.o", "report.json"} {
		artifacts[name] = []byte(name)
	}
	directory := filepath.Join(t.TempDir(), "outputs")
	if err := publishVERT010Outputs(directory, artifacts); err != nil {
		t.Fatal(err)
	}
	if err := publishVERT010Outputs(directory, artifacts); err == nil {
		t.Fatal("VERT-010 publisher replaced an existing artifact set")
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
