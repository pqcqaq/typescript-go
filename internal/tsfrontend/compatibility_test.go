package tsfrontend

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/ast"
)

func TestCompatibilityComparisonNoChangesAndStableJSON(t *testing.T) {
	t.Parallel()

	baseline := compatibilitySnapshot("same", []CompatibilityEntry{{Key: "KindB", Value: "2"}, {Key: "KindA", Value: "1"}})
	current := compatibilitySnapshot("same", []CompatibilityEntry{{Key: "KindA", Value: "1"}, {Key: "KindB", Value: "2"}})

	report, err := CompareCompatibilitySnapshots(baseline, current)
	if err != nil {
		t.Fatal(err)
	}
	if report.Changes == nil || len(report.Changes) != 0 {
		t.Fatalf("changes = %#v, want a non-nil empty list", report.Changes)
	}
	if baseline.Kinds[0].Key != "KindB" {
		t.Fatal("comparison mutated its input")
	}

	baselineJSON, err := MarshalCompatibilitySnapshot(baseline)
	if err != nil {
		t.Fatal(err)
	}
	currentJSON, err := MarshalCompatibilitySnapshot(current)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(baselineJSON, currentJSON) {
		t.Fatalf("canonical snapshots differ:\n%s\n%s", baselineJSON, currentJSON)
	}

	parsed, err := ParseCompatibilitySnapshot(baselineJSON)
	if err != nil {
		t.Fatal(err)
	}
	parsedJSON, err := MarshalCompatibilitySnapshot(parsed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(baselineJSON, parsedJSON) {
		t.Fatal("parse/marshal round trip was not byte stable")
	}
}

func TestCompatibilityComparisonClassifiesAllSurfacesAndChangeKinds(t *testing.T) {
	t.Parallel()

	oldCommit := compatibilityTestCommit("old")
	newCommit := compatibilityTestCommit("new")
	baseline := CompatibilitySnapshot{
		SchemaVersion:      CompatibilitySchemaVersion,
		TypeScriptGoCommit: oldCommit,
		Kinds: []CompatibilityEntry{
			{Key: "KindChanged", Value: "1"},
			{Key: "KindRemoved", Value: "2"},
		},
		API:       []CompatibilityEntry{{Key: "checker.API", Value: "func(int)"}},
		Stdlib:    []CompatibilityEntry{{Key: "lib.removed.d.ts", Value: "a"}, {Key: "lib.shared.d.ts", Value: "s"}},
		Semantics: []CompatibilityEntry{{Key: "narrowing", Value: "old-digest"}},
	}
	current := CompatibilitySnapshot{
		SchemaVersion:      CompatibilitySchemaVersion,
		TypeScriptGoCommit: newCommit,
		Kinds: []CompatibilityEntry{
			{Key: "KindAdded", Value: "3"},
			{Key: "KindChanged", Value: "10"},
		},
		API:       []CompatibilityEntry{{Key: "checker.API", Value: "func(string)"}},
		Stdlib:    []CompatibilityEntry{{Key: "lib.added.d.ts", Value: "b"}, {Key: "lib.shared.d.ts", Value: "s"}},
		Semantics: []CompatibilityEntry{{Key: "narrowing", Value: "new-digest"}},
	}

	report, err := CompareCompatibilitySnapshots(baseline, current)
	if err != nil {
		t.Fatal(err)
	}
	want := []CompatibilityChange{
		{Category: CompatibilityCategoryLock, Kind: CompatibilityChangeChanged, Key: "typescript-go.commit", Before: oldCommit, After: newCommit},
		{Category: CompatibilityCategoryKind, Kind: CompatibilityChangeAdded, Key: "KindAdded", After: "3"},
		{Category: CompatibilityCategoryKind, Kind: CompatibilityChangeChanged, Key: "KindChanged", Before: "1", After: "10"},
		{Category: CompatibilityCategoryKind, Kind: CompatibilityChangeRemoved, Key: "KindRemoved", Before: "2"},
		{Category: CompatibilityCategoryAPI, Kind: CompatibilityChangeChanged, Key: "checker.API", Before: "func(int)", After: "func(string)"},
		{Category: CompatibilityCategoryStdlib, Kind: CompatibilityChangeAdded, Key: "lib.added.d.ts", After: "b"},
		{Category: CompatibilityCategoryStdlib, Kind: CompatibilityChangeRemoved, Key: "lib.removed.d.ts", Before: "a"},
		{Category: CompatibilityCategorySemantic, Kind: CompatibilityChangeChanged, Key: "narrowing", Before: "old-digest", After: "new-digest"},
	}
	if !reflect.DeepEqual(report.Changes, want) {
		t.Fatalf("changes:\n got: %#v\nwant: %#v", report.Changes, want)
	}

	firstJSON, err := MarshalCompatibilityReport(report)
	if err != nil {
		t.Fatal(err)
	}
	reversed := report
	reversed.Changes = append([]CompatibilityChange(nil), report.Changes...)
	for left, right := 0, len(reversed.Changes)-1; left < right; left, right = left+1, right-1 {
		reversed.Changes[left], reversed.Changes[right] = reversed.Changes[right], reversed.Changes[left]
	}
	secondJSON, err := MarshalCompatibilityReport(reversed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatal("report serialization depends on input order")
	}
}

func TestCompatibilityComparisonReportsObservedCheckoutDrift(t *testing.T) {
	t.Parallel()

	pinnedCommit := compatibilityTestCommit("pinned")
	observedCommit := compatibilityTestCommit("observed")
	baseline := compatibilitySnapshot("pinned", nil)
	current := compatibilitySnapshot("pinned", nil)
	current.ObservedCheckoutCommit = observedCommit
	current.ExpectedCheckoutCommit = pinnedCommit
	report, err := CompareCompatibilitySnapshots(baseline, current)
	if err != nil {
		t.Fatal(err)
	}
	want := []CompatibilityChange{{
		Category: CompatibilityCategoryLock,
		Kind:     CompatibilityChangeChanged,
		Key:      "typescript-go.checkout",
		Before:   pinnedCommit,
		After:    observedCommit,
	}}
	if report.Compatible || !reflect.DeepEqual(report.Changes, want) {
		t.Fatalf("observed checkout report = %#v, want changes %#v", report, want)
	}
	data, err := MarshalCompatibilityReport(report)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"observedCheckoutCommit": "`+observedCommit+`"`)) {
		t.Fatalf("report JSON does not expose observed checkout commit:\n%s", data)
	}
}

func TestCompatibilityComparisonDoesNotTreatBaselineAsCheckoutLock(t *testing.T) {
	t.Parallel()

	baseline := compatibilitySnapshot("upstream", nil)
	current := compatibilitySnapshot("upstream", nil)
	current.ObservedCheckoutCommit = compatibilityTestCommit("fork-head")
	report, err := CompareCompatibilitySnapshots(baseline, current)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Compatible || len(report.Changes) != 0 {
		t.Fatalf("unanchored checkout observation invalidated its own baseline: %#v", report)
	}
}

func TestCompatibilityValidationRejectsInvalidSchemaDuplicatesAndTrailingJSON(t *testing.T) {
	t.Parallel()

	invalidSchema := compatibilitySnapshot("commit", nil)
	invalidSchema.SchemaVersion++
	if _, err := NormalizeCompatibilitySnapshot(invalidSchema); err == nil || !strings.Contains(err.Error(), "unsupported compatibility schema") {
		t.Fatalf("invalid schema error = %v", err)
	}

	duplicate := compatibilitySnapshot("commit", []CompatibilityEntry{{Key: "KindA", Value: "1"}, {Key: "KindA", Value: "2"}})
	if _, err := NormalizeCompatibilitySnapshot(duplicate); err == nil || !strings.Contains(err.Error(), "duplicate kind entry") {
		t.Fatalf("duplicate error = %v", err)
	}

	validJSON, err := MarshalCompatibilitySnapshot(compatibilitySnapshot("commit", nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseCompatibilitySnapshot(append(validJSON, []byte(" {}")...)); err == nil || !strings.Contains(err.Error(), "multiple JSON values") {
		t.Fatalf("trailing JSON error = %v", err)
	}
	unknown := bytes.Replace(validJSON, []byte(`"schemaVersion"`), []byte(`"unknown":1,"schemaVersion"`), 1)
	if _, err := ParseCompatibilitySnapshot(unknown); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}
}

func TestCompatibilityValidationRejectsInvalidCommits(t *testing.T) {
	t.Parallel()

	for _, commit := range []string{
		"",
		"main",
		strings.Repeat("a", 39),
		strings.Repeat("A", 40),
		strings.Repeat("z", 40),
	} {
		snapshot := compatibilitySnapshot("valid", nil)
		snapshot.TypeScriptGoCommit = commit
		if _, err := MarshalCompatibilitySnapshot(snapshot); err == nil {
			t.Errorf("commit %q was accepted", commit)
		}
	}
	snapshot := compatibilitySnapshot("valid", nil)
	snapshot.ExpectedCheckoutCommit = "main"
	if _, err := NormalizeCompatibilitySnapshot(snapshot); err == nil || !strings.Contains(err.Error(), "expected checkout commit") {
		t.Fatalf("invalid expected checkout commit error = %v", err)
	}
}

func TestResolveModulePathRejectsResolvedEscape(t *testing.T) {
	t.Parallel()

	moduleRoot := t.TempDir()
	outside := t.TempDir()
	writeTestFile(t, filepath.Join(outside, "fixture.ts"), "export const escaped = true\n")
	linked := filepath.Join(moduleRoot, "linked")
	if err := os.Symlink(outside, linked); err != nil {
		t.Skipf("cannot create directory symlink: %v", err)
	}
	if _, _, err := resolveModulePath(moduleRoot, "linked/fixture.ts"); err == nil || !strings.Contains(err.Error(), "resolves outside module root") {
		t.Fatalf("resolveModulePath error = %v, want resolved escape rejection", err)
	}
}

func TestCollectASTKindCompatibilityCoversCurrentKindDomain(t *testing.T) {
	t.Parallel()

	entries := CollectASTKindCompatibility()
	if got, want := len(entries), int(ast.KindCount); got != want {
		t.Fatalf("Kind count = %d, want %d", got, want)
	}
	if len(entries) != 351 {
		t.Fatalf("locked tsgo Kind count = %d, want 351", len(entries))
	}
	byName := make(map[string]string, len(entries))
	for index, entry := range entries {
		if got, want := entry.Key, ast.Kind(index).String(); got != want {
			t.Fatalf("Kind entry %d name = %q, want %q", index, got, want)
		}
		if got, want := entry.Value, strconv.Itoa(index); got != want {
			t.Fatalf("Kind entry %d value = %q, want %q", index, got, want)
		}
		if _, duplicate := byName[entry.Key]; duplicate {
			t.Fatalf("duplicate Kind name %q", entry.Key)
		}
		byName[entry.Key] = entry.Value
	}
	for kind := ast.Kind(0); kind < ast.KindCount; kind++ {
		if got, want := byName[kind.String()], strconv.Itoa(int(kind)); got != want {
			t.Fatalf("Kind %s value = %q, want %q", kind, got, want)
		}
	}
}

func TestCollectAPICompatibilityIgnoresCommentsAndFileOrder(t *testing.T) {
	t.Parallel()

	first := t.TempDir()
	second := t.TempDir()
	writeTestFile(t, filepath.Join(first, "api", "a.go"), `package api

// Exported has a comment that is not part of the Go API signature.
type Exported struct {
	Value int
	hidden string
}

func Z(value Exported) *Exported { return &value }
func (value *Exported) Method(input int) string { return "" }
func hidden() {}
`)
	writeTestFile(t, filepath.Join(first, "api", "z.go"), `package api

const (
	A = iota
	B
)

var V int
`)
	writeTestFile(t, filepath.Join(second, "api", "a.go"), `package api

// Different comments and declaration files must not affect the result.
var V int
const (
	A = iota
	B
)
`)
	writeTestFile(t, filepath.Join(second, "api", "z.go"), `package api

func hidden() { panic("body changes are ignored") }
func (value *Exported) Method(input int) string { return inputString(input) }
func Z(value Exported) *Exported { return nil }

type Exported struct {
	Value int
	hidden string
}

func inputString(int) string { return "" }
`)

	firstEntries, err := CollectAPICompatibility(first, []string{"api"})
	if err != nil {
		t.Fatal(err)
	}
	secondEntries, err := CollectAPICompatibility(second, []string{"api"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(firstEntries, secondEntries) {
		t.Fatalf("API compatibility depends on comments, bodies, or file order:\n%#v\n%#v", firstEntries, secondEntries)
	}

	wantKeys := []string{"api.A", "api.B", "api.Exported", "api.Exported.Method", "api.V", "api.Z"}
	gotKeys := make([]string, len(firstEntries))
	for index, entry := range firstEntries {
		gotKeys[index] = entry.Key
	}
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("API keys = %#v, want %#v", gotKeys, wantKeys)
	}
}

func TestCollectAPICompatibilityDetectsSignatureChanges(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "api", "api.go")
	writeTestFile(t, path, "package api\nfunc Exported(value int) string { return \"\" }\n")
	before, err := CollectAPICompatibility(root, []string{"api"})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, path, "package api\nfunc Exported(value string) string { return value }\n")
	after, err := CollectAPICompatibility(root, []string{"api"})
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(before, after) {
		t.Fatal("exported signature change was not detected")
	}
}

func TestCollectAPICompatibilityTracksIotaPosition(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "api", "api.go")
	writeTestFile(t, path, `package api
const (
	hidden = iota
	Exported
)
`)
	before, err := CollectAPICompatibility(root, []string{"api"})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, path, `package api
const (
	hidden = iota
	other
	Exported
)
`)
	after, err := CollectAPICompatibility(root, []string{"api"})
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(before, after) {
		t.Fatal("exported iota value change was not detected")
	}
	if got := compatibilityEntryValue(t, before, "api.Exported"); !strings.HasPrefix(got, "iota=1 ") {
		t.Fatalf("before exported iota variant = %q", got)
	}
	if got := compatibilityEntryValue(t, after, "api.Exported"); !strings.HasPrefix(got, "iota=2 ") {
		t.Fatalf("after exported iota variant = %q", got)
	}
}

func TestCollectAPICompatibilityTracksBuildConstraintVariants(t *testing.T) {
	t.Parallel()

	first := t.TempDir()
	second := t.TempDir()
	writeTestFile(t, filepath.Join(first, "api", "platform_windows.go"), "package api\nfunc Platform(value int) {}\n")
	writeTestFile(t, filepath.Join(first, "api", "platform_linux.go"), "package api\nfunc Platform(value string) {}\n")
	writeTestFile(t, filepath.Join(first, "api", "tagged.go"), "//go:build enterprise && !race\n\npackage api\nfunc Tagged() {}\n")
	writeTestFile(t, filepath.Join(second, "api", "platform_windows.go"), "package api\nfunc Platform(value string) {}\n")
	writeTestFile(t, filepath.Join(second, "api", "platform_linux.go"), "package api\nfunc Platform(value int) {}\n")
	writeTestFile(t, filepath.Join(second, "api", "tagged.go"), "//go:build enterprise && !race\n\npackage api\nfunc Tagged() {}\n")
	before, err := CollectAPICompatibility(first, []string{"api"})
	if err != nil {
		t.Fatal(err)
	}
	after, err := CollectAPICompatibility(second, []string{"api"})
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(before, after) {
		t.Fatal("build-constrained signature reassignment was not detected")
	}
	value := compatibilityEntryValue(t, before, "api.Platform")
	if !strings.Contains(value, "build linux\n") || !strings.Contains(value, "build windows\n") {
		t.Fatalf("build constraints are missing from API variants: %q", value)
	}
	if tagged := compatibilityEntryValue(t, before, "api.Tagged"); !strings.Contains(tagged, "build enterprise && !race\n") {
		t.Fatalf("explicit build constraint is missing from API variant: %q", tagged)
	}
}

func TestCollectAPICompatibilityNormalizesFunctionParameterNames(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "api", "api.go")
	writeTestFile(t, path, "package api\nfunc Exported(value int) (result string) { return \"\" }\n")
	before, err := CollectAPICompatibility(root, []string{"api"})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, path, "package api\nfunc Exported(input int) (output string) { return \"\" }\n")
	after, err := CollectAPICompatibility(root, []string{"api"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("parameter names changed the normalized API:\n%#v\n%#v", before, after)
	}
}

func TestCollectStdlibCompatibilitySortsPathsAndDetectsContentChanges(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "nested", "z.d.ts"), "interface Z {}\n")
	writeTestFile(t, filepath.Join(root, "a.d.ts"), "interface A {}\n")
	writeTestFile(t, filepath.Join(root, "ignored.txt"), "not a declaration")

	before, err := CollectStdlibCompatibility(root)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := []string{before[0].Key, before[1].Key}, []string{"a.d.ts", "nested/z.d.ts"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("stdlib paths = %#v, want %#v", got, want)
	}
	wantHash := sha256.Sum256([]byte("interface A {}\n"))
	if before[0].Value != hex.EncodeToString(wantHash[:]) {
		t.Fatalf("a.d.ts digest = %q", before[0].Value)
	}

	writeTestFile(t, filepath.Join(root, "nested", "z.d.ts"), "interface Z { value: string }\n")
	after, err := CollectStdlibCompatibility(root)
	if err != nil {
		t.Fatal(err)
	}
	if before[0] != after[0] || before[1] == after[1] {
		t.Fatalf("unexpected stdlib diff:\n%#v\n%#v", before, after)
	}
}

func TestCollectSemanticCompatibilityCopiesAndSortsInput(t *testing.T) {
	t.Parallel()

	input := map[string]string{"z": "last", "a": "first"}
	entries, err := CollectSemanticCompatibility(input)
	if err != nil {
		t.Fatal(err)
	}
	input["a"] = "mutated"
	want := []CompatibilityEntry{{Key: "a", Value: "first"}, {Key: "z", Value: "last"}}
	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("semantic entries = %#v, want %#v", entries, want)
	}
}

func TestSemanticSnapshotProjectionExcludesProvenance(t *testing.T) {
	t.Parallel()
	first := &ProgramSnapshot{
		SchemaVersion: SnapshotSchemaVersion,
		Provenance: ProvenanceSnapshot{
			TypeScriptGoCommit: "first",
			GoVersion:          "go-first",
			KindManifestHash:   "manifest-first",
		},
		ContentHash: "content-first",
		Nodes:       []NodeSnapshot{{ID: "node", Kind: "KindIdentifier"}},
	}
	second := *first
	second.Nodes = append([]NodeSnapshot(nil), first.Nodes...)
	second.Provenance.TypeScriptGoCommit = "second"
	second.Provenance.GoVersion = "go-second"
	second.Provenance.KindManifestHash = "manifest-second"
	second.ContentHash = "content-second"
	firstBytes, err := canonicalSemanticSnapshotBytes(first)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := canonicalSemanticSnapshotBytes(&second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatalf("semantic projection retained provenance:\n%s\n%s", firstBytes, secondBytes)
	}
	second.Nodes[0].Kind = "KindStringLiteral"
	secondBytes, err = canonicalSemanticSnapshotBytes(&second)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("semantic projection ignored a node change")
	}
}

func TestCompatibilityFixtureSourceExtensionsCoverTypeScriptAndJavaScript(t *testing.T) {
	t.Parallel()
	for _, extension := range []string{".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs"} {
		if !isCompatibilityFixtureSourceExtension(extension) {
			t.Errorf("source extension %q was rejected", extension)
		}
	}
	for _, extension := range []string{"", ".json", ".go"} {
		if isCompatibilityFixtureSourceExtension(extension) {
			t.Errorf("non-source extension %q was accepted", extension)
		}
	}
}

func TestCollectCompatibilitySnapshotFromCurrentCheckout(t *testing.T) {
	t.Parallel()

	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	moduleRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	options := CompatibilityCollectionOptions{
		ModuleRoot:         moduleRoot,
		TypeScriptGoCommit: "12318e599d21f516defea3b20e5d44b9369da723",
		SemanticDigests:    map[string]string{"fixture/basic": "semantic-digest"},
	}
	first, err := CollectCompatibilitySnapshot(options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CollectCompatibilitySnapshot(options)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, err := MarshalCompatibilitySnapshot(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := MarshalCompatibilitySnapshot(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatal("repeated checkout collection was not byte stable")
	}
	if got, want := len(first.Kinds), 351; got != want {
		t.Fatalf("Kind entries = %d, want %d", got, want)
	}
	if len(first.API) == 0 {
		t.Fatal("default API collection is empty")
	}
	if got, want := len(first.Stdlib), 108; got != want {
		t.Fatalf("stdlib entries = %d, want %d", got, want)
	}
	if got, want := first.Semantics, []CompatibilityEntry{{Key: "fixture/basic", Value: "semantic-digest"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("semantic entries = %#v, want %#v", got, want)
	}
	for _, prefix := range DefaultCompatibilityAPIPackages() {
		found := false
		for _, entry := range first.API {
			if strings.HasPrefix(entry.Key, prefix+".") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("default API package %q produced no exported declarations", prefix)
		}
	}
}

func compatibilitySnapshot(commit string, kinds []CompatibilityEntry) CompatibilitySnapshot {
	return CompatibilitySnapshot{
		SchemaVersion:      CompatibilitySchemaVersion,
		TypeScriptGoCommit: compatibilityTestCommit(commit),
		Kinds:              kinds,
		API:                []CompatibilityEntry{},
		Stdlib:             []CompatibilityEntry{},
		Semantics:          []CompatibilityEntry{},
	}
}

func compatibilityTestCommit(label string) string {
	digest := sha256.Sum256([]byte(label))
	return hex.EncodeToString(digest[:20])
}

func compatibilityEntryValue(t *testing.T, entries []CompatibilityEntry, key string) string {
	t.Helper()
	for _, entry := range entries {
		if entry.Key == key {
			return entry.Value
		}
	}
	t.Fatalf("compatibility entry %q not found", key)
	return ""
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
