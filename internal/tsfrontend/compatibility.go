package tsfrontend

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	goast "go/ast"
	"go/build/constraint"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	tsast "github.com/microsoft/typescript-go/internal/ast"
)

// CompatibilitySchemaVersion is the serialized upstream compatibility
// snapshot and report contract. Readers must reject unsupported versions.
const CompatibilitySchemaVersion uint32 = 1

// CompatibilityCategory identifies one independently auditable upstream
// surface. Its order is part of the stable report contract.
type CompatibilityCategory string

const (
	CompatibilityCategoryLock     CompatibilityCategory = "lock"
	CompatibilityCategoryKind     CompatibilityCategory = "kind"
	CompatibilityCategoryAPI      CompatibilityCategory = "api"
	CompatibilityCategoryStdlib   CompatibilityCategory = "stdlib"
	CompatibilityCategorySemantic CompatibilityCategory = "semantic"
)

// CompatibilityChangeKind describes how a keyed compatibility fact changed.
type CompatibilityChangeKind string

const (
	CompatibilityChangeAdded   CompatibilityChangeKind = "added"
	CompatibilityChangeRemoved CompatibilityChangeKind = "removed"
	CompatibilityChangeChanged CompatibilityChangeKind = "changed"
)

// CompatibilityEntry is a stable keyed fact. Value is a normalized signature,
// numeric Kind value, or digest depending on the containing category.
type CompatibilityEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// CompatibilitySnapshot records the upstream surfaces that can invalidate a
// typed snapshot or frontend adapter. All entry lists are key sorted.
type CompatibilitySnapshot struct {
	SchemaVersion      uint32 `json:"schemaVersion"`
	TypeScriptGoCommit string `json:"typescriptGoCommit"`
	// ObservedCheckoutCommit records the local checkout revision used while
	// collecting this snapshot. ExpectedCheckoutCommit is supplied by the
	// repository lock at the command boundary. Both are intentionally excluded
	// from the checked-in baseline so committing the baseline cannot make it
	// self-invalidating.
	ObservedCheckoutCommit string               `json:"-"`
	ExpectedCheckoutCommit string               `json:"-"`
	Kinds                  []CompatibilityEntry `json:"kinds"`
	API                    []CompatibilityEntry `json:"api"`
	Stdlib                 []CompatibilityEntry `json:"stdlib"`
	Semantics              []CompatibilityEntry `json:"semantics"`
}

// CompatibilityChange is one stable, categorized baseline-to-current change.
type CompatibilityChange struct {
	Category CompatibilityCategory   `json:"category"`
	Kind     CompatibilityChangeKind `json:"kind"`
	Key      string                  `json:"key"`
	Before   string                  `json:"before,omitempty"`
	After    string                  `json:"after,omitempty"`
}

// CompatibilityReport is the deterministic output of comparing two snapshots.
type CompatibilityReport struct {
	SchemaVersion          uint32                `json:"schemaVersion"`
	Compatible             bool                  `json:"compatible"`
	BaselineCommit         string                `json:"baselineCommit"`
	CurrentCommit          string                `json:"currentCommit"`
	ExpectedCheckoutCommit string                `json:"expectedCheckoutCommit,omitempty"`
	ObservedCheckoutCommit string                `json:"observedCheckoutCommit,omitempty"`
	Changes                []CompatibilityChange `json:"changes"`
}

// CompatibilityCollectionOptions selects the checkout surfaces to capture.
// SemanticDigests is supplied by the typed fixture runner so this package does
// not retain checker objects or depend on snapshot construction internals.
type CompatibilityCollectionOptions struct {
	ModuleRoot         string
	TypeScriptGoCommit string
	APIPackages        []string
	StdlibRoot         string
	SemanticDigests    map[string]string
}

// DefaultCompatibilityAPIPackages returns the tsgo packages whose exported
// declarations form the ts2bin frontend adapter boundary.
func DefaultCompatibilityAPIPackages() []string {
	return []string{
		"internal/ast",
		"internal/checker",
		"internal/compiler",
		"internal/tsoptions",
	}
}

