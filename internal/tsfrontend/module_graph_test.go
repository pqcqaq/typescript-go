package tsfrontend

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/bundled"
)

func TestFinalizeModuleGraphFindsOnlyEagerValueCycles(t *testing.T) {
	t.Parallel()
	modules := []ModuleSnapshot{
		{ID: "d", CanonicalPath: "d.ts", Format: "esm"},
		{ID: "b", CanonicalPath: "b.ts", Format: "esm"},
		{ID: "a", CanonicalPath: "a.ts", Format: "esm"},
		{ID: "c", CanonicalPath: "c.ts", Format: "esm"},
	}
	edges := []ModuleEdge{
		{Importer: "c", Imported: "d", Specifier: "./d", Kind: "dynamic-import", Value: true, DeferredEvaluation: true},
		{Importer: "b", Imported: "a", Specifier: "./a", Kind: "export", Value: true},
		{Importer: "a", Imported: "b", Specifier: "./b", Kind: "import", Value: true},
		{Importer: "d", Imported: "c", Specifier: "./c", Kind: "import-type", TypeOnly: true},
	}

	graph := finalizeModuleGraph(modules, edges)
	if len(graph.SCCs) != 3 {
		t.Fatalf("SCC count = %d, want 3: %#v", len(graph.SCCs), graph.SCCs)
	}
	componentByModule := make(map[ModuleID]int)
	for _, component := range graph.SCCs {
		for _, moduleID := range component.Modules {
			componentByModule[moduleID] = component.ID
		}
	}
	if componentByModule["a"] != componentByModule["b"] {
		t.Fatalf("eager cycle was split: %#v", graph.SCCs)
	}
	if componentByModule["c"] == componentByModule["d"] {
		t.Fatalf("deferred/type-only edges formed a runtime SCC: %#v", graph.SCCs)
	}
	if len(graph.Digest) != 64 {
		t.Fatalf("digest = %q, want SHA-256 hex", graph.Digest)
	}
	for _, module := range graph.Modules {
		if module.SCC != componentByModule[module.ID] {
			t.Fatalf("module %s SCC = %d, want %d", module.ID, module.SCC, componentByModule[module.ID])
		}
	}
}

func TestFinalizeModuleGraphIsIndependentOfInputOrder(t *testing.T) {
	t.Parallel()
	modules := []ModuleSnapshot{
		{ID: "a", CanonicalPath: "a.ts", Format: "esm"},
		{ID: "b", CanonicalPath: "b.ts", Format: "esm"},
		{ID: "c", CanonicalPath: "c.ts", Format: "json"},
	}
	edges := []ModuleEdge{
		{
			Importer: "a", Imported: "c", Specifier: "./c.json", Kind: "import", Value: true,
			Span:             Span{File: "a-file", Start: 30, End: 40},
			ImportAttributes: []ImportAttribute{{Name: "mode", Value: "strict"}, {Name: "type", Value: "json"}},
		},
		{
			Importer: "a", Imported: "b", Specifier: "./b", Kind: "import", Value: true,
			Span: Span{File: "a-file", Start: 10, End: 20},
		},
	}

	first := finalizeModuleGraph(modules, edges)
	reversedModules := slices.Clone(modules)
	reversedEdges := slices.Clone(edges)
	slices.Reverse(reversedModules)
	slices.Reverse(reversedEdges)
	slices.Reverse(reversedEdges[1].ImportAttributes)
	second := finalizeModuleGraph(reversedModules, reversedEdges)

	if first.Digest != second.Digest {
		t.Fatalf("digest changed with input order: %s != %s", first.Digest, second.Digest)
	}
	if !slices.Equal(first.Modules, second.Modules) || !slices.EqualFunc(first.SCCs, second.SCCs, func(a, b ModuleSCCSnapshot) bool {
		return a.ID == b.ID && slices.Equal(a.Modules, b.Modules)
	}) {
		t.Fatalf("normalized graph changed with input order:\n%#v\n%#v", first, second)
	}
	if len(first.Edges) != 2 || first.Edges[0].Specifier != "./b" || first.Edges[1].Specifier != "./c.json" {
		t.Fatalf("source order was not retained within importer: %#v", first.Edges)
	}
	if got := first.Edges[1].ImportAttributes; len(got) != 2 || got[0].Name != "mode" || got[1].Name != "type" {
		t.Fatalf("attributes were not canonicalized: %#v", got)
	}
}

