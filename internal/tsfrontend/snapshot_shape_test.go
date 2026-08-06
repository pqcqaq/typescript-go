package tsfrontend

import (
	"context"
	"slices"
	"strconv"
	"testing"
)

func TestValidateProgramSnapshotRejectsNumericLiteralWithoutPayloadText(t *testing.T) {
	request, frontend := snapshotTestRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true},"files":["main.ts"]}`,
		"/project/main.ts":       `export const value = 42;`,
	})
	snapshot, diagnostics := frontend.Build(context.Background(), request)
	if snapshot == nil || DiagnosticsHaveErrors(diagnostics) {
		t.Fatalf("snapshot/diagnostics = %#v / %#v", snapshot, diagnostics)
	}
	mutated := *snapshot
	mutated.Nodes = slices.Clone(snapshot.Nodes)
	index := slices.IndexFunc(mutated.Nodes, func(node NodeSnapshot) bool {
		return node.Kind == "KindNumericLiteral"
	})
	if index < 0 {
		t.Fatal("fixture has no numeric literal")
	}
	mutated.Nodes[index].SyntaxPayload.Text = ""
	if err := finalizeSnapshot(&mutated); err != nil {
		t.Fatal(err)
	}
	assertSnapshotValidationError(t, mutated, `requires payload field "text"`)
}

func TestValidateProgramSnapshotRejectsBinaryExpressionGenericChildRoles(t *testing.T) {
	snapshot := buildReplayAddSnapshot(t)
	mutated := *snapshot
	mutated.Nodes = slices.Clone(snapshot.Nodes)
	index := slices.IndexFunc(mutated.Nodes, func(node NodeSnapshot) bool {
		return node.Kind == "KindBinaryExpression"
	})
	if index < 0 {
		t.Fatal("fixture has no binary expression")
	}
	mutated.Nodes[index].NamedChildren = slices.Clone(snapshot.Nodes[index].NamedChildren)
	for childIndex := range mutated.Nodes[index].NamedChildren {
		mutated.Nodes[index].NamedChildren[childIndex].Role = "child[" + strconv.Itoa(childIndex) + "]"
	}
	if err := finalizeSnapshot(&mutated); err != nil {
		t.Fatal(err)
	}
	assertSnapshotValidationError(t, mutated, `child role "child[0]" is not allowed`)
}

func TestValidateProgramSnapshotRejectsForbiddenBinaryPayloadText(t *testing.T) {
	snapshot := buildReplayAddSnapshot(t)
	mutated := *snapshot
	mutated.Nodes = slices.Clone(snapshot.Nodes)
	index := slices.IndexFunc(mutated.Nodes, func(node NodeSnapshot) bool {
		return node.Kind == "KindBinaryExpression"
	})
	if index < 0 {
		t.Fatal("fixture has no binary expression")
	}
	mutated.Nodes[index].SyntaxPayload.Text = "not-a-binary-payload"
	if err := finalizeSnapshot(&mutated); err != nil {
		t.Fatal(err)
	}
	assertSnapshotValidationError(t, mutated, `forbids payload field "text"`)
}

func TestValidateProgramSnapshotRejectsMissingRequiredBinaryRole(t *testing.T) {
	snapshot := buildReplayAddSnapshot(t)
	mutated := *snapshot
	mutated.Nodes = slices.Clone(snapshot.Nodes)
	index := slices.IndexFunc(mutated.Nodes, func(node NodeSnapshot) bool {
		return node.Kind == "KindBinaryExpression"
	})
	if index < 0 {
		t.Fatal("fixture has no binary expression")
	}
	mutated.Nodes[index].NamedChildren = slices.Clone(snapshot.Nodes[index].NamedChildren)
	right := slices.IndexFunc(mutated.Nodes[index].NamedChildren, func(child NamedChildSnapshot) bool {
		return child.Role == "right"
	})
	if right < 0 {
		t.Fatal("binary expression has no right role")
	}
	mutated.Nodes[index].NamedChildren[right].Role = "jsDoc[0]"
	if err := finalizeSnapshot(&mutated); err != nil {
		t.Fatal(err)
	}
	assertSnapshotValidationError(t, mutated, `child role "right" has arity 0`)
}

func TestSnapshotModuleKindsCarryStableBindingRoles(t *testing.T) {
	request, frontend := snapshotTestRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true,"module":"esnext","moduleResolution":"bundler"},"files":["main.ts","dep.ts"]}`,
		"/project/dep.ts":        `export default 1; export const source = 2;`,
		"/project/main.ts": `import defaultValue, { source as local } from "./dep";
import * as all from "./dep";
export { local as renamed };
export * as namespace from "./dep";
export const total: number = defaultValue + all.source;`,
	})
	snapshot, diagnostics := frontend.Build(context.Background(), request)
	if snapshot == nil || DiagnosticsHaveErrors(diagnostics) {
		t.Fatalf("snapshot/diagnostics = %#v / %#v", snapshot, diagnostics)
	}
	expectations := []struct {
		kind  string
		roles []string
	}{
		{kind: "KindExportDeclaration", roles: []string{"exportClause", "moduleSpecifier"}},
		{kind: "KindExportSpecifier", roles: []string{"propertyName", "name"}},
		{kind: "KindImportClause", roles: []string{"defaultBinding", "namedBindings"}},
		{kind: "KindImportDeclaration", roles: []string{"importClause", "moduleSpecifier"}},
		{kind: "KindImportSpecifier", roles: []string{"propertyName", "name"}},
		{kind: "KindNamedExports", roles: []string{"specifier[0]"}},
		{kind: "KindNamedImports", roles: []string{"specifier[0]"}},
		{kind: "KindNamespaceExport", roles: []string{"name"}},
		{kind: "KindNamespaceImport", roles: []string{"name"}},
	}
	for _, expectation := range expectations {
		if !snapshotHasKindRoles(snapshot, expectation.kind, expectation.roles...) {
			t.Fatalf("snapshot has no %s node with roles %v", expectation.kind, expectation.roles)
		}
	}
}