// CollectCompatibilitySnapshot captures the current checkout without invoking
// Git or retaining any live frontend state. The caller supplies the resolved
// commit so lock-file provenance remains an explicit boundary.
func CollectCompatibilitySnapshot(options CompatibilityCollectionOptions) (CompatibilitySnapshot, error) {
	moduleRoot, err := filepath.Abs(options.ModuleRoot)
	if err != nil {
		return CompatibilitySnapshot{}, fmt.Errorf("resolve module root: %w", err)
	}
	info, err := os.Stat(moduleRoot)
	if err != nil {
		return CompatibilitySnapshot{}, fmt.Errorf("stat module root: %w", err)
	}
	if !info.IsDir() {
		return CompatibilitySnapshot{}, fmt.Errorf("module root %q is not a directory", moduleRoot)
	}

	packages := options.APIPackages
	if packages == nil {
		packages = DefaultCompatibilityAPIPackages()
	}
	api, err := CollectAPICompatibility(moduleRoot, packages)
	if err != nil {
		return CompatibilitySnapshot{}, err
	}

	stdlibRoot := options.StdlibRoot
	if stdlibRoot == "" {
		stdlibRoot = "internal/bundled/libs"
	}
	stdlibDir, _, err := resolveModulePath(moduleRoot, stdlibRoot)
	if err != nil {
		return CompatibilitySnapshot{}, fmt.Errorf("resolve stdlib root: %w", err)
	}
	stdlib, err := CollectStdlibCompatibility(stdlibDir)
	if err != nil {
		return CompatibilitySnapshot{}, err
	}
	semantics, err := CollectSemanticCompatibility(options.SemanticDigests)
	if err != nil {
		return CompatibilitySnapshot{}, err
	}

	return NormalizeCompatibilitySnapshot(CompatibilitySnapshot{
		SchemaVersion:      CompatibilitySchemaVersion,
		TypeScriptGoCommit: options.TypeScriptGoCommit,
		Kinds:              CollectASTKindCompatibility(),
		API:                api,
		Stdlib:             stdlib,
		Semantics:          semantics,
	})
}

// CollectASTKindCompatibility enumerates every real ast.Kind value. KindCount
// is a sentinel and is intentionally excluded.
func CollectASTKindCompatibility() []CompatibilityEntry {
	entries := make([]CompatibilityEntry, 0, int(tsast.KindCount))
	for kind := tsast.Kind(0); kind < tsast.KindCount; kind++ {
		entries = append(entries, CompatibilityEntry{
			Key:   kind.String(),
			Value: strconv.Itoa(int(kind)),
		})
	}
	return entries
}

