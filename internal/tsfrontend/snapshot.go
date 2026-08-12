package tsfrontend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
	"github.com/microsoft/typescript-go/internal/compiler"
	"github.com/microsoft/typescript-go/internal/tspath"
)

// captureGraph is mutable only while all records for one Program are being
// copied. It never escapes captureSnapshot; the returned ProgramSnapshot has
// only value fields and IDs.
type captureGraph struct {
	program         *compiler.Program
	projectRoot     string
	frontend        *Frontend
	fileIDs         map[string]FileID
	moduleIDs       map[string]ModuleID
	nodeIDs         map[*ast.Node]NodeID
	nodeByID        map[NodeID]*pendingNode
	nodeOccurrences map[FileID]map[string]int
	files           []*pendingFile
	origins         []OriginSnapshot
	types           map[string]*pendingType
	symbols         map[SymbolID]*pendingSymbol
	signatures      map[string]*pendingSignature
	moduleBindings  map[*ast.Node]pendingModuleBindings
}

type pendingFile struct {
	dto         FileSnapshot
	file        *ast.SourceFile
	moduleID    ModuleID
	rootNodeIDs []NodeID
}

type pendingNode struct {
	dto                     NodeSnapshot
	declaredKey             string
	narrowedKey             string
	contextualKey           string
	symbolKey               SymbolID
	resolvedKey             SymbolID
	signatureKey            string
	selectedOverloadOrdinal uint32
	assertionTargetKey      string
	assertionAssignable     bool
	assertionChain          []pendingAssertionProof
	nonNullOperandKey       string
	nonNullResultKey        string
	nonNullProofKind        string
	nonNullRemovedNull      bool
	nonNullRemovedUndef     bool
	captureBindings         []pendingCaptureBinding
	captureComplete         bool
}

type pendingAssertionProof struct {
	sourceKey           string
	targetKey           string
	assignable          bool
	openType            string
	representationProof string
}

type pendingCaptureBinding struct {
	symbol  SymbolID
	kind    string
	access  string
	mutable bool
}

type pendingModuleBindings struct {
	bindings []ModuleBindingSnapshot
	complete bool
}

type pendingType struct {
	key                    string
	kind                   string
	flags                  uint32
	objectFlags            uint32
	symbol                 SymbolID
	aliasSymbol            SymbolID
	scalar                 string
	debugText              string
	notLowerableReason     string
	elementKeys            []string
	typeArgumentKeys       []string
	baseTypeKeys           []string
	propertyKeys           []SymbolID
	propertyFacts          []pendingProperty
	callSignatureKeys      []string
	constructSignatureKeys []string
	indexInfos             []pendingIndexInfo
	constraintKey          string
	defaultKey             string
	variance               string
	ptr                    *checker.Type
}

type pendingProperty struct {
	symbol          SymbolID
	readKey         string
	writeKey        string
	optional        bool
	readonly        bool
	hasGetter       bool
	hasSetter       bool
	visibility      string
	privateIdentity string
}

type pendingIndexInfo struct {
	keyType     string
	valueType   string
	readonly    bool
	declaration NodeID
}

type pendingSymbol struct {
	id               SymbolID
	name             string
	flags            uint32
	checkFlags       uint32
	parent           SymbolID
	exportSymbol     SymbolID
	declarations     []NodeID
	valueDeclaration NodeID
	typeKey          string
	ptr              *ast.Symbol
}

type pendingSignature struct {
	key                    string
	declaration            NodeID
	flags                  uint32
	thisParameter          SymbolID
	parameters             []SymbolID
	parameterTypeKeys      []string
	parameterFacts         []pendingParameter
	minArgumentCount       int
	hasRest                bool
	typeParameterKeys      []string
	instantiatedTypeKeys   []string
	returnTypeKey          string
	predicate              TypePredicateSnapshot
	predicateTypeKey       string
	callingConventionClass string
	effects                []string
	effectProofKind        string
	effectProofComplete    bool
	effectImplementation   NodeID
	directEffects          []string
	effectCalls            []pendingEffectCall
	ptr                    *checker.Signature
}

type pendingEffectCall struct {
	node         NodeID
	signatureKey string
}

type pendingParameter struct {
	symbol   SymbolID
	typeKey  string
	optional bool
	rest     bool
}

type captureContext struct {
	graph              *captureGraph
	checker            *checker.Checker
	file               *ast.SourceFile
	fileID             FileID
	moduleID           ModuleID
	typeByPointer      map[*checker.Type]string
	symbolByPointer    map[*ast.Symbol]SymbolID
	signatureByPointer map[*checker.Signature]string
	ordinalType        int
	ordinalSignature   int
}

func (s *programBuild) captureSnapshot(ctx context.Context, frontend *Frontend) (*ProgramSnapshot, []Diagnostic) {
	if s == nil || s.program == nil {
		return nil, []Diagnostic{NewRegisteredDiagnostic(DiagnosticCodeInternalFailure, SourceSpan{}, "nil program")}
	}
	if frontend == nil {
		frontend = NewOSFrontend(TypeScriptGoCommit)
	}
	graph := &captureGraph{
		program:         s.program,
		projectRoot:     s.projectRoot,
		frontend:        frontend,
		fileIDs:         make(map[string]FileID),
		moduleIDs:       make(map[string]ModuleID),
		nodeIDs:         make(map[*ast.Node]NodeID),
		nodeByID:        make(map[NodeID]*pendingNode),
		nodeOccurrences: make(map[FileID]map[string]int),
		types:           make(map[string]*pendingType),
		symbols:         make(map[SymbolID]*pendingSymbol),
		signatures:      make(map[string]*pendingSignature),
		moduleBindings:  make(map[*ast.Node]pendingModuleBindings),
	}

	files := slices.Clone(s.program.SourceFiles())
	slices.SortFunc(files, func(a, b *ast.SourceFile) int {
		return strings.Compare(frontend.logicalPath(a.FileName(), s.projectRoot), frontend.logicalPath(b.FileName(), s.projectRoot))
	})
	for _, file := range files {
		// Bundled declarations are represented by the stdlib provenance hash, not
		// copied into every user snapshot. Their symbols/types remain capturable
		// as external records with stable @stdlib declaration anchors.
		if s.program.IsSourceFileDefaultLibrary(file.Path()) {
			continue
		}
		fileID := graph.fileID(file)
		moduleID := graph.moduleID(file)
		graph.assignNodeIDs(file, fileID)
		graph.files = append(graph.files, &pendingFile{
			file:        file,
			dto:         graph.fileDTO(file, fileID, moduleID),
			moduleID:    moduleID,
			rootNodeIDs: []NodeID{graph.nodeIDs[file.AsNode()]},
		})
	}

	var diagnostics []Diagnostic
	for _, pending := range graph.files {
		if err := graph.captureFile(ctx, pending); err != nil {
			diagnostic := NewRegisteredDiagnostic(DiagnosticCodeInternalFailure, SourceSpan{File: frontend.logicalPath(pending.file.FileName(), s.projectRoot)}, err.Error())
			diagnostic.Stage = DiagnosticStageSnapshot
			diagnostic.EntityID = string(pending.dto.ID)
			diagnostics = append(diagnostics, diagnostic)
		}
	}
	if len(diagnostics) != 0 {
		return nil, SortAndDeduplicateDiagnostics(diagnostics)
	}

	// Module resolution is copied only after all source node IDs exist, so edge
	// spans and import attributes never depend on map iteration order.
	moduleGraph := graph.captureModuleGraphData()
	stdlibHash, err := frontend.stdlibHash()
	if err != nil {
		diagnostic := NewRegisteredDiagnostic(DiagnosticCodeInternalFailure, SourceSpan{}, err.Error())
		diagnostic.Stage = DiagnosticStageSnapshot
		return nil, []Diagnostic{diagnostic}
	}

	snapshot, err := graph.finish(moduleGraph.Edges, moduleGraph.Modules, s, stdlibHash)
	if err != nil {
		diagnostic := NewRegisteredDiagnostic(DiagnosticCodeInternalFailure, SourceSpan{}, err.Error())
		diagnostic.Stage = DiagnosticStageSnapshot
		return nil, []Diagnostic{diagnostic}
	}
	snapshot.ModuleSCCs = cloneModuleSCCs(moduleGraph.SCCs)
	snapshot.ModuleGraphDigest = moduleGraph.Digest
	return snapshot, nil
}

func (g *captureGraph) fileID(file *ast.SourceFile) FileID {
	path := g.frontend.logicalPath(file.FileName(), g.projectRoot)
	if id, ok := g.fileIDs[path]; ok {
		return id
	}
	projectIdentity := g.frontend.logicalPath(g.projectRoot, g.projectRoot)
	id := stableID("file", projectIdentity, path)
	g.fileIDs[path] = FileID(id)
	return FileID(id)
}

func (g *captureGraph) moduleID(file *ast.SourceFile) ModuleID {
	path := g.frontend.logicalPath(file.FileName(), g.projectRoot)
	format := g.program.GetEmitModuleFormatOfFile(file).String()
	mode := g.program.GetImpliedNodeFormatForEmit(file).String()
	key := path + "|" + format + "|" + mode
	if id, ok := g.moduleIDs[key]; ok {
		return id
	}
	id := ModuleID(stableID("module", key))
	g.moduleIDs[key] = id
	return id
}

func (g *captureGraph) fileDTO(file *ast.SourceFile, id FileID, moduleID ModuleID) FileSnapshot {
	contentDigest := sha256.Sum256([]byte(file.Text()))
	refs := make([]string, 0, len(file.ReferencedFiles))
	for _, ref := range file.ReferencedFiles {
		if ref != nil {
			refs = append(refs, ref.FileName)
		}
	}
	typeRefs := make([]string, 0, len(file.TypeReferenceDirectives))
	for _, ref := range file.TypeReferenceDirectives {
		if ref != nil {
			typeRefs = append(typeRefs, ref.FileName)
		}
	}
	libRefs := make([]string, 0, len(file.LibReferenceDirectives))
	for _, ref := range file.LibReferenceDirectives {
		if ref != nil {
			libRefs = append(libRefs, ref.FileName)
		}
	}
	slices.Sort(refs)
	slices.Sort(typeRefs)
	slices.Sort(libRefs)
	return FileSnapshot{
		ID:                  id,
		CanonicalPath:       g.frontend.logicalPath(file.FileName(), g.projectRoot),
		ContentHash:         hex.EncodeToString(contentDigest[:]),
		SourceBlob:          file.Text(),
		ScriptKind:          file.ScriptKind.String(),
		IsDeclarationFile:   file.IsDeclarationFile,
		IsExternalModule:    ast.IsExternalModule(file),
		EmitModuleFormat:    g.program.GetEmitModuleFormatOfFile(file).String(),
		ImpliedModuleFormat: g.program.GetImpliedNodeFormatForEmit(file).String(),
		References:          refs,
		TypeReferences:      typeRefs,
		LibReferences:       libRefs,
		RootNodes:           []NodeID{},
	}
}

func (g *captureGraph) assignNodeIDs(file *ast.SourceFile, fileID FileID) {
	occurrences := make(map[string]int)
	g.nodeOccurrences[fileID] = occurrences
	var visit func(*ast.Node, NodeID)
	visit = func(node *ast.Node, parent NodeID) {
		if node == nil {
			return
		}
		kind := snapshotNodeKind(node)
		key := fmt.Sprintf("%d:%d:%d", kind, node.Pos(), node.End())
		occurrence := occurrences[key]
		occurrences[key]++
		id := NodeID(stableID("node", string(fileID), strconv.Itoa(int(kind)), strconv.Itoa(node.Pos()), strconv.Itoa(node.End()), strconv.Itoa(occurrence)))
		g.nodeIDs[node] = id
		g.nodeByID[id] = &pendingNode{dto: NodeSnapshot{
			ID:            id,
			Origin:        OriginID(stableID("origin", string(id))),
			File:          fileID,
			Kind:          kind.String(),
			KindValue:     int16(kind),
			Span:          Span{File: fileID, Start: max(0, node.Pos()), End: max(max(0, node.Pos()), node.End())},
			Parent:        parent,
			Children:      []NodeID{},
			NamedChildren: []NamedChild{},
			SyntaxPayload: SyntaxPayload{Tag: kind.String()},
			Constant:      ConstantSnapshot{Kind: "none"},
			Flow:          FlowFactSnapshot{},
		}}
		g.origins = append(g.origins, OriginSnapshot{ID: g.nodeByID[id].dto.Origin, Node: id, Span: g.nodeByID[id].dto.Span})
		for _, child := range g.nodeChildren(file, node, false) {
			visit(child, id)
			if childID, ok := g.nodeIDs[child]; ok {
				g.nodeByID[id].dto.Children = append(g.nodeByID[id].dto.Children, childID)
			}
		}
	}
	visit(file.AsNode(), "")
}

