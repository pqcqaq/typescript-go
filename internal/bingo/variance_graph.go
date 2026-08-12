package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const VarianceGraphSchemaVersion uint32 = 1
const maxVarianceGraphBytes = 256 << 10

type VarianceTransform string

const (
	VarianceTransformPositive VarianceTransform = "positive"
	VarianceTransformNegative VarianceTransform = "negative"
	VarianceTransformBoth     VarianceTransform = "both"
	VarianceTransformUnknown  VarianceTransform = "unknown"
)

type VarianceGraphNode struct {
	ID            uint32 `json:"id"`
	ContractIndex uint32 `json:"contractIndex"`
	ParameterID   uint32 `json:"parameterId"`
}

type VarianceDependencyEdge struct {
	ID               uint32            `json:"id"`
	OwnerNodeID      uint32            `json:"ownerNodeId"`
	DependencyNodeID uint32            `json:"dependencyNodeId"`
	Transform        VarianceTransform `json:"transform"`
	Path             string            `json:"path"`
}

type VarianceGraphProof struct {
	NodeID   uint32           `json:"nodeId"`
	SCCID    uint32           `json:"sccId"`
	Inferred VariancePolarity `json:"inferred"`
}

type VarianceGraphContract struct {
	SchemaVersion uint32                   `json:"schemaVersion"`
	Contracts     []VarianceContract       `json:"contracts"`
	Nodes         []VarianceGraphNode      `json:"nodes"`
	Edges         []VarianceDependencyEdge `json:"edges"`
	Proofs        []VarianceGraphProof     `json:"proofs"`
	ContentHash   string                   `json:"contentHash"`
}

func BuildVarianceGraph(contracts []VarianceContract, edges []VarianceDependencyEdge) (VarianceGraphContract, error) {
	graph := VarianceGraphContract{SchemaVersion: VarianceGraphSchemaVersion, Contracts: slices.Clone(contracts), Edges: slices.Clone(edges)}
	graph.Nodes = expectedVarianceGraphNodes(graph.Contracts)
	proofs, err := deriveVarianceGraphProofs(graph)
	if err != nil {
		return VarianceGraphContract{}, err
	}
	graph.Proofs = proofs
	_, hash, err := CanonicalVarianceGraph(graph)
	graph.ContentHash = hash
	return graph, err
}