// CollectAPICompatibility parses exported declarations from package directories
// beneath moduleRoot. It parses source syntax rather than generated text, so
// comments and source file ordering cannot affect the result. Mutually exclusive
// build-tag variants are retained as sorted alternative signatures.
func CollectAPICompatibility(moduleRoot string, packagePaths []string) ([]CompatibilityEntry, error) {
	moduleRoot, err := filepath.Abs(moduleRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve module root: %w", err)
	}

	seenPackages := make(map[string]struct{}, len(packagePaths))
	variants := make(map[string]map[string]struct{})
	for _, packagePath := range packagePaths {
		packageDir, canonicalPath, err := resolveModulePath(moduleRoot, packagePath)
		if err != nil {
			return nil, fmt.Errorf("resolve API package %q: %w", packagePath, err)
		}
		if _, exists := seenPackages[canonicalPath]; exists {
			return nil, fmt.Errorf("duplicate API package %q", canonicalPath)
		}
		seenPackages[canonicalPath] = struct{}{}
		if err := collectPackageAPI(packageDir, canonicalPath, variants); err != nil {
			return nil, err
		}
	}

	keys := make([]string, 0, len(variants))
	for key := range variants {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	entries := make([]CompatibilityEntry, 0, len(keys))
	for _, key := range keys {
		values := make([]string, 0, len(variants[key]))
		for value := range variants[key] {
			values = append(values, value)
		}
		sort.Strings(values)
		entries = append(entries, CompatibilityEntry{Key: key, Value: strings.Join(values, "\n||\n")})
	}
	return entries, nil
}

func collectPackageAPI(packageDir string, packagePath string, variants map[string]map[string]struct{}) error {
	entries, err := os.ReadDir(packageDir)
	if err != nil {
		return fmt.Errorf("read API package %q: %w", packagePath, err)
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go") {
			files = append(files, entry.Name())
		}
	}
	sort.Strings(files)
	if len(files) == 0 {
		return fmt.Errorf("API package %q contains no non-test Go files", packagePath)
	}

	packageName := ""
	for _, name := range files {
		filename := filepath.Join(packageDir, name)
		fset := token.NewFileSet()
		commentedFile, err := parser.ParseFile(fset, filename, nil, parser.SkipObjectResolution|parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse API file %q: %w", filename, err)
		}
		buildConstraint, err := compatibilityBuildConstraint(commentedFile)
		if err != nil {
			return fmt.Errorf("parse API file %q build constraint: %w", filename, err)
		}
		buildConstraint = combineCompatibilityBuildConstraints(buildConstraint, compatibilityFilenameBuildConstraint(name))
		// Parse a second AST without comments for the signature projection. This
		// keeps documentation edits out of the API digest while the separately
		// captured build constraint remains part of each variant.
		fset = token.NewFileSet()
		file, err := parser.ParseFile(fset, filename, nil, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse API file %q: %w", filename, err)
		}
		if packageName == "" {
			packageName = file.Name.Name
		} else if file.Name.Name != packageName {
			return fmt.Errorf("API package %q contains package %q and %q", packagePath, packageName, file.Name.Name)
		}
		if err := collectFileAPI(fset, file, packagePath, buildConstraint, variants); err != nil {
			return fmt.Errorf("collect API file %q: %w", filename, err)
		}
	}
	return nil
}

