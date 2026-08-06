package frontendwire

import (
	"fmt"
	"slices"
	"strings"
)

const (
	snapshotEvaluationFlagTypeContext uint32 = 1 << iota
)

const snapshotEvaluationFlagParserMissing uint32 = 1 << 31
const snapshotEffectAsyncModifier uint32 = 1 << 10

type snapshotEffectRuleMode uint8

const (
	snapshotEffectNeutral snapshotEffectRuleMode = iota
	snapshotEffectAccess
	snapshotEffectBinary
	snapshotEffectPrefix
	snapshotEffectCall
	snapshotEffectCallAlloc
	snapshotEffectLiteralAlloc
	snapshotEffectAlloc
	snapshotEffectAllocIncomplete
	snapshotEffectThrow
	snapshotEffectSuspend
	snapshotEffectNondeterministic
	snapshotEffectIncomplete
	snapshotEffectIncompleteCall
)

type snapshotEffectRuleDefinition struct {
	Mode  snapshotEffectRuleMode
	Kinds []string
}

// Every non-type, non-token Kind has one explicit effect disposition. Neutral
// means that child evaluation fully describes the effect; incomplete means the
// runtime behavior is deliberately not modeled and therefore cannot prove pure.
var snapshotEffectRuleRegistry = []snapshotEffectRuleDefinition{
	{Mode: snapshotEffectNeutral, Kinds: []string{
		"KindAsExpression", "KindBlock", "KindBreakStatement", "KindCaseBlock", "KindCaseClause", "KindCatchClause",
		"KindComputedPropertyName", "KindConditionalExpression", "KindContinueStatement", "KindDefaultClause", "KindDoStatement",
		"KindEmptyStatement", "KindEndOfFile", "KindExpressionStatement", "KindExpressionWithTypeArguments", "KindForStatement",
		"KindIfStatement", "KindInterfaceDeclaration", "KindLabeledStatement", "KindMetaProperty", "KindNonNullExpression",
		"KindNumericLiteral", "KindOmittedExpression", "KindParameter", "KindParenthesizedExpression", "KindPropertyAssignment", "KindQualifiedName",
		"KindReturnStatement", "KindSatisfiesExpression",
		"KindSemicolonClassElement", "KindShorthandPropertyAssignment", "KindStringLiteral", "KindSwitchStatement",
		"KindTemplateHead", "KindTemplateMiddle", "KindTemplateSpan", "KindTemplateTail", "KindTryStatement",
		"KindTypeAssertionExpression", "KindTypeOfExpression", "KindVariableDeclaration", "KindVariableDeclarationList",
		"KindVariableStatement", "KindVoidExpression", "KindWhileStatement",
	}},
	{Mode: snapshotEffectAccess, Kinds: []string{
		"KindElementAccessExpression", "KindPropertyAccessExpression",
	}},
	{Mode: snapshotEffectBinary, Kinds: []string{
		"KindBinaryExpression",
	}},
	{Mode: snapshotEffectPrefix, Kinds: []string{
		"KindPostfixUnaryExpression", "KindPrefixUnaryExpression",
	}},
	{Mode: snapshotEffectCall, Kinds: []string{
		"KindCallExpression",
	}},
	{Mode: snapshotEffectCallAlloc, Kinds: []string{
		"KindNewExpression", "KindTaggedTemplateExpression",
	}},
	{Mode: snapshotEffectLiteralAlloc, Kinds: []string{
		"KindArrayLiteralExpression", "KindObjectLiteralExpression",
	}},
	{Mode: snapshotEffectAlloc, Kinds: []string{
		"KindArrowFunction", "KindBigIntLiteral", "KindConstructor", "KindFunctionDeclaration", "KindFunctionExpression",
		"KindGetAccessor", "KindMethodDeclaration", "KindNoSubstitutionTemplateLiteral", "KindRegularExpressionLiteral",
		"KindSetAccessor", "KindTemplateExpression",
	}},
	{Mode: snapshotEffectAllocIncomplete, Kinds: []string{
		"KindClassDeclaration", "KindClassExpression", "KindEnumDeclaration",
	}},
	{Mode: snapshotEffectThrow, Kinds: []string{
		"KindThrowStatement",
	}},
	{Mode: snapshotEffectSuspend, Kinds: []string{
		"KindAwaitExpression", "KindYieldExpression",
	}},
	{Mode: snapshotEffectNondeterministic, Kinds: []string{
		"KindDebuggerStatement",
	}},
	{Mode: snapshotEffectIncomplete, Kinds: []string{
		"KindArrayBindingPattern", "KindBindingElement", "KindClassStaticBlockDeclaration", "KindDeleteExpression",
		"KindEnumMember", "KindExportAssignment", "KindExportDeclaration", "KindExportSpecifier", "KindExternalModuleReference",
		"KindForInStatement", "KindForOfStatement", "KindHeritageClause", "KindImportAttribute", "KindImportAttributes",
		"KindImportClause", "KindImportDeclaration", "KindImportEqualsDeclaration", "KindImportSpecifier", "KindJSImportDeclaration",
		"KindJsxAttribute", "KindJsxAttributes", "KindJsxClosingElement", "KindJsxClosingFragment", "KindJsxElement",
		"KindJsxExpression", "KindJsxFragment", "KindJsxNamespacedName", "KindJsxSpreadAttribute", "KindJsxText",
		"KindJsxTextAllWhiteSpaces", "KindMissingDeclaration", "KindModuleBlock", "KindModuleDeclaration", "KindNamedExports",
		"KindNamedImports", "KindNamedTupleMember", "KindNamespaceExport", "KindNamespaceExportDeclaration", "KindNamespaceImport",
		"KindNotEmittedStatement", "KindObjectBindingPattern", "KindPartiallyEmittedExpression", "KindPropertyDeclaration",
		"KindSourceFile", "KindSpreadAssignment", "KindSpreadElement", "KindSyntaxList", "KindSyntheticExpression",
		"KindSyntheticReferenceExpression", "KindUnknown", "KindWithStatement",
	}},
	{Mode: snapshotEffectIncompleteCall, Kinds: []string{
		"KindDecorator", "KindJsxOpeningElement", "KindJsxOpeningFragment", "KindJsxSelfClosingElement",
	}},
}

