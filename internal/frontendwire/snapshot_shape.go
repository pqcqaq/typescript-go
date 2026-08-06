package frontendwire

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

type snapshotPayloadField uint8

const (
	snapshotPayloadText snapshotPayloadField = 1 << iota
	snapshotPayloadOperator
	snapshotPayloadAll = snapshotPayloadText | snapshotPayloadOperator

	snapshotChildMany = -1
)

type snapshotChildRoleDefinition struct {
	Role          string
	Indexed       bool
	AbsoluteIndex bool
	Min           int
	Max           int
	ChildKind     string
}

type snapshotKindShapeDefinition struct {
	Kind            string
	AllowedPayload  snapshotPayloadField
	RequiredPayload snapshotPayloadField
	ChildRoles      []snapshotChildRoleDefinition
}

const (
	snapshotKindBinaryExpression    = "KindBinaryExpression"
	snapshotKindBlock               = "KindBlock"
	snapshotKindEndOfFile           = "KindEndOfFile"
	snapshotKindExportKeyword       = "KindExportKeyword"
	snapshotKindFunctionDeclaration = "KindFunctionDeclaration"
	snapshotKindIdentifier          = "KindIdentifier"
	snapshotKindNumberKeyword       = "KindNumberKeyword"
	snapshotKindNumericLiteral      = "KindNumericLiteral"
	snapshotKindParameter           = "KindParameter"
	snapshotKindPlusToken           = "KindPlusToken"
	snapshotKindReturnStatement     = "KindReturnStatement"
	snapshotKindSourceFile          = "KindSourceFile"
)

