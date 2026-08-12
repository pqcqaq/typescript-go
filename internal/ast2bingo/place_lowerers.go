package ast2bingo

import (
	"fmt"
	"strings"
)

func validateVERT011AsExpressionLowerer(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) error {
	if len(node.NamedChildren) != 2 || len(node.Children) != 2 {
		return fmt.Errorf("VERT-011 const assertion requires expression and type")
	}
	literal, err := requireRoleKind(node, "expression", snapshotKindStringLiteral, nodes)
	if err != nil || literal.SyntaxPayload.Text != "result" {
		return fmt.Errorf("VERT-011 const assertion must preserve the result key")
	}
	typeReference, err := requireRoleKind(node, "type", snapshotKindTypeReference, nodes)
	if err != nil || childText(typeReference, "child[0]", nodes) != "const" {
		return fmt.Errorf("VERT-011 assertion must be as const")
	}
	return nil
}

func validateVERT011ElementAccessLowerer(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) error {
	if len(node.NamedChildren) != 2 || len(node.Children) != 2 || childText(node, "child[0]", nodes) != "object" || childText(node, "child[1]", nodes) != "key" {
		return fmt.Errorf("VERT-011 element access must be object[key]")
	}
	if _, err := requireRoleKind(node, "child[0]", snapshotKindIdentifier, nodes); err != nil {
		return err
	}
	_, err := requireRoleKind(node, "child[1]", snapshotKindIdentifier, nodes)
	return err
}

func validateVERT011GetAccessorLowerer(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) error {
	if len(node.NamedChildren) != 3 || len(node.Children) != 3 || childText(node, "name", nodes) != "result" {
		return fmt.Errorf("VERT-011 getter must be result with an explicit return type and body")
	}
	if _, err := requireRoleKind(node, "returnType", snapshotKindUnionType, nodes); err != nil {
		return err
	}
	_, err := requireRoleKind(node, "body", snapshotKindBlock, nodes)
	return err
}

func validateVERT011SetAccessorLowerer(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) error {
	if len(node.NamedChildren) != 3 || len(node.Children) != 3 || childText(node, "name", nodes) != "result" {
		return fmt.Errorf("VERT-011 setter must be result with one parameter and body")
	}
	parameter, err := requireRoleKind(node, "parameter[0]", snapshotKindParameter, nodes)
	if err != nil || childText(parameter, "name", nodes) != "next" {
		return fmt.Errorf("VERT-011 setter parameter must be next")
	}
	_, err = requireRoleKind(node, "body", snapshotKindBlock, nodes)
	return err
}

func validateVERT011PropertyAssignmentLowerer(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) error {
	if len(node.NamedChildren) != 2 || len(node.Children) != 2 || childText(node, "name", nodes) != "backing" || childText(node, "initializer", nodes) != "value" {
		return fmt.Errorf("VERT-011 data property must initialize backing from value")
	}
	if _, err := requireRoleKind(node, "name", snapshotKindIdentifier, nodes); err != nil {
		return err
	}
	_, err := requireRoleKind(node, "initializer", snapshotKindIdentifier, nodes)
	return err
}

func validateVERT011ParenthesizedLowerer(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) error {
	if len(node.NamedChildren) != 1 || len(node.Children) != 1 {
		return fmt.Errorf("VERT-011 parenthesized expression requires one child")
	}
	expression, err := requireRoleKind(node, "expression", snapshotKindBinaryExpression, nodes)
	if err != nil || expression.SyntaxPayload.Operator != snapshotKindQuestionQuestionEqualsToken {
		return fmt.Errorf("VERT-011 parentheses must contain property ??=")
	}
	return nil
}

func validateVERT011StringLiteralLowerer(node NodeSnapshot, _ map[NodeID]NodeSnapshot) error {
	if len(node.NamedChildren) != 0 || len(node.Children) != 0 || node.SyntaxPayload.Text != "result" || node.Constant.Kind != "string" || node.Constant.Text != "result" {
		return fmt.Errorf("VERT-011 string literal must be the canonical result key")
	}
	return nil
}

func validateVERT011ThisLowerer(node NodeSnapshot, _ map[NodeID]NodeSnapshot) error {
	if len(node.NamedChildren) != 0 || len(node.Children) != 0 || len(nodeSymbolIDs(node)) == 0 || node.DeclaredType == 0 || node.NarrowedType == 0 || node.ContextualType != 0 {
		return fmt.Errorf("VERT-011 this must carry a receiver symbol and no children")
	}
	return nil
}

func validateVERT011TypeReferenceLowerer(node NodeSnapshot, nodes map[NodeID]NodeSnapshot) error {
	if len(node.NamedChildren) != 1 || len(node.Children) != 1 || childText(node, "child[0]", nodes) != "const" {
		return fmt.Errorf("VERT-011 type reference must be the const assertion marker")
	}
	identifier, err := requireRoleKind(node, "child[0]", snapshotKindIdentifier, nodes)
	if err != nil || strings.TrimSpace(identifier.SyntaxPayload.Text) != "const" {
		return fmt.Errorf("VERT-011 type reference is not const")
	}
	return nil
}
