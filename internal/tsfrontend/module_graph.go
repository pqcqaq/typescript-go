package tsfrontend

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/core"
	"github.com/microsoft/typescript-go/internal/module"
	"github.com/microsoft/typescript-go/internal/tspath"
)

const moduleGraphDigestSchema = 2

type capturedModuleGraph struct {
	Modules []ModuleSnapshot
	Edges   []ModuleEdge
	SCCs    []ModuleSCCSnapshot
	Digest  string
}

func cloneModuleSCCs(input []ModuleSCCSnapshot) []ModuleSCCSnapshot {
	if len(input) == 0 {
		return nil
	}
	result := make([]ModuleSCCSnapshot, len(input))
	for index := range input {
		result[index] = input[index]
		result[index].Modules = slices.Clone(input[index].Modules)
	}
	return result
}

// captureModuleGraph copies module resolution facts from Program-owned caches.
// It never resolves a specifier itself and never retains AST or resolver data.
func (g *captureGraph) captureModuleGraph() ([]ModuleEdge, []ModuleSnapshot) {
	graph := g.captureModuleGraphData()
	return graph.Edges, graph.Modules
}

func (g *captureGraph) captureModuleGraphData() capturedModuleGraph {
	moduleByID := make(map[ModuleID]ModuleSnapshot)
	for _, pending := range g.files {
		module := g.moduleSnapshotForFile(pending.file)
		moduleByID[module.ID] = mergeModuleSnapshot(moduleByID[module.ID], module)
	}

	edges := make([]ModuleEdge, 0)
	for _, pending := range g.files {
		file := pending.file
		seenResolutions := make(map[moduleResolutionKey]struct{})
		seenSpecifiers := make(map[*ast.Node]struct{})
		for _, specifier := range file.Imports() {
			if specifier == nil || !ast.IsStringLiteralLike(specifier) {
				continue
			}
			if _, duplicate := seenSpecifiers[specifier.AsNode()]; duplicate {
				continue
			}
			seenSpecifiers[specifier.AsNode()] = struct{}{}
			mode := g.program.GetModeForUsageLocation(file, specifier)
			seenResolutions[moduleResolutionKey{specifier.Text(), mode}] = struct{}{}
			resolution := g.program.GetResolvedModuleFromModuleSpecifier(file, specifier)
			edge, imported := g.moduleEdge(file, specifier, mode, resolution)
			edges = append(edges, edge)
			if imported.ID != "" {
				moduleByID[imported.ID] = mergeModuleSnapshot(moduleByID[imported.ID], imported)
			}
		}

		// Program may cache synthetic module requests (for example helper or JSX
		// imports) that do not have a source-owned module specifier node.
		g.program.ForEachResolvedModule(func(resolution *module.ResolvedModule, moduleName string, mode core.ResolutionMode, _ tspath.Path) {
			key := moduleResolutionKey{moduleName, mode}
			if _, ok := seenResolutions[key]; ok {
				return
			}
			seenResolutions[key] = struct{}{}
			edge, imported := g.syntheticModuleEdge(file, moduleName, mode, resolution)
			edges = append(edges, edge)
			if imported.ID != "" {
				moduleByID[imported.ID] = mergeModuleSnapshot(moduleByID[imported.ID], imported)
			}
		}, file)
	}

	modules := make([]ModuleSnapshot, 0, len(moduleByID))
	for _, module := range moduleByID {
		modules = append(modules, module)
	}
	return finalizeModuleGraph(modules, edges)
}

type moduleResolutionKey struct {
	name string
	mode core.ResolutionMode
}

func (g *captureGraph) moduleEdge(file *ast.SourceFile, specifier *ast.StringLiteralLike, mode core.ResolutionMode, resolution *module.ResolvedModule) (ModuleEdge, ModuleSnapshot) {
	use := classifyModuleUse(specifier)
	fileID := g.fileID(file)
	proof, proofCaptured := g.moduleBindings[specifier]
	edge := ModuleEdge{
		Importer:           g.moduleID(file),
		Source:             g.nodeIDs[use.source],
		SpecifierNode:      g.nodeIDs[specifier],
		Specifier:          specifier.Text(),
		Span:               spanForModuleSpecifier(fileID, specifier),
		ResolutionMode:     mode.String(),
		TypeOnly:           use.typeOnly,
		Value:              use.value,
		SideEffectOnly:     use.sideEffectOnly,
		Kind:               use.kind,
		ImportAttributes:   use.attributes,
		Bindings:           slices.Clone(proof.bindings),
		BindingsComplete:   proofCaptured && proof.complete,
		DeferredEvaluation: use.deferredEvaluation,
	}
	return g.attachModuleResolution(edge, mode, resolution)
}