// snapshotKindShapeRegistry is the executable syntax contract for payload-
// bearing nodes and the Kinds accepted by the primitive replay boundary. Kinds
// not listed here may carry no Text or Operator payload. Keep this sorted by
// Kind so lookup and review remain deterministic.
var snapshotKindShapeRegistry = []snapshotKindShapeDefinition{
	{Kind: "KindBigIntLiteral", AllowedPayload: snapshotPayloadText, RequiredPayload: snapshotPayloadText, ChildRoles: []snapshotChildRoleDefinition{}},
	{
		Kind:            snapshotKindBinaryExpression,
		AllowedPayload:  snapshotPayloadOperator,
		RequiredPayload: snapshotPayloadOperator,
		ChildRoles: []snapshotChildRoleDefinition{
			{Role: "modifier", Indexed: true, Min: 0, Max: snapshotChildMany},
			{Role: "left", Min: 1, Max: 1},
			{Role: "type", Min: 0, Max: 1},
			{Role: "operator", Min: 1, Max: 1},
			{Role: "right", Min: 1, Max: 1},
			{Role: "jsDoc", Indexed: true, Min: 0, Max: snapshotChildMany},
		},
	},
	{
		Kind: snapshotKindBlock,
		ChildRoles: []snapshotChildRoleDefinition{
			{Role: "statement", Indexed: true, Min: 0, Max: snapshotChildMany},
			{Role: "jsDoc", Indexed: true, Min: 0, Max: snapshotChildMany},
		},
	},
	{Kind: snapshotKindEndOfFile, ChildRoles: []snapshotChildRoleDefinition{}},
	{
		Kind: "KindExportDeclaration",
		ChildRoles: []snapshotChildRoleDefinition{
			{Role: "modifier", Indexed: true, Min: 0, Max: snapshotChildMany},
			{Role: "exportClause", Min: 0, Max: 1},
			{Role: "moduleSpecifier", Min: 0, Max: 1},
			{Role: "attributes", Min: 0, Max: 1},
			{Role: "jsDoc", Indexed: true, Min: 0, Max: snapshotChildMany},
		},
	},
	{Kind: snapshotKindExportKeyword, ChildRoles: []snapshotChildRoleDefinition{}},
	{
		Kind: "KindExportSpecifier",
		ChildRoles: []snapshotChildRoleDefinition{
			{Role: "propertyName", Min: 0, Max: 1},
			{Role: "name", Min: 1, Max: 1},
			{Role: "jsDoc", Indexed: true, Min: 0, Max: snapshotChildMany},
		},
	},
	{
		Kind: "KindExternalModuleReference",
		ChildRoles: []snapshotChildRoleDefinition{
			{Role: "expression", Min: 1, Max: 1},
			{Role: "jsDoc", Indexed: true, Min: 0, Max: snapshotChildMany},
		},
	},
	{
		Kind: snapshotKindFunctionDeclaration,
		ChildRoles: []snapshotChildRoleDefinition{
			{Role: "modifier", Indexed: true, Min: 0, Max: snapshotChildMany},
			{Role: "asteriskToken", Min: 0, Max: 1},
			{Role: "name", Min: 0, Max: 1},
			{Role: "typeParameter", Indexed: true, Min: 0, Max: snapshotChildMany},
			{Role: "parameter", Indexed: true, Min: 0, Max: snapshotChildMany},
			{Role: "returnType", Min: 0, Max: 1},
			{Role: "fullSignature", Min: 0, Max: 1},
			{Role: "body", Min: 0, Max: 1},
			{Role: "jsDoc", Indexed: true, Min: 0, Max: snapshotChildMany},
		},
	},
	{Kind: snapshotKindIdentifier, AllowedPayload: snapshotPayloadText, RequiredPayload: snapshotPayloadText, ChildRoles: []snapshotChildRoleDefinition{}},
	{
		Kind: "KindImportClause",
		ChildRoles: []snapshotChildRoleDefinition{
			{Role: "defaultBinding", Min: 0, Max: 1},
			{Role: "namedBindings", Min: 0, Max: 1},
			{Role: "jsDoc", Indexed: true, Min: 0, Max: snapshotChildMany},
		},
	},
	{
		Kind: "KindImportDeclaration",
		ChildRoles: []snapshotChildRoleDefinition{
			{Role: "modifier", Indexed: true, Min: 0, Max: snapshotChildMany},
			{Role: "importClause", Min: 0, Max: 1},
			{Role: "moduleSpecifier", Min: 1, Max: 1},
			{Role: "attributes", Min: 0, Max: 1},
			{Role: "jsDoc", Indexed: true, Min: 0, Max: snapshotChildMany},
		},
	},
	{
		Kind: "KindImportEqualsDeclaration",
		ChildRoles: []snapshotChildRoleDefinition{
			{Role: "modifier", Indexed: true, Min: 0, Max: snapshotChildMany},
			{Role: "name", Min: 1, Max: 1},
			{Role: "moduleReference", Min: 1, Max: 1},
			{Role: "jsDoc", Indexed: true, Min: 0, Max: snapshotChildMany},
		},
	},
	{
		Kind: "KindImportSpecifier",
		ChildRoles: []snapshotChildRoleDefinition{
			{Role: "propertyName", Min: 0, Max: 1},
			{Role: "name", Min: 1, Max: 1},
			{Role: "jsDoc", Indexed: true, Min: 0, Max: snapshotChildMany},
		},
	},
	{Kind: "KindJSDocText", AllowedPayload: snapshotPayloadText, ChildRoles: []snapshotChildRoleDefinition{}},
	{
		Kind: "KindJSImportDeclaration",
		ChildRoles: []snapshotChildRoleDefinition{
			{Role: "modifier", Indexed: true, Min: 0, Max: snapshotChildMany},
			{Role: "importClause", Min: 0, Max: 1},
			{Role: "moduleSpecifier", Min: 1, Max: 1},
			{Role: "attributes", Min: 0, Max: 1},
			{Role: "jsDoc", Indexed: true, Min: 0, Max: snapshotChildMany},
		},
	},
	{
		Kind: "KindNamedExports",
		ChildRoles: []snapshotChildRoleDefinition{
			{Role: "specifier", Indexed: true, Min: 0, Max: snapshotChildMany},
			{Role: "jsDoc", Indexed: true, Min: 0, Max: snapshotChildMany},
		},
	},
	{
		Kind: "KindNamedImports",
		ChildRoles: []snapshotChildRoleDefinition{
			{Role: "specifier", Indexed: true, Min: 0, Max: snapshotChildMany},
			{Role: "jsDoc", Indexed: true, Min: 0, Max: snapshotChildMany},
		},
	},
	{
		Kind: "KindNamespaceExport",
		ChildRoles: []snapshotChildRoleDefinition{
			{Role: "name", Min: 1, Max: 1},
			{Role: "jsDoc", Indexed: true, Min: 0, Max: snapshotChildMany},
		},
	},
	{
		Kind: "KindNamespaceImport",
		ChildRoles: []snapshotChildRoleDefinition{
			{Role: "name", Min: 1, Max: 1},
			{Role: "jsDoc", Indexed: true, Min: 0, Max: snapshotChildMany},
		},
	},
	{Kind: "KindNoSubstitutionTemplateLiteral", AllowedPayload: snapshotPayloadText, ChildRoles: []snapshotChildRoleDefinition{}},
	{Kind: snapshotKindNumberKeyword, ChildRoles: []snapshotChildRoleDefinition{}},
	{Kind: snapshotKindNumericLiteral, AllowedPayload: snapshotPayloadText, RequiredPayload: snapshotPayloadText, ChildRoles: []snapshotChildRoleDefinition{}},
	{
		Kind: snapshotKindParameter,
		ChildRoles: []snapshotChildRoleDefinition{
			{Role: "modifier", Indexed: true, Min: 0, Max: snapshotChildMany},
			{Role: "dotDotDotToken", Min: 0, Max: 1},
			{Role: "name", Min: 1, Max: 1},
			{Role: "questionToken", Min: 0, Max: 1},
			{Role: "type", Min: 0, Max: 1},
			{Role: "initializer", Min: 0, Max: 1},
			{Role: "jsDoc", Indexed: true, Min: 0, Max: snapshotChildMany},
		},
	},
	{Kind: snapshotKindPlusToken, ChildRoles: []snapshotChildRoleDefinition{}},
	{
		Kind:            "KindPostfixUnaryExpression",
		AllowedPayload:  snapshotPayloadOperator,
		RequiredPayload: snapshotPayloadOperator,
		ChildRoles: []snapshotChildRoleDefinition{
			{Role: "operand", Min: 1, Max: 1},
			{Role: "jsDoc", Indexed: true, Min: 0, Max: snapshotChildMany},
		},
	},
	{
		Kind:            "KindPrefixUnaryExpression",
		AllowedPayload:  snapshotPayloadOperator,
		RequiredPayload: snapshotPayloadOperator,
		ChildRoles: []snapshotChildRoleDefinition{
			{Role: "operand", Min: 1, Max: 1},
			{Role: "jsDoc", Indexed: true, Min: 0, Max: snapshotChildMany},
		},
	},
	{Kind: "KindPrivateIdentifier", AllowedPayload: snapshotPayloadText, RequiredPayload: snapshotPayloadText, ChildRoles: []snapshotChildRoleDefinition{}},
	{Kind: "KindRegularExpressionLiteral", AllowedPayload: snapshotPayloadText, RequiredPayload: snapshotPayloadText, ChildRoles: []snapshotChildRoleDefinition{}},
	{
		Kind: snapshotKindReturnStatement,
		ChildRoles: []snapshotChildRoleDefinition{
			{Role: "expression", Min: 0, Max: 1},
			{Role: "jsDoc", Indexed: true, Min: 0, Max: snapshotChildMany},
		},
	},
	{
		Kind: snapshotKindSourceFile,
		ChildRoles: []snapshotChildRoleDefinition{
			{Role: "statement", Indexed: true, Min: 0, Max: snapshotChildMany},
			// SourceFile's EOF token is the one generic IterChildren edge
			// retained by schema v2. Its absolute suffix and child Kind make
			// the edge unambiguous without changing existing snapshot hashes.
			{Role: "child", Indexed: true, AbsoluteIndex: true, Min: 1, Max: 1, ChildKind: snapshotKindEndOfFile},
			{Role: "jsDoc", Indexed: true, Min: 0, Max: snapshotChildMany},
		},
	},
	{Kind: "KindStringLiteral", AllowedPayload: snapshotPayloadText, ChildRoles: []snapshotChildRoleDefinition{}},
	{Kind: "KindTemplateHead", AllowedPayload: snapshotPayloadText, ChildRoles: []snapshotChildRoleDefinition{}},
	{Kind: "KindTemplateMiddle", AllowedPayload: snapshotPayloadText, ChildRoles: []snapshotChildRoleDefinition{}},
	{Kind: "KindTemplateTail", AllowedPayload: snapshotPayloadText, ChildRoles: []snapshotChildRoleDefinition{}},
}