// assignJSDocNodeIDs runs while the per-file checker lease is held. JSDoc is
// lazily parsed by the AST, so assigning IDs outside captureFile would race
// the parser's cache and make snapshot construction order-dependent.
func (g *captureGraph) assignJSDocNodeIDs(file *ast.SourceFile, fileID FileID) {
	occurrences := g.nodeOccurrences[fileID]
	seen := make(map[*ast.Node]struct{})
	var visit func(*ast.Node)
	visit = func(node *ast.Node) {
		if node == nil {
			return
		}
		for _, child := range g.nodeChildren(file, node, true) {
			if _, alreadyVisited := seen[child]; alreadyVisited {
				continue
			}
			seen[child] = struct{}{}
			if _, assigned := g.nodeIDs[child]; !assigned {
				parentID := g.nodeIDs[node]
				kind := snapshotNodeKind(child)
				key := fmt.Sprintf("%d:%d:%d", kind, child.Pos(), child.End())
				occurrence := occurrences[key]
				occurrences[key]++
				id := NodeID(stableID("node", string(fileID), strconv.Itoa(int(kind)), strconv.Itoa(child.Pos()), strconv.Itoa(child.End()), strconv.Itoa(occurrence)))
				g.nodeIDs[child] = id
				g.nodeByID[id] = &pendingNode{dto: NodeSnapshot{
					ID:            id,
					Origin:        OriginID(stableID("origin", string(id))),
					File:          fileID,
					Kind:          kind.String(),
					KindValue:     int16(kind),
					Span:          Span{File: fileID, Start: max(0, child.Pos()), End: max(max(0, child.Pos()), child.End())},
					Parent:        parentID,
					Children:      []NodeID{},
					NamedChildren: []NamedChild{},
					SyntaxPayload: SyntaxPayload{Tag: kind.String()},
					Constant:      ConstantSnapshot{Kind: "none"},
					Flow:          FlowFactSnapshot{},
				}}
				g.nodeByID[parentID].dto.Children = append(g.nodeByID[parentID].dto.Children, id)
				g.origins = append(g.origins, OriginSnapshot{ID: g.nodeByID[id].dto.Origin, Node: id, Span: g.nodeByID[id].dto.Span})
			}
			visit(child)
		}
	}
	seen[file.AsNode()] = struct{}{}
	visit(file.AsNode())
}

func (g *captureGraph) nodeChildren(file *ast.SourceFile, node *ast.Node, includeJSDoc bool) []*ast.Node {
	children := slices.Collect(node.IterChildren())
	if includeJSDoc {
		children = append(children, node.JSDoc(file)...)
	}
	return children
}

func snapshotNodeKind(node *ast.Node) ast.Kind {
	if node.Kind == ast.KindJsxText && node.AsJsxText().ContainsOnlyTriviaWhiteSpaces {
		return ast.KindJsxTextAllWhiteSpaces
	}
	return node.Kind
}

func snapshotSyntaxPayload(node *ast.Node) SyntaxPayload {
	payload := SyntaxPayload{Tag: snapshotNodeKind(node).String()}
	switch node.Kind {
	case ast.KindIdentifier, ast.KindPrivateIdentifier, ast.KindStringLiteral,
		ast.KindNumericLiteral, ast.KindBigIntLiteral, ast.KindRegularExpressionLiteral,
		ast.KindNoSubstitutionTemplateLiteral, ast.KindTemplateHead,
		ast.KindTemplateMiddle, ast.KindTemplateTail, ast.KindJSDocText:
		payload.Text = node.Text()
	case ast.KindBinaryExpression:
		payload.Operator = node.AsBinaryExpression().OperatorToken.Kind.String()
	case ast.KindPrefixUnaryExpression:
		payload.Operator = node.AsPrefixUnaryExpression().Operator.String()
	case ast.KindPostfixUnaryExpression:
		payload.Operator = node.AsPostfixUnaryExpression().Operator.String()
	}
	return payload
}

func syntaxChildRoleHints(node *ast.Node, file *ast.SourceFile) map[*ast.Node]string {
	roles := make(map[*ast.Node]string)
	add := func(child *ast.Node, role string) {
		if child != nil {
			roles[child] = role
		}
	}
	addList := func(children []*ast.Node, role string) {
		for index, child := range children {
			add(child, fmt.Sprintf("%s[%d]", role, index))
		}
	}

	addList(node.ModifierNodes(), "modifier")
	for index, child := range node.JSDoc(file) {
		add(child, fmt.Sprintf("jsDoc[%d]", index))
	}
	if node.CanHaveStatements() {
		addList(node.Statements(), "statement")
	}
	if ast.IsFunctionLike(node) {
		add(node.Name(), "name")
		data := node.FunctionLikeData()
		if data != nil {
			if data.TypeParameters != nil {
				addList(data.TypeParameters.Nodes, "typeParameter")
			}
			if data.Parameters != nil {
				addList(data.Parameters.Nodes, "parameter")
			}
			add(data.Type, "returnType")
			add(data.FullSignature, "fullSignature")
		}
		if body := node.BodyData(); body != nil {
			add(body.AsteriskToken, "asteriskToken")
		}
		add(node.Body(), "body")
	}

	switch node.Kind {
	case ast.KindImportDeclaration, ast.KindJSImportDeclaration:
		declaration := node.AsImportDeclaration()
		add(declaration.ImportClause, "importClause")
		add(declaration.ModuleSpecifier, "moduleSpecifier")
		add(declaration.Attributes, "attributes")
	case ast.KindImportEqualsDeclaration:
		declaration := node.AsImportEqualsDeclaration()
		add(declaration.Name(), "name")
		add(declaration.ModuleReference, "moduleReference")
	case ast.KindExternalModuleReference:
		add(node.AsExternalModuleReference().Expression, "expression")
	case ast.KindImportClause:
		clause := node.AsImportClause()
		add(clause.Name(), "defaultBinding")
		add(clause.NamedBindings, "namedBindings")
	case ast.KindNamedImports:
		addList(node.AsNamedImports().Elements.Nodes, "specifier")
	case ast.KindImportSpecifier:
		specifier := node.AsImportSpecifier()
		add(specifier.PropertyName, "propertyName")
		add(specifier.Name(), "name")
	case ast.KindNamespaceImport:
		add(node.Name(), "name")
	case ast.KindExportDeclaration:
		declaration := node.AsExportDeclaration()
		add(declaration.ExportClause, "exportClause")
		add(declaration.ModuleSpecifier, "moduleSpecifier")
		add(declaration.Attributes, "attributes")
	case ast.KindNamedExports:
		addList(node.AsNamedExports().Elements.Nodes, "specifier")
	case ast.KindExportSpecifier:
		specifier := node.AsExportSpecifier()
		add(specifier.PropertyName, "propertyName")
		add(specifier.Name(), "name")
	case ast.KindNamespaceExport:
		add(node.Name(), "name")
	case ast.KindVariableStatement:
		add(node.AsVariableStatement().DeclarationList, "declarationList")
	case ast.KindVariableDeclarationList:
		addList(node.AsVariableDeclarationList().Declarations.Nodes, "declaration")
	case ast.KindVariableDeclaration:
		add(node.Name(), "name")
		add(node.Type(), "type")
		add(node.Initializer(), "initializer")
	case ast.KindBindingElement:
		element := node.AsBindingElement()
		add(element.DotDotDotToken, "dotDotDotToken")
		add(element.PropertyName, "propertyName")
		add(node.Name(), "name")
		add(element.Initializer, "initializer")
	case ast.KindPropertyAssignment:
		assignment := node.AsPropertyAssignment()
		add(node.Name(), "name")
		add(assignment.Initializer, "initializer")
	case ast.KindShorthandPropertyAssignment:
		assignment := node.AsShorthandPropertyAssignment()
		add(node.Name(), "name")
		add(assignment.ObjectAssignmentInitializer, "initializer")
	case ast.KindSpreadAssignment:
		add(node.AsSpreadAssignment().Expression, "expression")
	case ast.KindParameter:
		parameter := node.AsParameterDeclaration()
		add(parameter.DotDotDotToken, "dotDotDotToken")
		add(node.Name(), "name")
		add(parameter.QuestionToken, "questionToken")
		add(node.Type(), "type")
		add(node.Initializer(), "initializer")
	case ast.KindReturnStatement:
		add(node.AsReturnStatement().Expression, "expression")
	case ast.KindForInStatement, ast.KindForOfStatement:
		statement := node.AsForInOrOfStatement()
		add(statement.AwaitModifier, "awaitModifier")
		add(statement.Initializer, "initializer")
		add(statement.Expression, "expression")
		add(statement.Statement, "statement")
	case ast.KindExpressionStatement:
		add(node.AsExpressionStatement().Expression, "expression")
	case ast.KindBinaryExpression:
		binary := node.AsBinaryExpression()
		add(binary.Left, "left")
		add(binary.Type, "type")
		add(binary.OperatorToken, "operator")
		add(binary.Right, "right")
	case ast.KindPrefixUnaryExpression:
		add(node.AsPrefixUnaryExpression().Operand, "operand")
	case ast.KindPostfixUnaryExpression:
		add(node.AsPostfixUnaryExpression().Operand, "operand")
	case ast.KindCallExpression:
		call := node.AsCallExpression()
		add(call.Expression, "callee")
		add(call.QuestionDotToken, "questionDot")
		if call.TypeArguments != nil {
			addList(call.TypeArguments.Nodes, "typeArgument")
		}
		if call.Arguments != nil {
			addList(call.Arguments.Nodes, "argument")
		}
	case ast.KindNewExpression:
		call := node.AsNewExpression()
		add(call.Expression, "constructor")
		if call.TypeArguments != nil {
			addList(call.TypeArguments.Nodes, "typeArgument")
		}
		if call.Arguments != nil {
			addList(call.Arguments.Nodes, "argument")
		}
	case ast.KindParenthesizedExpression, ast.KindAsExpression,
		ast.KindTypeAssertionExpression, ast.KindSatisfiesExpression,
		ast.KindNonNullExpression:
		add(node.Expression(), "expression")
		add(node.Type(), "type")
	}
	return roles
}

func (c *captureContext) captureSyntax(record *pendingNode, node *ast.Node) {
	record.dto.SyntaxPayload = snapshotSyntaxPayload(node)
	record.dto.NamedChildren = record.dto.NamedChildren[:0]
	hints := syntaxChildRoleHints(node, c.file)
	roleByID := make(map[NodeID]string, len(record.dto.Children))
	for index, child := range c.graph.nodeChildren(c.file, node, true) {
		childID, ok := c.graph.nodeIDs[child]
		if !ok {
			panic(checkerCaptureError{Operation: "capture syntax child", Cause: fmt.Sprintf("%s child %d has no NodeID", node.Kind, index)})
		}
		childRecord := c.graph.nodeByID[childID]
		if childRecord == nil || childRecord.dto.Parent != record.dto.ID {
			continue
		}
		role := hints[child]
		if role == "" {
			role = fmt.Sprintf("child[%d]", index)
		}
		roleByID[childID] = role
	}
	seenRoles := make(map[string]struct{}, len(record.dto.Children))
	for index, childID := range record.dto.Children {
		role := roleByID[childID]
		if role == "" {
			role = fmt.Sprintf("child[%d]", index)
		}
		if _, duplicate := seenRoles[role]; duplicate {
			role = fmt.Sprintf("%s#%d", role, index)
		}
		seenRoles[role] = struct{}{}
		record.dto.NamedChildren = append(record.dto.NamedChildren, NamedChild{Role: role, Node: childID})
	}
}

func (g *captureGraph) captureFile(ctx context.Context, pending *pendingFile) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("checker capture panic: %v", recovered)
		}
	}()
	_, err = withCheckerForFile(ctx, g.program, pending.file, func(typeChecker *checker.Checker) (struct{}, error) {
		g.assignJSDocNodeIDs(pending.file, pending.dto.ID)
		capture := &captureContext{
			graph:              g,
			checker:            typeChecker,
			file:               pending.file,
			fileID:             pending.dto.ID,
			moduleID:           pending.moduleID,
			typeByPointer:      make(map[*checker.Type]string),
			symbolByPointer:    make(map[*ast.Symbol]SymbolID),
			signatureByPointer: make(map[*checker.Signature]string),
		}
		seen := make(map[*ast.Node]struct{})
		var visit func(*ast.Node)
		visit = func(node *ast.Node) {
			if node == nil {
				return
			}
			if _, alreadyVisited := seen[node]; alreadyVisited {
				return
			}
			seen[node] = struct{}{}
			pendingNode := g.nodeByID[g.nodeIDs[node]]
			capture.captureNode(pendingNode, node)
			for _, child := range g.nodeChildren(pending.file, node, true) {
				visit(child)
			}
		}
		visit(pending.file.AsNode())
		capture.captureModuleBindings()
		pending.dto.RootNodes = []NodeID{g.nodeIDs[pending.file.AsNode()]}
		return struct{}{}, nil
	})
	return err
}