func CanonicalVarianceGraph(graph VarianceGraphContract) ([]byte, string, error) {
	graph.ContentHash = ""
	if err := verifyVarianceGraphStructure(graph); err != nil {
		return nil, "", err
	}
	wantProofs, err := deriveVarianceGraphProofs(graph)
	if err != nil {
		return nil, "", err
	}
	if !slices.Equal(graph.Proofs, wantProofs) {
		return nil, "", fmt.Errorf("variance graph proofs do not match canonical fixed point")
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

func VerifyCanonicalVarianceGraph(graph VarianceGraphContract) error {
	claimed := graph.ContentHash
	_, want, err := CanonicalVarianceGraph(graph)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("variance graph content hash mismatch")
	}
	return nil
}

func DecodeVarianceGraph(data []byte) (*VarianceGraphContract, error) {
	if len(data) > maxVarianceGraphBytes {
		return nil, fmt.Errorf("variance graph exceeds %d bytes", maxVarianceGraphBytes)
	}
	var graph VarianceGraphContract
	if err := jsonx.Unmarshal(data, &graph, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode variance graph: %w", err)
	}
	if err := VerifyCanonicalVarianceGraph(graph); err != nil {
		return nil, err
	}
	return &graph, nil
}

func verifyVarianceGraphStructure(graph VarianceGraphContract) error {
	if graph.SchemaVersion != VarianceGraphSchemaVersion || len(graph.Contracts) == 0 {
		return fmt.Errorf("invalid variance graph header")
	}
	previousKey := ""
	for index, contract := range graph.Contracts {
		if err := VerifyCanonicalVarianceContract(contract); err != nil {
			return fmt.Errorf("variance graph contract %d: %w", index+1, err)
		}
		if index != 0 && contract.DeclarationKey <= previousKey {
			return fmt.Errorf("variance graph contracts are duplicated or not canonical")
		}
		previousKey = contract.DeclarationKey
	}
	wantNodes := expectedVarianceGraphNodes(graph.Contracts)
	if !slices.Equal(graph.Nodes, wantNodes) {
		return fmt.Errorf("variance graph nodes do not match declaration parameters")
	}
	var previousOwner, previousDependency uint32
	previousPath := ""
	for index, edge := range graph.Edges {
		if edge.ID != uint32(index+1) || edge.OwnerNodeID == 0 || int(edge.OwnerNodeID) > len(graph.Nodes) || edge.DependencyNodeID == 0 || int(edge.DependencyNodeID) > len(graph.Nodes) || strings.TrimSpace(edge.Path) == "" || !validVarianceTransform(edge.Transform) {
			return fmt.Errorf("invalid variance dependency edge %d", index+1)
		}
		if index != 0 && (edge.OwnerNodeID < previousOwner || edge.OwnerNodeID == previousOwner && (edge.DependencyNodeID < previousDependency || edge.DependencyNodeID == previousDependency && edge.Path <= previousPath)) {
			return fmt.Errorf("variance dependency edges are duplicated or not canonical")
		}
		previousOwner, previousDependency, previousPath = edge.OwnerNodeID, edge.DependencyNodeID, edge.Path
	}
	if len(graph.Proofs) != len(graph.Nodes) {
		return fmt.Errorf("variance graph proof count mismatch")
	}
	return nil
}

func expectedVarianceGraphNodes(contracts []VarianceContract) []VarianceGraphNode {
	nodes := make([]VarianceGraphNode, 0)
	for contractIndex, contract := range contracts {
		for _, parameter := range contract.Parameters {
			nodes = append(nodes, VarianceGraphNode{ID: uint32(len(nodes) + 1), ContractIndex: uint32(contractIndex + 1), ParameterID: parameter.ID})
		}
	}
	return nodes
}

func deriveVarianceGraphProofs(graph VarianceGraphContract) ([]VarianceGraphProof, error) {
	if err := verifyVarianceGraphInputs(graph); err != nil {
		return nil, err
	}
	states := make([]VariancePolarity, len(graph.Nodes))
	for index, node := range graph.Nodes {
		states[index] = graph.Contracts[node.ContractIndex-1].Proofs[node.ParameterID-1].Inferred
	}
	maxChanges := len(states) * 4
	changes := 0
	for {
		changed := false
		for _, edge := range graph.Edges {
			propagated := applyVarianceTransform(edge.Transform, states[edge.DependencyNodeID-1])
			next := joinVariancePolarity(states[edge.OwnerNodeID-1], propagated)
			if next != states[edge.OwnerNodeID-1] {
				states[edge.OwnerNodeID-1] = next
				changes++
				changed = true
				if changes > maxChanges {
					return nil, fmt.Errorf("variance graph fixed point exceeded monotonic update budget")
				}
			}
		}
		if !changed {
			break
		}
	}
	sccIDs := varianceGraphSCCIDs(len(graph.Nodes), graph.Edges)
	proofs := make([]VarianceGraphProof, len(graph.Nodes))
	for index, node := range graph.Nodes {
		proofs[index] = VarianceGraphProof{NodeID: node.ID, SCCID: sccIDs[index], Inferred: states[index]}
	}
	return proofs, nil
}

func verifyVarianceGraphInputs(graph VarianceGraphContract) error {
	if graph.SchemaVersion != VarianceGraphSchemaVersion || len(graph.Contracts) == 0 {
		return fmt.Errorf("invalid variance graph input")
	}
	for _, contract := range graph.Contracts {
		if err := VerifyCanonicalVarianceContract(contract); err != nil {
			return err
		}
	}
	if !slices.Equal(graph.Nodes, expectedVarianceGraphNodes(graph.Contracts)) {
		return fmt.Errorf("variance graph node binding mismatch")
	}
	for _, edge := range graph.Edges {
		if edge.OwnerNodeID == 0 || int(edge.OwnerNodeID) > len(graph.Nodes) || edge.DependencyNodeID == 0 || int(edge.DependencyNodeID) > len(graph.Nodes) || !validVarianceTransform(edge.Transform) {
			return fmt.Errorf("invalid variance graph edge binding")
		}
	}
	return nil
}

func applyVarianceTransform(transform VarianceTransform, value VariancePolarity) VariancePolarity {
	switch transform {
	case VarianceTransformPositive:
		return value
	case VarianceTransformNegative:
		switch value {
		case VariancePositive:
			return VarianceNegative
		case VarianceNegative:
			return VariancePositive
		default:
			return value
		}
	case VarianceTransformBoth:
		if value == VarianceUnused || value == VarianceUnknown {
			return value
		}
		return VarianceBoth
	case VarianceTransformUnknown:
		return VarianceUnknown
	default:
		return VarianceUnknown
	}
}

func validVarianceTransform(transform VarianceTransform) bool {
	return transform == VarianceTransformPositive || transform == VarianceTransformNegative || transform == VarianceTransformBoth || transform == VarianceTransformUnknown
}

func varianceGraphSCCIDs(nodeCount int, edges []VarianceDependencyEdge) []uint32 {
	adjacency := make([][]int, nodeCount)
	for _, edge := range edges {
		adjacency[edge.OwnerNodeID-1] = append(adjacency[edge.OwnerNodeID-1], int(edge.DependencyNodeID-1))
	}
	indices := make([]int, nodeCount)
	low := make([]int, nodeCount)
	for index := range indices {
		indices[index] = -1
	}
	onStack := make([]bool, nodeCount)
	stack := make([]int, 0, nodeCount)
	components := make([][]int, 0)
	nextIndex := 0
	var visit func(int)
	visit = func(node int) {
		indices[node], low[node] = nextIndex, nextIndex
		nextIndex++
		stack = append(stack, node)
		onStack[node] = true
		for _, dependency := range adjacency[node] {
			if indices[dependency] == -1 {
				visit(dependency)
				low[node] = min(low[node], low[dependency])
			} else if onStack[dependency] {
				low[node] = min(low[node], indices[dependency])
			}
		}
		if low[node] != indices[node] {
			return
		}
		component := make([]int, 0)
		for {
			last := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[last] = false
			component = append(component, last)
			if last == node {
				break
			}
		}
		slices.Sort(component)
		components = append(components, component)
	}
	for node := 0; node < nodeCount; node++ {
		if indices[node] == -1 {
			visit(node)
		}
	}
	slices.SortFunc(components, func(left, right []int) int { return left[0] - right[0] })
	result := make([]uint32, nodeCount)
	for componentIndex, component := range components {
		for _, node := range component {
			result[node] = uint32(componentIndex + 1)
		}
	}
	return result
}
