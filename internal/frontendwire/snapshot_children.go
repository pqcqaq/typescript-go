package frontendwire

// These small DTO traversal helpers are shared by snapshot validation and
// other checker-free consumers. They deliberately operate only on copied
// node records; no AST or checker state can cross this boundary.

func namedChildren(node NodeSnapshot, prefix string) []NodeID {
	result := make([]NodeID, 0)
	for _, child := range node.NamedChildren {
		if child.Role == prefix || hasRolePrefix(child.Role, prefix) {
			result = append(result, child.Node)
		}
	}
	return result
}

func hasRolePrefix(role, prefix string) bool {
	return len(role) > len(prefix) && role[:len(prefix)] == prefix
}

func childByRole(node NodeSnapshot, role string) NodeID {
	for _, child := range node.NamedChildren {
		if child.Role == role {
			return child.Node
		}
	}
	return ""
}

func childText(node NodeSnapshot, role string, nodes map[NodeID]NodeSnapshot) string {
	child := childByRole(node, role)
	if child == "" {
		return ""
	}
	return nodes[child].SyntaxPayload.Text
}

func nodeSymbolIDs(node NodeSnapshot) []SymbolID {
	result := make([]SymbolID, 0, 2)
	seen := make(map[SymbolID]struct{}, 2)
	for _, symbol := range []SymbolID{node.Symbol, node.ResolvedSymbol} {
		if symbol == "" {
			continue
		}
		if _, duplicate := seen[symbol]; duplicate {
			continue
		}
		seen[symbol] = struct{}{}
		result = append(result, symbol)
	}
	return result
}

func nodeAndNamedChildSymbolIDs(node NodeSnapshot, role string, nodes map[NodeID]NodeSnapshot) []SymbolID {
	result := nodeSymbolIDs(node)
	childID := childByRole(node, role)
	if childID == "" {
		return result
	}
	child, ok := nodes[childID]
	if !ok {
		return result
	}
	seen := make(map[SymbolID]struct{}, len(result)+2)
	for _, symbol := range result {
		seen[symbol] = struct{}{}
	}
	for _, symbol := range nodeSymbolIDs(child) {
		if _, duplicate := seen[symbol]; duplicate {
			continue
		}
		seen[symbol] = struct{}{}
		result = append(result, symbol)
	}
	return result
}
