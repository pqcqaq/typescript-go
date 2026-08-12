package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const HIRVarianceConversionSchemaVersion uint32 = 1
const maxHIRVarianceConversionBytes = 768 << 10

type HIRVarianceParameterProof struct {
	ParameterID  uint32           `json:"parameterId"`
	Polarity     VariancePolarity `json:"polarity"`
	RelationPath []string         `json:"relationPath"`
}

type HIRVarianceConversionProof struct {
	SchemaVersion  uint32                      `json:"schemaVersion"`
	HIRValueID     uint32                      `json:"hirValueId"`
	DeclarationKey string                      `json:"declarationKey"`
	SourceTypeKey  string                      `json:"sourceTypeKey"`
	TargetTypeKey  string                      `json:"targetTypeKey"`
	VarianceGraph  VarianceGraphContract       `json:"varianceGraph"`
	RelationGraph  TypeRelationGraph           `json:"relationGraph"`
	SourceLayout   ObjectLayoutContract        `json:"sourceLayout"`
	TargetLayout   ObjectLayoutContract        `json:"targetLayout"`
	Parameters     []HIRVarianceParameterProof `json:"parameters"`
	DirectABIReuse bool                        `json:"directAbiReuse"`
	Reason         string                      `json:"reason"`
	ContentHash    string                      `json:"contentHash"`
}

func BuildHIRVarianceConversionProof(valueID uint32, declarationKey, sourceTypeKey, targetTypeKey string, variance VarianceGraphContract, relations TypeRelationGraph, sourceLayout, targetLayout ObjectLayoutContract) (HIRVarianceConversionProof, error) {
	proof := HIRVarianceConversionProof{SchemaVersion: HIRVarianceConversionSchemaVersion, HIRValueID: valueID, DeclarationKey: declarationKey, SourceTypeKey: sourceTypeKey, TargetTypeKey: targetTypeKey, VarianceGraph: variance, RelationGraph: relations, SourceLayout: sourceLayout, TargetLayout: targetLayout, DirectABIReuse: true, Reason: "variance.direct_hir_reuse"}
	parameters, err := deriveHIRVarianceParameterProofs(proof)
	if err != nil {
		return HIRVarianceConversionProof{}, err
	}
	proof.Parameters = parameters
	_, hash, err := CanonicalHIRVarianceConversionProof(proof)
	proof.ContentHash = hash
	return proof, err
}

func CanonicalHIRVarianceConversionProof(proof HIRVarianceConversionProof) ([]byte, string, error) {
	proof.ContentHash = ""
	if proof.SchemaVersion != HIRVarianceConversionSchemaVersion || proof.HIRValueID == 0 || strings.TrimSpace(proof.DeclarationKey) == "" || !proof.DirectABIReuse || proof.Reason != "variance.direct_hir_reuse" {
		return nil, "", fmt.Errorf("invalid HIR variance conversion header")
	}
	want, err := deriveHIRVarianceParameterProofs(proof)
	if err != nil {
		return nil, "", err
	}
	if !equalHIRVarianceParameterProofs(proof.Parameters, want) {
		return nil, "", fmt.Errorf("HIR variance parameter proofs do not match canonical admission")
	}
	encoded, err := jsonx.Marshal(proof)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	proof.ContentHash = hash
	encoded, err = jsonx.Marshal(proof)
	return encoded, hash, err
}

func VerifyCanonicalHIRVarianceConversionProof(proof HIRVarianceConversionProof) error {
	claimed := proof.ContentHash
	_, want, err := CanonicalHIRVarianceConversionProof(proof)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("HIR variance conversion content hash mismatch")
	}
	return nil
}

func DecodeHIRVarianceConversionProof(data []byte) (*HIRVarianceConversionProof, error) {
	if len(data) > maxHIRVarianceConversionBytes {
		return nil, fmt.Errorf("HIR variance conversion exceeds %d bytes", maxHIRVarianceConversionBytes)
	}
	var proof HIRVarianceConversionProof
	if err := jsonx.Unmarshal(data, &proof, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode HIR variance conversion: %w", err)
	}
	if err := VerifyCanonicalHIRVarianceConversionProof(proof); err != nil {
		return nil, err
	}
	return &proof, nil
}