func TestSnapshotImportEqualsCarriesStableBindingRoles(t *testing.T) {
	request, frontend := snapshotTestRequest(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true,"module":"node16","moduleResolution":"node16"},"files":["main.ts","dep.ts"]}`,
		"/project/dep.ts":        `export const source = 1;`,
		"/project/main.ts":       `import legacy = require("./dep"); export const value: number = legacy.source;`,
	})
	snapshot, diagnostics := frontend.Build(context.Background(), request)
	if snapshot == nil {
		t.Fatalf("snapshot/diagnostics = %#v / %#v", snapshot, diagnostics)
	}
	expectedDiagnosticKinds := map[string]bool{
		"KindImportEqualsDeclaration": true,
		"KindExternalModuleReference": true,
	}
	if len(diagnostics) != len(expectedDiagnosticKinds) {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.Code != DiagnosticCodeUnsupportedSyntax || diagnostic.RequiredCapability != "cjs-interop" || !expectedDiagnosticKinds[diagnostic.NodeKind] {
			t.Fatalf("unexpected import-equals diagnostic = %#v", diagnostic)
		}
		delete(expectedDiagnosticKinds, diagnostic.NodeKind)
	}
	if !snapshotHasKindRoles(snapshot, "KindImportEqualsDeclaration", "name", "moduleReference") {
		t.Fatal("snapshot has no import-equals declaration binding roles")
	}
	if !snapshotHasKindRoles(snapshot, "KindExternalModuleReference", "expression") {
		t.Fatal("snapshot has no external-module-reference expression role")
	}
}

func snapshotHasKindRoles(snapshot *ProgramSnapshot, kind string, roles ...string) bool {
	return slices.ContainsFunc(snapshot.Nodes, func(node NodeSnapshot) bool {
		if node.Kind != kind {
			return false
		}
		for _, role := range roles {
			if !slices.ContainsFunc(node.NamedChildren, func(child NamedChildSnapshot) bool {
				return child.Role == role
			}) {
				return false
			}
		}
		return true
	})
}
