package ast2bingo

import (
	"bytes"
	"os"
	"testing"

	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
)

func loadVERT013aSnapshot(t testing.TB) ProgramSnapshot {
	t.Helper()
	data, err := os.ReadFile("../../testdata/ts2bin/classcounter/frontend-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := frontendwire.DecodeFrontendSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	return frontend.Program
}

func TestLowerVERT013aClassContractIsDeterministic(t *testing.T) {
	snapshot := loadVERT013aSnapshot(t)
	first, err := LowerVERT013aClassContract(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LowerVERT013aClassContract(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	firstBytes, _, err := bingo.CanonicalClassContract(first)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, _, err := bingo.CanonicalClassContract(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("VERT-013a class lowering is not deterministic")
	}
}

func TestLowerVERT013aRejectsRehashedSemanticTampering(t *testing.T) {
	base := loadVERT013aSnapshot(t)
	for name, mutate := range map[string]func(*ProgramSnapshot) bool{
		"readonly field": func(s *ProgramSnapshot) bool {
			for i := range s.Types {
				for j := range s.Types[i].PropertyFacts {
					fact := &s.Types[i].PropertyFacts[j]
					if symbol, ok := indexPrimitiveSnapshot(*s).Symbols[fact.Symbol]; ok && symbol.Name == "value" {
						fact.Readonly = true
						return true
					}
				}
			}
			return false
		},
		"method effect": func(s *ProgramSnapshot) bool {
			for i := range s.Signatures {
				declaration := indexPrimitiveSnapshot(*s).Nodes[s.Signatures[i].Declaration]
				if declaration.Kind == "KindMethodDeclaration" {
					s.Signatures[i].Effects = []string{"read"}
					return true
				}
			}
			return false
		},
		"constructor optional parameter": func(s *ProgramSnapshot) bool {
			for i := range s.Signatures {
				declaration := indexPrimitiveSnapshot(*s).Nodes[s.Signatures[i].Declaration]
				if declaration.Kind == "KindConstructor" {
					s.Signatures[i].MinArgumentCount = 0
					s.Signatures[i].ParameterFacts[0].Optional = true
					return true
				}
			}
			return false
		},
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := base
			snapshot.Types = append([]TypeSnapshot(nil), base.Types...)
			for i := range snapshot.Types {
				snapshot.Types[i].PropertyFacts = append([]frontendwire.PropertySnapshot(nil), base.Types[i].PropertyFacts...)
			}
			snapshot.Signatures = append([]SignatureSnapshot(nil), base.Signatures...)
			for i := range snapshot.Signatures {
				snapshot.Signatures[i].Effects = append([]string(nil), base.Signatures[i].Effects...)
				snapshot.Signatures[i].ParameterFacts = append([]frontendwire.ParameterSnapshot(nil), base.Signatures[i].ParameterFacts...)
			}
			if !mutate(&snapshot) {
				t.Fatal("fixture did not expose tamper target")
			}
			if err := finalizeTestSnapshot(&snapshot); err != nil {
				t.Fatalf("rehash tampered snapshot: %v", err)
			}
			if _, err := LowerVERT013aClassContract(snapshot); err == nil {
				t.Fatal("tampered class snapshot accepted")
			}
		})
	}
}
