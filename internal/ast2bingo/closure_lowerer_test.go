package ast2bingo

import (
	"bytes"
	"os"
	"testing"

	"github.com/microsoft/typescript-go/internal/frontendwire"
)

func loadVERT012Snapshot(t testing.TB) ProgramSnapshot {
	t.Helper()
	data, err := os.ReadFile("../../testdata/ts2bin/closurecounter/frontend-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	return frontend.Program
}

func TestReplayVERT012SnapshotIsDeterministic(t *testing.T) {
	snapshot := loadVERT012Snapshot(t)
	identity := testCompilerIdentity(t, snapshot)
	first, err := ReplayVERT012Snapshot(snapshot, identity)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ReplayVERT012FrontendSnapshot(mustFrontendSnapshotBytes(t, snapshot), identity)
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
		t.Fatal("VERT-012 replay is not deterministic")
	}
}

func TestReplayVERT012RejectsCaptureTampering(t *testing.T) {
	base := loadVERT012Snapshot(t)
	identity := testCompilerIdentity(t, base)
	for name, mutate := range map[string]func(*ProgramSnapshot) bool{
		"readonly": func(s *ProgramSnapshot) bool {
			for i := range s.Nodes {
				if s.Nodes[i].Kind == "KindArrowFunction" {
					s.Nodes[i].CaptureBindings = append([]frontendwire.CaptureBindingSnapshot(nil), s.Nodes[i].CaptureBindings...)
					s.Nodes[i].CaptureBindings[0].Access = "read"
					return true
				}
			}
			return false
		},
		"immutable": func(s *ProgramSnapshot) bool {
			for i := range s.Nodes {
				if s.Nodes[i].Kind == "KindArrowFunction" {
					s.Nodes[i].CaptureBindings = append([]frontendwire.CaptureBindingSnapshot(nil), s.Nodes[i].CaptureBindings...)
					s.Nodes[i].CaptureBindings[0].Mutable = false
					return true
				}
			}
			return false
		},
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := base
			snapshot.Nodes = append([]NodeSnapshot(nil), base.Nodes...)
			if !mutate(&snapshot) {
				t.Fatal("fixture missing arrow")
			}
			if err := finalizeTestSnapshot(&snapshot); err != nil {
				t.Fatalf("rehash tampered snapshot: %v", err)
			}
			if _, err := ReplayVERT012Snapshot(snapshot, identity); err == nil {
				t.Fatal("tampered capture accepted")
			}
		})
	}
}