func collectFileAPI(fset *token.FileSet, file *goast.File, packagePath string, buildConstraint string, variants map[string]map[string]struct{}) error {
	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *goast.FuncDecl:
			if !goast.IsExported(declaration.Name.Name) {
				continue
			}
			functionType, err := formatNode(fset, declaration.Type)
			if err != nil {
				return err
			}
			key := packagePath + "." + declaration.Name.Name
			value := functionType
			if declaration.Recv != nil && len(declaration.Recv.List) != 0 {
				receiver, err := formatNode(fset, declaration.Recv.List[0].Type)
				if err != nil {
					return err
				}
				key = packagePath + "." + receiverBaseName(declaration.Recv.List[0].Type, receiver) + "." + declaration.Name.Name
				value = "recv " + receiver + " " + functionType
			}
			addAPIVariant(variants, key, compatibilityAPIVariant(buildConstraint, value))
		case *goast.GenDecl:
			switch declaration.Tok {
			case token.TYPE:
				for _, rawSpec := range declaration.Specs {
					spec := rawSpec.(*goast.TypeSpec)
					if !goast.IsExported(spec.Name.Name) {
						continue
					}
					signature, err := formatNode(fset, spec)
					if err != nil {
						return err
					}
					addAPIVariant(variants, packagePath+"."+spec.Name.Name, compatibilityAPIVariant(buildConstraint, "type "+signature))
				}
			case token.CONST, token.VAR:
				if err := collectValueAPI(fset, declaration, packagePath, buildConstraint, variants); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func collectValueAPI(fset *token.FileSet, declaration *goast.GenDecl, packagePath string, buildConstraint string, variants map[string]map[string]struct{}) error {
	var previousType goast.Expr
	var previousValues []goast.Expr
	for specIndex, rawSpec := range declaration.Specs {
		spec := rawSpec.(*goast.ValueSpec)
		effectiveType := spec.Type
		effectiveValues := spec.Values
		if declaration.Tok == token.CONST {
			if spec.Type == nil && len(spec.Values) == 0 {
				effectiveType = previousType
				effectiveValues = previousValues
			} else {
				previousType = spec.Type
				previousValues = spec.Values
			}
		}
		for index, name := range spec.Names {
			if !goast.IsExported(name.Name) {
				continue
			}
			values := effectiveValues
			if len(effectiveValues) == len(spec.Names) {
				values = []goast.Expr{effectiveValues[index]}
			}
			individual := &goast.ValueSpec{
				Names:  []*goast.Ident{goast.NewIdent(name.Name)},
				Type:   effectiveType,
				Values: values,
			}
			signature, err := formatNode(fset, individual)
			if err != nil {
				return err
			}
			value := declaration.Tok.String() + " " + signature
			if declaration.Tok == token.CONST && expressionsUseIota(effectiveValues) {
				value = fmt.Sprintf("iota=%d %s", specIndex, value)
			}
			addAPIVariant(variants, packagePath+"."+name.Name, compatibilityAPIVariant(buildConstraint, value))
		}
	}
	return nil
}

func expressionsUseIota(expressions []goast.Expr) bool {
	usesIota := false
	for _, expression := range expressions {
		goast.Inspect(expression, func(node goast.Node) bool {
			identifier, ok := node.(*goast.Ident)
			if ok && identifier.Name == "iota" {
				usesIota = true
				return false
			}
			return !usesIota
		})
		if usesIota {
			return true
		}
	}
	return false
}

func compatibilityBuildConstraint(file *goast.File) (string, error) {
	var goBuild string
	plusBuild := make([]string, 0)
	for _, group := range file.Comments {
		if group.Pos() > file.Package {
			break
		}
		for _, comment := range group.List {
			line := strings.TrimSpace(comment.Text)
			switch {
			case constraint.IsGoBuild(line):
				if goBuild != "" {
					return "", fmt.Errorf("multiple //go:build lines")
				}
				expression, err := constraint.Parse(line)
				if err != nil {
					return "", err
				}
				goBuild = expression.String()
			case constraint.IsPlusBuild(line):
				expression, err := constraint.Parse(line)
				if err != nil {
					return "", err
				}
				plusBuild = append(plusBuild, "("+expression.String()+")")
			}
		}
	}
	if goBuild != "" {
		return goBuild, nil
	}
	return strings.Join(plusBuild, " && "), nil
}

func compatibilityAPIVariant(buildConstraint string, value string) string {
	if buildConstraint == "" {
		return value
	}
	return "build " + buildConstraint + "\n" + value
}

func compatibilityFilenameBuildConstraint(name string) string {
	base := strings.TrimSuffix(name, ".go")
	parts := strings.Split(base, "_")
	if len(parts) < 2 {
		return ""
	}
	last := parts[len(parts)-1]
	if compatibilityGOOS[last] {
		return last
	}
	if !compatibilityGOARCH[last] {
		return ""
	}
	if len(parts) >= 3 {
		previous := parts[len(parts)-2]
		if compatibilityGOOS[previous] {
			return previous + " && " + last
		}
	}
	return last
}

func combineCompatibilityBuildConstraints(explicit string, filename string) string {
	switch {
	case explicit == "":
		return filename
	case filename == "", explicit == filename:
		return explicit
	default:
		return "(" + explicit + ") && (" + filename + ")"
	}
}

var compatibilityGOOS = map[string]bool{
	"aix": true, "android": true, "darwin": true, "dragonfly": true,
	"freebsd": true, "illumos": true, "ios": true, "js": true,
	"linux": true, "netbsd": true, "openbsd": true, "plan9": true,
	"solaris": true, "wasip1": true, "windows": true,
}

var compatibilityGOARCH = map[string]bool{
	"386": true, "amd64": true, "arm": true, "arm64": true,
	"loong64": true, "mips": true, "mips64": true, "mips64le": true,
	"mipsle": true, "ppc64": true, "ppc64le": true, "riscv64": true,
	"s390x": true, "wasm": true,
}

func receiverBaseName(expression goast.Expr, fallback string) string {
	switch expression := expression.(type) {
	case *goast.Ident:
		return expression.Name
	case *goast.StarExpr:
		return receiverBaseName(expression.X, fallback)
	case *goast.IndexExpr:
		return receiverBaseName(expression.X, fallback)
	case *goast.IndexListExpr:
		return receiverBaseName(expression.X, fallback)
	case *goast.SelectorExpr:
		return expression.Sel.Name
	default:
		return fallback
	}
}

func addAPIVariant(variants map[string]map[string]struct{}, key string, value string) {
	values := variants[key]
	if values == nil {
		values = make(map[string]struct{})
		variants[key] = values
	}
	values[value] = struct{}{}
}

func formatNode(fset *token.FileSet, node any) (string, error) {
	var savedParameterNames []savedFieldNames
	if astNode, ok := node.(goast.Node); ok {
		goast.Inspect(astNode, func(astNode goast.Node) bool {
			functionType, ok := astNode.(*goast.FuncType)
			if !ok {
				return true
			}
			for _, fields := range []*goast.FieldList{functionType.Params, functionType.Results} {
				if fields == nil {
					continue
				}
				for _, field := range fields.List {
					savedParameterNames = append(savedParameterNames, savedFieldNames{field: field, names: field.Names})
					field.Names = nil
				}
			}
			return true
		})
		defer func() {
			for index := len(savedParameterNames) - 1; index >= 0; index-- {
				saved := savedParameterNames[index]
				saved.field.Names = saved.names
			}
		}()
	}
	var buffer bytes.Buffer
	if err := format.Node(&buffer, fset, node); err != nil {
		return "", fmt.Errorf("format API declaration: %w", err)
	}
	return buffer.String(), nil
}

type savedFieldNames struct {
	field *goast.Field
	names []*goast.Ident
}

// CollectStdlibCompatibility hashes every declaration file under root by its
// canonical relative slash-separated path.
func CollectStdlibCompatibility(root string) ([]CompatibilityEntry, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve stdlib root: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat stdlib root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("stdlib root %q is not a directory", root)
	}

	entries := make([]CompatibilityEntry, 0)
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".d.ts") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(content)
		entries = append(entries, CompatibilityEntry{
			Key:   filepath.ToSlash(relative),
			Value: hex.EncodeToString(sum[:]),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("collect stdlib compatibility: %w", err)
	}
	sortEntries(entries)
	return entries, nil
}

// CollectSemanticCompatibility copies caller-provided fixture digests into a
// sorted entry list.
func CollectSemanticCompatibility(digests map[string]string) ([]CompatibilityEntry, error) {
	entries := make([]CompatibilityEntry, 0, len(digests))
	for key, digest := range digests {
		entries = append(entries, CompatibilityEntry{Key: key, Value: digest})
	}
	return normalizeEntries(entries, CompatibilityCategorySemantic)
}

// NormalizeCompatibilitySnapshot validates a snapshot and returns a sorted,
// detached copy suitable for comparison or serialization.
func NormalizeCompatibilitySnapshot(snapshot CompatibilitySnapshot) (CompatibilitySnapshot, error) {
	if snapshot.SchemaVersion != CompatibilitySchemaVersion {
		return CompatibilitySnapshot{}, fmt.Errorf("unsupported compatibility schema %d", snapshot.SchemaVersion)
	}
	if err := validateCommit(snapshot.TypeScriptGoCommit); err != nil {
		return CompatibilitySnapshot{}, fmt.Errorf("typescript-go commit: %w", err)
	}
	if snapshot.ObservedCheckoutCommit != "" {
		if err := validateCommit(snapshot.ObservedCheckoutCommit); err != nil {
			return CompatibilitySnapshot{}, fmt.Errorf("observed checkout commit: %w", err)
		}
	}
	if snapshot.ExpectedCheckoutCommit != "" {
		if err := validateCommit(snapshot.ExpectedCheckoutCommit); err != nil {
			return CompatibilitySnapshot{}, fmt.Errorf("expected checkout commit: %w", err)
		}
	}

	var err error
	if snapshot.Kinds, err = normalizeEntries(snapshot.Kinds, CompatibilityCategoryKind); err != nil {
		return CompatibilitySnapshot{}, err
	}
	if snapshot.API, err = normalizeEntries(snapshot.API, CompatibilityCategoryAPI); err != nil {
		return CompatibilitySnapshot{}, err
	}
	if snapshot.Stdlib, err = normalizeEntries(snapshot.Stdlib, CompatibilityCategoryStdlib); err != nil {
		return CompatibilitySnapshot{}, err
	}
	if snapshot.Semantics, err = normalizeEntries(snapshot.Semantics, CompatibilityCategorySemantic); err != nil {
		return CompatibilitySnapshot{}, err
	}
	return snapshot, nil
}

// CompareCompatibilitySnapshots returns every lock, Kind, API, stdlib, and
// semantic change in deterministic category/key/change order.
func CompareCompatibilitySnapshots(baseline CompatibilitySnapshot, current CompatibilitySnapshot) (CompatibilityReport, error) {
	baseline, err := NormalizeCompatibilitySnapshot(baseline)
	if err != nil {
		return CompatibilityReport{}, fmt.Errorf("baseline: %w", err)
	}
	current, err = NormalizeCompatibilitySnapshot(current)
	if err != nil {
		return CompatibilityReport{}, fmt.Errorf("current: %w", err)
	}

	report := CompatibilityReport{
		SchemaVersion:          CompatibilitySchemaVersion,
		BaselineCommit:         baseline.TypeScriptGoCommit,
		CurrentCommit:          current.TypeScriptGoCommit,
		ExpectedCheckoutCommit: current.ExpectedCheckoutCommit,
		ObservedCheckoutCommit: current.ObservedCheckoutCommit,
		Changes:                make([]CompatibilityChange, 0),
	}
	if baseline.TypeScriptGoCommit != current.TypeScriptGoCommit {
		report.Changes = append(report.Changes, CompatibilityChange{
			Category: CompatibilityCategoryLock,
			Kind:     CompatibilityChangeChanged,
			Key:      "typescript-go.commit",
			Before:   baseline.TypeScriptGoCommit,
			After:    current.TypeScriptGoCommit,
		})
	}
	if current.ExpectedCheckoutCommit != "" && current.ObservedCheckoutCommit != "" &&
		current.ObservedCheckoutCommit != current.ExpectedCheckoutCommit {
		report.Changes = append(report.Changes, CompatibilityChange{
			Category: CompatibilityCategoryLock,
			Kind:     CompatibilityChangeChanged,
			Key:      "typescript-go.checkout",
			Before:   current.ExpectedCheckoutCommit,
			After:    current.ObservedCheckoutCommit,
		})
	}
	report.Changes = append(report.Changes, diffEntries(CompatibilityCategoryKind, baseline.Kinds, current.Kinds)...)
	report.Changes = append(report.Changes, diffEntries(CompatibilityCategoryAPI, baseline.API, current.API)...)
	report.Changes = append(report.Changes, diffEntries(CompatibilityCategoryStdlib, baseline.Stdlib, current.Stdlib)...)
	report.Changes = append(report.Changes, diffEntries(CompatibilityCategorySemantic, baseline.Semantics, current.Semantics)...)
	sortChanges(report.Changes)
	report.Compatible = len(report.Changes) == 0
	return report, nil
}

func diffEntries(category CompatibilityCategory, baseline []CompatibilityEntry, current []CompatibilityEntry) []CompatibilityChange {
	baselineByKey := make(map[string]string, len(baseline))
	currentByKey := make(map[string]string, len(current))
	keys := make(map[string]struct{}, len(baseline)+len(current))
	for _, entry := range baseline {
		baselineByKey[entry.Key] = entry.Value
		keys[entry.Key] = struct{}{}
	}
	for _, entry := range current {
		currentByKey[entry.Key] = entry.Value
		keys[entry.Key] = struct{}{}
	}

	changes := make([]CompatibilityChange, 0)
	for key := range keys {
		before, hadBefore := baselineByKey[key]
		after, hasAfter := currentByKey[key]
		switch {
		case !hadBefore:
			changes = append(changes, CompatibilityChange{Category: category, Kind: CompatibilityChangeAdded, Key: key, After: after})
		case !hasAfter:
			changes = append(changes, CompatibilityChange{Category: category, Kind: CompatibilityChangeRemoved, Key: key, Before: before})
		case before != after:
			changes = append(changes, CompatibilityChange{Category: category, Kind: CompatibilityChangeChanged, Key: key, Before: before, After: after})
		}
	}
	return changes
}

// MarshalCompatibilitySnapshot emits stable JSON after validating and sorting
// a detached snapshot copy.
func MarshalCompatibilitySnapshot(snapshot CompatibilitySnapshot) ([]byte, error) {
	normalized, err := NormalizeCompatibilitySnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(normalized, "", "  ")
}

// ParseCompatibilitySnapshot decodes one strict snapshot and normalizes it.
func ParseCompatibilitySnapshot(data []byte) (CompatibilitySnapshot, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var snapshot CompatibilitySnapshot
	if err := decoder.Decode(&snapshot); err != nil {
		return CompatibilitySnapshot{}, fmt.Errorf("decode compatibility snapshot: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return CompatibilitySnapshot{}, err
	}
	return NormalizeCompatibilitySnapshot(snapshot)
}

// MarshalCompatibilityReport emits stable JSON after validating and sorting a
// detached report copy.
func MarshalCompatibilityReport(report CompatibilityReport) ([]byte, error) {
	normalized, err := normalizeCompatibilityReport(report)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(normalized, "", "  ")
}

func normalizeCompatibilityReport(report CompatibilityReport) (CompatibilityReport, error) {
	if report.SchemaVersion != CompatibilitySchemaVersion {
		return CompatibilityReport{}, fmt.Errorf("unsupported compatibility schema %d", report.SchemaVersion)
	}
	if err := validateCommit(report.BaselineCommit); err != nil {
		return CompatibilityReport{}, fmt.Errorf("baseline commit: %w", err)
	}
	if err := validateCommit(report.CurrentCommit); err != nil {
		return CompatibilityReport{}, fmt.Errorf("current commit: %w", err)
	}
	if report.ObservedCheckoutCommit != "" {
		if err := validateCommit(report.ObservedCheckoutCommit); err != nil {
			return CompatibilityReport{}, fmt.Errorf("observed checkout commit: %w", err)
		}
	}
	report.Changes = append([]CompatibilityChange(nil), report.Changes...)
	seen := make(map[string]struct{}, len(report.Changes))
	for _, change := range report.Changes {
		if categoryOrder(change.Category) < 0 {
			return CompatibilityReport{}, fmt.Errorf("unknown compatibility category %q", change.Category)
		}
		if changeOrder(change.Kind) < 0 {
			return CompatibilityReport{}, fmt.Errorf("unknown compatibility change kind %q", change.Kind)
		}
		if strings.TrimSpace(change.Key) == "" {
			return CompatibilityReport{}, fmt.Errorf("%s change has empty key", change.Category)
		}
		identity := string(change.Category) + "\x00" + change.Key
		if _, exists := seen[identity]; exists {
			return CompatibilityReport{}, fmt.Errorf("duplicate %s change key %q", change.Category, change.Key)
		}
		seen[identity] = struct{}{}
	}
	sortChanges(report.Changes)
	if report.Changes == nil {
		report.Changes = make([]CompatibilityChange, 0)
	}
	report.Compatible = len(report.Changes) == 0
	return report, nil
}

func normalizeEntries(entries []CompatibilityEntry, category CompatibilityCategory) ([]CompatibilityEntry, error) {
	result := append([]CompatibilityEntry(nil), entries...)
	sortEntries(result)
	if result == nil {
		result = make([]CompatibilityEntry, 0)
	}
	for index, entry := range result {
		if strings.TrimSpace(entry.Key) == "" {
			return nil, fmt.Errorf("%s entry has empty key", category)
		}
		if entry.Value == "" {
			return nil, fmt.Errorf("%s entry %q has empty value", category, entry.Key)
		}
		if index != 0 && result[index-1].Key == entry.Key {
			return nil, fmt.Errorf("duplicate %s entry key %q", category, entry.Key)
		}
	}
	return result, nil
}

func sortEntries(entries []CompatibilityEntry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Key < entries[j].Key
	})
}

