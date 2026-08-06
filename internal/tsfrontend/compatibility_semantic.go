package tsfrontend

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

const (
	// CompatibilityBaselineModulePath is the checked-in compatibility baseline
	// relative to the typescript-go module root.
	CompatibilityBaselineModulePath = "internal/tsfrontend/compatibility_baseline.json"
	compatibilityFixtureModulePath  = "internal/tsfrontend/testdata/frontend"
	semanticFixtureDigestDomain     = "ts2bin-semantic-fixture-v2"
	semanticFixtureSchemaVersion    = 2
)

type compatibilityFixtureManifest struct {
	SchemaVersion int      `json:"schemaVersion"`
	Cases         []string `json:"cases"`
}

type compatibilityFixtureCase struct {
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

// CollectCurrentCompatibilitySnapshot captures every compatibility surface
// from one module checkout, including semantic results from the checked-in
// frontend fixtures.
func CollectCurrentCompatibilitySnapshot(ctx context.Context, moduleRoot string) (CompatibilitySnapshot, error) {
	moduleRoot, err := filepath.Abs(moduleRoot)
	if err != nil {
		return CompatibilitySnapshot{}, fmt.Errorf("resolve module root: %w", err)
	}
	checkoutCommit, err := resolveCompatibilityCheckoutCommit(ctx, moduleRoot)
	if err != nil {
		return CompatibilitySnapshot{}, err
	}
	fixtureRoot, _, err := resolveModulePath(moduleRoot, compatibilityFixtureModulePath)
	if err != nil {
		return CompatibilitySnapshot{}, fmt.Errorf("resolve semantic fixture root: %w", err)
	}
	semanticDigests, err := CollectSemanticFixtureDigests(ctx, fixtureRoot)
	if err != nil {
		return CompatibilitySnapshot{}, err
	}
	snapshot, err := CollectCompatibilitySnapshot(CompatibilityCollectionOptions{
		ModuleRoot:         moduleRoot,
		TypeScriptGoCommit: TypeScriptGoCommit,
		SemanticDigests:    semanticDigests,
	})
	if err != nil {
		return CompatibilitySnapshot{}, err
	}
	finalCommit, err := resolveCompatibilityCheckoutCommit(ctx, moduleRoot)
	if err != nil {
		return CompatibilitySnapshot{}, err
	}
	if finalCommit != checkoutCommit {
		return CompatibilitySnapshot{}, fmt.Errorf("typescript-go checkout changed during compatibility collection: %s -> %s", checkoutCommit, finalCommit)
	}
	snapshot.ObservedCheckoutCommit = checkoutCommit
	return snapshot, nil
}

func resolveCompatibilityCheckoutCommit(ctx context.Context, moduleRoot string) (string, error) {
	command := exec.CommandContext(ctx, "git", "-C", moduleRoot, "rev-parse", "--verify", "HEAD")
	output, err := command.CombinedOutput()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", fmt.Errorf("resolve typescript-go checkout commit: %w: %s", err, strings.TrimSpace(string(output)))
	}
	commit := strings.TrimSpace(string(output))
	decoded, err := hex.DecodeString(commit)
	if err != nil || len(decoded) != 20 || strings.ToLower(commit) != commit {
		return "", fmt.Errorf("resolve typescript-go checkout commit: git returned invalid SHA-1 %q", commit)
	}
	return commit, nil
}

