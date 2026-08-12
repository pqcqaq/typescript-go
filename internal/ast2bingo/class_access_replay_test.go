package ast2bingo

import (
	"bytes"
	"testing"

	"github.com/microsoft/typescript-go/internal/bingo"
)

func TestReplayClassAccessSnapshotIsDeterministic(t *testing.T) {
	snapshot := loadClassAccessSnapshot(t)
	identity := testCompilerIdentity(t, snapshot)
	first, err := ReplayClassAccessSnapshot(snapshot, identity)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ReplayClassAccessSnapshot(snapshot, identity)
	if err != nil {
		t.Fatal(err)
	}
	left, err := first.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	right, err := second.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(left, right) || first.SchemaVersion != ClassAccessReplaySchemaVersion || first.HIR.SchemaVersion != bingo.ClassAccessHIRSchemaVersion || first.HIR.ClassAccess == nil || first.HIR.ClassAccess.ContentHash != first.Contract.ContentHash || first.Execution.ClassAccessHash != first.Contract.ContentHash || bingo.VerifyCanonicalClassAccessExecution(first.Execution) != nil {
		t.Fatal("OBJ-003b class access replay is not deterministically HIR-bound")
	}
}

func TestClassAccessReplayRejectsTamperedExecutionBinding(t *testing.T) {
	snapshot := loadClassAccessSnapshot(t)
	result, err := ReplayClassAccessSnapshot(snapshot, testCompilerIdentity(t, snapshot))
	if err != nil {
		t.Fatal(err)
	}
	result.Execution.Functions[0].FieldInitBits[1] = "0000000000000000"
	if _, err := result.CanonicalBytes(); err == nil {
		t.Fatal("replay accepted a tampered execution contract")
	}
}

func TestClassAccessReplayRejectsTamperedBinding(t *testing.T) {
	snapshot := loadClassAccessSnapshot(t)
	identity := testCompilerIdentity(t, snapshot)
	result, err := ReplayClassAccessSnapshot(snapshot, identity)
	if err != nil {
		t.Fatal(err)
	}
	result.HIR.ClassAccessProofs[0].Request.PrivateIdentity = "forged"
	if _, err := result.CanonicalBytes(); err == nil {
		t.Fatal("replay accepted a tampered access HIR binding")
	}
}
