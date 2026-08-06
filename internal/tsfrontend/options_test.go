package tsfrontend

import (
	"bytes"
	"os"
	"sync"
	"testing"

	"github.com/microsoft/typescript-go/internal/collections"
	"github.com/microsoft/typescript-go/internal/core"
)

func TestNormalizeOptionsDefaultsAndEffectiveStrict(t *testing.T) {
	t.Parallel()

	options, diagnostics := NormalizeOptions(BingoOptions{}, nil)
	if len(diagnostics) != 0 {
		t.Fatalf("default options produced diagnostics: %#v", diagnostics)
	}
	if options.Bingo.Profile != ProfileStatic {
		t.Fatalf("profile = %q, want static", options.Bingo.Profile)
	}
	if options.Bingo.Runtime != "core-es2020" || options.Bingo.LLVMMajor != 20 {
		t.Fatalf("unexpected defaults: %#v", options.Bingo)
	}
	if options.Bingo.Exceptions != ExceptionsNone {
		t.Fatalf("exceptions = %q, want %q", options.Bingo.Exceptions, ExceptionsNone)
	}
	if options.CompilerOptions == nil {
		t.Fatal("normalized compiler options are nil")
	}
	for name, value := range map[string]core.Tristate{
		"strict":              options.CompilerOptions.Strict,
		"strictNullChecks":    options.CompilerOptions.StrictNullChecks,
		"strictFunctionTypes": options.CompilerOptions.StrictFunctionTypes,
		"noImplicitAny":       options.CompilerOptions.NoImplicitAny,
	} {
		if value != core.TSTrue {
			t.Errorf("%s = %v, want true", name, value)
		}
	}
}

func TestNormalizeOptionsRejectsUnavailableLLVMEH(t *testing.T) {
	t.Parallel()

	options, diagnostics := NormalizeOptions(BingoOptions{Exceptions: ExceptionsLLVMEH}, nil)
	if options.Bingo.Exceptions != ExceptionsLLVMEH {
		t.Fatalf("normalized exceptions = %q, want preserved %q", options.Bingo.Exceptions, ExceptionsLLVMEH)
	}
	if len(diagnostics) != 1 || diagnostics[0].Code != DiagnosticCodeInvalidOption || diagnostics[0].EntityID != "bingoOptions.exceptions" {
		t.Fatalf("diagnostics = %#v, want one unavailable exception option rejection", diagnostics)
	}
}

func TestNormalizeOptionsReportsUnavailableAndInvalidProfiles(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		want    string
		profile Profile
	}{
		{name: "dynamic", want: DiagnosticCodeDynamicProfileUnavailable, profile: ProfileDynamic},
		{name: "unknown", want: DiagnosticCodeInvalidProfile, profile: Profile("future")},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, diagnostics := NormalizeOptions(BingoOptions{Profile: test.profile}, nil)
			if len(diagnostics) != 1 || diagnostics[0].Code != test.want {
				t.Fatalf("diagnostics = %#v, want one %s", diagnostics, test.want)
			}
			if diagnostics[0].Profile != test.profile {
				t.Fatalf("diagnostic profile = %q, want %q", diagnostics[0].Profile, test.profile)
			}
			if test.profile == ProfileDynamic && diagnostics[0].RequiredCapability != "runtime.dynamic" {
				t.Fatalf("required capability = %q", diagnostics[0].RequiredCapability)
			}
		})
	}
}

func TestNormalizeOptionsReportsEveryDisabledStrictRequirement(t *testing.T) {
	t.Parallel()

	input := &core.CompilerOptions{
		Strict:              core.TSFalse,
		StrictNullChecks:    core.TSFalse,
		StrictFunctionTypes: core.TSFalse,
		NoImplicitAny:       core.TSFalse,
	}
	options, diagnostics := NormalizeOptions(BingoOptions{}, input)
	if len(diagnostics) != 4 {
		t.Fatalf("diagnostic count = %d, want 4: %#v", len(diagnostics), diagnostics)
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.Code != DiagnosticCodeStrictOptionRequired {
			t.Errorf("code = %q", diagnostic.Code)
		}
	}
	if options.CompilerOptions.Strict != core.TSFalse {
		t.Fatalf("invalid strict option was changed: %v", options.CompilerOptions.Strict)
	}
}