func validateSnapshotKindShapeRegistry(registry []snapshotKindShapeDefinition) error {
	if len(registry) == 0 {
		return fmt.Errorf("registry is empty")
	}
	for index, definition := range registry {
		if strings.TrimSpace(definition.Kind) == "" {
			return fmt.Errorf("shape %d has an empty Kind", index)
		}
		if index > 0 && registry[index-1].Kind >= definition.Kind {
			return fmt.Errorf("shape Kinds are not sorted and unique at %q", definition.Kind)
		}
		if definition.AllowedPayload&^snapshotPayloadAll != 0 {
			return fmt.Errorf("shape for Kind %q allows unknown payload fields", definition.Kind)
		}
		if definition.RequiredPayload&^definition.AllowedPayload != 0 {
			return fmt.Errorf("shape for Kind %q requires forbidden payload fields", definition.Kind)
		}
		seenRoles := make(map[string]struct{}, len(definition.ChildRoles))
		for roleIndex, role := range definition.ChildRoles {
			if strings.TrimSpace(role.Role) == "" {
				return fmt.Errorf("shape for Kind %q child role %d is empty", definition.Kind, roleIndex)
			}
			if _, duplicate := seenRoles[role.Role]; duplicate {
				return fmt.Errorf("shape for Kind %q contains duplicate child role %q", definition.Kind, role.Role)
			}
			seenRoles[role.Role] = struct{}{}
			if role.Min < 0 || (role.Max != snapshotChildMany && role.Max < role.Min) {
				return fmt.Errorf("shape for Kind %q child role %q has invalid cardinality %d..%d", definition.Kind, role.Role, role.Min, role.Max)
			}
			if role.AbsoluteIndex && !role.Indexed {
				return fmt.Errorf("shape for Kind %q child role %q uses an absolute index without being indexed", definition.Kind, role.Role)
			}
		}
	}
	return nil
}

