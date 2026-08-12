package ast2bingo

import (
	"bytes"
	"os"
	"testing"

	"github.com/microsoft/typescript-go/internal/frontendwire"
	"github.com/microsoft/typescript-go/internal/llvmbackend"
)

func loadObjectViewSnapshot(t testing.TB) ProgramSnapshot {
	t.Helper()
	data, err := os.ReadFile("../../testdata/ts2bin/objectview/frontend-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	return frontend.Program
}

func TestReplayObjectViewSnapshotBindsRealIdentityConversion(t *testing.T) {
	snapshot := loadObjectViewSnapshot(t)
	identity := testCompilerIdentity(t, snapshot)
	first, err := ReplayObjectViewSnapshot(snapshot, identity)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ReplayObjectViewSnapshot(snapshot, identity)
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
		t.Fatal("ObjectView replay is not deterministic")
	}
	decoded, err := DecodeObjectViewReplay(left)
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.View.PreservesIdentity || decoded.View.ExposesWrites || decoded.MIR.Binding.Allocates || !decoded.MIR.Binding.PreservesIdentity || decoded.Gate.SourceValueID != 3 || decoded.MIR.Binding.SourceValueID != 3 {
		t.Fatalf("ObjectView identity binding is incomplete: %#v", decoded.MIR.Binding)
	}
	if len(decoded.View.Mappings) != 1 || decoded.View.Mappings[0].TargetPropertyKey != "value" || decoded.View.Source.Properties[0].WriteTypeKey == "" || decoded.View.Target.Properties[0].WriteTypeKey != "" {
		t.Fatalf("ObjectView source/target mapping is incomplete: %#v", decoded.View)
	}
	plan, err := llvmbackend.BuildObjectViewBackendPlan(decoded.MIR)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Allocates || len(plan.RuntimeCalls) != 0 || plan.SourceOffset != decoded.View.Mappings[0].SourceFieldOffset {
		t.Fatalf("ObjectView backend lost proof binding: %#v", plan)
	}
}

func TestObjectViewReplayStrictDecoderRejectsTamper(t *testing.T) {
	snapshot := loadObjectViewSnapshot(t)
	result, err := ReplayObjectViewSnapshot(snapshot, testCompilerIdentity(t, snapshot))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := result.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	unknown := bytes.Replace(encoded, []byte(`"schemaVersion":1`), []byte(`"schemaVersion":1,"unknown":true`), 1)
	if _, err := DecodeObjectViewReplay(unknown); err == nil {
		t.Fatal("ObjectView replay accepted unknown member")
	}
	result.MIR.Binding.Allocates = true
	if _, err := result.CanonicalBytes(); err == nil {
		t.Fatal("ObjectView replay accepted substituted MIR")
	}
	if _, err := DecodeObjectViewReplay(make([]byte, maxObjectViewReplayBytes+1)); err == nil {
		t.Fatal("ObjectView replay accepted oversized input")
	}
}

func TestReplayObjectViewSnapshotRejectsRehashedSourceTamper(t *testing.T) {
	base := loadObjectViewSnapshot(t)
	for name, mutate := range map[string]func(*ProgramSnapshot) bool{
		"target writable": func(snapshot *ProgramSnapshot) bool {
			for i := range snapshot.Types {
				if snapshot.Types[i].DebugText == "ReadonlyValue" {
					snapshot.Types[i].PropertyFacts[0].Readonly = false
					snapshot.Types[i].PropertyFacts[0].WriteType = snapshot.Types[i].PropertyFacts[0].ReadType
					return true
				}
			}
			return false
		},
		"assignment contextual type": func(snapshot *ProgramSnapshot) bool {
			for i := range snapshot.Nodes {
				if snapshot.Nodes[i].Kind == snapshotKindIdentifier && snapshot.Nodes[i].SyntaxPayload.Text == "object" && snapshot.Nodes[i].ContextualType != 0 {
					snapshot.Nodes[i].ContextualType = snapshot.Nodes[i].NarrowedType
					return true
				}
			}
			return false
		},
		"readonly receiver": func(snapshot *ProgramSnapshot) bool {
			indexes := indexPrimitiveSnapshot(*snapshot)
			for _, access := range snapshot.Nodes {
				if !vert010Access(access, "view", indexes.Nodes) {
					continue
				}
				receiverID := childByRole(access, "child[0]")
				for j := range snapshot.Nodes {
					if snapshot.Nodes[j].ID == receiverID {
						snapshot.Nodes[j].SyntaxPayload.Text = "object"
						return true
					}
				}
			}
			return false
		},
		"target property identity": func(snapshot *ProgramSnapshot) bool {
			var sourceProperty SymbolID
			for _, typ := range snapshot.Types {
				if typ.DebugText == "{ value: number; }" && len(typ.PropertyFacts) == 1 {
					sourceProperty = typ.PropertyFacts[0].Symbol
					break
				}
			}
			indexes := indexPrimitiveSnapshot(*snapshot)
			for _, node := range snapshot.Nodes {
				if node.Kind == snapshotKindReturnStatement {
					accessID := childByRole(node, "expression")
					for i := range snapshot.Nodes {
						if snapshot.Nodes[i].ID == accessID {
							snapshot.Nodes[i].Symbol = sourceProperty
							return sourceProperty != "" && indexes.Nodes[accessID].Symbol != sourceProperty
						}
					}
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
				t.Fatalf("rehash tampered snapshot: %v", err)
			}
			if _, err := ReplayObjectViewSnapshot(snapshot, testCompilerIdentity(t, snapshot)); err == nil {
				t.Fatal("ObjectView replay accepted rehashed source tamper")
			}
		})
	}
}

func cloneObjectViewTestSnapshot(base ProgramSnapshot) ProgramSnapshot {
	result := base
	result.Nodes = append([]NodeSnapshot(nil), base.Nodes...)
	for i := range result.Nodes {
		result.Nodes[i].Children = append([]NodeID(nil), base.Nodes[i].Children...)
		result.Nodes[i].NamedChildren = append([]frontendwire.NamedChildSnapshot(nil), base.Nodes[i].NamedChildren...)
	}
	result.Types = append([]TypeSnapshot(nil), base.Types...)
	for i := range result.Types {
		result.Types[i].PropertyFacts = append([]frontendwire.PropertySnapshot(nil), base.Types[i].PropertyFacts...)
	}
	return result
}

func FuzzDecodeObjectViewReplay(f *testing.F) {
	snapshot := loadObjectViewSnapshot(f)
	result, err := ReplayObjectViewSnapshot(snapshot, testCompilerIdentity(f, snapshot))
	if err != nil {
		f.Fatal(err)
	}
	encoded, err := result.CanonicalBytes()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxObjectViewReplayBytes+1 {
			return
		}
		result, err := DecodeObjectViewReplay(data)
		if err != nil {
			return
		}
		canonical, err := result.CanonicalBytes()
		if err != nil {
			t.Fatalf("accepted ObjectView replay is not canonical: %v", err)
		}
		if _, err := DecodeObjectViewReplay(canonical); err != nil {
			t.Fatalf("canonical ObjectView replay does not round trip: %v", err)
		}
	})
}
