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

func TestVERT013aReportStrictDecodeAndTamperRejection(t *testing.T) {
	digest := strings.Repeat("a", 64)
	report := vert013aArtifactReport{SchemaVersion: 1, FrontendSnapshotHash: digest, BuildPlanHash: digest, TargetContextHash: digest, CapabilityCatalogHash: digest, ClassContractHash: digest, HIRHash: digest, InstanceLayoutHash: digest, MIRHash: digest, BoundMIRHash: digest, LLVMIRHash: digest, ObjectHash: digest}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(encoded)
	report.ContentHash = hex.EncodeToString(hash[:])
	encoded, err = report.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeVERT013aArtifactReport(encoded); err != nil {
		t.Fatal(err)
	}
	unknown := bytes.Replace(encoded, []byte(`"schemaVersion":1`), []byte(`"schemaVersion":1,"unknown":true`), 1)
	if _, err := decodeVERT013aArtifactReport(unknown); err == nil {
		t.Fatal("VERT-013a report accepted an unknown member")
	}
	tampered := bytes.Replace(encoded, []byte(digest), []byte(strings.Repeat("b", 64)), 1)
	if _, err := decodeVERT013aArtifactReport(tampered); err == nil {
		t.Fatal("VERT-013a report accepted stale content hash")
	}
}

func TestPublishVERT013aOutputsIsNoClobber(t *testing.T) {
	artifacts := make(map[string][]byte)
	for _, name := range vert013aArtifactNames {
		artifacts[name] = []byte(name)
	}
	directory := filepath.Join(t.TempDir(), "outputs")
	if err := publishVERT013aOutputs(directory, artifacts); err != nil {
		t.Fatal(err)
	}
	if err := publishVERT013aOutputs(directory, artifacts); err == nil {
		t.Fatal("VERT-013a publisher replaced an existing artifact set")
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