func TestCaptureModuleGraphUsesProgramResolutionAndSyntaxFacts(t *testing.T) {
	t.Parallel()
	graph := captureModuleGraphForTest(t, map[string]string{
		"/project/tsconfig.json": `{
			"compilerOptions": {
				"module": "esnext",
				"moduleResolution": "bundler",
				"resolveJsonModule": true,
				"allowArbitraryExtensions": true,
				"noLib": true
			},
			"files": ["main.ts"]
		}`,
		"/project/main.ts": `
			import type { Shape } from "./types";
			import "./side";
			import data from "./data.json" with { type: "json" };
			import defer * as deferred from "./deferred";
			export { value } from "./value";
			export * as namespaceValue from "./value";
			export type { LazyShape } from "./lazy";
			export {} from "./side-two";
			type ImportedShape = import("./types").Shape;
			const lazy = import("./lazy");
			const dynamicJSON = import("./data.json", { with: { type: "json" } });
			void data;
			void deferred;
			void lazy;
			void dynamicJSON;
		`,
		"/project/types.ts":    `export interface Shape { value: number }`,
		"/project/side.ts":     `export const side = true;`,
		"/project/side-two.ts": `export const sideTwo = true;`,
		"/project/data.json":   `{"answer": 42}`,
		"/project/value.ts":    `export const value = 1;`,
		"/project/lazy.ts":     `export interface LazyShape { ok: true }`,
		"/project/deferred.ts": `export const deferred = true;`,
	})

	assertModuleEdge(t, graph.Edges, "./types", func(edge ModuleEdge) bool {
		return edge.Kind == "import" && edge.TypeOnly && !edge.Value && !edge.SideEffectOnly
	})
	assertModuleEdge(t, graph.Edges, "./side", func(edge ModuleEdge) bool {
		return edge.Kind == "import" && !edge.TypeOnly && !edge.Value && edge.SideEffectOnly
	})
	assertModuleEdge(t, graph.Edges, "./data.json", func(edge ModuleEdge) bool {
		return edge.Kind == "import" && edge.Value && edge.Extension == ".json" &&
			len(edge.ImportAttributes) == 1 && edge.ImportAttributes[0] == (ImportAttribute{Name: "type", Value: "json"})
	})
	assertModuleEdge(t, graph.Edges, "./value", func(edge ModuleEdge) bool {
		return edge.Kind == "export" && edge.Value && !edge.DeferredEvaluation
	})
	assertModuleEdge(t, graph.Edges, "./deferred", func(edge ModuleEdge) bool {
		return edge.Kind == "import" && edge.Value && edge.DeferredEvaluation
	})
	assertModuleEdge(t, graph.Edges, "./side-two", func(edge ModuleEdge) bool {
		return edge.Kind == "export" && !edge.Value && edge.SideEffectOnly
	})
	assertModuleEdge(t, graph.Edges, "./lazy", func(edge ModuleEdge) bool {
		return edge.Kind == "export" && edge.TypeOnly
	})
	assertModuleEdge(t, graph.Edges, "./lazy", func(edge ModuleEdge) bool {
		return edge.Kind == "dynamic-import" && edge.Value && edge.DeferredEvaluation
	})
	assertModuleEdge(t, graph.Edges, "./types", func(edge ModuleEdge) bool {
		return edge.Kind == "import-type" && edge.TypeOnly && !edge.Value
	})
	assertModuleEdge(t, graph.Edges, "./data.json", func(edge ModuleEdge) bool {
		return edge.Kind == "dynamic-import" && edge.DeferredEvaluation &&
			len(edge.ImportAttributes) == 1 && edge.ImportAttributes[0] == (ImportAttribute{Name: "type", Value: "json"})
	})

	if !slices.ContainsFunc(graph.Modules, func(module ModuleSnapshot) bool {
		return module.CanonicalPath == "data.json" && module.Format == "json"
	}) {
		t.Fatalf("resolved JSON module missing or misclassified: %#v", graph.Modules)
	}
	for _, edge := range graph.Edges {
		if edge.Specifier != "" && edge.Resolved == "" {
			t.Fatalf("Program resolution was not copied for %q: %#v", edge.Specifier, edge)
		}
	}
}

