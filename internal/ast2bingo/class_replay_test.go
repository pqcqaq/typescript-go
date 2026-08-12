package ast2bingo

import (
	"bytes"
	"testing"
)

func TestReplayVERT013aSnapshotIsDeterministic(t *testing.T) {
	snapshot := loadVERT013aSnapshot(t)
	identity := testCompilerIdentity(t, snapshot)
	first, err := ReplayVERT013aSnapshot(snapshot, identity)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ReplayVERT013aFrontendSnapshot(mustFrontendSnapshotBytes(t, snapshot), identity)
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, err := first.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := second.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("VERT-013a replay is not deterministic")
	}
}

func TestReplayVERT013aBindsContractAndHIR(t *testing.T) {
	snapshot := loadVERT013aSnapshot(t)
	result, err := ReplayVERT013aSnapshot(snapshot, testCompilerIdentity(t, snapshot))
	if err != nil {
		t.Fatal(err)
	}
	if result.HIR.Classes == nil || result.HIR.Classes.ContentHash != result.Contract.ContentHash || result.HIR.SchemaVersion != 12 {
		t.Fatal("VERT-013a replay did not bind class contract to HIR v12")
	}
}