func (c *captureContext) captureModuleBindings() {
	for _, specifier := range c.file.Imports() {
		if specifier == nil || !ast.IsStringLiteralLike(specifier.AsNode()) {
			continue
		}
		c.graph.moduleBindings[specifier.AsNode()] = c.moduleBindingProof(specifier.AsNode())
	}
}

func (c *captureContext) moduleBindingProof(specifier *ast.Node) pendingModuleBindings {
	proof := pendingModuleBindings{complete: true}
	if specifier == nil {
		proof.complete = false
		return proof
	}
	add := func(node *ast.Node, kind, importedName, localName, exportedName string, typeOnly, value bool) {
		binding := ModuleBindingSnapshot{Node: c.graph.nodeIDs[node], Kind: kind, ImportedName: importedName, LocalName: localName, ExportedName: exportedName, TypeOnly: typeOnly, Value: value}
		if node != nil {
			aliasNode := node
			switch node.Kind {
			case ast.KindImportClause:
				aliasNode = node.AsImportClause().Name()
			case ast.KindImportSpecifier:
				aliasNode = node.AsImportSpecifier().Name()
			case ast.KindNamespaceImport:
				aliasNode = node.Name()
			case ast.KindImportEqualsDeclaration:
				aliasNode = node.AsImportEqualsDeclaration().Name()
			case ast.KindExportSpecifier:
				aliasNode = node.AsExportSpecifier().Name()
			case ast.KindNamespaceExport:
				aliasNode = node.AsNamespaceExport().Name()
			}
			if aliasNode != nil {
				alias := safeSymbolAtLocation(c.checker, aliasNode)
				if alias != nil {
					binding.AliasSymbol = c.captureSymbol(alias)
					if alias.Flags&ast.SymbolFlagsAlias != 0 {
						if target := safeAliasedSymbol(c.checker, alias); target != nil {
							binding.TargetSymbol = c.captureSymbol(target)
						}
					}
				}
			}
		}
		proof.bindings = append(proof.bindings, binding)
	}

	parent := specifier.Parent
	switch {
	case parent != nil && (ast.IsImportDeclaration(parent) || ast.IsJSImportDeclaration(parent)):
		declaration := parent.AsImportDeclaration()
		if declaration.ImportClause == nil {
			return proof
		}
		clause := declaration.ImportClause.AsImportClause()
		phaseTypeOnly := clause.PhaseModifier == ast.KindTypeKeyword
		if clause.Name() != nil {
			add(declaration.ImportClause, "default-import", "default", clause.Name().Text(), "", phaseTypeOnly, !phaseTypeOnly)
		}
		switch bindings := clause.NamedBindings; {
		case bindings != nil && ast.IsNamedImports(bindings):
			for _, element := range bindings.AsNamedImports().Elements.Nodes {
				if element == nil || !ast.IsImportSpecifier(element) {
					proof.complete = false
					continue
				}
				spec := element.AsImportSpecifier()
				imported := spec.Name().Text()
				if spec.PropertyName != nil {
					imported = spec.PropertyName.Text()
				}
				typeOnly := phaseTypeOnly || spec.IsTypeOnly
				add(element, "named-import", imported, spec.Name().Text(), "", typeOnly, !typeOnly)
			}
		case bindings != nil && ast.IsNamespaceImport(bindings):
			name := bindings.Name()
			add(bindings, "namespace-import", "*", name.Text(), "", phaseTypeOnly, !phaseTypeOnly)
		}
	case parent != nil && ast.IsExportDeclaration(parent):
		declaration := parent.AsExportDeclaration()
		phaseTypeOnly := declaration.IsTypeOnly
		switch clause := declaration.ExportClause; {
		case clause == nil:
			add(parent, "export-star", "*", "", "*", phaseTypeOnly, !phaseTypeOnly)
		case ast.IsNamedExports(clause):
			for _, element := range clause.AsNamedExports().Elements.Nodes {
				if element == nil || !ast.IsExportSpecifier(element) {
					proof.complete = false
					continue
				}
				spec := element.AsExportSpecifier()
				imported := spec.Name().Text()
				if spec.PropertyName != nil {
					imported = spec.PropertyName.Text()
				}
				typeOnly := phaseTypeOnly || spec.IsTypeOnly
				if declaration.ModuleSpecifier != nil {
					add(element, "named-reexport", imported, "", spec.Name().Text(), typeOnly, !typeOnly)
				}
			}
		case ast.IsNamespaceExport(clause):
			name := clause.AsNamespaceExport().Name()
			add(clause, "namespace-reexport", "*", "", name.Text(), phaseTypeOnly, !phaseTypeOnly)
		default:
			proof.complete = false
		}
	case parent != nil && ast.IsExternalModuleReference(parent) && parent.Parent != nil && ast.IsImportEqualsDeclaration(parent.Parent):
		declaration := parent.Parent.AsImportEqualsDeclaration()
		name := declaration.Name()
		typeOnly := declaration.IsTypeOnly
		add(declaration.AsNode(), "import-equals", "*", name.Text(), "", typeOnly, !typeOnly)
	default:
		// Dynamic imports, require, and import-type edges have no static
		// bindings. Their explicit complete zero-binding proof is meaningful.
	}
	return proof
}

func (c *captureContext) captureNode(record *pendingNode, node *ast.Node) {
	if record == nil || node == nil {
		return
	}
	record.dto.NodeFlags = uint32(node.Flags)
	record.dto.ModifierBits = uint32(node.ModifierFlags())
	record.dto.Module = c.moduleID
	parentTypeContext := false
	if node.Parent != nil {
		if parent := c.graph.nodeByID[c.graph.nodeIDs[node.Parent]]; parent != nil {
			parentTypeContext = parent.dto.EvaluationFlags&snapshotEvaluationFlagTypeContext != 0
		}
	}
	if parentTypeContext || snapshotEffectASTNodeIsTypeContext(node) {
		record.dto.EvaluationFlags |= snapshotEvaluationFlagTypeContext
	}
	c.captureSyntax(record, node)
	if ast.NodeIsMissing(node) {
		record.dto.EvaluationFlags |= snapshotEvaluationFlagParserMissing
		return
	}
	if shouldQueryNodeSemantics(node) {
		if symbol := c.checker.GetSymbolAtLocation(node); symbol != nil {
			record.symbolKey = c.captureSymbol(symbol)
			record.dto.Symbol = record.symbolKey
		}
		if ast.IsIdentifier(node) || ast.IsPrivateIdentifier(node) {
			resolved := c.checker.GetResolvedSymbol(node)
			if resolved != nil {
				record.resolvedKey = c.captureSymbol(resolved)
				record.dto.ResolvedSymbol = record.resolvedKey
			}
		}
		if typ := safeTypeAtLocation(c.checker, node); typ != nil {
			record.narrowedKey = c.captureType(typ)
			record.dto.NarrowedType = 0
		}
		if symbol := record.symbolKey; symbol != "" {
			if symbolRecord := c.graph.symbols[symbol]; symbolRecord != nil && symbolRecord.typeKey != "" {
				record.declaredKey = symbolRecord.typeKey
			}
		}
		if isTypeAssertionNode(node) {
			c.captureAssertionChain(record, node)
		}
		if node.Kind == ast.KindNonNullExpression {
			c.captureNonNullProof(record, node)
		}
		if ast.IsExpression(node) {
			if contextual := safeContextualType(c.checker, node); contextual != nil {
				record.contextualKey = c.captureType(contextual)
			}
		}
	}
	if isResolvedSignatureNode(node) {
		signature := safeResolvedSignature(c.checker, node)
		if signature != nil {
			record.signatureKey = c.captureSignature(signature, callingConventionForNode(node))
			record.selectedOverloadOrdinal = selectedOverloadOrdinal(c.checker, node, signature)
			record.dto.SelectedOverloadOrdinal = record.selectedOverloadOrdinal
		}
	}
	record.dto.Constant = safeConstantValue(c.checker, node)
	if ast.IsFunctionLike(node) {
		record.captureBindings = c.captureBindings(node)
		record.captureComplete = true
		record.dto.CaptureComplete = true
		for _, binding := range record.captureBindings {
			if binding.symbol != "" {
				record.dto.CaptureSet = append(record.dto.CaptureSet, binding.symbol)
			}
		}
	}
}

func isTypeAssertionNode(node *ast.Node) bool {
	if node == nil {
		return false
	}
	switch node.Kind {
	case ast.KindAsExpression, ast.KindTypeAssertionExpression, ast.KindSatisfiesExpression:
		return true
	default:
		return false
	}
}

func (c *captureContext) captureAssertionChain(record *pendingNode, outer *ast.Node) {
	for current := outer; isTypeAssertionNode(current); current = current.Expression() {
		operand := current.Expression()
		if operand == nil || current.Type() == nil {
			panic(checkerCaptureError{Operation: "capture assertion chain", Cause: "missing operand or target type"})
		}
		source := safeTypeAtLocation(c.checker, operand)
		target := safeTypeFromTypeNode(c.checker, current.Type())
		assignable := safeTypeAssignable(c.checker, source, target)
		proof := pendingAssertionProof{
			sourceKey: c.captureType(source), targetKey: c.captureType(target), assignable: assignable,
			openType: openCheckerType(source, target), representationProof: assertionRepresentationProof(source, target, assignable),
		}
		record.assertionChain = append(record.assertionChain, proof)
	}
	// The wire order follows evaluation from the innermost source to the final
	// target, which makes any/unknown traversal directly auditable.
	slices.Reverse(record.assertionChain)
	if len(record.assertionChain) != 0 {
		first := record.assertionChain[0]
		last := record.assertionChain[len(record.assertionChain)-1]
		record.declaredKey = first.sourceKey
		record.assertionTargetKey = last.targetKey
		record.assertionAssignable = last.assignable
	}
}

func (c *captureContext) captureNonNullProof(record *pendingNode, node *ast.Node) {
	operand := node.Expression()
	if operand == nil {
		panic(checkerCaptureError{Operation: "capture non-null proof", Cause: "missing operand"})
	}
	source := safeTypeAtLocation(c.checker, operand)
	result := safeTypeAtLocation(c.checker, node)
	record.nonNullOperandKey = c.captureType(source)
	record.nonNullResultKey = c.captureType(result)
	record.declaredKey = record.nonNullOperandKey
	hadNull := checkerTypeContainsFlags(source, checker.TypeFlagsNull)
	hadUndefined := checkerTypeContainsFlags(source, checker.TypeFlagsUndefined)
	record.nonNullRemovedNull = hadNull && !checkerTypeContainsFlags(result, checker.TypeFlagsNull)
	record.nonNullRemovedUndef = hadUndefined && !checkerTypeContainsFlags(result, checker.TypeFlagsUndefined)
	switch {
	case source.Flags()&checker.TypeFlagsAny != 0:
		record.nonNullProofKind = "open-any"
	case source.Flags()&checker.TypeFlagsUnknown != 0:
		record.nonNullProofKind = "open-unknown"
	case !hadNull && !hadUndefined:
		record.nonNullProofKind = "redundant-non-null"
	case record.nonNullRemovedNull || record.nonNullRemovedUndef:
		record.nonNullProofKind = "assertion-strip"
	default:
		record.nonNullProofKind = "unproven"
	}
}

func openCheckerType(types ...*checker.Type) string {
	for _, typ := range types {
		if typ == nil {
			continue
		}
		if checkerTypeContainsFlags(typ, checker.TypeFlagsAny) {
			return "any"
		}
		if checkerTypeContainsFlags(typ, checker.TypeFlagsUnknown) {
			return "unknown"
		}
	}
	return ""
}

func assertionRepresentationProof(source, target *checker.Type, assignable bool) string {
	if source == target {
		return "identity"
	}
	if openCheckerType(source, target) != "" {
		return "open-type"
	}
	if assignable {
		return "source-assignable"
	}
	return "checked-adapter-required"
}

func checkerTypeContainsFlags(typ *checker.Type, flags checker.TypeFlags) bool {
	if typ == nil {
		return false
	}
	if typ.Flags()&flags != 0 {
		return true
	}
	if typ.Flags()&checker.TypeFlagsUnionOrIntersection != 0 {
		for _, member := range typ.Types() {
			if checkerTypeContainsFlags(member, flags) {
				return true
			}
		}
	}
	return false
}

