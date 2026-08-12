package ast2bingo

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
)

func loadClassAccessSnapshot(t testing.TB) ProgramSnapshot {
	t.Helper()
	data, err := os.ReadFile("../../testdata/ts2bin/classaccess/frontend-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	return frontend.Program
}

func TestLowerClassAccessReplayIsDeterministic(t *testing.T) {
	snapshot := loadClassAccessSnapshot(t)
	first, err := LowerClassAccessReplay(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LowerClassAccessReplay(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	left, _, err := bingo.CanonicalClassAccessContract(first.Contract)
	if err != nil {
		t.Fatal(err)
	}
	right, _, err := bingo.CanonicalClassAccessContract(second.Contract)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(left, right) || len(first.Requests) != 4 || len(first.Decisions) != 4 {
		t.Fatal("OBJ-003b class access replay is not deterministic and complete")
	}
	for _, decision := range first.Decisions {
		if !decision.Allowed || decision.Reason != bingo.ClassAccessAllowed {
			t.Fatalf("unexpected access decision: %#v", decision)
		}
	}
}

func TestLowerClassAccessReplayRejectsRehashedSemanticTampering(t *testing.T) {
	base := loadClassAccessSnapshot(t)
	mutations := map[string]func(*ProgramSnapshot) bool{
		"private identity": func(snapshot *ProgramSnapshot) bool {
			for typeIndex := range snapshot.Types {
				for factIndex := range snapshot.Types[typeIndex].PropertyFacts {
					fact := &snapshot.Types[typeIndex].PropertyFacts[factIndex]
					if fact.Visibility == "private" {
						fact.PrivateIdentity = "symbol_forged_private_identity"
						return true
					}
				}
			}
			return false
		},
		"private visibility": func(snapshot *ProgramSnapshot) bool {
			changed := false
			for typeIndex := range snapshot.Types {
				for factIndex := range snapshot.Types[typeIndex].PropertyFacts {
					fact := &snapshot.Types[typeIndex].PropertyFacts[factIndex]
					if fact.Visibility == "private" {
						fact.Visibility, fact.PrivateIdentity, changed = "protected", "", true
					}
				}
			}
			return changed
		},
		"protected receiver": func(snapshot *ProgramSnapshot) bool {
			for nodeIndex := range snapshot.Nodes {
				node := &snapshot.Nodes[nodeIndex]
				if node.Kind == "KindPropertyAccessExpression" && node.Span.Start == 233 {
					for receiverIndex := range snapshot.Nodes {
						if snapshot.Nodes[receiverIndex].ID == node.Children[0] {
							snapshot.Nodes[receiverIndex].NarrowedType = 3
							return true
						}
					}
				}
			}
			return false
		},
		"field initializer": func(snapshot *ProgramSnapshot) bool {
			for nodeIndex := range snapshot.Nodes {
				node := &snapshot.Nodes[nodeIndex]
				if node.Kind == "KindNumericLiteral" && node.SyntaxPayload.Text == "1" {
					node.SyntaxPayload.Text = "9"
					return true
				}
			}
			return false
		},
		"implicit constructor selection": func(snapshot *ProgramSnapshot) bool {
			for nodeIndex := range snapshot.Nodes {
				node := &snapshot.Nodes[nodeIndex]
				if node.Kind == "KindNewExpression" {
					node.SelectedSignature = 4
					return true
				}
			}
			return false
		},
		"derived heritage syntax": func(snapshot *ProgramSnapshot) bool {
			for nodeIndex := range snapshot.Nodes {
				node := &snapshot.Nodes[nodeIndex]
				if node.Kind == "KindHeritageClause" {
					node.Kind = "KindExpressionStatement"
					return true
				}
			}
			return false
		},
		"selected exported call": func(snapshot *ProgramSnapshot) bool {
			for nodeIndex := range snapshot.Nodes {
				node := &snapshot.Nodes[nodeIndex]
				if node.Kind == "KindCallExpression" && node.SelectedSignature == 5 {
					node.SelectedSignature = 1
					return true
				}
			}
			return false
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(base)
			if err != nil {
				t.Fatal(err)
			}
			var snapshot ProgramSnapshot
			if err := json.Unmarshal(encoded, &snapshot); err != nil {
				t.Fatal(err)
			}
			if !mutate(&snapshot) {
				t.Fatal("fixture did not expose tamper target")
			}
			if err := finalizeTestSnapshot(&snapshot); err != nil {
				t.Fatalf("rehash tampered snapshot: %v", err)
			}
			if _, err := LowerClassAccessReplay(snapshot); err == nil {
				t.Fatal("tampered class access snapshot accepted")
			}
		})
	}
}