type snapshotEffectKindMetadata struct {
	entry          KindManifestEntry
	definitelyType bool
	possiblyType   bool
}

var snapshotEffectMetadataByKind, snapshotEffectMetadataError = loadSnapshotEffectKindMetadata()
var snapshotEffectRuleByKind = indexSnapshotEffectRules(snapshotEffectRuleRegistry)

func loadSnapshotEffectKindMetadata() (map[string]snapshotEffectKindMetadata, error) {
	document, err := LoadKindManifest()
	if err != nil {
		return nil, err
	}
	result := make(map[string]snapshotEffectKindMetadata, len(document.Kinds))
	for _, entry := range document.Kinds {
		isTypeNodeKind := snapshotManifestKindIsTypeNode(entry)
		definitelyType := isTypeNodeKind && entry.Kind != "KindParameter" && entry.Kind != "KindExpressionWithTypeArguments" || entry.Kind == "KindInterfaceDeclaration"
		result[entry.Kind] = snapshotEffectKindMetadata{
			entry:          entry,
			definitelyType: definitelyType,
			possiblyType:   isTypeNodeKind || entry.Kind == "KindInterfaceDeclaration",
		}
	}
	return result, nil
}

func snapshotManifestKindIsTypeNode(entry KindManifestEntry) bool {
	if entry.Domain == "type" || entry.KindValue >= 183 && entry.KindValue <= 206 {
		return true
	}
	switch entry.Kind {
	case "KindAnyKeyword", "KindUnknownKeyword", "KindNumberKeyword", "KindBigIntKeyword", "KindObjectKeyword",
		"KindBooleanKeyword", "KindStringKeyword", "KindSymbolKeyword", "KindVoidKeyword", "KindUndefinedKeyword",
		"KindNeverKeyword", "KindIntrinsicKeyword", "KindExpressionWithTypeArguments":
		return true
	default:
		return false
	}
}

func indexSnapshotEffectRules(registry []snapshotEffectRuleDefinition) map[string]snapshotEffectRuleMode {
	result := make(map[string]snapshotEffectRuleMode)
	for _, definition := range registry {
		for _, kind := range definition.Kinds {
			result[kind] = definition.Mode
		}
	}
	return result
}

