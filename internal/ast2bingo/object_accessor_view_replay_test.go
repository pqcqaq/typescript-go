package ast2bingo

import (
	"bytes"
	"os"
	"testing"

	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
)

func loadObjectAccessorViewSnapshot(t testing.TB) ProgramSnapshot {
	t.Helper()
	data, err := os.ReadFile("../../testdata/ts2bin/objectaccessorview/frontend-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	return frontend.Program
}

func TestReplayObjectAccessorViewSnapshotBindsGetterReceiver(t *testing.T) {
	snapshot := loadObjectAccessorViewSnapshot(t)
	identity := testCompilerIdentity(t, snapshot)
	first, err := ReplayObjectAccessorViewSnapshot(snapshot, identity)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ReplayObjectAccessorViewSnapshot(snapshot, identity)
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
		t.Fatal("accessor-view replay is not deterministic")
	}
	decoded, err := DecodeObjectAccessorViewReplay(left)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.MIR.Reads) != 1 {
		t.Fatalf("unexpected accessor reads: %#v", decoded.MIR.Reads)
	}
	read := decoded.MIR.Reads[0]
	if read.Kind != bingo.ObjectPropertyAccessor || read.ReceiverValueID != decoded.MIR.Binding.ResultValueID || read.GetterSymbolKey == "" || read.GetterSignature != bingo.VERT011GetterSignature || !decoded.View.PreservesIdentity || decoded.View.ExposesWrites {
		t.Fatalf("accessor-view receiver proof is incomplete: %#v", read)
	}
}

func TestReplayObjectAccessorViewRejectsRehashedTypeProofTamper(t *testing.T) {
	base := loadObjectAccessorViewSnapshot(t)
	for name, mutate := range map[string]func(*ProgramSnapshot) bool{
		"contextual target": func(snapshot *ProgramSnapshot) bool {
			for i := range snapshot.Nodes {
				if snapshot.Nodes[i].SyntaxPayload.Text == "object" && snapshot.Nodes[i].ContextualType != 0 {
					snapshot.Nodes[i].ContextualType = snapshot.Nodes[i].NarrowedType
					return true
				}
			}
			return false
		},
		"getter-only target": func(snapshot *ProgramSnapshot) bool {
			for i := range snapshot.Types {
				if snapshot.Types[i].DebugText == "ReadonlyResult" {
					snapshot.Types[i].PropertyFacts[0].WriteType = snapshot.Types[i].PropertyFacts[0].ReadType
					return true
				}
			}
			return false
		},
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := cloneObjectViewTestSnapshot(base)
			if !mutate(&snapshot) {
				t.Fatal("fixture did not expose tamper target")
			}
			if err := finalizeTestSnapshot(&snapshot); err != nil {
				t.Fatal(err)
			}
			if _, err := ReplayObjectAccessorViewSnapshot(snapshot, testCompilerIdentity(t, snapshot)); err == nil {
				t.Fatal("accepted rehashed accessor-view tamper")
			}
		})
	}
}

func FuzzDecodeObjectAccessorViewReplay(f *testing.F) {
	snapshot := loadObjectAccessorViewSnapshot(f)
	result, err := ReplayObjectAccessorViewSnapshot(snapshot, testCompilerIdentity(f, snapshot))
	if err != nil {
		f.Fatal(err)
	}
	encoded, err := result.CanonicalBytes()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxObjectAccessorViewReplayBytes+1 {
			return
		}
		result, err := DecodeObjectAccessorViewReplay(data)
		if err != nil {
			return
		}
		canonical, err := result.CanonicalBytes()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeObjectAccessorViewReplay(canonical); err != nil {
			t.Fatal(err)
		}
	})
}
