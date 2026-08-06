package tsfrontend

import (
	"fmt"
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
	"github.com/microsoft/typescript-go/internal/checker"
)

const (
	snapshotEvaluationFlagTypeContext uint32 = 1 << iota
)

const snapshotEvaluationFlagParserMissing uint32 = 1 << 31

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
		kind := ast.Kind(entry.KindValue)
		definitelyType := entry.Domain == "type" && entry.Kind != "KindParameter" || ast.IsTypeNodeKind(kind) && kind != ast.KindExpressionWithTypeArguments || entry.Kind == "KindInterfaceDeclaration"
		result[entry.Kind] = snapshotEffectKindMetadata{
			entry:          entry,
			definitelyType: definitelyType,
		}
	}
	return result, nil
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

func validateCaptureEffectRuleRegistry() error {
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

func snapshotEffectASTNodeIsTypeContext(node *ast.Node) bool {
	if node == nil {
		return false
	}
	partOfType := ast.IsPartOfTypeNode(node)
	if partOfType && node.Parent != nil && ast.IsInExpressionContext(node) {
		partOfType = false
	}
	return snapshotEffectKindIsDefinitelyType(node.Kind.String()) || partOfType || ast.IsJSDocKind(node.Kind)
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

func checkerEffectPrimitiveKind(typ *checker.Type) snapshotEffectPrimitiveKind {
	if typ == nil {
		return snapshotEffectPrimitiveUnknown
	}
	return effectPrimitiveKindFromFlags(typ.Flags())
}

func effectPrimitiveKindFromFlags(flags checker.TypeFlags) snapshotEffectPrimitiveKind {
	if flags&(checker.TypeFlagsAnyOrUnknown|checker.TypeFlagsObject|checker.TypeFlagsUnion|checker.TypeFlagsIntersection|checker.TypeFlagsTypeVariable|checker.TypeFlagsConditional|checker.TypeFlagsIndexedAccess) != 0 {
		return snapshotEffectPrimitiveUnknown
	}
	switch {
	case flags&checker.TypeFlagsNumberLike != 0:
		return snapshotEffectPrimitiveNumber
	case flags&checker.TypeFlagsBigIntLike != 0:
		return snapshotEffectPrimitiveBigInt
	case flags&checker.TypeFlagsStringLike != 0:
		return snapshotEffectPrimitiveString
	case flags&checker.TypeFlagsBooleanLike != 0:
		return snapshotEffectPrimitiveBoolean
	case flags&checker.TypeFlagsPrimitive != 0:
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

func appendSnapshotEffect(effects map[string]struct{}, effect string) {
	if strings.TrimSpace(effect) != "" {
		effects[effect] = struct{}{}
	}
}