func shouldQueryNodeSemantics(node *ast.Node) bool {
	switch node.Kind {
	case ast.KindImportClause, ast.KindImportSpecifier, ast.KindExportSpecifier,
		ast.KindNamespaceImport, ast.KindNamedImports, ast.KindNamespaceExport,
		ast.KindNamedExports, ast.KindTypeParameter:
		// These syntax-only wrapper nodes are not stable checker query points
		// in typescript-go (notably for JS reparses and JSDoc-backed programs).
		// Their symbols/types are captured through their declaration children.
		return false
	}
	return ast.IsExpression(node) || ast.IsDeclaration(node) || ast.IsTypeNode(node) || ast.IsIdentifier(node) || ast.IsPrivateIdentifier(node)
}

func isResolvedSignatureNode(node *ast.Node) bool {
	return node != nil && ast.IsCallLikeExpression(node)
}

func callingConventionForNode(node *ast.Node) string {
	if node.Kind == ast.KindNewExpression {
		return "construct"
	}
	return "call"
}

type checkerCaptureError struct {
	Operation string
	Cause     any
}

func (e checkerCaptureError) Error() string {
	return fmt.Sprintf("checker operation %s failed: %v", e.Operation, e.Cause)
}

func checkedCheckerCall[T any](operation string, call func() T) (result T) {
	defer func() {
		if recovered := recover(); recovered != nil {
			panic(checkerCaptureError{Operation: operation, Cause: recovered})
		}
	}()
	return call()
}

func safeTypeAtLocation(c *checker.Checker, node *ast.Node) *checker.Type {
	operation := "GetTypeAtLocation"
	if node != nil {
		operation = fmt.Sprintf("GetTypeAtLocation(%s@%d:%d)", node.Kind, node.Pos(), node.End())
	}
	result := checkedCheckerCall(operation, func() *checker.Type {
		return c.GetTypeAtLocation(node)
	})
	if result == nil {
		panic(checkerCaptureError{Operation: operation, Cause: "returned nil type"})
	}
	return result
}

func safeContextualType(c *checker.Checker, node *ast.Node) *checker.Type {
	return checkedCheckerCall("GetContextualType", func() *checker.Type {
		return c.GetContextualType(node, checker.ContextFlagsNone)
	})
}

func safeTypeFromTypeNode(c *checker.Checker, node *ast.Node) *checker.Type {
	if c == nil || node == nil {
		panic(checkerCaptureError{Operation: "GetTypeFromTypeNode", Cause: "nil checker or node"})
	}
	result := checkedCheckerCall("GetTypeFromTypeNode", func() *checker.Type {
		return c.GetTypeFromTypeNode(node)
	})
	if result == nil {
		panic(checkerCaptureError{Operation: "GetTypeFromTypeNode", Cause: "returned nil type"})
	}
	return result
}

func safeTypeAssignable(c *checker.Checker, source, target *checker.Type) bool {
	if c == nil || source == nil || target == nil {
		panic(checkerCaptureError{Operation: "IsTypeAssignableTo", Cause: "nil checker or type"})
	}
	return checkedCheckerCall("IsTypeAssignableTo", func() bool {
		return c.IsTypeAssignableTo(source, target)
	})
}

func safeResolvedSignature(c *checker.Checker, node *ast.Node) *checker.Signature {
	return checkedCheckerCall("GetResolvedSignature", func() *checker.Signature {
		return c.GetResolvedSignature(node)
	})
}

func selectedOverloadOrdinal(c *checker.Checker, node *ast.Node, selected *checker.Signature) (result uint32) {
	return checkedCheckerCall("selectedOverloadOrdinal", func() uint32 {
		if c == nil || node == nil || selected == nil {
			panic(checkerCaptureError{Operation: "selectedOverloadOrdinal", Cause: "nil checker, node, or signature"})
		}
		expression := ast.GetInvokedExpression(node)
		if expression == nil {
			return 0
		}
		typ := c.GetTypeAtLocation(expression)
		if typ == nil {
			panic(checkerCaptureError{Operation: "selectedOverloadOrdinal", Cause: "callee has nil type"})
		}
		kind := checker.SignatureKindCall
		if node.Kind == ast.KindNewExpression {
			kind = checker.SignatureKindConstruct
		}
		for index, candidate := range c.GetSignaturesOfType(typ, kind) {
			if candidate == selected {
				return uint32(index + 1)
			}
			if candidate != nil && selected.Declaration() != nil && candidate.Declaration() == selected.Declaration() {
				return uint32(index + 1)
			}
		}
		return 0
	})
}

func safeConstantValue(c *checker.Checker, node *ast.Node) (result ConstantSnapshot) {
	result.Kind = "none"
	return checkedCheckerCall("GetConstantValue", func() ConstantSnapshot {
		switch node.Kind {
		case ast.KindStringLiteral, ast.KindNoSubstitutionTemplateLiteral:
			return ConstantSnapshot{Kind: "string", Text: node.Text()}
		case ast.KindNumericLiteral:
			text := strings.ReplaceAll(node.Text(), "_", "")
			if value, err := strconv.ParseFloat(text, 64); err == nil {
				return ConstantSnapshot{Kind: "number", Text: node.Text(), Number: value}
			}
			return ConstantSnapshot{Kind: "number", Text: node.Text()}
		case ast.KindBigIntLiteral:
			return ConstantSnapshot{Kind: "bigint", Text: node.Text()}
		case ast.KindTrueKeyword:
			return ConstantSnapshot{Kind: "boolean", Bool: true}
		case ast.KindFalseKeyword:
			return ConstantSnapshot{Kind: "boolean", Bool: false}
		case ast.KindNullKeyword:
			return ConstantSnapshot{Kind: "null"}
		}
		if node.Kind != ast.KindEnumMember && node.Kind != ast.KindPropertyAccessExpression && node.Kind != ast.KindElementAccessExpression {
			return result
		}
		value := c.GetConstantValue(node)
		switch typed := value.(type) {
		case string:
			result.Kind, result.Text = "string", typed
		case bool:
			result.Kind, result.Bool = "boolean", typed
		case float64:
			if math.IsNaN(typed) {
				result.Kind, result.Text = "number", "NaN"
			} else if math.IsInf(typed, 1) {
				result.Kind, result.Text = "number", "+Infinity"
			} else if math.IsInf(typed, -1) {
				result.Kind, result.Text = "number", "-Infinity"
			} else {
				result.Kind, result.Number = "number", typed
			}
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			result.Kind, result.Text = "number", fmt.Sprint(typed)
		}
		return result
	})
}

func (c *captureContext) captureSet(functionNode *ast.Node) []SymbolID {
	bindings := c.captureBindings(functionNode)
	set := make(map[SymbolID]struct{}, len(bindings))
	for _, binding := range bindings {
		if binding.symbol != "" {
			set[binding.symbol] = struct{}{}
		}
	}
	result := make([]SymbolID, 0, len(set))
	for symbol := range set {
		result = append(result, symbol)
	}
	slices.Sort(result)
	return result
}

func (c *captureContext) captureBindings(functionNode *ast.Node) []pendingCaptureBinding {
	bindings := make(map[SymbolID]pendingCaptureBinding)
	seenSpecial := make(map[string]pendingCaptureBinding)
	lexicalSpecials := ast.IsArrowFunction(functionNode)
	addSpecial := func(kind string) {
		seenSpecial[kind] = pendingCaptureBinding{kind: kind, access: "read", mutable: false}
	}
	var visit func(*ast.Node)
	visit = func(node *ast.Node) {
		if node == nil || (node != functionNode && ast.IsFunctionLike(node)) {
			return
		}
		if lexicalSpecials && (node.Kind == ast.KindThisKeyword || node.Kind == ast.KindSuperKeyword) {
			kind := "this"
			if node.Kind == ast.KindSuperKeyword {
				kind = "super"
				addSpecial("this")
			}
			addSpecial(kind)
		} else if lexicalSpecials && ast.IsMetaProperty(node) && node.AsMetaProperty().KeywordToken == ast.KindNewKeyword && node.Name() != nil && node.Name().Text() == "target" {
			addSpecial("new.target")
			return
		} else if (ast.IsIdentifier(node) || ast.IsPrivateIdentifier(node)) && runtimeCaptureName(node) {
			symbol := safeSymbolAtLocation(c.checker, node)
			if node.Text() == "arguments" {
				if lexicalSpecials {
					addSpecial("arguments")
				}
				return
			}
			if symbol != nil && !symbolDeclaredWithin(symbol, functionNode) {
				id := c.captureSymbol(symbol)
				if id != "" {
					access := captureAccess(node)
					binding := bindings[id]
					binding.symbol = id
					binding.kind = "binding"
					binding.access = mergeCaptureAccess(binding.access, access)
					binding.mutable = binding.mutable || captureSymbolMutable(symbol)
					bindings[id] = binding
				}
			}
		}
		for child := range node.IterChildren() {
			visit(child)
		}
	}
	visit(functionNode)
	result := make([]pendingCaptureBinding, 0, len(bindings)+len(seenSpecial))
	for _, binding := range bindings {
		result = append(result, binding)
	}
	for _, binding := range seenSpecial {
		result = append(result, binding)
	}
	slices.SortStableFunc(result, func(left, right pendingCaptureBinding) int {
		if cmp := strings.Compare(string(left.symbol), string(right.symbol)); cmp != 0 {
			return cmp
		}
		return strings.Compare(left.kind, right.kind)
	})
	return result
}

func runtimeCaptureName(node *ast.Node) bool {
	if node == nil || ast.IsPartOfTypeNode(node) {
		return false
	}
	parent := node.Parent
	if parent == nil {
		return true
	}
	// Property names and declaration names are not runtime bindings of the
	// enclosing closure. Computed names are expressions and remain candidates.
	if parent.Kind == ast.KindPropertyAccessExpression && parent.AsPropertyAccessExpression().Name() == node {
		return false
	}
	if parent.Kind == ast.KindQualifiedName && parent.AsQualifiedName().Right == node {
		return false
	}
	if ast.IsDeclaration(parent) && parent.Name() == node {
		return false
	}
	if ast.IsBindingElement(parent) && parent.AsBindingElement().PropertyName == node {
		return false
	}
	if parent.Kind == ast.KindPropertyAssignment && parent.AsPropertyAssignment().Name() == node {
		return false
	}
	return true
}

func captureAccess(node *ast.Node) string {
	if node == nil {
		return "read"
	}
	if ast.IsAssignmentTarget(node) {
		if parent := node.Parent; parent != nil && parent.Kind == ast.KindBinaryExpression && ast.IsCompoundAssignment(parent.AsBinaryExpression().OperatorToken.Kind) {
			return "readwrite"
		}
		return "write"
	}
	return "read"
}

func mergeCaptureAccess(previous, current string) string {
	if previous == "" {
		return current
	}
	if previous == current {
		return previous
	}
	return "readwrite"
}

func captureSymbolMutable(symbol *ast.Symbol) bool {
	if symbol == nil {
		return false
	}
	if symbol.Flags&ast.SymbolFlagsProperty != 0 {
		return true
	}
	for _, declaration := range symbol.Declarations {
		if declaration == nil {
			continue
		}
		if declaration.ModifierFlags()&ast.ModifierFlagsReadonly != 0 {
			continue
		}
		if declaration.Kind == ast.KindVariableDeclaration {
			list := declaration.Parent
			if list != nil && list.Kind == ast.KindVariableDeclarationList && ast.IsVarConst(list) {
				continue
			}
		}
		return true
	}
	return false
}

func safeSymbolAtLocation(c *checker.Checker, node *ast.Node) (result *ast.Symbol) {
	return checkedCheckerCall("GetSymbolAtLocation", func() *ast.Symbol {
		return c.GetSymbolAtLocation(node)
	})
}

func safeAliasedSymbol(c *checker.Checker, symbol *ast.Symbol) (result *ast.Symbol) {
	return checkedCheckerCall("GetAliasedSymbol", func() *ast.Symbol {
		return c.GetAliasedSymbol(symbol)
	})
}

func symbolDeclaredWithin(symbol *ast.Symbol, functionNode *ast.Node) bool {
	for _, declaration := range symbol.Declarations {
		for current := declaration; current != nil; current = current.Parent {
			if current == functionNode {
				return true
			}
		}
	}
	return false
}

