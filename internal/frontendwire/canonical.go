package frontendwire

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

func hashCanonical(value any) (string, error) {
	encoded, err := jsonx.Marshal(value, jsonx.Deterministic(true))
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func finalizeSnapshot(snapshot *ProgramSnapshot) error {
	snapshot.ContentHash = ""
	encoded, err := jsonx.Marshal(snapshot, jsonx.Deterministic(true))
	if err != nil {
		return fmt.Errorf("encode canonical snapshot: %w", err)
	}
	digest := sha256.Sum256(encoded)
	snapshot.ContentHash = hex.EncodeToString(digest[:])
	return nil
}

func effectSummary(effects []string, unknown bool) []string {
	if unknown {
		return []string{"unknown"}
	}
	if len(effects) == 0 {
		return []string{"pure"}
	}
	result := slices.Clone(effects)
	slices.Sort(result)
	return slices.Compact(result)
}

const moduleGraphDigestSchema = 2

func digestModuleGraph(modules []ModuleSnapshot, edges []ModuleEdge, sccs []ModuleSCCSnapshot) string {
	payload := struct {
		Schema  int                 `json:"schema"`
		Modules []ModuleSnapshot    `json:"modules"`
		Edges   []ModuleEdge        `json:"edges"`
		SCCs    []ModuleSCCSnapshot `json:"sccs"`
	}{moduleGraphDigestSchema, modules, edges, sccs}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func digestModuleGraphSchema(modules []ModuleSnapshot, edges []ModuleEdge, sccs []ModuleSCCSnapshot, schema int) string {
	if schema == moduleGraphDigestSchema {
		return digestModuleGraph(modules, edges, sccs)
	}
	type legacyEdge struct {
		Importer           ModuleID          `json:"importer"`
		Imported           ModuleID          `json:"imported,omitempty"`
		Specifier          string            `json:"specifier"`
		Span               Span              `json:"span"`
		ResolutionMode     string            `json:"resolutionMode"`
		Resolved           string            `json:"resolved,omitempty"`
		Package            string            `json:"package,omitempty"`
		Extension          string            `json:"extension,omitempty"`
		TypeOnly           bool              `json:"typeOnly"`
		Value              bool              `json:"value"`
		SideEffectOnly     bool              `json:"sideEffectOnly"`
		Kind               string            `json:"kind"`
		ImportAttributes   []ImportAttribute `json:"importAttributes,omitempty"`
		DeferredEvaluation bool              `json:"deferredEvaluation"`
	}
	legacy := make([]legacyEdge, 0, len(edges))
	for _, edge := range edges {
		legacy = append(legacy, legacyEdge{edge.Importer, edge.Imported, edge.Specifier, edge.Span, edge.ResolutionMode, edge.Resolved, edge.Package, edge.Extension, edge.TypeOnly, edge.Value, edge.SideEffectOnly, edge.Kind, edge.ImportAttributes, edge.DeferredEvaluation})
	}
	payload := struct {
		Schema  int                 `json:"schema"`
		Modules []ModuleSnapshot    `json:"modules"`
		Edges   []legacyEdge        `json:"edges"`
		SCCs    []ModuleSCCSnapshot `json:"sccs"`
	}{schema, modules, legacy, sccs}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

// Keep a small deterministic helper here because the validator must not pull
// the Program-owned module resolver into the replay dependency closure.
func normalizeImportAttributes(input []ImportAttribute) []ImportAttribute {
	result := slices.Clone(input)
	slices.SortFunc(result, func(a, b ImportAttribute) int {
		if order := strings.Compare(a.Name, b.Name); order != 0 {
			return order
		}
		return strings.Compare(a.Value, b.Value)
	})
	return result
}

func compareModuleBindingSnapshots(a, b ModuleBindingSnapshot) int {
	for _, pair := range [][2]string{
		{string(a.Node), string(b.Node)}, {a.Kind, b.Kind}, {a.ImportedName, b.ImportedName},
		{a.LocalName, b.LocalName}, {a.ExportedName, b.ExportedName},
		{string(a.AliasSymbol), string(b.AliasSymbol)}, {string(a.TargetSymbol), string(b.TargetSymbol)},
	} {
		if order := strings.Compare(pair[0], pair[1]); order != 0 {
			return order
		}
	}
	if order := compareBool(a.TypeOnly, b.TypeOnly); order != 0 {
		return order
	}
	return compareBool(a.Value, b.Value)
}

func compareModuleEdges(a, b ModuleEdge) int {
	if order := strings.Compare(string(a.Importer), string(b.Importer)); order != 0 {
		return order
	}
	if order := strings.Compare(string(a.Span.File), string(b.Span.File)); order != 0 {
		return order
	}
	if a.Span.Start != b.Span.Start {
		return compareInt(a.Span.Start, b.Span.Start)
	}
	if a.Span.End != b.Span.End {
		return compareInt(a.Span.End, b.Span.End)
	}
	for _, pair := range [][2]string{
		{a.Kind, b.Kind}, {string(a.Source), string(b.Source)}, {string(a.SpecifierNode), string(b.SpecifierNode)},
		{a.Specifier, b.Specifier}, {a.ResolutionMode, b.ResolutionMode}, {string(a.Imported), string(b.Imported)},
		{a.Resolved, b.Resolved}, {a.Package, b.Package}, {a.Extension, b.Extension},
	} {
		if order := strings.Compare(pair[0], pair[1]); order != 0 {
			return order
		}
	}
	for _, pair := range [][2]bool{
		{a.TypeOnly, b.TypeOnly}, {a.Value, b.Value}, {a.SideEffectOnly, b.SideEffectOnly},
		{a.DeferredEvaluation, b.DeferredEvaluation}, {a.BindingsComplete, b.BindingsComplete},
	} {
		if order := compareBool(pair[0], pair[1]); order != 0 {
			return order
		}
	}
	if order := compareImportAttributes(a.ImportAttributes, b.ImportAttributes); order != 0 {
		return order
	}
	for index := 0; index < len(a.Bindings) && index < len(b.Bindings); index++ {
		if order := compareModuleBindingSnapshots(a.Bindings[index], b.Bindings[index]); order != 0 {
			return order
		}
	}
	return compareInt(len(a.Bindings), len(b.Bindings))
}

func compareInt(a, b int) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func compareBool(a, b bool) int {
	if a == b {
		return 0
	}
	if !a {
		return -1
	}
	return 1
}

func compareImportAttributes(a, b []ImportAttribute) int {
	for index := 0; index < len(a) && index < len(b); index++ {
		if order := strings.Compare(a[index].Name, b[index].Name); order != 0 {
			return order
		}
		if order := strings.Compare(a[index].Value, b[index].Value); order != 0 {
			return order
		}
	}
	return compareInt(len(a), len(b))
}
