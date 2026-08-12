package ast2bingo

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/frontendwire"
)

func loadFunctionThunkSnapshot(t testing.TB) ProgramSnapshot {
	t.Helper()
	data, err := os.ReadFile("../../testdata/ts2bin/functionthunk/frontend-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	return frontend.Program
}

func TestReplayFunctionThunkSnapshotIsDeterministicAndSelfContained(t *testing.T) {
	snapshot := loadFunctionThunkSnapshot(t)
	identity := testCompilerIdentity(t, snapshot)
	first, err := ReplayFunctionThunkSnapshot(snapshot, identity)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ReplayFunctionThunkSnapshot(snapshot, identity)
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
	if !bytes.Equal(left, right) || first.AssignmentNodeID == "" || first.SourceSignatureHash == first.TargetSignatureHash {
		t.Fatal("function thunk replay is not deterministic or completely bound")
	}
	if _, err := DecodeFunctionThunkReplay(left); err != nil {
		t.Fatal(err)
	}
	hir, err := LowerFunctionThunkHIR(first)
	if err != nil {
		t.Fatal(err)
	}
	if hir.FrontendSnapshotHash != first.FrontendSnapshotHash || hir.SourceSignatureHash != first.SourceSignatureHash || hir.TargetSignatureHash != first.TargetSignatureHash || hir.AssignmentNodeID != string(first.AssignmentNodeID) || hir.Thunk.ContentHash != first.Thunk.ContentHash {
		t.Fatal("function thunk replay/HIR provenance chain is incomplete")
	}
}

func TestReplayFunctionThunkSnapshotRejectsRehashedFactSubstitution(t *testing.T) {
	base := loadFunctionThunkSnapshot(t)
	for name, mutate := range map[string]func(*ProgramSnapshot) bool{
		"contextual target": func(snapshot *ProgramSnapshot) bool {
			for i := range snapshot.Nodes {
				if snapshot.Nodes[i].Kind == snapshotKindIdentifier && snapshot.Nodes[i].SyntaxPayload.Text == "source" && snapshot.Nodes[i].ContextualType != 0 {
					snapshot.Nodes[i].ContextualType = snapshot.Nodes[i].NarrowedType
					return true
				}
			}
			return false
		},
		"parameter type": func(snapshot *ProgramSnapshot) bool {
			indexes := indexPrimitiveSnapshot(*snapshot)
			for i := range snapshot.Signatures {
				if snapshot.Signatures[i].EffectProof.Kind == "body-resolved" && indexes.Types[snapshot.Signatures[i].ParameterFacts[0].Type].DebugText == "Animal" {
					snapshot.Signatures[i].ParameterFacts[0].Type = snapshot.Signatures[i].ReturnType
					return true
				}
			}
			return false
		},
		"return type": func(snapshot *ProgramSnapshot) bool {
			indexes := indexPrimitiveSnapshot(*snapshot)
			for i := range snapshot.Signatures {
				if snapshot.Signatures[i].EffectProof.Kind == "body-resolved" && indexes.Types[snapshot.Signatures[i].ReturnType].DebugText == "Dog" {
					snapshot.Signatures[i].ReturnType = snapshot.Signatures[i].ParameterFacts[0].Type
					return true
				}
			}
			return false
		},
		"effect proof": func(snapshot *ProgramSnapshot) bool {
			for i := range snapshot.Signatures {
				if snapshot.Signatures[i].EffectProof.Kind == "body-resolved" {
					snapshot.Signatures[i].EffectProof.Complete = false
					return true
				}
			}
			return false
		},
		"base relation": func(snapshot *ProgramSnapshot) bool {
			for i := range snapshot.Types {
				if snapshot.Types[i].DebugText == "Dog" {
					snapshot.Types[i].BaseTypes = nil
					snapshot.Types[i].TypePayload.BaseTypes = nil
					return true
				}
			}
			return false
		},
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := cloneFunctionThunkSnapshot(base)
			if !mutate(&snapshot) {
				t.Fatal("fixture did not expose tamper target")
			}
			if err := finalizeTestSnapshot(&snapshot); err != nil {
				t.Fatalf("rehash substitution: %v", err)
			}
			if _, err := ReplayFunctionThunkSnapshot(snapshot, testCompilerIdentity(t, snapshot)); err == nil {
				t.Fatal("function thunk replay accepted substituted fact")
			}
		})
	}
}

func TestLowerFunctionThunkHIRRejectsSubstitutedReplayProvenance(t *testing.T) {
	snapshot := loadFunctionThunkSnapshot(t)
	replay, err := ReplayFunctionThunkSnapshot(snapshot, testCompilerIdentity(t, snapshot))
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*FunctionThunkReplayResult){
		"frontend":         func(value *FunctionThunkReplayResult) { value.FrontendSnapshotHash = strings.Repeat("a", 64) },
		"source signature": func(value *FunctionThunkReplayResult) { value.SourceSignatureHash = strings.Repeat("b", 64) },
		"target signature": func(value *FunctionThunkReplayResult) { value.TargetSignatureHash = strings.Repeat("c", 64) },
		"assignment":       func(value *FunctionThunkReplayResult) { value.AssignmentNodeID = "node/other" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := replay
			mutate(&candidate)
			candidate.ContentHash = ""
			if _, err := LowerFunctionThunkHIR(candidate); err == nil {
				t.Fatal("function thunk HIR lowering accepted substituted replay provenance")
			}
		})
	}
}

func cloneFunctionThunkSnapshot(base ProgramSnapshot) ProgramSnapshot {
	result := base
	result.Nodes = append([]NodeSnapshot(nil), base.Nodes...)
	for i := range result.Nodes {
		result.Nodes[i].Children = append([]NodeID(nil), base.Nodes[i].Children...)
		result.Nodes[i].NamedChildren = append([]frontendwire.NamedChildSnapshot(nil), base.Nodes[i].NamedChildren...)
	}
	result.Types = append([]TypeSnapshot(nil), base.Types...)
	for i := range result.Types {
		result.Types[i].BaseTypes = append([]TypeID(nil), base.Types[i].BaseTypes...)
		result.Types[i].TypePayload.BaseTypes = append([]TypeID(nil), base.Types[i].TypePayload.BaseTypes...)
	}
	result.Signatures = append([]SignatureSnapshot(nil), base.Signatures...)
	for i := range result.Signatures {
		result.Signatures[i].ParameterFacts = append([]frontendwire.ParameterSnapshot(nil), base.Signatures[i].ParameterFacts...)
		result.Signatures[i].Effects = append([]string(nil), base.Signatures[i].Effects...)
	}
	return result
}

func FuzzDecodeFunctionThunkReplay(f *testing.F) {
	snapshot := loadFunctionThunkSnapshot(f)
	replay, err := ReplayFunctionThunkSnapshot(snapshot, testCompilerIdentity(f, snapshot))
	if err != nil {
		f.Fatal(err)
	}
	encoded, err := replay.CanonicalBytes()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Fuzz(func(t *testing.T, data []byte) { _, _ = DecodeFunctionThunkReplay(data) })
}
