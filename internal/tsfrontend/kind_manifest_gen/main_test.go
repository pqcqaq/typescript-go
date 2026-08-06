package main

import (
	"bytes"
	"os"
	"testing"
)

func TestGeneratedManifestMatchesCheckedIn(t *testing.T) {
	t.Parallel()
	generated, err := generateManifest()
	if err != nil {
		t.Fatal(err)
	}
	checkedIn, err := os.ReadFile("../kind_manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(generated, checkedIn) {
		t.Fatalf("generated Kind manifest differs from checked-in file")
	}
}

func TestReviewedKindInventoryRejectsUnreviewedKind(t *testing.T) {
	t.Parallel()
	names := currentKindNames()
	names = append(names, "KindFuture")
	if err := validateReviewedKindInventory(names); err == nil {
		t.Fatal("unreviewed Kind inventory was accepted")
	}
}