func sortChanges(changes []CompatibilityChange) {
	sort.Slice(changes, func(i, j int) bool {
		leftCategory := categoryOrder(changes[i].Category)
		rightCategory := categoryOrder(changes[j].Category)
		if leftCategory != rightCategory {
			return leftCategory < rightCategory
		}
		if changes[i].Key != changes[j].Key {
			return changes[i].Key < changes[j].Key
		}
		return changeOrder(changes[i].Kind) < changeOrder(changes[j].Kind)
	})
}

func categoryOrder(category CompatibilityCategory) int {
	switch category {
	case CompatibilityCategoryLock:
		return 0
	case CompatibilityCategoryKind:
		return 1
	case CompatibilityCategoryAPI:
		return 2
	case CompatibilityCategoryStdlib:
		return 3
	case CompatibilityCategorySemantic:
		return 4
	default:
		return -1
	}
}

func changeOrder(kind CompatibilityChangeKind) int {
	switch kind {
	case CompatibilityChangeAdded:
		return 0
	case CompatibilityChangeRemoved:
		return 1
	case CompatibilityChangeChanged:
		return 2
	default:
		return -1
	}
}

func validateCommit(commit string) error {
	decoded, err := hex.DecodeString(commit)
	if err != nil || len(decoded) != 20 || strings.ToLower(commit) != commit {
		return fmt.Errorf("commit %q is not a lowercase 40-character SHA-1", commit)
	}
	return nil
}

