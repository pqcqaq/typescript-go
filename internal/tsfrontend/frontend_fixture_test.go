package tsfrontend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/frontendwire"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

const frontendFixtureRoot = "testdata/frontend"

type frontendFixtureManifest struct {
	SchemaVersion int      `json:"schemaVersion"`
	Cases         []string `json:"cases"`
}

type frontendFixtureCase struct {
	SchemaVersion   int            `json:"schemaVersion"`
	ID              string         `json:"id"`
	Category        string         `json:"category"`
	Mode            string         `json:"mode"`
	Features        []string       `json:"features"`
	Handbook        []string       `json:"handbook"`
	ASTGroups       []string       `json:"astGroups"`
	Profile         Profile        `json:"profile"`
	Target          string         `json:"target"`
	Runtime         string         `json:"runtime"`
	Artifacts       []string       `json:"artifacts"`
	Oracle          string         `json:"oracle"`
	TimeoutMS       int            `json:"timeoutMs"`
	Requires        []string       `json:"requires"`
	Source          string         `json:"source"`
	Files           []string       `json:"files,omitempty"`
	CompilerOptions map[string]any `json:"compilerOptions,omitempty"`
	Expect          struct {
		CheckCodes []string `json:"checkCodes"`
		BuildCodes []string `json:"buildCodes"`
		Snapshot   bool     `json:"snapshot"`
	} `json:"expect"`
}

type frontendFixtureObservation struct {
	fixture     frontendFixtureCase
	check       CheckResult
	snapshot    *ProgramSnapshot
	diagnostics []Diagnostic
}

func TestFrontendConformanceFixtures(t *testing.T) {
	manifest := loadFrontendFixtureJSON[frontendFixtureManifest](t, filepath.Join(frontendFixtureRoot, "manifest.json"))
	fixtures := validateFrontendFixtureManifest(t, manifest)
	observed := make(map[string]frontendFixtureObservation, len(fixtures))

	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.ID, func(t *testing.T) {
			observation := runFrontendFixture(t, fixture)
			observed[fixture.ID] = observation
		})
	}

	t.Run("goldens", func(t *testing.T) {
		assertFrontendFixtureGolden(t, "manifest.golden.json", frontendManifestProjectionOf(manifest, fixtures))
		assertFrontendFixtureGolden(t, "diagnostics.golden.json", frontendDiagnosticsProjectionOf(observed))
		assertFrontendFixtureGolden(t, "snapshot.golden.json", frontendSnapshotProjectionOf(observed["literals/number-integer"]))
		assertFrontendFixtureGolden(t, "snapshot-contract.golden.json", frontendSnapshotContractProjectionOf(t, observed))
		assertFrontendFixtureGolden(t, "module.golden.json", frontendModuleProjectionOf(observed["modules/cycle-a"]))
		assertFrontendFixtureGolden(t, "config.golden.json", frontendConfigProjectionOf(observed["literals/number-integer"]))
	})
}

