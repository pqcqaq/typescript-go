package ast2bingo

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
)

func propertyAccessAdmissionReplayFixture(t testing.TB) PropertyAccessAdmissionReplay {
	t.Helper()
	data, err := os.ReadFile("../../testdata/ts2bin/propertyaccessadmission/frontend-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := NewCompilerBuildIdentity(frontend.Program.Provenance.TypeScriptGoCommit, strings.Repeat("a", 40))
	if err != nil {
		t.Fatal(err)
	}
	replay, err := ReplayPropertyAccessAdmissionSnapshot(frontend.Program, identity)
	if err != nil {
		t.Fatal(err)
	}
	return replay
}

func TestPropertyAccessAdmissionReplayRebuildsRealKeyDomains(t *testing.T) {
	replay := propertyAccessAdmissionReplayFixture(t)
	if len(replay.Accesses) != 4 {
		t.Fatalf("accesses=%d", len(replay.Accesses))
	}
	want := map[string]bingo.PropertyAccessDecision{"direct": bingo.PropertyAccessPlaceRef, "literal": bingo.PropertyAccessPlaceRef, "finite": bingo.PropertyAccessFiniteDispatch, "dynamic": bingo.PropertyAccessDynamicBoundary}
	for _, access := range replay.Accesses {
		if access.Admission.Decision != want[access.FunctionName] || access.AccessNodeID == "" || access.ReceiverTypeHash == "" || access.KeyTypeHash == "" {
			t.Fatalf("unexpected evidence: %#v", access)
		}
	}
	encoded, err := replay.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePropertyAccessAdmissionReplay(append(encoded, '\n')); err != nil {
		t.Fatal(err)
	}
	hir, err := LowerPropertyAccessAdmissionHIR(replay)
	if err != nil {
		t.Fatal(err)
	}
	if hir.ReplayHash != replay.ContentHash || hir.FrontendSnapshotHash != replay.FrontendSnapshotHash || len(hir.Functions) != 4 {
		t.Fatalf("invalid replay HIR join: %#v", hir)
	}
}

func TestPropertyAccessAdmissionReplayRejectsSubstitution(t *testing.T) {
	base := propertyAccessAdmissionReplayFixture(t)
	for name, mutate := range map[string]func(*PropertyAccessAdmissionReplay){
		"frontend": func(v *PropertyAccessAdmissionReplay) { v.FrontendSnapshotHash = strings.Repeat("b", 64) },
		"function": func(v *PropertyAccessAdmissionReplay) { v.Accesses[0].FunctionName = "other" },
		"node":     func(v *PropertyAccessAdmissionReplay) { v.Accesses[0].AccessNodeID = "node_other" },
		"receiver": func(v *PropertyAccessAdmissionReplay) { v.Accesses[0].ReceiverTypeHash = strings.Repeat("c", 64) },
		"key":      func(v *PropertyAccessAdmissionReplay) { v.Accesses[0].KeyTypeHash = strings.Repeat("d", 64) },
		"decision": func(v *PropertyAccessAdmissionReplay) { v.Accesses[0].Admission.Decision = bingo.PropertyAccessReject },
		"order":    func(v *PropertyAccessAdmissionReplay) { v.Accesses[0], v.Accesses[1] = v.Accesses[1], v.Accesses[0] },
	} {
		t.Run(name, func(t *testing.T) {
			value := base
			value.Accesses = append([]PropertyAccessAdmissionEvidence(nil), base.Accesses...)
			mutate(&value)
			if _, err := value.CanonicalBytes(); err == nil {
				t.Fatal("accepted substitution")
			}
		})
	}
	encoded, err := base.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	unknown := bytes.Replace(encoded, []byte(`"schemaVersion":1`), []byte(`"unknown":true,"schemaVersion":1`), 1)
	if _, err := DecodePropertyAccessAdmissionReplay(unknown); err == nil {
		t.Fatal("accepted unknown member")
	}
	if _, err := DecodePropertyAccessAdmissionReplay(make([]byte, maxPropertyAccessAdmissionReplayBytes+1)); err == nil {
		t.Fatal("accepted oversized replay")
	}
}

func FuzzDecodePropertyAccessAdmissionReplay(f *testing.F) {
	replay := propertyAccessAdmissionReplayFixture(f)
	encoded, err := replay.CanonicalBytes()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Fuzz(func(t *testing.T, data []byte) { _, _ = DecodePropertyAccessAdmissionReplay(data) })
}
