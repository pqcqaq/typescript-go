package frontendwire

import (
	"strings"
	"testing"
)

func TestSnapshotKindShapeRegistryIsSortedAndValid(t *testing.T) {
	if err := validateSnapshotKindShapeRegistry(snapshotKindShapeRegistry); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotKindShapeRegistryRejectsRequiredForbiddenPayload(t *testing.T) {
	registry := []snapshotKindShapeDefinition{{
		Kind:            "KindSynthetic",
		RequiredPayload: snapshotPayloadText,
	}}
	if err := validateSnapshotKindShapeRegistry(registry); err == nil || !strings.Contains(err.Error(), "requires forbidden payload") {
		t.Fatalf("malformed registry validation error = %v", err)
	}
}