func validateSnapshotEffectRuleRegistry() error {
	if snapshotEffectMetadataError != nil {
		return fmt.Errorf("load Kind metadata: %w", snapshotEffectMetadataError)
	}
	seen := make(map[string]struct{})
	for index, definition := range snapshotEffectRuleRegistry {
		if definition.Mode > snapshotEffectIncompleteCall {
			return fmt.Errorf("effect rule group %d has invalid mode %d", index, definition.Mode)
		}
		if len(definition.Kinds) == 0 {
			return fmt.Errorf("effect rule group %d is empty", index)
		}
		if !slices.IsSorted(definition.Kinds) {
			return fmt.Errorf("effect rule group %d is not sorted", index)
		}
		for _, kind := range definition.Kinds {
			if _, ok := snapshotEffectMetadataByKind[kind]; !ok {
				return fmt.Errorf("effect rule references unknown Kind %q", kind)
			}
			if _, duplicate := seen[kind]; duplicate {
				return fmt.Errorf("effect rule binds Kind %q more than once", kind)
			}
			seen[kind] = struct{}{}
		}
	}
	missing := make([]string, 0)
	for kind, metadata := range snapshotEffectMetadataByKind {
		if metadata.definitelyType || metadata.entry.Domain == "lexical" {
			continue
		}
		if _, ok := seen[kind]; !ok {
			missing = append(missing, kind)
		}
	}
	if len(missing) != 0 {
		slices.Sort(missing)
		return fmt.Errorf("executable Kinds have no effect rule: %v", missing)
	}
	return nil
}

func snapshotEffectRuleForKind(kind string) (snapshotEffectRuleMode, bool) {
	metadata, ok := snapshotEffectMetadataByKind[kind]
	if !ok || snapshotEffectMetadataError != nil {
		return snapshotEffectIncomplete, false
	}
	if metadata.definitelyType {
		return snapshotEffectNeutral, true
	}
	if metadata.entry.Domain == "lexical" {
		return snapshotEffectNeutral, true
	}
	mode, ok := snapshotEffectRuleByKind[kind]
	if !ok {
		return snapshotEffectIncomplete, false
	}
	return mode, true
}

func snapshotEffectKindIsDefinitelyType(kind string) bool {
	return snapshotEffectMetadataByKind[kind].definitelyType
}

func snapshotEffectKindIsPossiblyType(kind string) bool {
	return snapshotEffectMetadataByKind[kind].possiblyType
}

func snapshotEffectKindIdentityMatches(kind string, value int16) bool {
	metadata, ok := snapshotEffectMetadataByKind[kind]
	return ok && metadata.entry.KindValue == value
}

func validateSnapshotEffectTypeContexts(values []NodeSnapshot, nodes map[NodeID]NodeSnapshot) error {
	for _, node := range values {
		hasTypeFlag := node.EvaluationFlags&snapshotEvaluationFlagTypeContext != 0
		required := snapshotEffectKindIsDefinitelyType(node.Kind)
		if node.Parent != "" {
			parent := nodes[node.Parent]
			required = required || parent.EvaluationFlags&snapshotEvaluationFlagTypeContext != 0 || snapshotEffectChildHasTypeRole(parent, node.ID)
		}
		if required && !hasTypeFlag {
			return fmt.Errorf("node %q (%s) under %q (%s) is in a type context without the type-context evaluation flag", node.ID, node.Kind, node.Parent, nodes[node.Parent].Kind)
		}
		if hasTypeFlag && !required && !snapshotEffectKindIsPossiblyType(node.Kind) {
			return fmt.Errorf("node %q (%s) under %q (%s) has a forged type-context evaluation flag", node.ID, node.Kind, node.Parent, nodes[node.Parent].Kind)
		}
	}
	return nil
}

func snapshotEffectChildHasTypeRole(parent NodeSnapshot, child NodeID) bool {
	for _, named := range parent.NamedChildren {
		if named.Node != child {
			continue
		}
		return named.Role == "type" || named.Role == "returnType" || named.Role == "fullSignature" ||
			strings.HasPrefix(named.Role, "typeArgument[") || strings.HasPrefix(named.Role, "typeParameter[") || strings.HasPrefix(named.Role, "jsDoc[")
	}
	return false
}

type snapshotEffectPrimitiveKind uint8

const (
	snapshotEffectPrimitiveUnknown snapshotEffectPrimitiveKind = iota
	snapshotEffectPrimitiveNumber
	snapshotEffectPrimitiveBigInt
	snapshotEffectPrimitiveString
	snapshotEffectPrimitiveBoolean
	snapshotEffectPrimitiveOther
)

