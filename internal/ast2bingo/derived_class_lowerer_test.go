package ast2bingo

import (
	"bytes"
	"os"
	"testing"

	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
)

func loadVERT013bSnapshot(t testing.TB) ProgramSnapshot {
	t.Helper()
	data, err := os.ReadFile("../../testdata/ts2bin/derivedcounter/frontend-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	return frontend.Program
}

func TestLowerVERT013bClassContractIsDeterministic(t *testing.T) {
	snapshot := loadVERT013bSnapshot(t)
	first, err := LowerVERT013bClassContract(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LowerVERT013bClassContract(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	left, _, err := bingo.CanonicalVERT013bClassContract(first)
	if err != nil {
		t.Fatal(err)
	}
	right, _, err := bingo.CanonicalVERT013bClassContract(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(left, right) {
		t.Fatal("VERT-013b class contract is not deterministic")
	}
	if first.Classes[1].Super.Callee != first.Classes[0].Constructor.SymbolKey {
		t.Fatal("derived super callee is not base constructor")
	}
}

func TestLowerVERT013bRejectsRehashedSemanticTampering(t *testing.T) {
	base := loadVERT013bSnapshot(t)
	for name, mutate := range map[string]func(*ProgramSnapshot) bool{
		"base relation": func(s *ProgramSnapshot) bool {
			for i := range s.Types {
				if len(s.Types[i].BaseTypes) == 1 {
					s.Types[i].BaseTypes = nil
					return true
				}
			}
			return false
		},
		"super target": func(s *ProgramSnapshot) bool {
			for i := range s.Nodes {
				if s.Nodes[i].Kind == "KindCallExpression" && s.Nodes[i].Span.Start == 274 {
					s.Nodes[i].SelectedSignature = 3
					return true
				}
			}
			return false
		},
		"derived method effect": func(s *ProgramSnapshot) bool {
			for i := range s.Signatures {
				if s.Signatures[i].ID == 4 {
					s.Signatures[i].Effects = []string{"read"}
					return true
				}
			}
			return false
		},
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := base
			snapshot.Nodes = append([]NodeSnapshot(nil), base.Nodes...)
			snapshot.Types = append([]TypeSnapshot(nil), base.Types...)
			for i := range snapshot.Types {
				snapshot.Types[i].BaseTypes = append([]TypeID(nil), base.Types[i].BaseTypes...)
			}
			snapshot.Signatures = append([]SignatureSnapshot(nil), base.Signatures...)
			for i := range snapshot.Signatures {
				snapshot.Signatures[i].Effects = append([]string(nil), base.Signatures[i].Effects...)
			}
			if !mutate(&snapshot) {
				t.Fatal("fixture did not expose tamper target")
			}
			if err := finalizeTestSnapshot(&snapshot); err != nil {
				t.Fatalf("rehash tampered snapshot: %v", err)
			}
			if _, err := LowerVERT013bClassContract(snapshot); err == nil {
				t.Fatal("tampered derived snapshot accepted")
			}
		})
	}
}