func TestCaptureModuleGraphClassifiesNamedTypeOnlySpecifiers(t *testing.T) {
	t.Parallel()
	graph := captureModuleGraphForTest(t, map[string]string{
		"/project/tsconfig.json": `{
			"compilerOptions": {
				"module": "esnext",
				"moduleResolution": "bundler",
				"noLib": true
			},
			"files": ["main.ts"]
		}`,
		"/project/main.ts": `
			import { type Shape } from "./import-types";
			import { type MixedShape, value } from "./import-mixed";
			export { type ExportedShape } from "./export-types";
			export { type MixedExportedShape, value as exportedValue } from "./export-mixed";
			type LocalShape = Shape | MixedShape;
			void value;
		`,
		"/project/import-types.ts": `export interface Shape { value: number }`,
		"/project/import-mixed.ts": `export interface MixedShape { value: number }; export const value = 1;`,
		"/project/export-types.ts": `export interface ExportedShape { value: number }`,
		"/project/export-mixed.ts": `export interface MixedExportedShape { value: number }; export const value = 1;`,
	})

	assertModuleEdge(t, graph.Edges, "./import-types", func(edge ModuleEdge) bool {
		return edge.Kind == "import" && edge.TypeOnly && !edge.Value && !edge.SideEffectOnly
	})
	assertModuleEdge(t, graph.Edges, "./import-mixed", func(edge ModuleEdge) bool {
		return edge.Kind == "import" && !edge.TypeOnly && edge.Value && !edge.SideEffectOnly
	})
	assertModuleEdge(t, graph.Edges, "./export-types", func(edge ModuleEdge) bool {
		return edge.Kind == "export" && edge.TypeOnly && !edge.Value && !edge.SideEffectOnly
	})
	assertModuleEdge(t, graph.Edges, "./export-mixed", func(edge ModuleEdge) bool {
		return edge.Kind == "export" && !edge.TypeOnly && edge.Value && !edge.SideEffectOnly
	})
}

func TestCaptureModuleGraphClassifiesImportEquals(t *testing.T) {
	t.Parallel()
	graph := captureModuleGraphForTest(t, map[string]string{
		"/project/tsconfig.json": `{
			"compilerOptions": {
				"module": "node16",
				"moduleResolution": "node16",
				"noLib": true
			},
			"files": ["main.ts"]
		}`,
		"/project/main.ts": `
			import dependency = require("./dependency");
			import type Shape = require("./shape");
			void dependency;
		`,
		"/project/dependency.ts": `export = 1;`,
		"/project/shape.ts":      `export interface Shape { ok: true }`,
	})

	assertModuleEdge(t, graph.Edges, "./dependency", func(edge ModuleEdge) bool {
		return edge.Kind == "import-equals" && edge.Value && !edge.TypeOnly && edge.ResolutionMode == "CommonJS"
	})
	assertModuleEdge(t, graph.Edges, "./shape", func(edge ModuleEdge) bool {
		return edge.Kind == "import-equals" && edge.TypeOnly && !edge.Value && edge.ResolutionMode == "CommonJS"
	})
}

