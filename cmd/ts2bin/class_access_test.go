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

func TestClassAccessReportStrictDecodeAndTamperRejection(t *testing.T) {
	digest := strings.Repeat("a", 64)
	report := classAccessArtifactReport{SchemaVersion: 1, FrontendSnapshotHash: digest, BuildPlanHash: digest, TargetContextHash: digest, CapabilityCatalogHash: digest, AccessContractHash: digest, ExecutionHash: digest, ReplayHash: digest, HIRHash: digest, MIRHash: digest, BaseLayoutHash: digest, DerivedLayoutHash: digest, LayoutHash: digest, BoundMIRHash: digest, BackendPlanHash: digest, LLVMIRHash: digest, ObjectHash: digest}
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
	if _, err := decodeClassAccessArtifactReport(encoded); err != nil {
		t.Fatal(err)
	}
	unknown := bytes.Replace(encoded, []byte(`"schemaVersion":1`), []byte(`"schemaVersion":1,"unknown":true`), 1)
	if _, err := decodeClassAccessArtifactReport(unknown); err == nil {
		t.Fatal("classaccess report accepted unknown member")
	}
	tampered := bytes.Replace(encoded, []byte(digest), []byte(strings.Repeat("b", 64)), 1)
	if _, err := decodeClassAccessArtifactReport(tampered); err == nil {
		t.Fatal("classaccess report accepted stale hash")
	}
}

func TestPublishClassAccessOutputsIsNoClobber(t *testing.T) {
	artifacts := map[string][]byte{}
	for _, name := range classAccessArtifactNames {
		artifacts[name] = []byte(name)
	}
	directory := filepath.Join(t.TempDir(), "outputs")
	if err := publishClassAccessOutputs(directory, artifacts); err != nil {
		t.Fatal(err)
	}
	if err := publishClassAccessOutputs(directory, artifacts); err == nil {
		t.Fatal("classaccess publisher replaced existing artifacts")
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