func (g *captureGraph) syntheticModuleEdge(file *ast.SourceFile, specifier string, mode core.ResolutionMode, resolution *module.ResolvedModule) (ModuleEdge, ModuleSnapshot) {
	edge := ModuleEdge{
		Importer:         g.moduleID(file),
		Specifier:        specifier,
		Span:             Span{File: g.fileID(file)},
		ResolutionMode:   mode.String(),
		Value:            true,
		Kind:             "import",
		BindingsComplete: true,
	}
	return g.attachModuleResolution(edge, mode, resolution)
}

func (g *captureGraph) attachModuleResolution(edge ModuleEdge, mode core.ResolutionMode, resolution *module.ResolvedModule) (ModuleEdge, ModuleSnapshot) {
	if resolution == nil || !resolution.IsResolved() {
		return edge, ModuleSnapshot{}
	}

	edge.Resolved = g.frontend.logicalPath(resolution.ResolvedFileName, g.projectRoot)
	edge.Extension = resolution.Extension
	edge.Package = resolvedPackageIdentity(resolution)
	target := g.program.GetSourceFileForResolvedModule(resolution.ResolvedFileName)
	format := g.moduleFormat(target, resolution.Extension, mode)
	if target != nil {
		module := g.moduleSnapshotForFile(target)
		module.External = module.External || resolution.IsExternalLibraryImport
		if edge.Package != "" {
			module.Package = edge.Package
		}
		edge.Imported = module.ID
		return edge, module
	}

	moduleID := g.resolvedModuleID(edge.Resolved, format, mode)
	edge.Imported = moduleID
	return edge, ModuleSnapshot{
		ID:            moduleID,
		CanonicalPath: edge.Resolved,
		Package:       edge.Package,
		Format:        format,
		External:      resolution.IsExternalLibraryImport,
	}
}

func (g *captureGraph) moduleSnapshotForFile(file *ast.SourceFile) ModuleSnapshot {
	if file == nil {
		return ModuleSnapshot{}
	}
	return ModuleSnapshot{
		ID:            g.moduleID(file),
		CanonicalPath: g.frontend.logicalPath(file.FileName(), g.projectRoot),
		Format:        g.moduleFormat(file, "", core.ResolutionModeNone),
		External:      g.program.IsSourceFileFromExternalLibrary(file),
	}
}

func (g *captureGraph) resolvedModuleID(canonicalPath, format string, mode core.ResolutionMode) ModuleID {
	key := canonicalPath + "|" + format + "|" + mode.String()
	if id, ok := g.moduleIDs[key]; ok {
		return id
	}
	id := ModuleID(stableID("module", key))
	g.moduleIDs[key] = id
	return id
}

func (g *captureGraph) moduleFormat(file *ast.SourceFile, extension string, mode core.ResolutionMode) string {
	if file != nil && ast.IsJsonSourceFile(file) || strings.EqualFold(extension, ".json") {
		return "json"
	}
	if file != nil {
		implied := g.program.GetImpliedNodeFormatForEmit(file)
		switch implied {
		case core.ResolutionModeCommonJS:
			return "cjs"
		case core.ResolutionModeESM:
			return "esm"
		}
		emit := g.program.GetEmitModuleFormatOfFile(file)
		if emit == core.ModuleKindCommonJS {
			return "cjs"
		}
		if emit.IsNonNodeESM() {
			return "esm"
		}
	}

	lowerExtension := strings.ToLower(extension)
	switch {
	case strings.HasSuffix(lowerExtension, ".cts"), strings.HasSuffix(lowerExtension, ".cjs"):
		return "cjs"
	case strings.HasSuffix(lowerExtension, ".mts"), strings.HasSuffix(lowerExtension, ".mjs"):
		return "esm"
	}
	switch mode {
	case core.ResolutionModeCommonJS:
		return "cjs"
	case core.ResolutionModeESM:
		return "esm"
	default:
		return "unknown"
	}
}

func resolvedPackageIdentity(resolution *module.ResolvedModule) string {
	if resolution == nil || resolution.PackageId.Name == "" {
		return ""
	}
	return resolution.PackageId.String()
}

