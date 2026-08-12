package ast2bingo

import (
	"bytes"
	"os"
	"testing"

	"github.com/microsoft/typescript-go/internal/frontendwire"
)

func loadVERT011Snapshot(t testing.TB) ProgramSnapshot {
	t.Helper()
	data, err := os.ReadFile("../../testdata/ts2bin/propertynullishassign/frontend-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	return frontend.Program
}

func TestReplayVERT011SnapshotIsDeterministic(t *testing.T) {
	snapshot := loadVERT011Snapshot(t)
	identity := testCompilerIdentity(t, snapshot)
	first, err := ReplayVERT011Snapshot(snapshot, identity)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ReplayVERT011Snapshot(snapshot, identity)
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
		t.Fatal("VERT-011 replay is not deterministic")
	}
	decoded, err := ReplayVERT011FrontendSnapshot(mustFrontendSnapshotBytes(t, snapshot), identity)
	if err != nil {
		t.Fatal(err)
	}
	decodedBytes, err := decoded.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, decodedBytes) {
		t.Fatal("VERT-011 strict frontend decode changed replay")
	}
}

func TestReplayVERT011RejectsRehashedSourceTampering(t *testing.T) {
	base := loadVERT011Snapshot(t)
	identity := testCompilerIdentity(t, base)
	tests := []struct {
		name string
		edit func(*ProgramSnapshot) bool
	}{
		{"dynamic key", func(snapshot *ProgramSnapshot) bool {
			return editChildText(snapshot, snapshotKindElementAccessExpression, "child[1]", "dynamicKey")
		}},
		{"getter symbol substitution", func(snapshot *ProgramSnapshot) bool {
			for i := range snapshot.Symbols {
				if snapshot.Symbols[i].Name == "result" && len(snapshot.Symbols[i].Declarations) == 2 {
					snapshot.Symbols[i].Name = "other"
					return true
				}
			}
			return false
		}},
		{"backing type mismatch", func(snapshot *ProgramSnapshot) bool {
			changed := false
			for i := range snapshot.Types {
				if len(snapshot.Types[i].PropertyFacts) == 2 {
					snapshot.Types[i].PropertyFacts[0].WriteType = 1
					changed = true
				}
			}
			return changed
		}},
		{"result type mismatch", func(snapshot *ProgramSnapshot) bool {
			changed := false
			for i := range snapshot.Types {
				if len(snapshot.Types[i].PropertyFacts) == 2 {
					snapshot.Types[i].PropertyFacts[1].ReadType = 1
					changed = true
				}
			}
			return changed
		}},
		{"getter body substitution", func(snapshot *ProgramSnapshot) bool {
			for i := range snapshot.Nodes {
				if snapshot.Nodes[i].Kind == snapshotKindPropertyAccessExpression && vert011BackingAccess(snapshot.Nodes[i], indexNodes(*snapshot)) {
					return editChildTextByNode(snapshot, &snapshot.Nodes[i], "child[1]", "result")
				}
			}
			return false
		}},
		{"setter body substitution", func(snapshot *ProgramSnapshot) bool {
			for i := range snapshot.Nodes {
				if snapshot.Nodes[i].Kind == snapshotKindBinaryExpression && snapshot.Nodes[i].SyntaxPayload.Operator == snapshotKindEqualsToken {
					return editChildTextByNode(snapshot, &snapshot.Nodes[i], "right", "value")
				}
			}
			return false
		}},
		{"copied receiver", func(snapshot *ProgramSnapshot) bool {
			return editChildText(snapshot, snapshotKindElementAccessExpression, "child[0]", "key")
		}},
		{"RHS changed", func(snapshot *ProgramSnapshot) bool {
			return editFirstNode(snapshot, snapshotKindNumericLiteral, func(node *NodeSnapshot) {
				node.Constant.Number = 2
				node.Constant.Text = "2"
				node.SyntaxPayload.Text = "2"
			})
		}},
		{"missing accessor declaration", func(snapshot *ProgramSnapshot) bool {
			for i := range snapshot.Symbols {
				if snapshot.Symbols[i].Name == "result" && len(snapshot.Symbols[i].Declarations) == 2 {
					snapshot.Symbols[i].Declarations = snapshot.Symbols[i].Declarations[:1]
					return true
				}
			}
			return false
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			broken := cloneReplaySnapshot(t, &base)
			if !test.edit(&broken) {
				t.Fatal("mutation target not found")
			}
			if err := finalizeTestSnapshot(&broken); err != nil {
				t.Fatalf("tampered snapshot is not structurally valid: %v", err)
			}
			if _, err := ReplayVERT011Snapshot(broken, identity); err == nil {
				t.Fatal("tampered VERT-011 source was accepted")
			}
		})
	}
}

func editFirstNode(snapshot *ProgramSnapshot, kind string, edit func(*NodeSnapshot)) bool {
	for i := range snapshot.Nodes {
		if snapshot.Nodes[i].Kind == kind {
			edit(&snapshot.Nodes[i])
			return true
		}
	}
	return false
}

func editChildText(snapshot *ProgramSnapshot, kind, role, text string) bool {
	for i := range snapshot.Nodes {
		if snapshot.Nodes[i].Kind == kind {
			return editChildTextByNode(snapshot, &snapshot.Nodes[i], role, text)
		}
	}
	return false
}

func editChildTextByNode(snapshot *ProgramSnapshot, node *NodeSnapshot, role, text string) bool {
	child := childByRole(*node, role)
	for i := range snapshot.Nodes {
		if snapshot.Nodes[i].ID == child {
			snapshot.Nodes[i].SyntaxPayload.Text = text
			return true
		}
	}
	return false
}

func indexNodes(snapshot ProgramSnapshot) map[NodeID]NodeSnapshot {
	nodes := make(map[NodeID]NodeSnapshot, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		nodes[node.ID] = node
	}
	return nodes
}

func mustFrontendSnapshotBytes(t testing.TB, snapshot ProgramSnapshot) []byte {
	t.Helper()
	frontend, err := frontendwire.NewFrontendSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := frontend.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