func deriveHIRVarianceParameterProofs(proof HIRVarianceConversionProof) ([]HIRVarianceParameterProof, error) {
	if err := VerifyCanonicalVarianceGraph(proof.VarianceGraph); err != nil {
		return nil, fmt.Errorf("variance graph: %w", err)
	}
	if err := VerifyCanonicalTypeRelationGraph(proof.RelationGraph); err != nil {
		return nil, fmt.Errorf("type relation graph: %w", err)
	}
	if proof.SourceLayout.TypeKey != proof.SourceTypeKey || proof.TargetLayout.TypeKey != proof.TargetTypeKey {
		return nil, fmt.Errorf("conversion layout type binding mismatch")
	}
	if err := VerifyObjectLayoutMutableAlias(proof.SourceLayout, proof.TargetLayout); err != nil {
		return nil, err
	}
	source, ok := findTypeRelationNode(proof.RelationGraph, proof.SourceTypeKey)
	if !ok {
		return nil, fmt.Errorf("conversion source type is absent from relation graph")
	}
	target, ok := findTypeRelationNode(proof.RelationGraph, proof.TargetTypeKey)
	if !ok {
		return nil, fmt.Errorf("conversion target type is absent from relation graph")
	}
	if source.DeclarationKey != proof.DeclarationKey || target.DeclarationKey != proof.DeclarationKey || len(source.ArgumentKeys) != len(target.ArgumentKeys) {
		return nil, fmt.Errorf("conversion generic declaration binding mismatch")
	}
	contractIndex := -1
	for i, contract := range proof.VarianceGraph.Contracts {
		if contract.DeclarationKey == proof.DeclarationKey {
			contractIndex = i
			break
		}
	}
	if contractIndex < 0 {
		return nil, fmt.Errorf("conversion declaration is absent from variance graph")
	}
	contract := proof.VarianceGraph.Contracts[contractIndex]
	if len(source.ArgumentKeys) != len(contract.Parameters) {
		return nil, fmt.Errorf("conversion type argument count mismatch")
	}
	result := make([]HIRVarianceParameterProof, len(contract.Parameters))
	for i, parameter := range contract.Parameters {
		graphProof := proof.VarianceGraph.Proofs[varianceNodeOffset(proof.VarianceGraph.Contracts, contractIndex)+i]
		row := HIRVarianceParameterProof{ParameterID: parameter.ID, Polarity: graphProof.Inferred}
		from, to := source.ArgumentKeys[i], target.ArgumentKeys[i]
		if from == to {
			row.RelationPath = []string{from}
			result[i] = row
			continue
		}
		if !contract.Proofs[i].DirectABIReuse {
			return nil, fmt.Errorf("parameter %d has no declaration-level direct ABI admission", parameter.ID)
		}
		switch graphProof.Inferred {
		case VariancePositive:
			row.RelationPath, ok = relationPath(proof.RelationGraph, from, to)
		case VarianceNegative:
			row.RelationPath, ok = relationPath(proof.RelationGraph, to, from)
		default:
			return nil, fmt.Errorf("parameter %d polarity %q prohibits direct ABI reuse", parameter.ID, graphProof.Inferred)
		}
		if !ok {
			return nil, fmt.Errorf("parameter %d type arguments do not satisfy %s relation", parameter.ID, graphProof.Inferred)
		}
		result[i] = row
	}
	return result, nil
}

func findTypeRelationNode(graph TypeRelationGraph, key string) (TypeRelationNode, bool) {
	i, ok := slices.BinarySearchFunc(graph.Nodes, key, func(node TypeRelationNode, key string) int { return strings.Compare(node.TypeKey, key) })
	if !ok {
		return TypeRelationNode{}, false
	}
	return graph.Nodes[i], true
}
func relationPath(graph TypeRelationGraph, from, to string) ([]string, bool) {
	path, err := FindTypeRelationPath(graph, from, to)
	return path, err == nil
}
func varianceNodeOffset(contracts []VarianceContract, index int) int {
	offset := 0
	for i := 0; i < index; i++ {
		offset += len(contracts[i].Parameters)
	}
	return offset
}
func equalHIRVarianceParameterProofs(a, b []HIRVarianceParameterProof) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ParameterID != b[i].ParameterID || a[i].Polarity != b[i].Polarity || !slices.Equal(a[i].RelationPath, b[i].RelationPath) {
			return false
		}
	}
	return true
}
