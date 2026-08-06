package tsfrontend

import (
	"bytes"
	"os"
	"slices"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
	tsdiagnostics "github.com/microsoft/typescript-go/internal/diagnostics"
)

func TestDiagnosticRegistryIsSortedUniqueAndClassified(t *testing.T) {
	t.Parallel()

	registry := DiagnosticRegistry()
	if len(registry) == 0 {
		t.Fatal("empty diagnostic registry")
	}
	if !slices.IsSortedFunc(registry, func(a, b DiagnosticDefinition) int {
		if a.Code < b.Code {
			return -1
		}
		if a.Code > b.Code {
			return 1
		}
		return 0
	}) {
		t.Fatal("diagnostic registry is not code sorted")
	}
	for i := 1; i < len(registry); i++ {
		if registry[i-1].Code == registry[i].Code {
			t.Fatalf("duplicate registry code %s", registry[i].Code)
		}
	}
	registry[0].Code = "changed"
	if fresh := DiagnosticRegistry(); fresh[0].Code == "changed" {
		t.Fatal("DiagnosticRegistry returned shared mutable storage")
	}

	for _, test := range []struct {
		code string
		want DiagnosticCategory
	}{
		{code: "TS2322", want: DiagnosticCategoryTS},
		{code: "BINGO1101_OTHER", want: DiagnosticCategoryBingo},
		{code: DiagnosticCodeUnsafeAssertionChain, want: DiagnosticCategoryBingoUnsafe},
		{code: "BINGO-UNSAFE", want: DiagnosticCategoryBingoUnsafe},
		{code: "LLVM9001", want: DiagnosticCategoryLLVM},
		{code: "UNKNOWN", want: ""},
	} {
		if got := ClassifyDiagnosticCode(test.code); got != test.want {
			t.Errorf("ClassifyDiagnosticCode(%q) = %q, want %q", test.code, got, test.want)
		}
	}
}

func TestSortAndDeduplicateDiagnosticsUsesStableContract(t *testing.T) {
	t.Parallel()

	bingo := NewDiagnostic(
		DiagnosticCodeUnsupportedSyntax,
		DiagnosticCategoryBingo,
		DiagnosticStageSubset,
		DiagnosticSeverityError,
		SourceSpan{File: `z\main.ts`, Start: 5, End: 8},
		"subset.unsupported_syntax",
		"z",
	)
	bingo.EntityID = "node:2"
	bingoDuplicate := bingo
	bingoDuplicate.Arguments = []string{"a"}
	unsafe := NewRegisteredDiagnostic(DiagnosticCodeUnsafeAssertionChain, SourceSpan{File: "a.ts", Start: 2, End: 3})
	unsafe.EntityID = "node:1"
	typeScriptSyntax := NewDiagnostic("TS1005", DiagnosticCategoryTS, DiagnosticStageSyntax, DiagnosticSeverityError, SourceSpan{File: "z.ts", Start: 20, End: 21}, "ts.expected")
	typeScriptDuplicate := typeScriptSyntax
	typeScriptDuplicate.Stage = DiagnosticStageSemantic
	typeScriptSemantic := NewDiagnostic("TS2322", DiagnosticCategoryTS, DiagnosticStageSemantic, DiagnosticSeverityError, SourceSpan{File: "a.ts", Start: 10, End: 11}, "ts.assign")
	llvm := NewDiagnostic("LLVM9001", DiagnosticCategoryLLVM, DiagnosticStageBackend, DiagnosticSeverityError, SourceSpan{}, "llvm.verify")
	input := []Diagnostic{llvm, bingo, typeScriptDuplicate, typeScriptSemantic, unsafe, bingoDuplicate, typeScriptSyntax}
	originalPath := input[1].PrimarySpan.File

	got := SortAndDeduplicateDiagnostics(input)
	wantCodes := []string{"TS1005", "TS2322", DiagnosticCodeUnsafeAssertionChain, DiagnosticCodeUnsupportedSyntax, "LLVM9001"}
	if len(got) != len(wantCodes) {
		t.Fatalf("diagnostic count = %d, want %d: %#v", len(got), len(wantCodes), got)
	}
	for i, want := range wantCodes {
		if got[i].Code != want {
			t.Errorf("diagnostic[%d].Code = %q, want %q", i, got[i].Code, want)
		}
	}
	if got[3].PrimarySpan.File != "z/main.ts" {
		t.Fatalf("canonical path = %q", got[3].PrimarySpan.File)
	}
	if len(got[3].Arguments) != 1 || got[3].Arguments[0] != "a" {
		t.Fatalf("dedup did not deterministically retain the sorted entry: %#v", got[3].Arguments)
	}
	if input[1].PrimarySpan.File != originalPath {
		t.Fatal("sort mutated input diagnostic")
	}
}

func TestConvertTSDiagnosticCopiesStableFields(t *testing.T) {
	t.Parallel()

	related := ast.NewCompilerDiagnostic(tsdiagnostics.Identifier_expected)
	input := ast.NewCompilerDiagnostic(tsdiagnostics.X_0_expected, ";")
	input.AddRelatedInfo(related)

	got := ConvertTSDiagnostic(input, DiagnosticStageSyntax)
	if got.Code != "TS1005" || got.Category != DiagnosticCategoryTS || got.Severity != DiagnosticSeverityError {
		t.Fatalf("unexpected converted diagnostic: %#v", got)
	}
	if got.Stage != DiagnosticStageSyntax || got.MessageKey != string(input.MessageKey()) {
		t.Fatalf("stage/key not copied: %#v", got)
	}
	if len(got.Arguments) != 1 || got.Arguments[0] != ";" {
		t.Fatalf("arguments = %#v", got.Arguments)
	}
	if len(got.RelatedSpans) != 1 || got.RelatedSpans[0].Code != "TS1003" {
		t.Fatalf("related spans = %#v", got.RelatedSpans)
	}
	input.MessageArgs()[0] = "changed"
	if got.Arguments[0] != ";" {
		t.Fatal("converted diagnostic retained input argument storage")
	}
}

func TestDiagnosticCategoryGolden(t *testing.T) {
	t.Parallel()

	diagnostics := []Diagnostic{
		NewDiagnostic("LLVM9001", DiagnosticCategoryLLVM, DiagnosticStageBackend, DiagnosticSeverityError, SourceSpan{}, "llvm.verify"),
		NewRegisteredDiagnostic(DiagnosticCodeUnsafeAssertionChain, SourceSpan{File: "src/assert.ts", Start: 7, End: 12}),
		NewRegisteredDiagnostic(DiagnosticCodeUnsupportedSyntax, SourceSpan{File: "src/using.ts", Start: 3, End: 8}),
		NewDiagnostic("TS2322", DiagnosticCategoryTS, DiagnosticStageSemantic, DiagnosticSeverityError, SourceSpan{File: "src/main.ts", Start: 1, End: 2}, "ts.assign"),
	}
	got, err := CanonicalDiagnosticsJSON(diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/diagnostics/categories.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	want = bytes.TrimSpace(want)
	if !bytes.Equal(got, want) {
		t.Fatalf("diagnostic golden mismatch:\ngot:  %s\nwant: %s", got, want)
	}
}

func TestDiagnosticsHaveErrors(t *testing.T) {
	t.Parallel()

	if DiagnosticsHaveErrors([]Diagnostic{{Severity: DiagnosticSeverityWarning}}) {
		t.Fatal("warning was treated as an error")
	}
	if !DiagnosticsHaveErrors([]Diagnostic{{Severity: DiagnosticSeverityNote}, {Severity: DiagnosticSeverityError}}) {
		t.Fatal("error was not detected")
	}
}
