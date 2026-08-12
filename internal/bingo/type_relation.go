package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const TypeRelationGraphSchemaVersion uint32 = 1
const maxTypeRelationGraphBytes = 256 << 10

type TypeRelationNode struct {
	TypeKey        string   `json:"typeKey"`
	DeclarationKey string   `json:"declarationKey"`
	ArgumentKeys   []string `json:"argumentKeys"`
}

type TypeRelationEdge struct {
	SubTypeKey   string `json:"subTypeKey"`
	SuperTypeKey string `json:"superTypeKey"`
	Path         string `json:"path"`
}

type TypeRelationGraph struct {
	SchemaVersion uint32             `json:"schemaVersion"`
	Nodes         []TypeRelationNode `json:"nodes"`
	Edges         []TypeRelationEdge `json:"edges"`
	ContentHash   string             `json:"contentHash"`
}

func BuildTypeRelationGraph(nodes []TypeRelationNode, edges []TypeRelationEdge) (TypeRelationGraph, error) {
	graph := TypeRelationGraph{SchemaVersion: TypeRelationGraphSchemaVersion, Nodes: slices.Clone(nodes), Edges: slices.Clone(edges)}
	slices.SortFunc(graph.Nodes, func(a, b TypeRelationNode) int { return strings.Compare(a.TypeKey, b.TypeKey) })
	slices.SortFunc(graph.Edges, func(a, b TypeRelationEdge) int {
		if result := strings.Compare(a.SubTypeKey, b.SubTypeKey); result != 0 {
			return result
		}
		if result := strings.Compare(a.SuperTypeKey, b.SuperTypeKey); result != 0 {
			return result
		}
		return strings.Compare(a.Path, b.Path)
	})
	_, hash, err := CanonicalTypeRelationGraph(graph)
	graph.ContentHash = hash
	return graph, err
}

func CanonicalTypeRelationGraph(graph TypeRelationGraph) ([]byte, string, error) {
	graph.ContentHash = ""
	if err := verifyTypeRelationGraphStructure(graph); err != nil {
		return nil, "", err
	}
	encoded, err := jsonx.Marshal(graph)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	graph.ContentHash = hash
	encoded, err = jsonx.Marshal(graph)
	return encoded, hash, err
}

func VerifyCanonicalTypeRelationGraph(graph TypeRelationGraph) error {
	claimed := graph.ContentHash
	_, want, err := CanonicalTypeRelationGraph(graph)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("type relation graph content hash mismatch")
	}
	return nil
}

func DecodeTypeRelationGraph(data []byte) (*TypeRelationGraph, error) {
	if len(data) > maxTypeRelationGraphBytes {
		return nil, fmt.Errorf("type relation graph exceeds %d bytes", maxTypeRelationGraphBytes)
	}
	var graph TypeRelationGraph
	if err := jsonx.Unmarshal(data, &graph, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode type relation graph: %w", err)
	}
	if err := VerifyCanonicalTypeRelationGraph(graph); err != nil {
		return nil, err
	}
	return &graph, nil
}

func FindTypeRelationPath(graph TypeRelationGraph, subTypeKey, superTypeKey string) ([]string, error) {
	if err := VerifyCanonicalTypeRelationGraph(graph); err != nil {
		return nil, err
	}
	nodes := make(map[string]struct{}, len(graph.Nodes))
	for _, node := range graph.Nodes {
		nodes[node.TypeKey] = struct{}{}
	}
	if _, ok := nodes[subTypeKey]; !ok {
		return nil, fmt.Errorf("unknown subtype key %q", subTypeKey)
	}
	if _, ok := nodes[superTypeKey]; !ok {
		return nil, fmt.Errorf("unknown supertype key %q", superTypeKey)
	}
	if subTypeKey == superTypeKey {
		return []string{subTypeKey}, nil
	}
	adj := make(map[string][]string)
	for _, edge := range graph.Edges {
		adj[edge.SubTypeKey] = append(adj[edge.SubTypeKey], edge.SuperTypeKey)
	}
	queue := [][]string{{subTypeKey}}
	seen := map[string]bool{subTypeKey: true}
	for len(queue) != 0 {
		path := queue[0]
		queue = queue[1:]
		for _, next := range adj[path[len(path)-1]] {
			if seen[next] {
				continue
			}
			seen[next] = true
			candidate := append(slices.Clone(path), next)
			if next == superTypeKey {
				return candidate, nil
			}
			queue = append(queue, candidate)
		}
	}
	return nil, fmt.Errorf("type %q is not a subtype of %q", subTypeKey, superTypeKey)
}

func verifyTypeRelationGraphStructure(graph TypeRelationGraph) error {
	if graph.SchemaVersion != TypeRelationGraphSchemaVersion || len(graph.Nodes) == 0 {
		return fmt.Errorf("invalid type relation graph header")
	}
	nodes := make(map[string]struct{}, len(graph.Nodes))
	previous := ""
	for i, node := range graph.Nodes {
		if strings.TrimSpace(node.TypeKey) == "" || strings.TrimSpace(node.DeclarationKey) == "" || i != 0 && node.TypeKey <= previous {
			return fmt.Errorf("type relation nodes are invalid or not canonical")
		}
		for _, key := range node.ArgumentKeys {
			if strings.TrimSpace(key) == "" {
				return fmt.Errorf("type relation node has empty argument key")
			}
		}
		nodes[node.TypeKey] = struct{}{}
		previous = node.TypeKey
	}
	previous = ""
	for _, edge := range graph.Edges {
		key := edge.SubTypeKey + "\x00" + edge.SuperTypeKey + "\x00" + edge.Path
		if _, ok := nodes[edge.SubTypeKey]; !ok {
			return fmt.Errorf("type relation edge has unknown subtype")
		}
		if _, ok := nodes[edge.SuperTypeKey]; !ok {
			return fmt.Errorf("type relation edge has unknown supertype")
		}
		if edge.SubTypeKey == edge.SuperTypeKey || strings.TrimSpace(edge.Path) == "" || key <= previous {
			return fmt.Errorf("type relation edges are invalid or not canonical")
		}
		previous = key
	}
	return nil
}