type moduleUse struct {
	source             *ast.Node
	kind               string
	typeOnly           bool
	value              bool
	sideEffectOnly     bool
	attributes         []ImportAttribute
	deferredEvaluation bool
}

func classifyModuleUse(specifier *ast.StringLiteralLike) moduleUse {
	use := moduleUse{kind: "import", value: true}
	if specifier == nil {
		return use
	}

	parent := specifier.Parent
	switch {
	case parent != nil && (ast.IsImportDeclaration(parent) || ast.IsJSImportDeclaration(parent)):
		use.source = parent
		declaration := parent.AsImportDeclaration()
		use.attributes = copyImportAttributes(declaration.Attributes)
		if declaration.ImportClause == nil {
			use.value = false
			use.sideEffectOnly = true
			return use
		}
		clause := declaration.ImportClause.AsImportClause()
		use.typeOnly = importClauseIsEntirelyTypeOnly(clause)
		use.value = !use.typeOnly
		use.deferredEvaluation = clause.PhaseModifier == ast.KindDeferKeyword
		return use

	case parent != nil && ast.IsExportDeclaration(parent):
		use.source = parent
		use.kind = "export"
		declaration := parent.AsExportDeclaration()
		use.attributes = copyImportAttributes(declaration.Attributes)
		use.typeOnly = exportDeclarationIsEntirelyTypeOnly(declaration)
		use.value = !use.typeOnly
		if !use.typeOnly && declaration.ExportClause != nil && ast.IsNamedExports(declaration.ExportClause) && len(declaration.ExportClause.Elements()) == 0 {
			use.value = false
			use.sideEffectOnly = true
		}
		return use

	case parent != nil && ast.IsExternalModuleReference(parent) && parent.Parent != nil && ast.IsImportEqualsDeclaration(parent.Parent):
		use.source = parent.Parent
		use.kind = "import-equals"
		use.typeOnly = parent.Parent.IsTypeOnly()
		use.value = !use.typeOnly
		return use

	case parent != nil && ast.IsCallExpression(parent) && ast.IsImportCall(parent):
		use.source = parent
		use.kind = "dynamic-import"
		use.deferredEvaluation = true
		use.attributes = copyDynamicImportAttributes(parent)
		return use

	case parent != nil && ast.IsCallExpression(parent) && ast.IsRequireCall(parent, true):
		use.source = parent
		use.kind = "require"
		use.deferredEvaluation = !isTopLevelModuleUse(parent)
		return use
	}

	for current := parent; current != nil; current = current.Parent {
		if ast.IsImportTypeNode(current) {
			use.source = current
			use.kind = "import-type"
			use.typeOnly = true
			use.value = false
			use.attributes = copyImportAttributes(current.AsImportTypeNode().Attributes)
			return use
		}
		if ast.IsSourceFile(current) {
			break
		}
	}
	return use
}

func importClauseIsEntirelyTypeOnly(clause *ast.ImportClause) bool {
	if clause == nil {
		return false
	}
	if clause.PhaseModifier == ast.KindTypeKeyword {
		return true
	}
	if clause.Name() != nil || clause.NamedBindings == nil || !ast.IsNamedImports(clause.NamedBindings) {
		return false
	}
	elements := clause.NamedBindings.Elements()
	if len(elements) == 0 {
		return false
	}
	for _, element := range elements {
		if element == nil || !ast.IsImportSpecifier(element) || !element.IsTypeOnly() {
			return false
		}
	}
	return true
}

func exportDeclarationIsEntirelyTypeOnly(declaration *ast.ExportDeclaration) bool {
	if declaration == nil {
		return false
	}
	if declaration.IsTypeOnly {
		return true
	}
	if declaration.ExportClause == nil || !ast.IsNamedExports(declaration.ExportClause) {
		return false
	}
	elements := declaration.ExportClause.Elements()
	if len(elements) == 0 {
		return false
	}
	for _, element := range elements {
		if element == nil || !ast.IsExportSpecifier(element) || !element.IsTypeOnly() {
			return false
		}
	}
	return true
}

func spanForModuleSpecifier(fileID FileID, specifier *ast.StringLiteralLike) Span {
	start := max(0, specifier.Pos())
	return Span{File: fileID, Start: start, End: max(start, specifier.End())}
}