// CollectSemanticFixtureDigests runs each manifest-listed frontend fixture and
// hashes its canonical check diagnostics, build diagnostics, and snapshot. The
// case expectations are verified before a digest can be returned, so the
// baseline update path cannot silently bless unexpected diagnostics or
// snapshot presence.
func CollectSemanticFixtureDigests(ctx context.Context, fixtureRoot string) (map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fixtureRoot, err := filepath.Abs(fixtureRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve semantic fixture root: %w", err)
	}
	info, err := os.Stat(fixtureRoot)
	if err != nil {
		return nil, fmt.Errorf("stat semantic fixture root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("semantic fixture root %q is not a directory", fixtureRoot)
	}

	manifest, err := readCompatibilityFixtureJSON[compatibilityFixtureManifest](filepath.Join(fixtureRoot, "manifest.json"))
	if err != nil {
		return nil, err
	}
	if manifest.SchemaVersion != semanticFixtureSchemaVersion {
		return nil, fmt.Errorf("semantic fixture manifest schema = %d, want %d", manifest.SchemaVersion, semanticFixtureSchemaVersion)
	}
	if len(manifest.Cases) == 0 {
		return nil, fmt.Errorf("semantic fixture manifest has no cases")
	}
	if !sort.StringsAreSorted(manifest.Cases) {
		return nil, fmt.Errorf("semantic fixture manifest cases must be path-sorted")
	}

	digests := make(map[string]string, len(manifest.Cases))
	seenPaths := make(map[string]struct{}, len(manifest.Cases))
	for _, requestedPath := range manifest.Cases {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		casePath, canonicalPath, err := resolveModulePath(fixtureRoot, requestedPath)
		if err != nil {
			return nil, fmt.Errorf("resolve semantic fixture %q: %w", requestedPath, err)
		}
		if canonicalPath != requestedPath || !strings.HasSuffix(canonicalPath, ".case.json") {
			return nil, fmt.Errorf("invalid semantic fixture manifest path %q", requestedPath)
		}
		if _, duplicate := seenPaths[canonicalPath]; duplicate {
			return nil, fmt.Errorf("duplicate semantic fixture path %q", canonicalPath)
		}
		seenPaths[canonicalPath] = struct{}{}

		fixture, err := readCompatibilityFixtureJSON[compatibilityFixtureCase](casePath)
		if err != nil {
			return nil, err
		}
		if err := validateCompatibilityFixture(canonicalPath, fixture); err != nil {
			return nil, err
		}
		if _, duplicate := digests[fixture.ID]; duplicate {
			return nil, fmt.Errorf("duplicate semantic fixture id %q", fixture.ID)
		}
		digest, err := collectSemanticFixtureDigest(ctx, fixtureRoot, fixture)
		if err != nil {
			return nil, fmt.Errorf("semantic fixture %q: %w", fixture.ID, err)
		}
		digests[fixture.ID] = digest
	}
	return digests, nil
}

func validateCompatibilityFixture(manifestPath string, fixture compatibilityFixtureCase) error {
	if fixture.SchemaVersion != semanticFixtureSchemaVersion {
		return fmt.Errorf("semantic fixture %q schema = %d, want %d", manifestPath, fixture.SchemaVersion, semanticFixtureSchemaVersion)
	}
	if strings.TrimSpace(fixture.ID) == "" || strings.TrimSpace(fixture.Category) == "" || strings.TrimSpace(fixture.Source) == "" {
		return fmt.Errorf("semantic fixture %q requires id, category, and source", manifestPath)
	}
	if fixture.Mode != "accept" && fixture.Mode != "reject" {
		return fmt.Errorf("semantic fixture %q has invalid mode %q", fixture.ID, fixture.Mode)
	}
	if len(fixture.Handbook) == 0 || len(fixture.ASTGroups) == 0 || fixture.Profile == "" || strings.TrimSpace(fixture.Target) == "" || strings.TrimSpace(fixture.Runtime) == "" || len(fixture.Artifacts) == 0 || strings.TrimSpace(fixture.Oracle) == "" || fixture.TimeoutMS <= 0 || len(fixture.Requires) == 0 {
		return fmt.Errorf("semantic fixture %q requires handbook, astGroups, profile, target, runtime, artifacts, oracle, positive timeoutMs, and requires", fixture.ID)
	}
	if fixture.Profile != ProfileStatic && fixture.Profile != ProfileInterop && fixture.Profile != ProfileUnsafe {
		return fmt.Errorf("semantic fixture %q has unsupported profile %q", fixture.ID, fixture.Profile)
	}
	for label, values := range map[string][]string{
		"features": fixture.Features, "handbook": fixture.Handbook, "astGroups": fixture.ASTGroups,
		"artifacts": fixture.Artifacts, "requires": fixture.Requires,
	} {
		if len(values) == 0 || !sort.StringsAreSorted(values) {
			return fmt.Errorf("semantic fixture %q %s must be non-empty and sorted", fixture.ID, label)
		}
		for index := 1; index < len(values); index++ {
			if values[index-1] == values[index] {
				return fmt.Errorf("semantic fixture %q %s contains duplicate %q", fixture.ID, label, values[index])
			}
		}
	}
	source := filepath.ToSlash(filepath.Clean(fixture.Source))
	if source != fixture.Source || !isCompatibilityFixtureSourceExtension(filepath.Ext(source)) {
		return fmt.Errorf("semantic fixture %q has invalid source %q", fixture.ID, fixture.Source)
	}
	caseStem := strings.TrimSuffix(manifestPath, ".case.json")
	if strings.TrimSuffix(source, filepath.Ext(source)) != caseStem {
		return fmt.Errorf("semantic fixture %q source %q is not paired with %q", fixture.ID, source, manifestPath)
	}
	files := compatibilityFixtureFiles(fixture)
	if !slices.Contains(files, source) {
		return fmt.Errorf("semantic fixture %q files do not contain source %q", fixture.ID, source)
	}
	for label, codes := range map[string][]string{"checkCodes": fixture.Expect.CheckCodes, "buildCodes": fixture.Expect.BuildCodes} {
		if !sort.StringsAreSorted(codes) {
			return fmt.Errorf("semantic fixture %q %s must be sorted", fixture.ID, label)
		}
		for index := 1; index < len(codes); index++ {
			if codes[index-1] == codes[index] {
				return fmt.Errorf("semantic fixture %q %s contains duplicate %q", fixture.ID, label, codes[index])
			}
		}
	}
	return nil
}

func isCompatibilityFixtureSourceExtension(extension string) bool {
	switch extension {
	case ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs":
		return true
	default:
		return false
	}
}

func collectSemanticFixtureDigest(ctx context.Context, fixtureRoot string, fixture compatibilityFixtureCase) (string, error) {
	request, err := compatibilityFixtureBuildRequest(fixtureRoot, fixture)
	if err != nil {
		return "", err
	}
	check, err := Check(ctx, request)
	if err != nil {
		return "", fmt.Errorf("check: %w", err)
	}
	checkCodes := compatibilityDiagnosticCodes(check.Diagnostics)
	if !slices.Equal(checkCodes, fixture.Expect.CheckCodes) {
		return "", compatibilityFixtureDiagnosticMismatch("check", checkCodes, fixture.Expect.CheckCodes, check.Diagnostics)
	}
	checkBytes, err := CanonicalDiagnosticsJSON(check.Diagnostics)
	if err != nil {
		return "", fmt.Errorf("encode check diagnostics: %w", err)
	}

	frontend := NewFrontend(request.FileSystem, bundled.LibPath(), TypeScriptGoCommit, StandardLibraryHash)
	snapshot, diagnostics := frontend.Build(ctx, request)
	buildCodes := compatibilityDiagnosticCodes(diagnostics)
	if !slices.Equal(buildCodes, fixture.Expect.BuildCodes) {
		return "", compatibilityFixtureDiagnosticMismatch("build", buildCodes, fixture.Expect.BuildCodes, diagnostics)
	}
	if (snapshot != nil) != fixture.Expect.Snapshot {
		return "", fmt.Errorf("snapshot present = %t, want %t", snapshot != nil, fixture.Expect.Snapshot)
	}
	secondFrontend := NewFrontend(request.FileSystem, bundled.LibPath(), TypeScriptGoCommit, StandardLibraryHash)
	secondSnapshot, secondDiagnostics := secondFrontend.Build(ctx, request)
	if (secondSnapshot != nil) != (snapshot != nil) {
		return "", fmt.Errorf("repeated build changed snapshot presence from %t to %t", snapshot != nil, secondSnapshot != nil)
	}
	secondCheck, err := Check(ctx, request)
	if err != nil {
		return "", fmt.Errorf("repeated check: %w", err)
	}
	secondCheckBytes, err := CanonicalDiagnosticsJSON(secondCheck.Diagnostics)
	if err != nil {
		return "", fmt.Errorf("encode repeated check diagnostics: %w", err)
	}
	if !bytes.Equal(checkBytes, secondCheckBytes) {
		return "", fmt.Errorf("repeated check changed canonical diagnostics")
	}

	buildBytes, err := CanonicalDiagnosticsJSON(diagnostics)
	if err != nil {
		return "", fmt.Errorf("encode build diagnostics: %w", err)
	}
	secondBuildBytes, err := CanonicalDiagnosticsJSON(secondDiagnostics)
	if err != nil {
		return "", fmt.Errorf("encode repeated build diagnostics: %w", err)
	}
	if !bytes.Equal(buildBytes, secondBuildBytes) {
		return "", fmt.Errorf("repeated build changed canonical diagnostics")
	}
	var snapshotBytes []byte
	if snapshot != nil {
		snapshotBytes, err = canonicalSemanticSnapshotBytes(snapshot)
		if err != nil {
			return "", fmt.Errorf("encode snapshot: %w", err)
		}
		secondSnapshotBytes, err := canonicalSemanticSnapshotBytes(secondSnapshot)
		if err != nil {
			return "", fmt.Errorf("encode repeated snapshot: %w", err)
		}
		if !bytes.Equal(snapshotBytes, secondSnapshotBytes) {
			return "", fmt.Errorf("repeated build changed canonical semantic snapshot bytes")
		}
	}

	digest := sha256.New()
	writeSemanticDigestPart(digest, "domain", []byte(semanticFixtureDigestDomain))
	writeSemanticDigestPart(digest, "fixture", []byte(fixture.ID))
	writeSemanticDigestPart(digest, "check-diagnostics", checkBytes)
	writeSemanticDigestPart(digest, "build-diagnostics", buildBytes)
	if snapshot == nil {
		writeSemanticDigestPart(digest, "snapshot-presence", []byte("absent"))
	} else {
		writeSemanticDigestPart(digest, "snapshot-presence", []byte("present"))
	}
	writeSemanticDigestPart(digest, "snapshot", snapshotBytes)
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func canonicalSemanticSnapshotBytes(snapshot *ProgramSnapshot) ([]byte, error) {
	semantic := *snapshot
	// Provenance is audited independently by the lock, Kind, and stdlib
	// categories. Excluding it here prevents one provenance change from being
	// reported again as a change to every semantic fixture.
	semantic.Provenance = ProvenanceSnapshot{}
	semantic.ContentHash = ""
	return semantic.CanonicalBytes()
}

func compatibilityFixtureBuildRequest(fixtureRoot string, fixture compatibilityFixtureCase) (BuildRequest, error) {
	files := make(map[string]string, len(compatibilityFixtureFiles(fixture))+1)
	seen := make(map[string]struct{}, len(compatibilityFixtureFiles(fixture)))
	for _, requestedPath := range compatibilityFixtureFiles(fixture) {
		path, canonicalPath, err := resolveModulePath(fixtureRoot, requestedPath)
		if err != nil {
			return BuildRequest{}, fmt.Errorf("resolve fixture file %q: %w", requestedPath, err)
		}
		if canonicalPath != requestedPath {
			return BuildRequest{}, fmt.Errorf("fixture file path %q is not canonical", requestedPath)
		}
		if _, duplicate := seen[canonicalPath]; duplicate {
			return BuildRequest{}, fmt.Errorf("duplicate fixture file %q", canonicalPath)
		}
		seen[canonicalPath] = struct{}{}
		contents, err := os.ReadFile(path)
		if err != nil {
			return BuildRequest{}, fmt.Errorf("read fixture file %q: %w", canonicalPath, err)
		}
		files["/project/"+canonicalPath] = string(contents)
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
		return BuildRequest{}, fmt.Errorf("encode fixture config: %w", err)
	}
	files["/project/tsconfig.json"] = string(config)
	return BuildRequest{
		ConfigPath:       "/project/tsconfig.json",
		CurrentDirectory: "/project",
		ProjectRoot:      "/project",
		FileSystem:       vfstest.FromMap(files, true),
	}, nil
}

func compatibilityFixtureFiles(fixture compatibilityFixtureCase) []string {
	if len(fixture.Files) == 0 {
		return []string{filepath.ToSlash(filepath.Clean(fixture.Source))}
	}
	files := make([]string, len(fixture.Files))
	for index, name := range fixture.Files {
		files[index] = filepath.ToSlash(filepath.Clean(name))
	}
	return files
}

func compatibilityDiagnosticCodes(diagnostics []Diagnostic) []string {
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

func compatibilityFixtureDiagnosticMismatch(label string, got, want []string, diagnostics []Diagnostic) error {
	encoded, err := CanonicalDiagnosticsJSON(diagnostics)
	if err != nil {
		return fmt.Errorf("%s diagnostic codes = %v, want %v (encode diagnostics: %v)", label, got, want, err)
	}
	return fmt.Errorf("%s diagnostic codes = %v, want %v; diagnostics = %s", label, got, want, encoded)
}

func writeSemanticDigestPart(writer io.Writer, label string, data []byte) {
	_, _ = io.WriteString(writer, label)
	_, _ = writer.Write([]byte{0})
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(data)))
	_, _ = writer.Write(size[:])
	_, _ = writer.Write(data)
}

func readCompatibilityFixtureJSON[T any](path string) (T, error) {
	var value T
	file, err := os.Open(path)
	if err != nil {
		return value, fmt.Errorf("open semantic fixture JSON %q: %w", path, err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, fmt.Errorf("decode semantic fixture JSON %q: %w", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return value, fmt.Errorf("decode semantic fixture JSON %q: multiple JSON values", path)
		}
		return value, fmt.Errorf("decode semantic fixture JSON %q: %w", path, err)
	}
	return value, nil
}