func (c *captureContext) captureSymbol(symbol *ast.Symbol) SymbolID {
	if symbol == nil {
		return ""
	}
	if id, ok := c.symbolByPointer[symbol]; ok {
		return id
	}
	id := SymbolID(stableSymbolID(c.checker, symbol, c.graph.projectRoot, c.graph.frontend.defaultLibraryPath, c.graph.frontend.caseSensitivePaths))
	c.symbolByPointer[symbol] = id
	if existing := c.graph.symbols[id]; existing != nil {
		return id
	}
	record := &pendingSymbol{
		id:           id,
		name:         stableWireSymbolName(c.checker, symbol, c.graph.projectRoot, c.graph.frontend.defaultLibraryPath, c.graph.frontend.caseSensitivePaths),
		flags:        uint32(symbol.Flags),
		checkFlags:   uint32(symbol.CheckFlags),
		ptr:          symbol,
		declarations: []NodeID{},
	}
	c.graph.symbols[id] = record
	if symbol.Parent != nil {
		record.parent = c.captureSymbol(symbol.Parent)
	}
	if symbol.ExportSymbol != nil && symbol.ExportSymbol != symbol {
		record.exportSymbol = c.captureSymbol(symbol.ExportSymbol)
	}
	for _, declaration := range symbol.Declarations {
		if nodeID, ok := c.graph.nodeIDs[declaration]; ok {
			record.declarations = append(record.declarations, nodeID)
		}
	}
	slices.Sort(record.declarations)
	if symbol.ValueDeclaration != nil {
		record.valueDeclaration = c.graph.nodeIDs[symbol.ValueDeclaration]
	}
	if !c.symbolComesOnlyFromStandardLibrary(symbol) {
		if typ := safeTypeOfSymbol(c.checker, symbol); typ != nil {
			record.typeKey = c.captureType(typ)
		}
	}
	return id
}

func (c *captureContext) symbolComesOnlyFromStandardLibrary(symbol *ast.Symbol) bool {
	return c.symbolComesOnlyFromStandardLibraryWithSeen(symbol, make(map[*ast.Symbol]bool))
}

func (c *captureContext) symbolComesOnlyFromStandardLibraryWithSeen(symbol *ast.Symbol, seen map[*ast.Symbol]bool) bool {
	if c == nil || c.graph == nil || c.graph.program == nil || symbol == nil || seen[symbol] {
		return false
	}
	seen[symbol] = true
	hasDeclaration := false
	for _, declaration := range symbol.Declarations {
		if declaration == nil {
			continue
		}
		file := ast.GetSourceFileOfNode(declaration)
		if file == nil || !c.graph.program.IsSourceFileDefaultLibrary(file.Path()) {
			return false
		}
		hasDeclaration = true
	}
	if hasDeclaration {
		return true
	}
	return c.symbolComesOnlyFromStandardLibraryWithSeen(symbol.Parent, seen)
}

func safeTypeOfSymbol(c *checker.Checker, symbol *ast.Symbol) (result *checker.Type) {
	result = checkedCheckerCall("GetTypeOfSymbol", func() *checker.Type {
		return c.GetTypeOfSymbol(symbol)
	})
	if result == nil {
		panic(checkerCaptureError{Operation: "GetTypeOfSymbol", Cause: "returned nil type"})
	}
	return result
}

func stableSymbolID(typeChecker *checker.Checker, symbol *ast.Symbol, projectRoot, defaultLibraryPath string, caseSensitive ...bool) string {
	return stableSymbolIDWithSeen(typeChecker, symbol, projectRoot, defaultLibraryPath, pathCasePolicy(caseSensitive), make(map[*ast.Symbol]bool))
}

func stableSymbolIDWithSeen(typeChecker *checker.Checker, symbol *ast.Symbol, projectRoot, defaultLibraryPath string, caseSensitive bool, seen map[*ast.Symbol]bool) string {
	if symbol == nil {
		return ""
	}
	if seen[symbol] {
		return stableID("symbol-cycle", stableSymbolName(typeChecker, symbol, projectRoot, defaultLibraryPath, caseSensitive), strconv.FormatUint(uint64(symbol.Flags), 10))
	}
	seen[symbol] = true
	defer delete(seen, symbol)

	declarations := make([]string, 0, len(symbol.Declarations))
	for _, declaration := range symbol.Declarations {
		if declaration == nil {
			continue
		}
		fileName := ""
		if file := ast.GetSourceFileOfNode(declaration); file != nil {
			fileName = logicalPath(file.FileName(), projectRoot, defaultLibraryPath, caseSensitive)
		}
		declarations = append(declarations, fmt.Sprintf("%s:%d:%d:%d", fileName, declaration.Kind, declaration.Pos(), declaration.End()))
	}
	slices.Sort(declarations)
	parent := stableSymbolIDWithSeen(typeChecker, symbol.Parent, projectRoot, defaultLibraryPath, caseSensitive, seen)
	return stableID(
		"symbol",
		parent,
		stableSymbolName(typeChecker, symbol, projectRoot, defaultLibraryPath, caseSensitive),
		strconv.FormatUint(uint64(symbol.Flags), 10),
		strconv.FormatUint(uint64(symbol.CheckFlags), 10),
		strings.Join(declarations, ";"),
	)
}

func stableSymbolName(typeChecker *checker.Checker, symbol *ast.Symbol, projectRoot, defaultLibraryPath string, caseSensitive ...bool) string {
	if symbol == nil {
		return ""
	}
	useCaseSensitivePaths := pathCasePolicy(caseSensitive)
	name := ast.SymbolName(symbol)
	name = canonicalUniqueSymbolName(typeChecker, symbol, name, projectRoot, defaultLibraryPath, useCaseSensitivePaths)
	if len(name) < 2 || name[0] != '"' || name[len(name)-1] != '"' {
		return name
	}
	path := tspath.NormalizePath(name[1 : len(name)-1])
	projectRoot = strings.TrimSuffix(tspath.NormalizePath(projectRoot), "/")
	defaultLibraryPath = strings.TrimSuffix(tspath.NormalizePath(defaultLibraryPath), "/")
	if path == projectRoot || strings.HasPrefix(path, projectRoot+"/") ||
		path == defaultLibraryPath || strings.HasPrefix(path, defaultLibraryPath+"/") {
		return `"` + logicalPath(path, projectRoot, defaultLibraryPath, useCaseSensitivePaths) + `"`
	}
	return name
}

func stableWireSymbolName(typeChecker *checker.Checker, symbol *ast.Symbol, projectRoot, defaultLibraryPath string, caseSensitive ...bool) string {
	return strings.ToValidUTF8(stableSymbolName(typeChecker, symbol, projectRoot, defaultLibraryPath, caseSensitive...), "\uFFFD")
}

func canonicalUniqueSymbolName(typeChecker *checker.Checker, symbol *ast.Symbol, name, projectRoot, defaultLibraryPath string, caseSensitive bool) string {
	prefix := ast.InternalSymbolNamePrefix + "@"
	if !strings.HasPrefix(name, prefix) {
		return name
	}
	separator := strings.LastIndexByte(name, '@')
	if separator < len(prefix) || !isDecimalSymbolSuffix(name[separator+1:]) {
		return name
	}
	base := name[:separator]
	nameType := safeNameTypeOfSymbol(typeChecker, symbol)
	if nameType == nil || nameType.Flags()&checker.TypeFlagsUniqueESSymbol == 0 {
		return base
	}
	declarationSymbol := nameType.Symbol()
	if declarationSymbol == nil || declarationSymbol == symbol {
		return base
	}
	anchor := stableSymbolIDWithSeen(nil, declarationSymbol, projectRoot, defaultLibraryPath, caseSensitive, make(map[*ast.Symbol]bool))
	return base + "@" + anchor
}

func safeNameTypeOfSymbol(typeChecker *checker.Checker, symbol *ast.Symbol) (result *checker.Type) {
	if typeChecker == nil || symbol == nil {
		return nil
	}
	return checkedCheckerCall("GetNameTypeOfSymbol", func() *checker.Type {
		return typeChecker.GetNameTypeOfSymbol(symbol)
	})
}