func serializedEffectPrimitiveKind(node NodeSnapshot, types map[TypeID]TypeSnapshot) snapshotEffectPrimitiveKind {
	id := node.NarrowedType
	if id == 0 {
		id = node.DeclaredType
	}
	typ, ok := types[id]
	if !ok {
		return snapshotEffectPrimitiveUnknown
	}
	return effectPrimitiveKindFromFlags(typ.Flags)
}

func effectPrimitiveKindFromFlags(flags uint32) snapshotEffectPrimitiveKind {
	const (
		typeFlagsAnyOrUnknown  = uint32(1<<0 | 1<<1)
		typeFlagsObject        = uint32(1 << 20)
		typeFlagsIndexedAccess = uint32(1 << 25)
		typeFlagsConditional   = uint32(1 << 26)
		typeFlagsUnion         = uint32(1 << 27)
		typeFlagsIntersection  = uint32(1 << 28)
		typeFlagsTypeVariable  = uint32(1<<19 | 1<<25)
		typeFlagsStringLike    = uint32(1<<5 | 1<<10 | 1<<22 | 1<<23)
		typeFlagsNumberLike    = uint32(1<<6 | 1<<11 | 1<<16)
		typeFlagsBigIntLike    = uint32(1<<7 | 1<<12)
		typeFlagsBooleanLike   = uint32(1<<8 | 1<<13)
		typeFlagsPrimitive     = uint32(1<<3 | 1<<4 | 1<<2 | typeFlagsStringLike | typeFlagsNumberLike | typeFlagsBigIntLike | typeFlagsBooleanLike | 1<<9 | 1<<14 | 1<<15 | 1<<16)
	)
	if flags&(typeFlagsAnyOrUnknown|typeFlagsObject|typeFlagsUnion|typeFlagsIntersection|typeFlagsTypeVariable|typeFlagsConditional|typeFlagsIndexedAccess) != 0 {
		return snapshotEffectPrimitiveUnknown
	}
	switch {
	case flags&typeFlagsNumberLike != 0:
		return snapshotEffectPrimitiveNumber
	case flags&typeFlagsBigIntLike != 0:
		return snapshotEffectPrimitiveBigInt
	case flags&typeFlagsStringLike != 0:
		return snapshotEffectPrimitiveString
	case flags&typeFlagsBooleanLike != 0:
		return snapshotEffectPrimitiveBoolean
	case flags&typeFlagsPrimitive != 0:
		return snapshotEffectPrimitiveOther
	default:
		return snapshotEffectPrimitiveUnknown
	}
}

func snapshotBinaryEffects(operator string, left, right snapshotEffectPrimitiveKind) ([]string, bool) {
	switch operator {
	case "KindEqualsToken", "KindAmpersandAmpersandEqualsToken", "KindBarBarEqualsToken", "KindQuestionQuestionEqualsToken",
		"KindAmpersandAmpersandToken", "KindBarBarToken", "KindQuestionQuestionToken", "KindCommaToken",
		"KindEqualsEqualsEqualsToken", "KindExclamationEqualsEqualsToken":
		return nil, true
	case "KindEqualsEqualsToken", "KindExclamationEqualsToken":
		return nil, left != snapshotEffectPrimitiveUnknown && right != snapshotEffectPrimitiveUnknown
	case "KindPlusToken", "KindPlusEqualsToken":
		if left == snapshotEffectPrimitiveString && right == snapshotEffectPrimitiveString {
			return []string{"alloc"}, true
		}
		return nil, sameNumericPrimitive(left, right)
	case "KindMinusToken", "KindMinusEqualsToken", "KindAsteriskToken", "KindAsteriskEqualsToken", "KindAsteriskAsteriskToken",
		"KindAsteriskAsteriskEqualsToken", "KindSlashToken", "KindSlashEqualsToken", "KindPercentToken", "KindPercentEqualsToken",
		"KindAmpersandToken", "KindAmpersandEqualsToken", "KindBarToken", "KindBarEqualsToken", "KindCaretToken", "KindCaretEqualsToken",
		"KindLessThanLessThanToken", "KindLessThanLessThanEqualsToken", "KindGreaterThanGreaterThanToken", "KindGreaterThanGreaterThanEqualsToken":
		return nil, sameNumericPrimitive(left, right)
	case "KindGreaterThanGreaterThanGreaterThanToken", "KindGreaterThanGreaterThanGreaterThanEqualsToken":
		return nil, left == snapshotEffectPrimitiveNumber && right == snapshotEffectPrimitiveNumber
	case "KindLessThanToken", "KindLessThanEqualsToken", "KindGreaterThanToken", "KindGreaterThanEqualsToken":
		return nil, sameNumericPrimitive(left, right) || left == snapshotEffectPrimitiveString && right == snapshotEffectPrimitiveString
	case "KindInstanceOfKeyword":
		return nil, false
	case "KindInKeyword":
		return nil, false
	default:
		return nil, false
	}
}