func copyImportAttributes(attributes *ast.ImportAttributesNode) []ImportAttribute {
	if attributes == nil || attributes.AsImportAttributes().Attributes == nil {
		return nil
	}
	result := make([]ImportAttribute, 0, len(attributes.AsImportAttributes().Attributes.Nodes))
	for _, node := range attributes.AsImportAttributes().Attributes.Nodes {
		if node == nil || !ast.IsImportAttribute(node) {
			continue
		}
		attribute := node.AsImportAttribute()
		result = append(result, ImportAttribute{
			Name:  importAttributeText(attribute.Name()),
			Value: importAttributeText(attribute.Value),
		})
	}
	return normalizeImportAttributes(result)
}

func copyDynamicImportAttributes(call *ast.Node) []ImportAttribute {
	arguments := call.Arguments()
	if len(arguments) < 2 || !ast.IsObjectLiteralExpression(arguments[1]) {
		return nil
	}
	options := arguments[1]
	for _, property := range options.Properties() {
		if property == nil || !ast.IsPropertyAssignment(property) {
			continue
		}
		name := importAttributeText(property.Name())
		if name != "with" && name != "assert" {
			continue
		}
		value := property.Initializer()
		if value == nil || !ast.IsObjectLiteralExpression(value) {
			continue
		}
		result := make([]ImportAttribute, 0, len(value.Properties()))
		for _, attributeNode := range value.Properties() {
			if attributeNode == nil || !ast.IsPropertyAssignment(attributeNode) {
				continue
			}
			result = append(result, ImportAttribute{
				Name:  importAttributeText(attributeNode.Name()),
				Value: importAttributeText(attributeNode.Initializer()),
			})
		}
		return normalizeImportAttributes(result)
	}
	return nil
}

func importAttributeText(node *ast.Node) string {
	if node == nil {
		return ""
	}
	if ast.IsIdentifier(node) || ast.IsStringLiteralLike(node) || ast.IsNumericLiteral(node) {
		return node.Text()
	}
	switch node.Kind {
	case ast.KindTrueKeyword:
		return "true"
	case ast.KindFalseKeyword:
		return "false"
	case ast.KindNullKeyword:
		return "null"
	default:
		return node.Kind.String()
	}
}

func normalizeImportAttributes(attributes []ImportAttribute) []ImportAttribute {
	if len(attributes) == 0 {
		return nil
	}
	result := slices.Clone(attributes)
	slices.SortFunc(result, func(a, b ImportAttribute) int {
		if order := strings.Compare(a.Name, b.Name); order != 0 {
			return order
		}
		return strings.Compare(a.Value, b.Value)
	})
	return result
}

func isTopLevelModuleUse(node *ast.Node) bool {
	for current := node.Parent; current != nil; current = current.Parent {
		if ast.IsFunctionLike(current) {
			return false
		}
		if ast.IsSourceFile(current) {
			return true
		}
	}
	return false
}

func mergeModuleSnapshot(existing, candidate ModuleSnapshot) ModuleSnapshot {
	if existing.ID == "" {
		return candidate
	}
	existing.External = existing.External || candidate.External
	if existing.CanonicalPath == "" || candidate.CanonicalPath != "" && candidate.CanonicalPath < existing.CanonicalPath {
		existing.CanonicalPath = candidate.CanonicalPath
	}
	if existing.Package == "" || candidate.Package != "" && candidate.Package < existing.Package {
		existing.Package = candidate.Package
	}
	if existing.Format == "" || existing.Format == "unknown" && candidate.Format != "" {
		existing.Format = candidate.Format
	}
	return existing
}

// finalizeModuleGraph normalizes graph order, computes eager-value SCCs, and
// hashes the complete canonical graph payload. It is independent of Program so
// determinism and cycle behavior can be tested directly.
func finalizeModuleGraph(modules []ModuleSnapshot, edges []ModuleEdge) capturedModuleGraph {
	normalizedModules := normalizeModuleSnapshots(modules, edges)
	normalizedEdges := normalizeModuleEdges(edges)
	sccs := moduleGraphSCCs(normalizedModules, normalizedEdges)
	sccByModule := make(map[ModuleID]int, len(normalizedModules))
	for _, component := range sccs {
		for _, moduleID := range component.Modules {
			sccByModule[moduleID] = component.ID
		}
	}
	for index := range normalizedModules {
		normalizedModules[index].SCC = sccByModule[normalizedModules[index].ID]
	}
	digest := digestModuleGraph(normalizedModules, normalizedEdges, sccs)
	return capturedModuleGraph{Modules: normalizedModules, Edges: normalizedEdges, SCCs: sccs, Digest: digest}
}