func validateFrontendFixtureManifest(t *testing.T, manifest frontendFixtureManifest) []frontendFixtureCase {
	t.Helper()
	if manifest.SchemaVersion != 2 {
		t.Fatalf("frontend fixture manifest schema = %d, want 2", manifest.SchemaVersion)
	}
	if len(manifest.Cases) < 52 {
		t.Fatalf("frontend fixture manifest has %d cases, want at least 52", len(manifest.Cases))
	}
	if !sort.StringsAreSorted(manifest.Cases) {
		t.Fatal("frontend fixture manifest cases must be path-sorted")
	}

	listed := make(map[string]struct{}, len(manifest.Cases))
	ids := make(map[string]struct{}, len(manifest.Cases))
	featureCoverage := make(map[string]struct{})
	modes := make(map[string]int)
	fixtures := make([]frontendFixtureCase, 0, len(manifest.Cases))
	for _, relative := range manifest.Cases {
		if filepath.IsAbs(relative) || filepath.Ext(relative) != ".json" || !strings.HasSuffix(relative, ".case.json") {
			t.Fatalf("invalid frontend case manifest path %q", relative)
		}
		relative = filepath.ToSlash(filepath.Clean(relative))
		if _, duplicate := listed[relative]; duplicate {
			t.Fatalf("duplicate frontend case path %q", relative)
		}
		listed[relative] = struct{}{}
		fixture := loadFrontendFixtureJSON[frontendFixtureCase](t, filepath.Join(frontendFixtureRoot, filepath.FromSlash(relative)))
		if fixture.SchemaVersion != 2 {
			t.Fatalf("%s: schema = %d, want 2", relative, fixture.SchemaVersion)
		}
		if fixture.ID == "" || fixture.Category == "" || fixture.Source == "" {
			t.Fatalf("%s: id, category, and source are required", relative)
		}
		if _, duplicate := ids[fixture.ID]; duplicate {
			t.Fatalf("duplicate frontend fixture id %q", fixture.ID)
		}
		ids[fixture.ID] = struct{}{}
		if fixture.Mode != "accept" && fixture.Mode != "reject" {
			t.Fatalf("%s: mode %q is not accept or reject", fixture.ID, fixture.Mode)
		}
		modes[fixture.Mode]++
		for _, feature := range fixture.Features {
			featureCoverage[feature] = struct{}{}
		}
		validateFrontendFixtureCase(t, relative, fixture)
		fixtures = append(fixtures, fixture)
	}
	if modes["accept"] == 0 || modes["reject"] == 0 {
		t.Fatalf("frontend fixtures require accept and reject cases: %#v", modes)
	}
	for _, feature := range []string{
		"literals", "functions", "classes", "generics", "narrowing", "module", "import-cycle",
		"any", "unknown", "as-any-as", "non-null", "async", "jsx", "using", "eh",
	} {
		if _, covered := featureCoverage[feature]; !covered {
			t.Errorf("frontend fixture feature %q is not covered", feature)
		}
	}

	caseFiles, err := filepath.Glob(filepath.Join(frontendFixtureRoot, "cases", "*", "*.case.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(caseFiles) != len(listed) {
		t.Fatalf("found %d case manifests, global manifest lists %d", len(caseFiles), len(listed))
	}
	for _, name := range caseFiles {
		relative, err := filepath.Rel(frontendFixtureRoot, name)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := listed[filepath.ToSlash(relative)]; !ok {
			t.Errorf("orphan frontend case manifest %s", filepath.ToSlash(relative))
		}
	}

	tsSources, err := filepath.Glob(filepath.Join(frontendFixtureRoot, "cases", "*", "*.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if len(tsSources) < 52 {
		t.Fatalf("found %d independent .ts fixtures, want at least 52", len(tsSources))
	}
	return fixtures
}

func validateFrontendFixtureCase(t *testing.T, manifestPath string, fixture frontendFixtureCase) {
	t.Helper()
	source := normalizedFrontendFixturePath(t, fixture.ID, fixture.Source)
	if filepath.Ext(source) != ".ts" && filepath.Ext(source) != ".tsx" && filepath.Ext(source) != ".js" {
		t.Fatalf("%s: invalid source extension %q", fixture.ID, fixture.Source)
	}
	if len(fixture.Handbook) == 0 || len(fixture.ASTGroups) == 0 || fixture.Profile == "" || fixture.Target == "" || fixture.Runtime == "" || len(fixture.Artifacts) == 0 || fixture.Oracle == "" || fixture.TimeoutMS <= 0 || len(fixture.Requires) == 0 {
		t.Fatalf("%s: handbook, astGroups, profile, target, runtime, artifacts, oracle, positive timeoutMs, and requires are mandatory", fixture.ID)
	}
	if fixture.Profile != ProfileStatic && fixture.Profile != ProfileInterop && fixture.Profile != ProfileUnsafe {
		t.Fatalf("%s: unsupported fixture profile %q", fixture.ID, fixture.Profile)
	}
	for label, values := range map[string][]string{
		"features": fixture.Features, "handbook": fixture.Handbook, "astGroups": fixture.ASTGroups,
		"artifacts": fixture.Artifacts, "requires": fixture.Requires,
	} {
		if len(values) == 0 || !sort.StringsAreSorted(values) {
			t.Fatalf("%s: %s must be non-empty and sorted", fixture.ID, label)
		}
		for index := 1; index < len(values); index++ {
			if values[index-1] == values[index] {
				t.Fatalf("%s: %s contains duplicate %q", fixture.ID, label, values[index])
			}
		}
	}
	caseStem := strings.TrimSuffix(filepath.ToSlash(manifestPath), ".case.json")
	sourceStem := strings.TrimSuffix(source, filepath.Ext(source))
	if caseStem != sourceStem {
		t.Fatalf("%s: source %q is not paired with %q", fixture.ID, source, manifestPath)
	}
	files := frontendFixtureFiles(fixture)
	if !slices.Contains(files, source) {
		t.Fatalf("%s: files does not contain primary source %q", fixture.ID, source)
	}
	for _, name := range files {
		name = normalizedFrontendFixturePath(t, fixture.ID, name)
		if _, err := os.Stat(filepath.Join(frontendFixtureRoot, filepath.FromSlash(name))); err != nil {
			t.Fatalf("%s: fixture file %q: %v", fixture.ID, name, err)
		}
	}
	for label, codes := range map[string][]string{"checkCodes": fixture.Expect.CheckCodes, "buildCodes": fixture.Expect.BuildCodes} {
		if !sort.StringsAreSorted(codes) {
			t.Fatalf("%s: %s must be sorted", fixture.ID, label)
		}
		for index := 1; index < len(codes); index++ {
			if codes[index-1] == codes[index] {
				t.Fatalf("%s: %s contains duplicate %q", fixture.ID, label, codes[index])
			}
		}
	}
}

func normalizedFrontendFixturePath(t *testing.T, fixtureID, name string) string {
	t.Helper()
	if filepath.IsAbs(name) || strings.ContainsRune(name, '\x00') {
		t.Fatalf("%s: fixture path must be relative: %q", fixtureID, name)
	}
	normalized := filepath.ToSlash(filepath.Clean(name))
	if normalized == "." || normalized == ".." || strings.HasPrefix(normalized, "../") {
		t.Fatalf("%s: fixture path escapes the fixture root: %q", fixtureID, name)
	}
	return normalized
}

func runFrontendFixture(t *testing.T, fixture frontendFixtureCase) frontendFixtureObservation {
	t.Helper()
	check, err := Check(context.Background(), frontendFixtureBuildRequest(t, fixture))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	assertFrontendDiagnosticCodes(t, "Check", check.Diagnostics, fixture.Expect.CheckCodes)

	firstRequest := frontendFixtureBuildRequest(t, fixture)
	secondRequest := frontendFixtureBuildRequest(t, fixture)
	firstFrontend := NewFrontend(firstRequest.FileSystem, bundled.LibPath(), TypeScriptGoCommit, StandardLibraryHash)
	secondFrontend := NewFrontend(secondRequest.FileSystem, bundled.LibPath(), TypeScriptGoCommit, StandardLibraryHash)
	firstSnapshot, firstDiagnostics := firstFrontend.Build(context.Background(), firstRequest)
	secondSnapshot, secondDiagnostics := secondFrontend.Build(context.Background(), secondRequest)
	assertFrontendDiagnosticCodes(t, "Build", firstDiagnostics, fixture.Expect.BuildCodes)
	if (firstSnapshot != nil) != fixture.Expect.Snapshot {
		t.Errorf("Build snapshot present = %t, want %t; diagnostics = %v", firstSnapshot != nil, fixture.Expect.Snapshot, frontendDiagnosticCodes(firstDiagnostics))
	}
	if (secondSnapshot != nil) != (firstSnapshot != nil) {
		t.Fatal("repeated Build changed snapshot presence")
	}
	firstDiagnosticBytes, err := CanonicalDiagnosticsJSON(firstDiagnostics)
	if err != nil {
		t.Fatal(err)
	}
	secondDiagnosticBytes, err := CanonicalDiagnosticsJSON(secondDiagnostics)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstDiagnosticBytes, secondDiagnosticBytes) {
		t.Fatalf("repeated Build changed diagnostic bytes:\n%s\n%s", firstDiagnosticBytes, secondDiagnosticBytes)
	}
	if firstSnapshot != nil {
		firstBytes, err := firstSnapshot.CanonicalBytes()
		if err != nil {
			t.Fatal(err)
		}
		secondBytes, err := secondSnapshot.CanonicalBytes()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(firstBytes, secondBytes) {
			index := firstDifferentByte(firstBytes, secondBytes)
			start := max(0, index-40)
			firstEnd := min(len(firstBytes), index+80)
			secondEnd := min(len(secondBytes), index+80)
			t.Fatalf("repeated Build changed canonical snapshot bytes at byte %d: first=%q second=%q", index, firstBytes[start:firstEnd], secondBytes[start:secondEnd])
		}
		wireSnapshot, err := frontendwire.NewFrontendSnapshot(*firstSnapshot)
		if err != nil {
			t.Fatalf("wire snapshot validation: %v", err)
		}
		wireBytes, err := wireSnapshot.CanonicalBytes()
		if err != nil {
			t.Fatalf("encode wire snapshot: %v", err)
		}
		decodedWire, err := frontendwire.DecodeFrontendSnapshot(wireBytes)
		if err != nil {
			t.Fatalf("decode wire snapshot: %v", err)
		}
		decodedProgramBytes, err := decodedWire.Program.CanonicalBytes()
		if err != nil {
			t.Fatalf("encode decoded wire program: %v", err)
		}
		if !bytes.Equal(firstBytes, decodedProgramBytes) {
			index := firstDifferentByte(firstBytes, decodedProgramBytes)
			start := max(0, index-40)
			firstEnd := min(len(firstBytes), index+80)
			decodedEnd := min(len(decodedProgramBytes), index+80)
			t.Fatalf("wire round trip changed captured program bytes at byte %d: captured=%q decoded=%q", index, firstBytes[start:firstEnd], decodedProgramBytes[start:decodedEnd])
		}
		reencodedWireBytes, err := decodedWire.CanonicalBytes()
		if err != nil {
			t.Fatalf("re-encode wire snapshot: %v", err)
		}
		if !bytes.Equal(wireBytes, reencodedWireBytes) {
			t.Fatal("wire round trip changed envelope bytes")
		}
		snapshotDiagnosticBytes, err := CanonicalDiagnosticsJSON(firstSnapshot.Diagnostics)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(firstDiagnosticBytes, snapshotDiagnosticBytes) {
			t.Fatal("returned diagnostics differ from snapshot diagnostics")
		}
	}
	return frontendFixtureObservation{fixture: fixture, check: check, snapshot: firstSnapshot, diagnostics: firstDiagnostics}
}

func firstDifferentByte(left, right []byte) int {
	limit := min(len(left), len(right))
	for index := 0; index < limit; index++ {
		if left[index] != right[index] {
			return index
		}
	}
	return limit
}

func frontendFixtureBuildRequest(t *testing.T, fixture frontendFixtureCase) BuildRequest {
	t.Helper()
	files := make(map[string]string, len(frontendFixtureFiles(fixture))+1)
	for _, relative := range frontendFixtureFiles(fixture) {
		contents, err := os.ReadFile(filepath.Join(frontendFixtureRoot, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		files["/project/"+filepath.ToSlash(relative)] = string(contents)
	}
	compilerOptions := map[string]any{
		"strict":           true,
		"noEmit":           true,
		"target":           "ES2020",
		"module":           "ESNext",
		"moduleResolution": "Bundler",
		"lib":              []string{"ES5"},
		"skipLibCheck":     true,
	}
	for key, value := range fixture.CompilerOptions {
		compilerOptions[key] = value
	}
	config, err := json.Marshal(map[string]any{
		"compilerOptions": compilerOptions,
		"files":           []string{fixture.Source},
	})
	if err != nil {
		t.Fatal(err)
	}
	files["/project/tsconfig.json"] = string(config)
	return BuildRequest{
		ConfigPath:       "/project/tsconfig.json",
		CurrentDirectory: "/project",
		ProjectRoot:      "/project",
		FileSystem:       vfstest.FromMap(files, true),
	}
}

func frontendFixtureFiles(fixture frontendFixtureCase) []string {
	if len(fixture.Files) == 0 {
		return []string{filepath.ToSlash(filepath.Clean(fixture.Source))}
	}
	files := make([]string, len(fixture.Files))
	for index, name := range fixture.Files {
		files[index] = filepath.ToSlash(filepath.Clean(name))
	}
	return files
}

func assertFrontendDiagnosticCodes(t *testing.T, label string, diagnostics []Diagnostic, want []string) {
	t.Helper()
	got := frontendDiagnosticCodes(diagnostics)
	if !slices.Equal(got, want) {
		t.Errorf("%s diagnostic codes = %v, want %v; diagnostics = %#v", label, got, want, diagnostics)
	}
}

func frontendDiagnosticCodes(diagnostics []Diagnostic) []string {
	seen := make(map[string]struct{}, len(diagnostics))
	for _, diagnostic := range diagnostics {
		seen[diagnostic.Code] = struct{}{}
	}
	result := make([]string, 0, len(seen))
	for code := range seen {
		result = append(result, code)
	}
	sort.Strings(result)
	return result
}

func loadFrontendFixtureJSON[T any](t *testing.T, name string) T {
	t.Helper()
	file, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var value T
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			t.Fatalf("decode %s: multiple JSON values", name)
		}
		t.Fatalf("decode %s: %v", name, err)
	}
	return value
}

type frontendManifestProjection struct {
	SchemaVersion   int                       `json:"schemaVersion"`
	CaseCount       int                       `json:"caseCount"`
	TypeScriptCount int                       `json:"typescriptCount"`
	Categories      []frontendCountProjection `json:"categories"`
	Modes           []frontendCountProjection `json:"modes"`
	Features        []string                  `json:"features"`
}

type frontendCountProjection struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func frontendManifestProjectionOf(manifest frontendFixtureManifest, fixtures []frontendFixtureCase) frontendManifestProjection {
	categories := make(map[string]int)
	modes := make(map[string]int)
	features := make(map[string]struct{})
	typescriptCount := 0
	for _, fixture := range fixtures {
		categories[fixture.Category]++
		modes[fixture.Mode]++
		if filepath.Ext(fixture.Source) == ".ts" {
			typescriptCount++
		}
		for _, feature := range fixture.Features {
			features[feature] = struct{}{}
		}
	}
	return frontendManifestProjection{
		SchemaVersion:   manifest.SchemaVersion,
		CaseCount:       len(fixtures),
		TypeScriptCount: typescriptCount,
		Categories:      frontendSortedCounts(categories),
		Modes:           frontendSortedCounts(modes),
		Features:        frontendSortedKeys(features),
	}
}

type frontendDiagnosticProjection struct {
	Cases []frontendDiagnosticCaseProjection `json:"cases"`
}

type frontendDiagnosticCaseProjection struct {
	Case  string       `json:"case"`
	Check []Diagnostic `json:"check"`
	Build []Diagnostic `json:"build"`
}

func frontendDiagnosticsProjectionOf(observed map[string]frontendFixtureObservation) frontendDiagnosticProjection {
	ids := make([]string, 0, len(observed))
	for id := range observed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := frontendDiagnosticProjection{Cases: make([]frontendDiagnosticCaseProjection, 0, len(ids))}
	for _, id := range ids {
		observation := observed[id]
		if len(observation.check.Diagnostics) == 0 && len(observation.diagnostics) == 0 {
			continue
		}
		result.Cases = append(result.Cases, frontendDiagnosticCaseProjection{
			Case:  id,
			Check: slices.Clone(observation.check.Diagnostics),
			Build: slices.Clone(observation.diagnostics),
		})
	}
	return result
}

type frontendSnapshotContractProjection struct {
	Cases []frontendSnapshotContractCase `json:"cases"`
}

type frontendSnapshotContractCase struct {
	Case     string          `json:"case"`
	Snapshot json.RawMessage `json:"snapshot"`
}

func frontendSnapshotContractProjectionOf(t *testing.T, observed map[string]frontendFixtureObservation) frontendSnapshotContractProjection {
	t.Helper()
	caseIDs := []string{
		"functions/closure",
		"functions/overload",
		"modules/type-import",
		"narrowing/null-guard",
		"safety/as-any-as",
	}
	result := frontendSnapshotContractProjection{Cases: make([]frontendSnapshotContractCase, 0, len(caseIDs))}
	for _, id := range caseIDs {
		observation, ok := observed[id]
		if !ok || observation.snapshot == nil {
			t.Fatalf("snapshot contract case %q did not produce a snapshot", id)
		}
		canonical, err := observation.snapshot.CanonicalBytes()
		if err != nil {
			t.Fatalf("snapshot contract case %q: %v", id, err)
		}
		result.Cases = append(result.Cases, frontendSnapshotContractCase{Case: id, Snapshot: json.RawMessage(canonical)})
	}
	return result
}

type frontendSnapshotProjection struct {
	Case           string   `json:"case"`
	SchemaVersion  uint32   `json:"schemaVersion"`
	FileCount      int      `json:"fileCount"`
	ModuleCount    int      `json:"moduleCount"`
	NodeCount      int      `json:"nodeCount"`
	TypeCount      int      `json:"typeCount"`
	SymbolCount    int      `json:"symbolCount"`
	SignatureCount int      `json:"signatureCount"`
	CanonicalPaths []string `json:"canonicalPaths"`
	NodeKinds      []string `json:"nodeKinds"`
	TypeKinds      []string `json:"typeKinds"`
	HasContentHash bool     `json:"hasContentHash"`
}

func frontendSnapshotProjectionOf(observation frontendFixtureObservation) frontendSnapshotProjection {
	result := frontendSnapshotProjection{Case: observation.fixture.ID, CanonicalPaths: []string{}, NodeKinds: []string{}, TypeKinds: []string{}}
	if observation.snapshot == nil {
		return result
	}
	snapshot := observation.snapshot
	result.SchemaVersion = snapshot.SchemaVersion
	result.FileCount = len(snapshot.Files)
	result.ModuleCount = len(snapshot.Modules)
	result.NodeCount = len(snapshot.Nodes)
	result.TypeCount = len(snapshot.Types)
	result.SymbolCount = len(snapshot.Symbols)
	result.SignatureCount = len(snapshot.Signatures)
	result.HasContentHash = len(snapshot.ContentHash) == 64
	for _, file := range snapshot.Files {
		result.CanonicalPaths = append(result.CanonicalPaths, file.CanonicalPath)
	}
	for _, node := range snapshot.Nodes {
		result.NodeKinds = append(result.NodeKinds, node.Kind)
	}
	for _, record := range snapshot.Types {
		result.TypeKinds = append(result.TypeKinds, record.Kind)
	}
	result.CanonicalPaths = frontendSortedUnique(result.CanonicalPaths)
	result.NodeKinds = frontendSortedUnique(result.NodeKinds)
	result.TypeKinds = frontendSortedUnique(result.TypeKinds)
	return result
}

type frontendModuleProjection struct {
	Case    string                    `json:"case"`
	Modules []frontendModuleGoldenRow `json:"modules"`
	Edges   []frontendEdgeGoldenRow   `json:"edges"`
	SCCs    [][]string                `json:"sccs"`
}

type frontendModuleGoldenRow struct {
	Path   string `json:"path"`
	Format string `json:"format"`
	SCC    int    `json:"scc"`
}

type frontendEdgeGoldenRow struct {
	Importer  string `json:"importer"`
	Imported  string `json:"imported"`
	Specifier string `json:"specifier"`
	Kind      string `json:"kind"`
	TypeOnly  bool   `json:"typeOnly"`
	Value     bool   `json:"value"`
}

func frontendModuleProjectionOf(observation frontendFixtureObservation) frontendModuleProjection {
	result := frontendModuleProjection{Case: observation.fixture.ID, Modules: []frontendModuleGoldenRow{}, Edges: []frontendEdgeGoldenRow{}, SCCs: [][]string{}}
	if observation.snapshot == nil {
		return result
	}
	paths := make(map[ModuleID]string, len(observation.snapshot.Modules))
	for _, module := range observation.snapshot.Modules {
		paths[module.ID] = module.CanonicalPath
		result.Modules = append(result.Modules, frontendModuleGoldenRow{Path: module.CanonicalPath, Format: module.Format, SCC: module.SCC})
	}
	for _, edge := range observation.snapshot.ModuleEdges {
		result.Edges = append(result.Edges, frontendEdgeGoldenRow{
			Importer: paths[edge.Importer], Imported: paths[edge.Imported], Specifier: edge.Specifier,
			Kind: edge.Kind, TypeOnly: edge.TypeOnly, Value: edge.Value,
		})
	}
	for _, component := range observation.snapshot.ModuleSCCs {
		members := make([]string, 0, len(component.Modules))
		for _, moduleID := range component.Modules {
			members = append(members, paths[moduleID])
		}
		sort.Strings(members)
		result.SCCs = append(result.SCCs, members)
	}
	sort.Slice(result.Modules, func(i, j int) bool { return result.Modules[i].Path < result.Modules[j].Path })
	sort.Slice(result.Edges, func(i, j int) bool {
		left, right := result.Edges[i], result.Edges[j]
		return fmt.Sprintf("%s\x00%s\x00%s", left.Importer, left.Specifier, left.Kind) < fmt.Sprintf("%s\x00%s\x00%s", right.Importer, right.Specifier, right.Kind)
	})
	sort.Slice(result.SCCs, func(i, j int) bool {
		return strings.Join(result.SCCs[i], "\x00") < strings.Join(result.SCCs[j], "\x00")
	})
	return result
}

type frontendConfigProjection struct {
	Case                string  `json:"case"`
	BingoSchemaVersion  int     `json:"bingoSchemaVersion"`
	Profile             Profile `json:"profile"`
	Runtime             string  `json:"runtime"`
	LLVMMajor           int     `json:"llvmMajor"`
	Strict              bool    `json:"strict"`
	StrictNullChecks    bool    `json:"strictNullChecks"`
	StrictFunctionTypes bool    `json:"strictFunctionTypes"`
	NoImplicitAny       bool    `json:"noImplicitAny"`
	Target              string  `json:"target"`
	Module              string  `json:"module"`
	ModuleResolution    string  `json:"moduleResolution"`
}

func frontendConfigProjectionOf(observation frontendFixtureObservation) frontendConfigProjection {
	result := frontendConfigProjection{Case: observation.fixture.ID}
	if observation.snapshot == nil {
		return result
	}
	config := observation.snapshot.Config
	result.BingoSchemaVersion = config.BingoSchemaVersion
	result.Profile = config.Bingo.Profile
	result.Runtime = config.Bingo.Runtime
	result.LLVMMajor = config.Bingo.LLVMMajor
	result.Strict = config.TypeScript.Strict
	result.StrictNullChecks = config.TypeScript.StrictNullChecks
	result.StrictFunctionTypes = config.TypeScript.StrictFunctionTypes
	result.NoImplicitAny = config.TypeScript.NoImplicitAny
	result.Target = config.TypeScript.Target
	result.Module = config.TypeScript.Module
	result.ModuleResolution = config.TypeScript.ModuleResolution
	return result
}

func assertFrontendFixtureGolden(t *testing.T, name string, value any) {
	t.Helper()
	got, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	goldenPath := filepath.Join(frontendFixtureRoot, "goldens", name)
	if os.Getenv("TS2BIN_UPDATE_GOLDENS") == "1" {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, got, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	if !bytes.Equal(got, want) {
		t.Fatalf("%s mismatch\nwant:\n%s\ngot:\n%s", name, want, got)
	}
}

func frontendSortedCounts(values map[string]int) []frontendCountProjection {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]frontendCountProjection, 0, len(keys))
	for _, key := range keys {
		result = append(result, frontendCountProjection{Name: key, Count: values[key]})
	}
	return result
}

func frontendSortedKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func frontendSortedUnique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	return frontendSortedKeys(seen)
}