func resolveModulePath(moduleRoot string, requested string) (absolute string, canonical string, err error) {
	if requested == "" {
		return "", "", fmt.Errorf("path is empty")
	}
	localPath := filepath.FromSlash(requested)
	if filepath.IsAbs(localPath) {
		return "", "", fmt.Errorf("path must be module-relative")
	}
	cleaned := filepath.Clean(localPath)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("path %q escapes or names the module root", requested)
	}
	root, err := filepath.Abs(moduleRoot)
	if err != nil {
		return "", "", fmt.Errorf("resolve module root: %w", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", fmt.Errorf("resolve module root links: %w", err)
	}
	lexicalTarget := filepath.Join(root, cleaned)
	relative, err := filepath.Rel(root, lexicalTarget)
	if err != nil {
		return "", "", err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("path %q escapes module root", requested)
	}
	resolvedTarget, err := filepath.EvalSymlinks(lexicalTarget)
	if err != nil {
		return "", "", fmt.Errorf("resolve path %q links: %w", requested, err)
	}
	resolvedRelative, err := filepath.Rel(resolvedRoot, resolvedTarget)
	if err != nil {
		return "", "", fmt.Errorf("compare resolved path %q: %w", requested, err)
	}
	if resolvedRelative == ".." || strings.HasPrefix(resolvedRelative, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("path %q resolves outside module root", requested)
	}
	return resolvedTarget, filepath.ToSlash(relative), nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode compatibility snapshot: multiple JSON values")
		}
		return fmt.Errorf("decode compatibility snapshot: %w", err)
	}
	return nil
}