func normalizeModuleSnapshots(modules []ModuleSnapshot, edges []ModuleEdge) []ModuleSnapshot {
	byID := make(map[ModuleID]ModuleSnapshot, len(modules))
	for _, module := range modules {
		if module.ID == "" {
			continue
		}
		if module.Format == "" {
			module.Format = "unknown"
		}
		byID[module.ID] = mergeModuleSnapshot(byID[module.ID], module)
	}
	for _, edge := range edges {
		if edge.Importer != "" {
			byID[edge.Importer] = mergeModuleSnapshot(byID[edge.Importer], ModuleSnapshot{ID: edge.Importer})
		}
		if edge.Imported != "" {
			byID[edge.Imported] = mergeModuleSnapshot(byID[edge.Imported], ModuleSnapshot{ID: edge.Imported, CanonicalPath: edge.Resolved, Package: edge.Package, Format: "unknown"})
		}
	}
	result := make([]ModuleSnapshot, 0, len(byID))
	for _, module := range byID {
		result = append(result, module)
	}
	slices.SortFunc(result, func(a, b ModuleSnapshot) int { return strings.Compare(string(a.ID), string(b.ID)) })
	return result
}

func normalizeModuleEdges(edges []ModuleEdge) []ModuleEdge {
	result := make([]ModuleEdge, len(edges))
	copy(result, edges)
	for index := range result {
		result[index].ImportAttributes = normalizeImportAttributes(result[index].ImportAttributes)
		result[index].Bindings = slices.Clone(result[index].Bindings)
		if result[index].Kind == "" {
			result[index].Kind = "import"
		}
	}
	slices.SortFunc(result, compareModuleEdges)
	return result
}