func snapshotPrefixEffects(operator string, operand snapshotEffectPrimitiveKind) ([]string, bool) {
	switch operator {
	case "KindExclamationToken":
		return nil, true
	case "KindPlusToken", "KindMinusToken", "KindTildeToken", "KindPlusPlusToken", "KindMinusMinusToken":
		return nil, operand == snapshotEffectPrimitiveNumber || operand == snapshotEffectPrimitiveBigInt
	default:
		return nil, false
	}
}

func sameNumericPrimitive(left, right snapshotEffectPrimitiveKind) bool {
	return left == right && (left == snapshotEffectPrimitiveNumber || left == snapshotEffectPrimitiveBigInt)
}

func snapshotEffectModeHasCall(mode snapshotEffectRuleMode) bool {
	return mode == snapshotEffectCall || mode == snapshotEffectCallAlloc || mode == snapshotEffectIncompleteCall
}

func snapshotEffectModeStopsProof(mode snapshotEffectRuleMode) bool {
	return mode == snapshotEffectAllocIncomplete || mode == snapshotEffectIncomplete || mode == snapshotEffectIncompleteCall
}

func appendSnapshotEffect(effects map[string]struct{}, effect string) {
	if strings.TrimSpace(effect) != "" {
		effects[effect] = struct{}{}
	}
}

func snapshotEffectChildByRole(node NodeSnapshot, role string) NodeID {
	for _, named := range node.NamedChildren {
		if named.Role == role {
			return named.Node
		}
	}
	return ""
}

func snapshotEffectChildText(node NodeSnapshot, role string, nodes map[NodeID]NodeSnapshot) string {
	return nodes[snapshotEffectChildByRole(node, role)].SyntaxPayload.Text
}

func snapshotEffectChildrenByPrefix(node NodeSnapshot, prefix string) []NodeID {
	result := make([]NodeID, 0)
	for _, named := range node.NamedChildren {
		if strings.HasPrefix(named.Role, prefix) {
			result = append(result, named.Node)
		}
	}
	return result
}

func snapshotEffectNodeAccess(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) (read, write bool) {
	current := node
	for {
		parent, ok := nodes[current.Parent]
		if !ok {
			return true, false
		}
		switch parent.Kind {
		case "KindParenthesizedExpression", "KindArrayLiteralExpression", "KindObjectLiteralExpression":
			current = parent
			continue
		case "KindPropertyAssignment":
			if snapshotEffectChildByRole(parent, "initializer") == current.ID {
				current = parent
				continue
			}
		case "KindShorthandPropertyAssignment":
			if snapshotEffectChildByRole(parent, "initializer") == current.ID {
				return true, false
			}
			if snapshotEffectChildByRole(parent, "name") == current.ID {
				current = parent
				continue
			}
		case "KindBinaryExpression":
			if snapshotEffectChildByRole(parent, "left") != current.ID || !snapshotEffectIsAssignmentOperator(parent.SyntaxPayload.Operator) {
				return true, false
			}
			if parent.SyntaxPayload.Operator == "KindEqualsToken" {
				return false, true
			}
			return true, true
		case "KindPrefixUnaryExpression", "KindPostfixUnaryExpression":
			if snapshotEffectChildByRole(parent, "operand") == current.ID &&
				(parent.SyntaxPayload.Operator == "KindPlusPlusToken" || parent.SyntaxPayload.Operator == "KindMinusMinusToken") {
				return true, true
			}
		case "KindForInStatement", "KindForOfStatement":
			if snapshotEffectChildByRole(parent, "initializer") == current.ID {
				return false, true
			}
		case "KindDeleteExpression":
			return false, true
		}
		return true, false
	}
}