func lookupSnapshotKindShape(kind string) (snapshotKindShapeDefinition, bool) {
	index, ok := slices.BinarySearchFunc(snapshotKindShapeRegistry, kind, func(definition snapshotKindShapeDefinition, kind string) int {
		return strings.Compare(definition.Kind, kind)
	})
	if !ok {
		return snapshotKindShapeDefinition{}, false
	}
	return snapshotKindShapeRegistry[index], true
}

func validateSnapshotKindShape(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) error {
	definition, registered := lookupSnapshotKindShape(node.Kind)
	presentPayload := snapshotPayloadField(0)
	if node.SyntaxPayload.Text != "" {
		presentPayload |= snapshotPayloadText
	}
	if node.SyntaxPayload.Operator != "" {
		presentPayload |= snapshotPayloadOperator
	}
	if !registered {
		if presentPayload != 0 {
			return fmt.Errorf("node %q Kind %q has payload fields without a registered shape", node.ID, node.Kind)
		}
		return nil
	}
	if missing := definition.RequiredPayload &^ presentPayload; missing != 0 {
		return fmt.Errorf("node %q Kind %q requires payload field %q", node.ID, node.Kind, snapshotPayloadFieldName(missing))
	}
	if forbidden := presentPayload &^ definition.AllowedPayload; forbidden != 0 {
		return fmt.Errorf("node %q Kind %q forbids payload field %q", node.ID, node.Kind, snapshotPayloadFieldName(forbidden))
	}

	counts := make([]int, len(definition.ChildRoles))
	previousRole := -1
	for childIndex, child := range node.NamedChildren {
		roleIndex, indexedValue, ok := matchSnapshotChildRole(child.Role, definition.ChildRoles)
		if !ok {
			return fmt.Errorf("node %q Kind %q child role %q is not allowed", node.ID, node.Kind, child.Role)
		}
		role := definition.ChildRoles[roleIndex]
		if roleIndex < previousRole {
			return fmt.Errorf("node %q Kind %q child role %q is out of order", node.ID, node.Kind, child.Role)
		}
		previousRole = roleIndex
		if role.Indexed {
			want := counts[roleIndex]
			if role.AbsoluteIndex {
				want = childIndex
			}
			if indexedValue != want {
				return fmt.Errorf("node %q Kind %q child role %q has index %d, want %d", node.ID, node.Kind, child.Role, indexedValue, want)
			}
		}
		counts[roleIndex]++
		if role.Max != snapshotChildMany && counts[roleIndex] > role.Max {
			return fmt.Errorf("node %q Kind %q child role %q exceeds maximum arity %d", node.ID, node.Kind, role.Role, role.Max)
		}
		if role.ChildKind != "" {
			childNode, ok := nodes[child.Node]
			if !ok || childNode.Kind != role.ChildKind {
				return fmt.Errorf("node %q Kind %q child role %q requires child Kind %q", node.ID, node.Kind, child.Role, role.ChildKind)
			}
		}
	}
	for roleIndex, role := range definition.ChildRoles {
		if counts[roleIndex] < role.Min {
			return fmt.Errorf("node %q Kind %q child role %q has arity %d, want at least %d", node.ID, node.Kind, role.Role, counts[roleIndex], role.Min)
		}
	}
	return nil
}

func matchSnapshotChildRole(value string, roles []snapshotChildRoleDefinition) (int, int, bool) {
	for index, role := range roles {
		if !role.Indexed {
			if value == role.Role {
				return index, 0, true
			}
			continue
		}
		prefix := role.Role + "["
		if !strings.HasPrefix(value, prefix) || !strings.HasSuffix(value, "]") {
			continue
		}
		rawIndex := strings.TrimSuffix(strings.TrimPrefix(value, prefix), "]")
		parsed, err := strconv.Atoi(rawIndex)
		if err != nil || parsed < 0 || strconv.Itoa(parsed) != rawIndex {
			continue
		}
		return index, parsed, true
	}
	return 0, 0, false
}

func snapshotPayloadFieldName(fields snapshotPayloadField) string {
	if fields&snapshotPayloadText != 0 {
		return "text"
	}
	if fields&snapshotPayloadOperator != 0 {
		return "operator"
	}
	return "unknown"
}