func compareModuleBindingSnapshots(a, b ModuleBindingSnapshot) int {
	for _, pair := range [][2]string{
		{string(a.Node), string(b.Node)},
		{a.Kind, b.Kind},
		{a.ImportedName, b.ImportedName},
		{a.LocalName, b.LocalName},
		{a.ExportedName, b.ExportedName},
		{string(a.AliasSymbol), string(b.AliasSymbol)},
		{string(a.TargetSymbol), string(b.TargetSymbol)},
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
		{a.Kind, b.Kind},
		{string(a.Source), string(b.Source)},
		{string(a.SpecifierNode), string(b.SpecifierNode)},
		{a.Specifier, b.Specifier},
		{a.ResolutionMode, b.ResolutionMode},
		{string(a.Imported), string(b.Imported)},
		{a.Resolved, b.Resolved},
		{a.Package, b.Package},
		{a.Extension, b.Extension},
	} {
		if order := strings.Compare(pair[0], pair[1]); order != 0 {
			return order
		}
	}
	if order := compareBool(a.TypeOnly, b.TypeOnly); order != 0 {
		return order
	}
	if order := compareBool(a.Value, b.Value); order != 0 {
		return order
	}
	if order := compareBool(a.SideEffectOnly, b.SideEffectOnly); order != 0 {
		return order
	}
	if order := compareBool(a.DeferredEvaluation, b.DeferredEvaluation); order != 0 {
		return order
	}
	if order := compareBool(a.BindingsComplete, b.BindingsComplete); order != 0 {
		return order
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

func moduleGraphSCCs(modules []ModuleSnapshot, edges []ModuleEdge) []ModuleSCCSnapshot {
	moduleIDs := make([]ModuleID, 0, len(modules))
	moduleSet := make(map[ModuleID]struct{}, len(modules))
	for _, module := range modules {
		if module.ID == "" {
			continue
		}
		moduleIDs = append(moduleIDs, module.ID)
		moduleSet[module.ID] = struct{}{}
	}
	slices.Sort(moduleIDs)

	adjacency := make(map[ModuleID][]ModuleID, len(moduleIDs))
	seenTargets := make(map[ModuleID]map[ModuleID]struct{}, len(moduleIDs))
	for _, edge := range edges {
		if !isEagerModuleEdge(edge) {
			continue
		}
		if _, ok := moduleSet[edge.Importer]; !ok {
			continue
		}
		if _, ok := moduleSet[edge.Imported]; !ok {
			continue
		}
		if seenTargets[edge.Importer] == nil {
			seenTargets[edge.Importer] = make(map[ModuleID]struct{})
		}
		if _, duplicate := seenTargets[edge.Importer][edge.Imported]; duplicate {
			continue
		}
		seenTargets[edge.Importer][edge.Imported] = struct{}{}
		adjacency[edge.Importer] = append(adjacency[edge.Importer], edge.Imported)
	}

	index := 0
	indices := make(map[ModuleID]int, len(moduleIDs))
	lowLinks := make(map[ModuleID]int, len(moduleIDs))
	onStack := make(map[ModuleID]bool, len(moduleIDs))
	stack := make([]ModuleID, 0, len(moduleIDs))
	components := make([][]ModuleID, 0)
	for _, moduleID := range moduleIDs {
		indices[moduleID] = -1
	}

	var visit func(ModuleID)
	visit = func(moduleID ModuleID) {
		indices[moduleID] = index
		lowLinks[moduleID] = index
		index++
		stack = append(stack, moduleID)
		onStack[moduleID] = true

		for _, importedID := range adjacency[moduleID] {
			if indices[importedID] == -1 {
				visit(importedID)
				lowLinks[moduleID] = min(lowLinks[moduleID], lowLinks[importedID])
			} else if onStack[importedID] {
				lowLinks[moduleID] = min(lowLinks[moduleID], indices[importedID])
			}
		}
		if lowLinks[moduleID] != indices[moduleID] {
			return
		}

		component := make([]ModuleID, 0, 1)
		for {
			last := len(stack) - 1
			member := stack[last]
			stack = stack[:last]
			onStack[member] = false
			component = append(component, member)
			if member == moduleID {
				break
			}
		}
		slices.Sort(component)
		components = append(components, component)
	}

	for _, moduleID := range moduleIDs {
		if indices[moduleID] == -1 {
			visit(moduleID)
		}
	}
	slices.SortFunc(components, compareModuleIDLists)
	result := make([]ModuleSCCSnapshot, len(components))
	for index, component := range components {
		result[index] = ModuleSCCSnapshot{ID: index, Modules: component}
	}
	return result
}

func isEagerModuleEdge(edge ModuleEdge) bool {
	return edge.Imported != "" &&
		!edge.TypeOnly &&
		(edge.Value || edge.SideEffectOnly) &&
		!edge.DeferredEvaluation &&
		edge.Kind != "dynamic-import"
}

func compareModuleIDLists(a, b []ModuleID) int {
	for index := 0; index < len(a) && index < len(b); index++ {
		if order := strings.Compare(string(a[index]), string(b[index])); order != 0 {
			return order
		}
	}
	return compareInt(len(a), len(b))
}

func digestModuleGraph(modules []ModuleSnapshot, edges []ModuleEdge, sccs []ModuleSCCSnapshot) string {
	return digestModuleGraphSchema(modules, edges, sccs, moduleGraphDigestSchema)
}

func digestModuleGraphSchema(modules []ModuleSnapshot, edges []ModuleEdge, sccs []ModuleSCCSnapshot, schema int) string {
	if schema == 1 {
		type legacyModuleEdge struct {
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
		legacyEdges := make([]legacyModuleEdge, 0, len(edges))
		for _, edge := range edges {
			legacyEdges = append(legacyEdges, legacyModuleEdge{
				Importer: edge.Importer, Imported: edge.Imported, Specifier: edge.Specifier, Span: edge.Span,
				ResolutionMode: edge.ResolutionMode, Resolved: edge.Resolved, Package: edge.Package, Extension: edge.Extension,
				TypeOnly: edge.TypeOnly, Value: edge.Value, SideEffectOnly: edge.SideEffectOnly, Kind: edge.Kind,
				ImportAttributes: edge.ImportAttributes, DeferredEvaluation: edge.DeferredEvaluation,
			})
		}
		payload := struct {
			Schema  int                 `json:"schema"`
			Modules []ModuleSnapshot    `json:"modules"`
			Edges   []legacyModuleEdge  `json:"edges"`
			SCCs    []ModuleSCCSnapshot `json:"sccs"`
		}{Schema: schema, Modules: modules, Edges: legacyEdges, SCCs: sccs}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return ""
		}
		digest := sha256.Sum256(encoded)
		return hex.EncodeToString(digest[:])
	}
	payload := struct {
		Schema  int                 `json:"schema"`
		Modules []ModuleSnapshot    `json:"modules"`
		Edges   []ModuleEdge        `json:"edges"`
		SCCs    []ModuleSCCSnapshot `json:"sccs"`
	}{
		Schema:  schema,
		Modules: modules,
		Edges:   edges,
		SCCs:    sccs,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}