func snapshotEffectIsAssignmentOperator(operator string) bool {
	switch operator {
	case "KindEqualsToken", "KindPlusEqualsToken", "KindMinusEqualsToken", "KindAsteriskEqualsToken", "KindAsteriskAsteriskEqualsToken",
		"KindSlashEqualsToken", "KindPercentEqualsToken", "KindLessThanLessThanEqualsToken", "KindGreaterThanGreaterThanEqualsToken",
		"KindGreaterThanGreaterThanGreaterThanEqualsToken", "KindAmpersandEqualsToken", "KindBarEqualsToken", "KindBarBarEqualsToken",
		"KindAmpersandAmpersandEqualsToken", "KindQuestionQuestionEqualsToken", "KindCaretEqualsToken":
		return true
	default:
		return false
	}
}

func snapshotEffectRuntimeBindingName(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) bool {
	if node.Kind != "KindIdentifier" && node.Kind != "KindPrivateIdentifier" || node.EvaluationFlags&snapshotEvaluationFlagTypeContext != 0 {
		return false
	}
	parent, ok := nodes[node.Parent]
	if !ok {
		return true
	}
	if snapshotEffectChildByRole(parent, "name") == node.ID && parent.Kind != "KindShorthandPropertyAssignment" {
		return false
	}
	if snapshotEffectChildByRole(parent, "propertyName") == node.ID {
		return false
	}
	if parent.Kind == "KindMetaProperty" {
		return false
	}
	if parent.Kind == "KindPropertyAssignment" {
		return snapshotEffectChildByRole(parent, "initializer") == node.ID
	}
	if parent.Kind == "KindPropertyAccessExpression" || parent.Kind == "KindQualifiedName" {
		return len(parent.Children) == 0 || parent.Children[0] == node.ID
	}
	return true
}

func snapshotEffectIsDirectInvokedBinding(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) bool {
	parent, ok := nodes[node.Parent]
	if !ok || !isSnapshotResolvedSignatureNode(parent) {
		return false
	}
	switch parent.Kind {
	case "KindCallExpression":
		return snapshotEffectChildByRole(parent, "callee") == node.ID
	case "KindNewExpression":
		return snapshotEffectChildByRole(parent, "constructor") == node.ID
	case "KindBinaryExpression":
		return snapshotEffectChildByRole(parent, "right") == node.ID
	default:
		return len(parent.Children) != 0 && parent.Children[0] == node.ID
	}
}

func snapshotEffectSymbolDeclaredWithin(symbol SymbolSnapshot, implementation NodeID, nodes map[NodeID]NodeSnapshot) bool {
	for _, declaration := range symbol.Declarations {
		for current := declaration; current != ""; current = nodes[current].Parent {
			if current == implementation {
				return true
			}
			if _, ok := nodes[current]; !ok {
				break
			}
		}
	}
	return false
}

func snapshotEffectAccessHasAccessor(node NodeSnapshot, nodes map[NodeID]NodeSnapshot, symbols map[SymbolID]SymbolSnapshot) bool {
	ids := []SymbolID{node.Symbol, node.ResolvedSymbol}
	for _, child := range node.Children {
		childNode := nodes[child]
		ids = append(ids, childNode.Symbol, childNode.ResolvedSymbol)
	}
	for _, id := range ids {
		for _, declaration := range symbols[id].Declarations {
			kind := nodes[declaration].Kind
			if kind == "KindGetAccessor" || kind == "KindSetAccessor" {
				return true
			}
		}
	}
	return false
}

func snapshotEffectIsDynamicImport(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) bool {
	callee := nodes[snapshotEffectChildByRole(node, "callee")]
	if callee.Kind == "KindImportKeyword" {
		return true
	}
	if callee.Kind == "KindMetaProperty" {
		for _, child := range callee.Children {
			if nodes[child].Kind == "KindImportKeyword" {
				return true
			}
		}
	}
	return false
}

func snapshotEffectIsDestructuringLiteral(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) bool {
	current := node
	for {
		parent, ok := nodes[current.Parent]
		if !ok {
			return false
		}
		switch parent.Kind {
		case "KindBinaryExpression":
			return parent.SyntaxPayload.Operator == "KindEqualsToken" && snapshotEffectChildByRole(parent, "left") == current.ID
		case "KindForOfStatement":
			return snapshotEffectChildByRole(parent, "initializer") == current.ID
		case "KindPropertyAssignment", "KindArrayLiteralExpression", "KindObjectLiteralExpression":
			current = parent
		default:
			return false
		}
	}
}