func isDecimalSymbolSuffix(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func logicalDebugText(text, projectRoot, defaultLibraryPath string) string {
	if text == "" {
		return ""
	}
	replacements := [][2]string{
		{strings.TrimSuffix(tspath.NormalizePath(defaultLibraryPath), "/"), "@stdlib"},
		{strings.TrimSuffix(tspath.NormalizePath(projectRoot), "/"), "."},
	}
	for _, replacement := range replacements {
		if replacement[0] == "" {
			continue
		}
		text = strings.ReplaceAll(text, replacement[0], replacement[1])
		text = strings.ReplaceAll(text, strings.ReplaceAll(replacement[0], "/", `\`), replacement[1])
	}
	return text
}

func (c *captureContext) captureType(typ *checker.Type) string {
	if typ == nil {
		return ""
	}
	if key, ok := c.typeByPointer[typ]; ok {
		return key
	}
	c.ordinalType++
	key := fmt.Sprintf("%s/type/%08d", c.fileID, c.ordinalType)
	c.typeByPointer[typ] = key
	kind := typeKind(typ)
	isCheckerError := typ == c.checker.GetErrorType()
	if isCheckerError {
		kind = "error"
	}
	record := &pendingType{
		key:                    key,
		flags:                  uint32(typ.Flags()),
		objectFlags:            uint32(typ.ObjectFlags()),
		kind:                   kind,
		ptr:                    typ,
		elementKeys:            []string{},
		typeArgumentKeys:       []string{},
		baseTypeKeys:           []string{},
		propertyKeys:           []SymbolID{},
		propertyFacts:          []pendingProperty{},
		callSignatureKeys:      []string{},
		constructSignatureKeys: []string{},
		indexInfos:             []pendingIndexInfo{},
	}
	c.graph.types[key] = record
	if typ.Symbol() != nil {
		record.symbol = c.captureSymbol(typ.Symbol())
	}
	if typ.Alias() != nil && typ.Alias().Symbol() != nil {
		record.aliasSymbol = c.captureSymbol(typ.Alias().Symbol())
	}
	record.scalar = typeScalar(typ, record.kind, record.symbol, record.aliasSymbol)
	if typ.Flags()&checker.TypeFlagsObject != 0 && typ.ObjectFlags()&checker.ObjectFlagsClassOrInterface != 0 {
		variances := safeVariances(c.checker, typ)
		parts := make([]string, 0, len(variances))
		for _, variance := range variances {
			parts = append(parts, variance.String())
		}
		record.variance = strings.Join(parts, ",")
	}
	record.debugText = logicalDebugText(safeTypeToString(c.checker, typ), c.graph.projectRoot, c.graph.frontend.defaultLibraryPath)
	if typ.Flags()&checker.TypeFlagsAny != 0 {
		record.notLowerableReason = "any-value"
	}
	if typ.Flags()&checker.TypeFlagsTypeParameter != 0 && !typ.AsTypeParameter().IsThisType() {
		record.notLowerableReason = "unresolved-type-parameter"
	}
	if isCheckerError {
		record.notLowerableReason = "checker-error-type"
	}

	add := func(label string, child *checker.Type) {
		if child != nil {
			key := c.captureType(child)
			if label == "element" {
				record.elementKeys = append(record.elementKeys, key)
			} else if label == "argument" {
				record.typeArgumentKeys = append(record.typeArgumentKeys, key)
			} else if label == "base" {
				record.baseTypeKeys = append(record.baseTypeKeys, key)
			} else if label == "constraint" {
				record.constraintKey = key
			} else if label == "default" {
				record.defaultKey = key
			}
		}
	}
	flags := typ.Flags()
	if c.symbolComesOnlyFromStandardLibrary(typ.Symbol()) || typ.Alias() != nil && c.symbolComesOnlyFromStandardLibrary(typ.Alias().Symbol()) {
		record.scalar += "|external:stdlib"
		if alias := typ.Alias(); alias != nil && len(alias.TypeArguments()) != 0 {
			for _, child := range alias.TypeArguments() {
				add("argument", child)
			}
		} else if flags&checker.TypeFlagsObject != 0 && typ.ObjectFlags()&checker.ObjectFlagsReference != 0 {
			for _, child := range safeTypeArguments(c.checker, typ) {
				add("argument", child)
			}
		}
		return key
	}
	switch {
	case flags&checker.TypeFlagsUnionOrIntersection != 0:
		for _, child := range typ.Types() {
			add("element", child)
		}
	case flags&checker.TypeFlagsObject != 0:
		if typ.ObjectFlags()&checker.ObjectFlagsReference != 0 {
			for _, child := range safeTypeArguments(c.checker, typ) {
				add("argument", child)
			}
		}
		if typ.ObjectFlags()&checker.ObjectFlagsClassOrInterface != 0 {
			for _, child := range safeBaseTypes(c.checker, typ) {
				add("base", child)
			}
		}
		properties := safeProperties(c.checker, typ)
		sort.SliceStable(properties, func(i, j int) bool { return ast.SymbolName(properties[i]) < ast.SymbolName(properties[j]) })
		for _, property := range properties {
			id := c.captureSymbol(property)
			record.propertyKeys = append(record.propertyKeys, id)
			record.propertyFacts = append(record.propertyFacts, c.capturePropertyFact(typ, property, id))
		}
		for _, signature := range safeSignatures(c.checker, typ, checker.SignatureKindCall) {
			record.callSignatureKeys = append(record.callSignatureKeys, c.captureSignature(signature, "call"))
		}
		for _, signature := range safeSignatures(c.checker, typ, checker.SignatureKindConstruct) {
			record.constructSignatureKeys = append(record.constructSignatureKeys, c.captureSignature(signature, "construct"))
		}
		for _, info := range safeIndexInfos(c.checker, typ) {
			if info == nil {
				continue
			}
			index := pendingIndexInfo{keyType: c.captureType(info.KeyType()), valueType: c.captureType(info.ValueType()), readonly: info.IsReadonly()}
			if declaration := info.Declaration(); declaration != nil {
				index.declaration = c.graph.nodeIDs[declaration]
			}
			record.indexInfos = append(record.indexInfos, index)
		}
	case flags&checker.TypeFlagsTypeParameter != 0:
		add("constraint", safeConstraint(c.checker, typ))
		add("default", safeDefault(c.checker, typ))
	case flags&checker.TypeFlagsIndexedAccess != 0:
		if indexed := typ.AsIndexedAccessType(); indexed != nil {
			add("element", indexed.ObjectType())
			add("element", indexed.IndexType())
		}
	case flags&checker.TypeFlagsConditional != 0:
		if conditional := typ.AsConditionalType(); conditional != nil {
			add("element", conditional.CheckType())
			add("element", conditional.ExtendsType())
			add("element", safeTrueType(c.checker, typ))
			add("element", safeFalseType(c.checker, typ))
		}
	case flags&checker.TypeFlagsTemplateLiteral != 0:
		if template := typ.AsTemplateLiteralType(); template != nil {
			for _, child := range template.Types() {
				add("element", child)
			}
		}
	}
	return key
}

func (c *captureContext) capturePropertyFact(owner *checker.Type, property *ast.Symbol, id SymbolID) pendingProperty {
	fact := pendingProperty{symbol: id, visibility: "public"}
	if property == nil {
		return fact
	}
	modifiers := checker.GetDeclarationModifierFlagsFromSymbol(property)
	fact.optional = property.Flags&ast.SymbolFlagsOptional != 0
	fact.readonly = property.CheckFlags&ast.CheckFlagsReadonly != 0 || modifiers&ast.ModifierFlagsReadonly != 0
	fact.hasGetter = property.Flags&ast.SymbolFlagsGetAccessor != 0
	fact.hasSetter = property.Flags&ast.SymbolFlagsSetAccessor != 0
	for _, declaration := range property.Declarations {
		if ast.IsGetAccessorDeclaration(declaration) {
			fact.hasGetter = true
		}
		if ast.IsSetAccessorDeclaration(declaration) {
			fact.hasSetter = true
		}
	}
	if property.Flags&ast.SymbolFlagsAccessor == 0 {
		fact.hasGetter = true
		fact.hasSetter = !fact.readonly
	}
	switch {
	case modifiers&ast.ModifierFlagsPrivate != 0:
		fact.visibility = "private"
	case modifiers&ast.ModifierFlagsProtected != 0:
		fact.visibility = "protected"
	}
	if strings.HasPrefix(ast.SymbolName(property), "#") {
		fact.visibility = "private"
	}
	if fact.visibility == "private" {
		fact.privateIdentity = string(id)
	}
	if symbol := c.graph.symbols[id]; symbol != nil {
		if fact.hasGetter {
			fact.readKey = symbol.typeKey
		}
		if fact.hasSetter && !fact.readonly {
			fact.writeKey = symbol.typeKey
		}
	}
	if contextual := safeTypeOfProperty(c.checker, owner, ast.SymbolName(property)); contextual != nil {
		key := c.captureType(contextual)
		if fact.hasGetter {
			fact.readKey = key
		}
		if fact.hasSetter && !fact.readonly {
			fact.writeKey = key
		}
	}
	// Accessor symbols expose one merged checker type, but their read and
	// write representations can differ. Capture the concrete getter result and
	// setter parameter when declarations provide them.
	for _, declaration := range property.Declarations {
		switch {
		case ast.IsGetAccessorDeclaration(declaration) && fact.hasGetter:
			if typ := safeTypeAtLocation(c.checker, declaration); typ != nil {
				fact.readKey = c.captureType(typ)
			}
		case ast.IsSetAccessorDeclaration(declaration) && fact.hasSetter:
			parameters := declaration.Parameters()
			if len(parameters) != 0 {
				if typ := safeTypeAtLocation(c.checker, parameters[0]); typ != nil {
					fact.writeKey = c.captureType(typ)
				}
			}
		}
	}
	return fact
}

func safeTypeOfProperty(c *checker.Checker, owner *checker.Type, name string) (result *checker.Type) {
	return checkedCheckerCall("GetTypeOfPropertyOfType", func() *checker.Type { return c.GetTypeOfPropertyOfType(owner, name) })
}

func safeTypeToString(c *checker.Checker, typ *checker.Type) (result string) {
	return checkedCheckerCall("TypeToString", func() string {
		return c.TypeToString(typ)
	})
}

func typeKind(typ *checker.Type) string {
	flags := typ.Flags()
	switch {
	case flags&checker.TypeFlagsAny != 0:
		return "any"
	case flags&checker.TypeFlagsUnknown != 0:
		return "unknown"
	case flags&checker.TypeFlagsNever != 0:
		return "never"
	case flags&checker.TypeFlagsIntrinsic != 0:
		return "intrinsic"
	case flags&checker.TypeFlagsLiteral != 0:
		return "literal"
	case flags&checker.TypeFlagsUnion != 0:
		return "union"
	case flags&checker.TypeFlagsIntersection != 0:
		return "intersection"
	case flags&checker.TypeFlagsTypeParameter != 0:
		return "typeParameter"
	case flags&checker.TypeFlagsIndexedAccess != 0:
		return "indexedAccess"
	case flags&checker.TypeFlagsConditional != 0:
		return "conditional"
	case flags&checker.TypeFlagsTemplateLiteral != 0:
		return "template"
	case flags&checker.TypeFlagsObject != 0 && typ.ObjectFlags()&checker.ObjectFlagsMapped != 0:
		return "mapped"
	case flags&checker.TypeFlagsObject != 0:
		if typ.IsTupleType() {
			return "tuple"
		}
		return "object"
	default:
		return "error"
	}
}

func typeScalar(typ *checker.Type, kind string, symbol, alias SymbolID) string {
	parts := []string{kind, strconv.FormatUint(uint64(typ.Flags()), 10), strconv.FormatUint(uint64(typ.ObjectFlags()), 10), string(symbol), string(alias)}
	if typ.Flags()&checker.TypeFlagsLiteral != 0 {
		if literal := typ.AsLiteralType(); literal != nil {
			parts = append(parts, fmt.Sprintf("literal:%T:%v", literal.Value(), literal.Value()))
		}
	}
	if typ.Flags()&checker.TypeFlagsIntrinsic != 0 {
		if intrinsic := typ.AsIntrinsicType(); intrinsic != nil {
			parts = append(parts, "intrinsic:"+intrinsic.IntrinsicName())
		}
	}
	return strings.Join(parts, "|")
}

func safeTypeArguments(c *checker.Checker, typ *checker.Type) (result []*checker.Type) {
	return checkedCheckerCall("GetTypeArguments", func() []*checker.Type {
		return c.GetTypeArguments(typ)
	})
}

func safeVariances(c *checker.Checker, typ *checker.Type) []checker.VarianceFlags {
	return checkedCheckerCall("GetVariances", func() []checker.VarianceFlags {
		return c.GetVariances(typ)
	})
}

func safeBaseTypes(c *checker.Checker, typ *checker.Type) (result []*checker.Type) {
	return checkedCheckerCall("GetBaseTypes", func() []*checker.Type {
		return c.GetBaseTypes(typ)
	})
}

func safeProperties(c *checker.Checker, typ *checker.Type) (result []*ast.Symbol) {
	return checkedCheckerCall("GetPropertiesOfType", func() []*ast.Symbol {
		return slices.Clone(c.GetPropertiesOfType(typ))
	})
}

func safeSignatures(c *checker.Checker, typ *checker.Type, kind checker.SignatureKind) (result []*checker.Signature) {
	return checkedCheckerCall("GetSignaturesOfType", func() []*checker.Signature {
		return c.GetSignaturesOfType(typ, kind)
	})
}

func safeIndexInfos(c *checker.Checker, typ *checker.Type) (result []*checker.IndexInfo) {
	return checkedCheckerCall("GetIndexInfosOfType", func() []*checker.IndexInfo {
		return c.GetIndexInfosOfType(typ)
	})
}

func safeConstraint(c *checker.Checker, typ *checker.Type) (result *checker.Type) {
	return checkedCheckerCall("GetConstraintOfTypeParameter", func() *checker.Type {
		return c.GetConstraintOfTypeParameter(typ)
	})
}

func safeDefault(c *checker.Checker, typ *checker.Type) (result *checker.Type) {
	return checkedCheckerCall("GetDefaultFromTypeParameter", func() *checker.Type {
		return c.GetDefaultFromTypeParameter(typ)
	})
}

func safeTrueType(c *checker.Checker, typ *checker.Type) (result *checker.Type) {
	return checkedCheckerCall("GetTrueTypeOfConditionalType", func() *checker.Type {
		return c.GetTrueTypeOfConditionalType(typ)
	})
}

func safeFalseType(c *checker.Checker, typ *checker.Type) (result *checker.Type) {
	return checkedCheckerCall("GetFalseTypeOfConditionalType", func() *checker.Type {
		return c.GetFalseTypeOfConditionalType(typ)
	})
}

func (c *captureContext) captureSignature(signature *checker.Signature, convention string) string {
	if signature == nil {
		return ""
	}
	if key, ok := c.signatureByPointer[signature]; ok {
		return key
	}
	c.ordinalSignature++
	key := fmt.Sprintf("%s/signature/%08d", c.fileID, c.ordinalSignature)
	c.signatureByPointer[signature] = key
	record := &pendingSignature{
		key:                    key,
		flags:                  uint32(signature.Flags()),
		minArgumentCount:       signature.MinArgumentCount(),
		hasRest:                signature.HasRestParameter(),
		callingConventionClass: convention,
		parameters:             []SymbolID{},
		parameterTypeKeys:      []string{},
		parameterFacts:         []pendingParameter{},
		typeParameterKeys:      []string{},
		instantiatedTypeKeys:   []string{},
		effects:                []string{"unknown"},
		effectProofKind:        "declaration-only",
		ptr:                    signature,
	}
	c.graph.signatures[key] = record
	if declaration := signature.Declaration(); declaration != nil {
		record.declaration = c.graph.nodeIDs[declaration]
		if symbol := declaration.Symbol(); symbol != nil {
			c.captureSymbol(symbol)
		}
	}
	c.captureSignatureEffectProof(record, signature)
	if thisParameter := signature.ThisParameter(); thisParameter != nil {
		record.thisParameter = c.captureSymbol(thisParameter)
	}
	parameters := signature.Parameters()
	for index, parameter := range parameters {
		id := c.captureSymbol(parameter)
		record.parameters = append(record.parameters, id)
		fact := pendingParameter{
			symbol:   id,
			optional: parameter.Flags&ast.SymbolFlagsOptional != 0 || parameter.CheckFlags&ast.CheckFlagsOptionalParameter != 0 || index >= record.minArgumentCount,
			rest:     parameter.CheckFlags&ast.CheckFlagsRestParameter != 0 || record.hasRest && index == len(parameters)-1,
		}
		if parameterType := safeTypeOfSymbol(c.checker, parameter); parameterType != nil {
			fact.typeKey = c.captureType(parameterType)
			record.parameterTypeKeys = append(record.parameterTypeKeys, fact.typeKey)
		}
		record.parameterFacts = append(record.parameterFacts, fact)
	}
	for _, typeParameter := range signature.TypeParameters() {
		record.typeParameterKeys = append(record.typeParameterKeys, c.captureType(typeParameter))
	}
	for _, typeArgument := range safeInstantiatedTypeArguments(c.checker, signature) {
		record.instantiatedTypeKeys = append(record.instantiatedTypeKeys, c.captureType(typeArgument))
	}
	if returnType := safeReturnType(c.checker, signature); returnType != nil {
		record.returnTypeKey = c.captureType(returnType)
	}
	if predicate := safePredicate(c.checker, signature); predicate != nil {
		record.predicate = TypePredicateSnapshot{Present: true, Kind: int32(predicate.Kind()), ParameterIndex: predicate.ParameterIndex(), ParameterName: predicate.ParameterName()}
		if predicate.Type() != nil {
			record.predicateTypeKey = c.captureType(predicate.Type())
		}
	}
	return key
}

func (c *captureContext) captureSignatureEffectProof(record *pendingSignature, signature *checker.Signature) {
	implementation := signatureEffectImplementation(c.checker, signature)
	if implementation == nil || implementation.Body() == nil {
		record.effectProofKind = "declaration-only"
		record.effectProofComplete = false
		record.effects = []string{"unknown"}
		return
	}
	record.effectProofKind = "body-resolved"
	record.effectImplementation = c.graph.nodeIDs[implementation]
	if record.effectImplementation == "" {
		record.effectProofComplete = false
		record.effects = []string{"unknown"}
		return
	}
	record.effectProofComplete = true
	effects := map[string]struct{}{}
	calls := make([]pendingEffectCall, 0)
	if implementation.ModifierFlags()&ast.ModifierFlagsAsync != 0 || implementation.BodyData() != nil && implementation.BodyData().AsteriskToken != nil {
		appendSnapshotEffect(effects, "alloc")
	}
	var visit func(*ast.Node)
	visit = func(node *ast.Node) {
		if node == nil || snapshotEffectASTNodeIsTypeContext(node) {
			return
		}
		mode, registered := snapshotEffectRuleForKind(node.Kind.String())
		if !registered {
			record.effectProofComplete = false
		}
		switch mode {
		case snapshotEffectAccess:
			if ast.IsWriteAccess(node) {
				appendSnapshotEffect(effects, "write")
			}
			if !ast.IsWriteOnlyAccess(node) {
				appendSnapshotEffect(effects, "read")
			}
			if ast.IsElementAccessExpression(node) || signatureEffectAccessHasAccessor(c.checker, node) {
				record.effectProofComplete = false
			}
		case snapshotEffectBinary:
			binary := node.AsBinaryExpression()
			direct, complete := snapshotBinaryEffects(
				binary.OperatorToken.Kind.String(),
				checkerEffectPrimitiveKind(safeTypeAtLocation(c.checker, binary.Left)),
				checkerEffectPrimitiveKind(safeTypeAtLocation(c.checker, binary.Right)),
			)
			for _, effect := range direct {
				appendSnapshotEffect(effects, effect)
			}
			if !complete {
				record.effectProofComplete = false
			}
			if binary.OperatorToken.Kind == ast.KindInstanceOfKeyword {
				c.capturePendingEffectCall(record, node, &calls)
				record.effectProofComplete = false
			}
		case snapshotEffectPrefix:
			var operand *ast.Node
			operator := ""
			if node.Kind == ast.KindPrefixUnaryExpression {
				operand = node.AsPrefixUnaryExpression().Operand
				operator = node.AsPrefixUnaryExpression().Operator.String()
			} else {
				operand = node.AsPostfixUnaryExpression().Operand
				operator = node.AsPostfixUnaryExpression().Operator.String()
			}
			direct, complete := snapshotPrefixEffects(operator, checkerEffectPrimitiveKind(safeTypeAtLocation(c.checker, operand)))
			for _, effect := range direct {
				appendSnapshotEffect(effects, effect)
			}
			if !complete {
				record.effectProofComplete = false
			}
		case snapshotEffectCall, snapshotEffectCallAlloc, snapshotEffectIncompleteCall:
			c.capturePendingEffectCall(record, node, &calls)
			if mode == snapshotEffectCallAlloc {
				appendSnapshotEffect(effects, "alloc")
			}
			if mode == snapshotEffectIncompleteCall || ast.IsImportCall(node) {
				record.effectProofComplete = false
			}
		case snapshotEffectLiteralAlloc:
			if ast.IsArrayLiteralOrObjectLiteralDestructuringPattern(node) {
				record.effectProofComplete = false
			} else {
				appendSnapshotEffect(effects, "alloc")
			}
		case snapshotEffectAlloc:
			appendSnapshotEffect(effects, "alloc")
		case snapshotEffectAllocIncomplete:
			appendSnapshotEffect(effects, "alloc")
			record.effectProofComplete = false
		case snapshotEffectThrow:
			appendSnapshotEffect(effects, "throw")
		case snapshotEffectSuspend:
			appendSnapshotEffect(effects, "suspend")
		case snapshotEffectNondeterministic:
			appendSnapshotEffect(effects, "nondeterministic")
		case snapshotEffectIncomplete:
			if node.Kind == ast.KindDeleteExpression {
				appendSnapshotEffect(effects, "write")
			}
			record.effectProofComplete = false
		}
		if (ast.IsIdentifier(node) || ast.IsPrivateIdentifier(node)) && runtimeCaptureName(node) && !isDirectInvokedBinding(node) {
			if symbol := safeSymbolAtLocation(c.checker, node); symbol != nil && !symbolDeclaredWithin(symbol, implementation) {
				appendCaptureAccessEffects(effects, captureAccess(node))
			}
		}
		if node != implementation && ast.IsFunctionLike(node) {
			return
		}
		for child := range node.IterChildren() {
			visit(child)
		}
	}
	if data := implementation.FunctionLikeData(); data != nil && data.Parameters != nil {
		for _, parameter := range data.Parameters.Nodes {
			if parameter == nil {
				continue
			}
			visit(parameter.Name())
			visit(parameter.Initializer())
		}
	}
	visit(implementation.Body())
	record.directEffects = make([]string, 0, len(effects))
	for effect := range effects {
		record.directEffects = append(record.directEffects, effect)
	}
	slices.Sort(record.directEffects)
	record.effectCalls = calls
	if record.effectProofComplete {
		record.effects = effectSummary(record.directEffects, false)
	} else {
		record.effects = []string{"unknown"}
	}
}

func (c *captureContext) capturePendingEffectCall(record *pendingSignature, node *ast.Node, calls *[]pendingEffectCall) {
	selected := safeResolvedSignature(c.checker, node)
	call := pendingEffectCall{node: c.graph.nodeIDs[node]}
	if selected == nil || call.node == "" {
		record.effectProofComplete = false
	} else {
		call.signatureKey = c.captureSignature(selected, callingConventionForNode(node))
	}
	// Calls inherit callee effects during fixed-point closure; a resolved pure
	// call is not itself a direct effect.
	*calls = append(*calls, call)
}

func appendCaptureAccessEffects(effects map[string]struct{}, access string) {
	if access == "read" || access == "readwrite" {
		appendSnapshotEffect(effects, "read")
	}
	if access == "write" || access == "readwrite" {
		appendSnapshotEffect(effects, "write")
	}
}

func isDirectInvokedBinding(node *ast.Node) bool {
	return node != nil && node.Parent != nil && ast.IsCallLikeExpression(node.Parent) && ast.GetInvokedExpression(node.Parent) == node
}

func signatureEffectAccessHasAccessor(c *checker.Checker, node *ast.Node) bool {
	if node == nil {
		return false
	}
	symbol := safeSymbolAtLocation(c, node)
	if symbol == nil && node.Name() != nil {
		symbol = safeSymbolAtLocation(c, node.Name())
	}
	if symbol == nil {
		return false
	}
	return slices.ContainsFunc(symbol.Declarations, func(declaration *ast.Node) bool {
		return declaration != nil && (ast.IsGetAccessorDeclaration(declaration) || ast.IsSetAccessorDeclaration(declaration))
	})
}

func signatureEffectImplementation(c *checker.Checker, signature *checker.Signature) *ast.Node {
	seen := make(map[*checker.Signature]struct{})
	for current := signature; current != nil; current = current.Target() {
		if _, duplicate := seen[current]; duplicate {
			break
		}
		seen[current] = struct{}{}
		declaration := current.Declaration()
		if declaration == nil {
			continue
		}
		if declaration.Body() != nil {
			return declaration
		}
		if symbol := declaration.Symbol(); symbol != nil {
			var implementation *ast.Node
			for _, candidate := range symbol.Declarations {
				if candidate == nil || !ast.IsFunctionLikeDeclaration(candidate) || candidate.Body() == nil {
					continue
				}
				if implementation != nil && implementation != candidate {
					return nil
				}
				implementation = candidate
			}
			if implementation != nil {
				return implementation
			}
		}
	}
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

func closePendingSignatureEffects(signatures map[string]*pendingSignature) {
	keys := sortedSignatureKeys(signatures)
	for _, key := range keys {
		record := signatures[key]
		record.effects = effectSummary(record.directEffects, !record.effectProofComplete)
	}
	for changed := true; changed; {
		changed = false
		for _, key := range keys {
			record := signatures[key]
			unknown := !record.effectProofComplete
			effects := slices.Clone(record.directEffects)
			for _, call := range record.effectCalls {
				callee := signatures[call.signatureKey]
				if call.signatureKey == "" || callee == nil || slices.Equal(callee.effects, []string{"unknown"}) {
					unknown = true
					continue
				}
				for _, effect := range callee.effects {
					if effect != "pure" {
						effects = append(effects, effect)
					}
				}
			}
			next := effectSummary(effects, unknown)
			if !slices.Equal(record.effects, next) {
				record.effects = next
				changed = true
			}
		}
	}
}

func safeReturnType(c *checker.Checker, signature *checker.Signature) (result *checker.Type) {
	result = checkedCheckerCall("GetReturnTypeOfSignature", func() *checker.Type {
		return c.GetReturnTypeOfSignature(signature)
	})
	if result == nil {
		panic(checkerCaptureError{Operation: "GetReturnTypeOfSignature", Cause: "returned nil type"})
	}
	return result
}

func safeInstantiatedTypeArguments(c *checker.Checker, signature *checker.Signature) (result []*checker.Type) {
	return checkedCheckerCall("GetInstantiatedTypeArgumentsOfSignature", func() []*checker.Type {
		return c.GetInstantiatedTypeArgumentsOfSignature(signature)
	})
}

func safePredicate(c *checker.Checker, signature *checker.Signature) (result *checker.TypePredicate) {
	return checkedCheckerCall("GetTypePredicateOfSignature", func() *checker.TypePredicate {
		return c.GetTypePredicateOfSignature(signature)
	})
}

func (g *captureGraph) finish(edges []ModuleEdge, modules []ModuleSnapshot, build *programBuild, stdlibHash string) (*ProgramSnapshot, error) {
	closePendingSignatureEffects(g.signatures)
	frontendBingo := frontendBingoOptions(build.options.Bingo)
	typeHashes, signatureHashes := canonicalSemanticHashes(g.types, g.signatures, g.symbols)
	_, typeHashToID := assignTypeIDs(g.types, typeHashes)
	_, signatureHashToID := assignSignatureIDs(g.signatures, signatureHashes)

	symbolIDs := make(map[SymbolID]SymbolID, len(g.symbols))
	for id := range g.symbols {
		symbolIDs[id] = id
	}

	snapshot := &ProgramSnapshot{
		SchemaVersion: SnapshotSchemaVersion,
		Config: ConfigSnapshot{
			BingoSchemaVersion:   OptionsSchemaVersion,
			Bingo:                frontendBingo,
			CanonicalConfigPath:  g.frontend.logicalPath(build.configPath, build.projectRoot),
			CanonicalProjectRoot: ".",
			TypeScript:           snapshotTypeScriptOptionsForProject(build.options.CompilerOptions, g.projectRoot, g.frontend.caseSensitivePaths),
			BingoDigest:          "",
			TypeScriptDigest:     "",
		},
		Provenance: provenanceSnapshot(g.frontend, stdlibHash, KindManifestDigest()),
		Files:      []FileSnapshot{}, Modules: modules, ModuleEdges: edges, Origins: slices.Clone(g.origins),
		Nodes: []NodeSnapshot{}, Types: []TypeSnapshot{}, Symbols: []SymbolSnapshot{}, Signatures: []SignatureSnapshot{}, Diagnostics: []Diagnostic{},
	}
	if digest, err := hashCanonical(struct {
		Schema int          `json:"schema"`
		Bingo  BingoOptions `json:"bingo"`
	}{OptionsSchemaVersion, frontendBingo}); err == nil {
		snapshot.Config.BingoDigest = digest
	} else {
		return nil, err
	}
	if digest, err := hashCanonical(snapshot.Config.TypeScript); err == nil {
		snapshot.Config.TypeScriptDigest = digest
	} else {
		return nil, err
	}

	for _, file := range g.files {
		file.dto.RootNodes = slices.Clone(file.rootNodeIDs)
		snapshot.Files = append(snapshot.Files, file.dto)
	}
	slices.SortFunc(snapshot.Files, func(a, b FileSnapshot) int { return strings.Compare(a.CanonicalPath, b.CanonicalPath) })
	for _, origin := range g.origins {
		snapshot.Origins = appendUniqueOrigin(snapshot.Origins, origin)
	}
	slices.SortFunc(snapshot.Origins, func(a, b OriginSnapshot) int { return strings.Compare(string(a.ID), string(b.ID)) })

	for _, pending := range g.nodeByID {
		dto := pending.dto
		dto.DeclaredType = typeHashToID[typeHashes[pending.declaredKey]]
		dto.NarrowedType = typeHashToID[typeHashes[pending.narrowedKey]]
		dto.ContextualType = typeHashToID[typeHashes[pending.contextualKey]]
		dto.SelectedSignature = signatureHashToID[signatureHashes[pending.signatureKey]]
		dto.SelectedOverloadOrdinal = pending.selectedOverloadOrdinal
		dto.AssertionTarget = typeHashToID[typeHashes[pending.assertionTargetKey]]
		dto.AssertionAssignable = pending.assertionAssignable
		for _, proof := range pending.assertionChain {
			dto.AssertionChain = append(dto.AssertionChain, AssertionProofSnapshot{
				SourceType: typeHashToID[typeHashes[proof.sourceKey]], TargetType: typeHashToID[typeHashes[proof.targetKey]],
				Assignable: proof.assignable, OpenType: proof.openType, RepresentationProof: proof.representationProof,
			})
		}
		if pending.nonNullOperandKey != "" {
			dto.NonNullProof = NonNullProofSnapshot{
				Present: true, OperandType: typeHashToID[typeHashes[pending.nonNullOperandKey]], ResultType: typeHashToID[typeHashes[pending.nonNullResultKey]],
				ProofKind: pending.nonNullProofKind, RemovedNull: pending.nonNullRemovedNull, RemovedUndefined: pending.nonNullRemovedUndef,
			}
		}
		for _, binding := range pending.captureBindings {
			dto.CaptureBindings = append(dto.CaptureBindings, CaptureBindingSnapshot{
				Symbol: symbolIDs[binding.symbol], Kind: binding.kind, Access: binding.access, Mutable: binding.mutable,
			})
		}
		dto.CaptureComplete = pending.captureComplete
		if pending.symbolKey != "" {
			dto.Symbol = symbolIDs[pending.symbolKey]
		}
		if pending.resolvedKey != "" {
			dto.ResolvedSymbol = symbolIDs[pending.resolvedKey]
		}
		if pending.declaredKey != "" {
			dto.Flow.DeclaredTypeHash = typeHashes[pending.declaredKey]
		}
		if pending.narrowedKey != "" {
			dto.Flow.NarrowedTypeHash = typeHashes[pending.narrowedKey]
		}
		if pending.contextualKey != "" {
			dto.Flow.ContextualTypeHash = typeHashes[pending.contextualKey]
		}
		dto.Flow.Narrowed = pending.declaredKey != "" && pending.narrowedKey != "" && typeHashes[pending.declaredKey] != typeHashes[pending.narrowedKey]
		switch {
		case pending.nonNullProofKind != "":
			dto.Flow.ProofKind = pending.nonNullProofKind
		case len(pending.assertionChain) != 0:
			dto.Flow.ProofKind = "assertion"
		case dto.Flow.Narrowed:
			dto.Flow.ProofKind = "checker-flow"
		}
		snapshot.Nodes = append(snapshot.Nodes, dto)
	}
	slices.SortFunc(snapshot.Nodes, func(a, b NodeSnapshot) int { return strings.Compare(string(a.ID), string(b.ID)) })

	seenTypeIDs := make(map[TypeID]struct{}, len(typeHashToID))
	for _, id := range sortedTypeKeys(g.types) {
		pending := g.types[id]
		hash := typeHashes[id]
		denseID := typeHashToID[hash]
		if _, duplicate := seenTypeIDs[denseID]; duplicate {
			continue
		}
		seenTypeIDs[denseID] = struct{}{}
		dto := TypeSnapshot{ID: denseID, CanonicalHash: hash, Kind: pending.kind, Flags: pending.flags, ObjectFlags: pending.objectFlags, Symbol: pending.symbol, AliasSymbol: pending.aliasSymbol, Variance: pending.variance, DebugText: pending.debugText, NotLowerableReason: pending.notLowerableReason}
		for _, key := range pending.elementKeys {
			dto.ElementTypes = append(dto.ElementTypes, typeHashToID[typeHashes[key]])
		}
		for _, key := range pending.typeArgumentKeys {
			dto.TypeArguments = append(dto.TypeArguments, typeHashToID[typeHashes[key]])
		}
		for _, key := range pending.baseTypeKeys {
			dto.BaseTypes = append(dto.BaseTypes, typeHashToID[typeHashes[key]])
		}
		for _, symbol := range pending.propertyKeys {
			dto.Properties = append(dto.Properties, symbolIDs[symbol])
		}
		for _, property := range pending.propertyFacts {
			dto.PropertyFacts = append(dto.PropertyFacts, PropertySnapshot{
				Symbol:          symbolIDs[property.symbol],
				ReadType:        typeHashToID[typeHashes[property.readKey]],
				WriteType:       typeHashToID[typeHashes[property.writeKey]],
				Optional:        property.optional,
				Readonly:        property.readonly,
				HasGetter:       property.hasGetter,
				HasSetter:       property.hasSetter,
				Visibility:      property.visibility,
				PrivateIdentity: property.privateIdentity,
			})
		}
		for _, key := range pending.callSignatureKeys {
			dto.CallSignatures = append(dto.CallSignatures, signatureHashToID[signatureHashes[key]])
		}
		for _, key := range pending.constructSignatureKeys {
			dto.ConstructSignatures = append(dto.ConstructSignatures, signatureHashToID[signatureHashes[key]])
		}
		for _, info := range pending.indexInfos {
			dto.IndexInfos = append(dto.IndexInfos, IndexInfoSnapshot{KeyType: typeHashToID[typeHashes[info.keyType]], ValueType: typeHashToID[typeHashes[info.valueType]], Readonly: info.readonly, Declaration: info.declaration})
		}
		dto.ConstraintType = typeHashToID[typeHashes[pending.constraintKey]]
		dto.DefaultType = typeHashToID[typeHashes[pending.defaultKey]]
		dto.TypePayload = TypePayload{
			Tag:           dto.Kind,
			Scalar:        pending.scalar,
			Elements:      slices.Clone(dto.ElementTypes),
			TypeArguments: slices.Clone(dto.TypeArguments),
			BaseTypes:     slices.Clone(dto.BaseTypes),
		}
		snapshot.Types = append(snapshot.Types, dto)
	}
	slices.SortFunc(snapshot.Types, func(a, b TypeSnapshot) int { return strings.Compare(a.CanonicalHash, b.CanonicalHash) })

	for _, id := range sortedSymbolIDs(g.symbols) {
		record := g.symbols[id]
		snapshot.Symbols = append(snapshot.Symbols, SymbolSnapshot{ID: record.id, Name: record.name, Flags: record.flags, CheckFlags: record.checkFlags, Parent: record.parent, ExportSymbol: record.exportSymbol, Declarations: slices.Clone(record.declarations), ValueDeclaration: record.valueDeclaration, Type: typeHashToID[typeHashes[record.typeKey]]})
	}
	slices.SortFunc(snapshot.Symbols, func(a, b SymbolSnapshot) int { return strings.Compare(string(a.ID), string(b.ID)) })

	seenSignatureIDs := make(map[SignatureID]struct{}, len(signatureHashToID))
	for _, key := range sortedSignatureKeys(g.signatures) {
		record := g.signatures[key]
		hash := signatureHashes[key]
		denseID := signatureHashToID[hash]
		if _, duplicate := seenSignatureIDs[denseID]; duplicate {
			continue
		}
		seenSignatureIDs[denseID] = struct{}{}
		predicate := record.predicate
		predicate.Type = typeHashToID[typeHashes[record.predicateTypeKey]]
		parameterFacts := make([]ParameterSnapshot, 0, len(record.parameterFacts))
		for _, parameter := range record.parameterFacts {
			parameterFacts = append(parameterFacts, ParameterSnapshot{
				Symbol:   symbolIDs[parameter.symbol],
				Type:     typeHashToID[typeHashes[parameter.typeKey]],
				Optional: parameter.optional,
				Rest:     parameter.rest,
			})
		}
		effectCalls := make([]SignatureEffectCallSnapshot, 0, len(record.effectCalls))
		for _, call := range record.effectCalls {
			effectCalls = append(effectCalls, SignatureEffectCallSnapshot{
				Node:      call.node,
				Signature: signatureHashToID[signatureHashes[call.signatureKey]],
			})
		}
		snapshot.Signatures = append(snapshot.Signatures, SignatureSnapshot{
			ID: denseID, CanonicalHash: hash, Declaration: record.declaration, Flags: record.flags,
			ThisParameter: record.thisParameter, Parameters: slices.Clone(record.parameters), ParameterFacts: parameterFacts,
			MinArgumentCount: record.minArgumentCount, HasRest: record.hasRest,
			TypeParameters: mapTypeKeys(record.typeParameterKeys, typeHashes, typeHashToID), InstantiatedTypeArguments: mapTypeKeys(record.instantiatedTypeKeys, typeHashes, typeHashToID),
			ReturnType: typeHashToID[typeHashes[record.returnTypeKey]], Predicate: predicate,
			CallingConventionClass: record.callingConventionClass, Effects: slices.Clone(record.effects),
			EffectProof: SignatureEffectProofSnapshot{
				Kind:           record.effectProofKind,
				Implementation: record.effectImplementation,
				Complete:       record.effectProofComplete,
				DirectEffects:  slices.Clone(record.directEffects),
				Calls:          effectCalls,
			},
		})
	}
	slices.SortFunc(snapshot.Signatures, func(a, b SignatureSnapshot) int { return strings.Compare(a.CanonicalHash, b.CanonicalHash) })
	return snapshot, nil
}

func appendUniqueOrigin(origins []OriginSnapshot, origin OriginSnapshot) []OriginSnapshot {
	for _, existing := range origins {
		if existing.ID == origin.ID {
			return origins
		}
	}
	return append(origins, origin)
}

func mapTypeKeys(keys []string, hashes map[string]string, hashToID map[string]TypeID) []TypeID {
	result := make([]TypeID, 0, len(keys))
	for _, key := range keys {
		result = append(result, hashToID[hashes[key]])
	}
	return result
}

func stableID(prefix string, parts ...string) string {
	sum := sha256.Sum256([]byte(prefix + "\x00" + strings.Join(parts, "\x00")))
	return prefix + "_" + hex.EncodeToString(sum[:16])
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func sortedTypeKeys(types map[string]*pendingType) []string {
	result := make([]string, 0, len(types))
	for key := range types {
		result = append(result, key)
	}
	slices.Sort(result)
	return result
}
func sortedSymbolIDs(symbols map[SymbolID]*pendingSymbol) []SymbolID {
	result := make([]SymbolID, 0, len(symbols))
	for key := range symbols {
		result = append(result, key)
	}
	slices.Sort(result)
	return result
}
func sortedSignatureKeys(signatures map[string]*pendingSignature) []string {
	result := make([]string, 0, len(signatures))
	for key := range signatures {
		result = append(result, key)
	}
	slices.Sort(result)
	return result
}

func assignTypeIDs(types map[string]*pendingType, hashes map[string]string) (map[string]TypeID, map[string]TypeID) {
	unique := make(map[string]string)
	for key, hash := range hashes {
		if old, ok := unique[hash]; !ok || key < old {
			unique[hash] = key
		}
	}
	hashesSorted := make([]string, 0, len(unique))
	for hash := range unique {
		hashesSorted = append(hashesSorted, hash)
	}
	slices.Sort(hashesSorted)
	ids := make(map[string]TypeID, len(types))
	hashToID := make(map[string]TypeID, len(unique))
	for i, hash := range hashesSorted {
		id := TypeID(i + 1)
		hashToID[hash] = id
		ids[unique[hash]] = id
	}
	for key, hash := range hashes {
		if _, ok := ids[key]; !ok {
			ids[key] = hashToID[hash]
		}
	}
	return ids, hashToID
}

func assignSignatureIDs(signatures map[string]*pendingSignature, hashes map[string]string) (map[string]SignatureID, map[string]SignatureID) {
	unique := make(map[string]string)
	for key, hash := range hashes {
		if old, ok := unique[hash]; !ok || key < old {
			unique[hash] = key
		}
	}
	ordered := make([]string, 0, len(unique))
	for hash := range unique {
		ordered = append(ordered, hash)
	}
	slices.Sort(ordered)
	ids := make(map[string]SignatureID, len(signatures))
	hashToID := make(map[string]SignatureID, len(unique))
	for i, hash := range ordered {
		id := SignatureID(i + 1)
		hashToID[hash] = id
		ids[unique[hash]] = id
	}
	for key, hash := range hashes {
		if _, ok := ids[key]; !ok {
			ids[key] = hashToID[hash]
		}
	}
	return ids, hashToID
}

func digestString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
