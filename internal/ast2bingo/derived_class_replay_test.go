package ast2bingo

import (
	"bytes"
	"testing"
)

func TestReplayVERT013bSnapshotIsDeterministic(t *testing.T) {
	snapshot := loadVERT013bSnapshot(t)
	identity := testCompilerIdentity(t, snapshot)
	first, err := ReplayVERT013bSnapshot(snapshot, identity)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ReplayVERT013bFrontendSnapshot(mustFrontendSnapshotBytes(t, snapshot), identity)
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
	if !bytes.Equal(left, right) {
		t.Fatal("VERT-013b replay is not deterministic")
	}
	if first.HIR.SchemaVersion != 13 || first.HIR.DerivedClasses == nil || first.HIR.DerivedClasses.ContentHash != first.Contract.ContentHash {
		t.Fatal("VERT-013b replay did not bind contract to HIR v13")
	}
}