func TestNormalizeOptionsRejectsMalformedPhaseOneValues(t *testing.T) {
	t.Parallel()

	_, diagnostics := NormalizeOptions(BingoOptions{
		LLVMMajor:   19,
		GC:          GCMode("moving"),
		Exceptions:  ExceptionMode("setjmp"),
		Overflow:    OverflowMode("wrap-i32"),
		BoundsCheck: BoundsCheckMode("sometimes"),
		Emit:        []EmitArtifact{EmitHIR, "machine-code"},
	}, nil)
	if len(diagnostics) != 6 {
		t.Fatalf("diagnostic count = %d, want 6: %#v", len(diagnostics), diagnostics)
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.Code != DiagnosticCodeInvalidOption || diagnostic.Stage != DiagnosticStageConfiguration {
			t.Errorf("unexpected invalid-option diagnostic: %#v", diagnostic)
		}
	}
}

func TestCanonicalOptionsEquatesExpandedStrictAndSetOrder(t *testing.T) {
	t.Parallel()

	pathsA := collections.NewOrderedMapFromList([]collections.MapEntry[string, []string]{
		{Key: "z/*", Value: []string{"./z/*"}},
		{Key: "a/*", Value: []string{"./a/*"}},
	})
	pathsB := collections.NewOrderedMapFromList([]collections.MapEntry[string, []string]{
		{Key: "a/*", Value: []string{"./a/*"}},
		{Key: "z/*", Value: []string{"./z/*"}},
	})
	a, aDiagnostics := NormalizeOptions(BingoOptions{
		Features: []string{"+sse2", "+avx", "+sse2"},
		Emit:     []EmitArtifact{EmitObject, EmitHIR, EmitObject},
	}, &core.CompilerOptions{Paths: pathsA})
	b, bDiagnostics := NormalizeOptions(BingoOptions{
		Features: []string{"+avx", "+sse2"},
		Emit:     []EmitArtifact{EmitHIR, EmitObject},
	}, &core.CompilerOptions{
		Strict:              core.TSTrue,
		StrictNullChecks:    core.TSTrue,
		StrictFunctionTypes: core.TSTrue,
		NoImplicitAny:       core.TSTrue,
		Paths:               pathsB,
	})
	if len(aDiagnostics) != 0 || len(bDiagnostics) != 0 {
		t.Fatalf("unexpected diagnostics: %#v %#v", aDiagnostics, bDiagnostics)
	}
	aJSON, err := a.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	bJSON, err := b.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(aJSON, bJSON) {
		t.Fatalf("canonical JSON differs:\n%s\n%s", aJSON, bJSON)
	}
	aDigest, err := a.Digest()
	if err != nil {
		t.Fatal(err)
	}
	bDigest, err := b.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if aDigest != bDigest {
		t.Fatalf("digest differs: %s vs %s", aDigest, bDigest)
	}
}

func TestDefaultOptionsGolden(t *testing.T) {
	t.Parallel()

	normalized, diagnostics := NormalizeOptions(BingoOptions{}, nil)
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	got, err := normalized.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/options/default.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	want = bytes.TrimSpace(want)
	if !bytes.Equal(got, want) {
		t.Fatalf("options golden mismatch:\ngot:  %s\nwant: %s", got, want)
	}
}

func TestNormalizeOptionsDoesNotAliasInputs(t *testing.T) {
	t.Parallel()

	features := []string{"B", "a"}
	emit := []EmitArtifact{EmitObject, EmitHIR}
	input := BingoOptions{Features: features, Emit: emit}
	compiler := &core.CompilerOptions{Lib: []string{"es2020"}}
	options, diagnostics := NormalizeOptions(input, compiler)
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	options.Bingo.Features[0] = "changed"
	options.Bingo.Emit[0] = "changed"
	options.CompilerOptions.Lib[0] = "changed"
	if features[0] != "B" || emit[0] != EmitObject || compiler.Lib[0] != "es2020" {
		t.Fatal("normalized options retained mutable input storage")
	}
}

func TestNormalizeOptionsConcurrentCalls(t *testing.T) {
	t.Parallel()

	input := BingoOptions{Profile: ProfileInterop, Features: []string{"z", "a"}}
	const workers = 16
	var wait sync.WaitGroup
	wait.Add(workers)
	results := make(chan string, workers)
	for range workers {
		go func() {
			defer wait.Done()
			normalized, diagnostics := NormalizeOptions(input, nil)
			if len(diagnostics) != 0 {
				results <- "diagnostic"
				return
			}
			digest, err := normalized.Digest()
			if err != nil {
				results <- "error"
				return
			}
			results <- digest
		}()
	}
	wait.Wait()
	close(results)
	var first string
	for result := range results {
		if first == "" {
			first = result
		}
		if result != first {
			t.Fatalf("concurrent digest mismatch: %q vs %q", first, result)
		}
	}
}