func TestCaptureModuleGraphUsesPackageExportsConditions(t *testing.T) {
	t.Parallel()
	graph := captureModuleGraphForTest(t, map[string]string{
		"/project/tsconfig.json": `{
			"compilerOptions": {
				"module": "node16",
				"moduleResolution": "node16",
				"noLib": true
			},
			"files": ["main.mts", "main.cts"]
		}`,
		"/project/main.mts": `import { value } from "fixture-package"; export const esm = value;`,
		"/project/main.cts": `import value = require("fixture-package"); export = value;`,
		"/project/node_modules/fixture-package/package.json": `{
			"name": "fixture-package",
			"version": "1.2.3",
			"exports": {
				".": {
					"import": "./esm.d.mts",
					"require": "./cjs.d.cts"
				}
			}
		}`,
		"/project/node_modules/fixture-package/esm.d.mts": `export declare const value: "esm";`,
		"/project/node_modules/fixture-package/cjs.d.cts": `declare const value: "cjs"; export = value;`,
	})

	type goldenEdge struct {
		ResolutionMode string `json:"resolutionMode"`
		Resolved       string `json:"resolved"`
		Package        string `json:"package"`
		Format         string `json:"format"`
		External       bool   `json:"external"`
	}
	type golden struct {
		SchemaVersion int          `json:"schemaVersion"`
		Specifier     string       `json:"specifier"`
		Edges         []goldenEdge `json:"edges"`
	}
	modules := make(map[ModuleID]ModuleSnapshot, len(graph.Modules))
	for _, module := range graph.Modules {
		modules[module.ID] = module
	}
	projection := golden{SchemaVersion: 1, Specifier: "fixture-package", Edges: []goldenEdge{}}
	for _, edge := range graph.Edges {
		if edge.Specifier != projection.Specifier {
			continue
		}
		module := modules[edge.Imported]
		projection.Edges = append(projection.Edges, goldenEdge{
			ResolutionMode: edge.ResolutionMode,
			Resolved:       edge.Resolved,
			Package:        edge.Package,
			Format:         module.Format,
			External:       module.External,
		})
	}
	slices.SortFunc(projection.Edges, func(left, right goldenEdge) int {
		return strings.Compare(left.ResolutionMode, right.ResolutionMode)
	})
	if len(projection.Edges) != 2 {
		t.Fatalf("package exports edges = %#v", projection.Edges)
	}
	got, err := json.MarshalIndent(projection, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/module/package-exports.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(append(got, '\n'), want) {
		t.Fatalf("package exports golden mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestCaptureModuleGraphClassifiesCommonJSRequire(t *testing.T) {
	t.Parallel()
	graph := captureModuleGraphForTest(t, map[string]string{
		"/project/tsconfig.json": `{
			"compilerOptions": {
				"module": "node16",
				"moduleResolution": "node16",
				"allowJs": true,
				"checkJs": false,
				"noLib": true
			},
			"files": ["main.cjs"]
		}`,
		"/project/main.cjs": `
			const eager = require("./eager.cjs");
			function loadLater() { return require("./later.cjs"); }
			module.exports = { eager, loadLater };
		`,
		"/project/eager.cjs": `module.exports = 1;`,
		"/project/later.cjs": `module.exports = 2;`,
	})

	assertModuleEdge(t, graph.Edges, "./eager.cjs", func(edge ModuleEdge) bool {
		return edge.Kind == "require" && edge.ResolutionMode == "CommonJS" && edge.Value && !edge.DeferredEvaluation
	})
	assertModuleEdge(t, graph.Edges, "./later.cjs", func(edge ModuleEdge) bool {
		return edge.Kind == "require" && edge.ResolutionMode == "CommonJS" && edge.Value && edge.DeferredEvaluation
	})
	for _, module := range graph.Modules {
		if module.CanonicalPath == "main.cjs" || module.CanonicalPath == "eager.cjs" || module.CanonicalPath == "later.cjs" {
			if module.Format != "cjs" {
				t.Fatalf("CommonJS module %s format = %q", module.CanonicalPath, module.Format)
			}
		}
	}
}

func captureModuleGraphForTest(t *testing.T, files map[string]string) capturedModuleGraph {
	t.Helper()
	request := testBuildRequest(files)
	build, err := buildProgram(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if build == nil || build.program == nil {
		t.Fatalf("program was not built: %#v", build)
	}
	frontend := NewFrontend(request.FileSystem, bundled.LibPath(), TypeScriptGoCommit, StandardLibraryHash)
	graph := &captureGraph{
		program:     build.program,
		projectRoot: build.projectRoot,
		frontend:    frontend,
		fileIDs:     make(map[string]FileID),
		moduleIDs:   make(map[string]ModuleID),
	}
	for _, file := range build.program.SourceFiles() {
		if build.program.IsSourceFileDefaultLibrary(file.Path()) {
			continue
		}
		graph.files = append(graph.files, &pendingFile{file: file, moduleID: graph.moduleID(file)})
	}
	return graph.captureModuleGraphData()
}

func assertModuleEdge(t *testing.T, edges []ModuleEdge, specifier string, predicate func(ModuleEdge) bool) {
	t.Helper()
	for _, edge := range edges {
		if edge.Specifier == specifier && predicate(edge) {
			return
		}
	}
	t.Fatalf("no matching edge for %q in %#v", specifier, edges)
}
