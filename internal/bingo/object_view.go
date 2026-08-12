package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const ObjectViewSchemaVersion uint32 = 1
const ObjectViewHIRGateSchemaVersion uint32 = 1
const maxObjectViewBytes = 768 << 10
const maxObjectViewHIRGateBytes = 2 << 20
const maxObjectViewOperationBytes = 512 << 10

type ObjectViewPropertyMapping struct {
	TargetPropertyKey string             `json:"targetPropertyKey"`
	SourcePropertyKey string             `json:"sourcePropertyKey"`
	Kind              ObjectPropertyKind `json:"kind"`
	ReadRelationPath  []string           `json:"readRelationPath"`
	SourceFieldOffset uint32             `json:"sourceFieldOffset,omitempty"`
	SourcePresenceBit int32              `json:"sourcePresenceBit"`
}

type ObjectViewProof struct {
	SchemaVersion     uint32                      `json:"schemaVersion"`
	Source            ObjectSemanticContract      `json:"source"`
	Target            ObjectSemanticContract      `json:"target"`
	Relations         TypeRelationGraph           `json:"relations"`
	SourceLayout      ObjectLayoutContract        `json:"sourceLayout"`
	TargetLayout      ObjectLayoutContract        `json:"targetLayout"`
	Mappings          []ObjectViewPropertyMapping `json:"mappings"`
	PreservesIdentity bool                        `json:"preservesIdentity"`
	ExposesWrites     bool                        `json:"exposesWrites"`
	ContentHash       string                      `json:"contentHash"`
}

type ObjectViewHIRGate struct {
	SchemaVersion uint32          `json:"schemaVersion"`
	FunctionID    FunctionID      `json:"functionId"`
	SourceValueID ValueID         `json:"sourceValueId"`
	HIR           HIRModule       `json:"hir"`
	View          ObjectViewProof `json:"view"`
	ContentHash   string          `json:"contentHash"`
}

// ObjectViewOperation is the explicit HIR-side operation contract consumed by
// the next lowering pass. It carries the proof instead of a filename/type
// switch, so a verifier can recompute the mapping independently.
type ObjectViewOperation struct {
	SchemaVersion uint32                      `json:"schemaVersion"`
	FunctionID    FunctionID                  `json:"functionId"`
	SourceValueID ValueID                     `json:"sourceValueId"`
	ResultValueID ValueID                     `json:"resultValueId"`
	TargetTypeKey string                      `json:"targetTypeKey"`
	View          ObjectViewProof             `json:"view"`
	Mappings      []ObjectViewPropertyMapping `json:"mappings"`
	ContentHash   string                      `json:"contentHash"`
}

func BuildObjectViewProof(source, target ObjectSemanticContract, relations TypeRelationGraph, sourceLayout, targetLayout ObjectLayoutContract) (ObjectViewProof, error) {
	proof := ObjectViewProof{SchemaVersion: ObjectViewSchemaVersion, Source: source, Target: target, Relations: relations, SourceLayout: sourceLayout, TargetLayout: targetLayout, PreservesIdentity: true}
	mappings, err := deriveObjectViewMappings(proof)
	if err != nil {
		return ObjectViewProof{}, err
	}
	proof.Mappings = mappings
	_, hash, err := CanonicalObjectViewProof(proof)
	proof.ContentHash = hash
	return proof, err
}

func CanonicalObjectViewProof(proof ObjectViewProof) ([]byte, string, error) {
	proof.ContentHash = ""
	if proof.SchemaVersion != ObjectViewSchemaVersion || !proof.PreservesIdentity || proof.ExposesWrites {
		return nil, "", fmt.Errorf("invalid ObjectView header")
	}
	want, err := deriveObjectViewMappings(proof)
	if err != nil {
		return nil, "", err
	}
	if !equalObjectViewMappings(proof.Mappings, want) {
		return nil, "", fmt.Errorf("ObjectView mappings do not match canonical plan")
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

func VerifyCanonicalObjectViewProof(proof ObjectViewProof) error {
	claimed := proof.ContentHash
	_, want, err := CanonicalObjectViewProof(proof)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("ObjectView content hash mismatch")
	}
	return nil
}
func DecodeObjectViewProof(data []byte) (*ObjectViewProof, error) {
	if len(data) > maxObjectViewBytes {
		return nil, fmt.Errorf("ObjectView exceeds %d bytes", maxObjectViewBytes)
	}
	var proof ObjectViewProof
	if err := jsonx.Unmarshal(data, &proof, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode ObjectView: %w", err)
	}
	if err := VerifyCanonicalObjectViewProof(proof); err != nil {
		return nil, err
	}
	return &proof, nil
}

func BuildObjectViewHIRGate(hir HIRModule, functionID FunctionID, valueID ValueID, view ObjectViewProof) (ObjectViewHIRGate, error) {
	gate := ObjectViewHIRGate{SchemaVersion: ObjectViewHIRGateSchemaVersion, FunctionID: functionID, SourceValueID: valueID, HIR: hir, View: view}
	_, hash, err := CanonicalObjectViewHIRGate(gate)
	gate.ContentHash = hash
	return gate, err
}
func CanonicalObjectViewHIRGate(gate ObjectViewHIRGate) ([]byte, string, error) {
	gate.ContentHash = ""
	if gate.SchemaVersion != ObjectViewHIRGateSchemaVersion || gate.FunctionID == 0 || gate.SourceValueID == 0 {
		return nil, "", fmt.Errorf("invalid ObjectView HIR gate header")
	}
	if err := verifyCanonicalKnownHIR(gate.HIR); err != nil {
		return nil, "", err
	}
	if err := VerifyCanonicalObjectViewProof(gate.View); err != nil {
		return nil, "", err
	}
	foundFunction, foundValue := false, false
	for _, function := range gate.HIR.Functions {
		if function.ID != gate.FunctionID {
			continue
		}
		foundFunction = true
		for _, block := range function.Blocks {
			for _, operation := range block.Operations {
				if operation.ID == gate.SourceValueID {
					if foundValue {
						return nil, "", fmt.Errorf("ObjectView HIR value is duplicated")
					}
					foundValue = true
					if operation.Type != TypeObject || operation.ObjectTypeKey != gate.View.Source.TypeKey {
						return nil, "", fmt.Errorf("ObjectView HIR source binding mismatch")
					}
				}
			}
		}
	}
	if !foundFunction || !foundValue {
		return nil, "", fmt.Errorf("ObjectView HIR function/value binding is missing")
	}
	encoded, err := jsonx.Marshal(gate)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	gate.ContentHash = hash
	encoded, err = jsonx.Marshal(gate)
	return encoded, hash, err
}
func VerifyCanonicalObjectViewHIRGate(gate ObjectViewHIRGate) error {
	claimed := gate.ContentHash
	_, want, err := CanonicalObjectViewHIRGate(gate)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("ObjectView HIR gate content hash mismatch")
	}
	return nil
}
func DecodeObjectViewHIRGate(data []byte) (*ObjectViewHIRGate, error) {
	if len(data) > maxObjectViewHIRGateBytes {
		return nil, fmt.Errorf("ObjectView HIR gate exceeds %d bytes", maxObjectViewHIRGateBytes)
	}
	var gate ObjectViewHIRGate
	if err := jsonx.Unmarshal(data, &gate, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode ObjectView HIR gate: %w", err)
	}
	if err := VerifyCanonicalObjectViewHIRGate(gate); err != nil {
		return nil, err
	}
	return &gate, nil
}

func BuildObjectViewOperation(gate ObjectViewHIRGate) (ObjectViewOperation, error) {
	if err := VerifyCanonicalObjectViewHIRGate(gate); err != nil {
		return ObjectViewOperation{}, err
	}
	resultID, err := nextObjectViewValueID(gate.HIR, gate.FunctionID)
	if err != nil {
		return ObjectViewOperation{}, err
	}
	operation := ObjectViewOperation{SchemaVersion: ObjectViewSchemaVersion, FunctionID: gate.FunctionID, SourceValueID: gate.SourceValueID, ResultValueID: resultID, TargetTypeKey: gate.View.Target.TypeKey, View: gate.View, Mappings: cloneObjectViewMappings(gate.View.Mappings)}
	_, hash, err := CanonicalObjectViewOperation(operation)
	operation.ContentHash = hash
	return operation, err
}

func CanonicalObjectViewOperation(operation ObjectViewOperation) ([]byte, string, error) {
	operation.ContentHash = ""
	if operation.SchemaVersion != ObjectViewSchemaVersion || operation.FunctionID == 0 || operation.SourceValueID == 0 || operation.ResultValueID == 0 || operation.ResultValueID == operation.SourceValueID {
		return nil, "", fmt.Errorf("invalid ObjectView operation header")
	}
	if err := VerifyCanonicalObjectViewProof(operation.View); err != nil {
		return nil, "", err
	}
	if operation.TargetTypeKey != operation.View.Target.TypeKey {
		return nil, "", fmt.Errorf("ObjectView operation target binding mismatch")
	}
	if !equalObjectViewMappings(operation.Mappings, operation.View.Mappings) {
		return nil, "", fmt.Errorf("ObjectView operation mappings differ from proof")
	}
	encoded, err := jsonx.Marshal(operation)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	operation.ContentHash = hash
	encoded, err = jsonx.Marshal(operation)
	return encoded, hash, err
}

func nextObjectViewValueID(module HIRModule, functionID FunctionID) (ValueID, error) {
	var maximum ValueID
	found := false
	for _, function := range module.Functions {
		if function.ID != functionID {
			continue
		}
		found = true
		for _, parameter := range function.Parameters {
			if parameter.Value > maximum {
				maximum = parameter.Value
			}
		}
		for _, block := range function.Blocks {
			for _, operation := range block.Operations {
				if operation.ID > maximum {
					maximum = operation.ID
				}
			}
		}
	}
	if !found || maximum == ^ValueID(0) {
		return 0, fmt.Errorf("ObjectView operation has no allocatable HIR value ID")
	}
	return maximum + 1, nil
}

func VerifyCanonicalObjectViewOperation(operation ObjectViewOperation) error {
	claimed := operation.ContentHash
	_, want, err := CanonicalObjectViewOperation(operation)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("ObjectView operation content hash mismatch")
	}
	return nil
}

func DecodeObjectViewOperation(data []byte) (*ObjectViewOperation, error) {
	if len(data) > maxObjectViewOperationBytes {
		return nil, fmt.Errorf("ObjectView operation exceeds %d bytes", maxObjectViewOperationBytes)
	}
	var operation ObjectViewOperation
	if err := jsonx.Unmarshal(data, &operation, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode ObjectView operation: %w", err)
	}
	if err := VerifyCanonicalObjectViewOperation(operation); err != nil {
		return nil, err
	}
	return &operation, nil
}

func deriveObjectViewMappings(proof ObjectViewProof) ([]ObjectViewPropertyMapping, error) {
	if err := VerifyCanonicalObjectSemanticContract(proof.Source); err != nil {
		return nil, fmt.Errorf("source ObjectView semantics: %w", err)
	}
	if err := VerifyCanonicalObjectSemanticContract(proof.Target); err != nil {
		return nil, fmt.Errorf("target ObjectView semantics: %w", err)
	}
	if err := VerifyCanonicalTypeRelationGraph(proof.Relations); err != nil {
		return nil, err
	}
	if proof.SourceLayout.TypeKey != proof.Source.TypeKey || proof.TargetLayout.TypeKey != proof.Target.TypeKey {
		return nil, fmt.Errorf("ObjectView semantic/layout type binding mismatch")
	}
	if proof.SourceLayout.Target != proof.TargetLayout.Target {
		return nil, fmt.Errorf("ObjectView layouts use different target contexts")
	}
	if err := verifyObjectLayoutContractHash(proof.SourceLayout); err != nil {
		return nil, err
	}
	if err := verifyObjectLayoutContractHash(proof.TargetLayout); err != nil {
		return nil, err
	}
	sourceProperties := objectPropertiesByKey(proof.Source.Properties)
	sourceLayout := objectLayoutPropertiesByKey(proof.SourceLayout.Properties)
	targetLayout := objectLayoutPropertiesByKey(proof.TargetLayout.Properties)
	result := make([]ObjectViewPropertyMapping, 0, len(proof.Target.Properties))
	for _, target := range proof.Target.Properties {
		if target.WriteTypeKey != "" {
			return nil, fmt.Errorf("ObjectView target property %q exposes writes", target.Key)
		}
		source, ok := sourceProperties[target.Key]
		if !ok {
			return nil, fmt.Errorf("%s", ObjectReasonPropertyMissing)
		}
		if !matchingPrivateIdentity(source, target) {
			return nil, fmt.Errorf("%s", ObjectReasonPrivateIdentityMismatch)
		}
		if source.Kind != target.Kind {
			return nil, fmt.Errorf("%s", ObjectReasonPropertyKindMismatch)
		}
		if !target.Optional && source.Optional {
			return nil, fmt.Errorf("%s", ObjectReasonPropertyMissing)
		}
		if source.ReadTypeKey == "" || target.ReadTypeKey == "" {
			return nil, fmt.Errorf("%s", ObjectReasonReadTypeUnproven)
		}
		path, err := FindTypeRelationPath(proof.Relations, source.ReadTypeKey, target.ReadTypeKey)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", ObjectReasonReadTypeUnproven, err)
		}
		sourcePhysical, ok := sourceLayout[target.Key]
		if !ok {
			return nil, fmt.Errorf("ObjectView source layout property %q is missing", target.Key)
		}
		targetPhysical, ok := targetLayout[target.Key]
		if !ok || sourcePhysical.Kind != targetPhysical.Kind {
			return nil, fmt.Errorf("ObjectView target layout property %q is missing or incompatible", target.Key)
		}
		result = append(result, ObjectViewPropertyMapping{TargetPropertyKey: target.Key, SourcePropertyKey: source.Key, Kind: source.Kind, ReadRelationPath: path, SourceFieldOffset: sourcePhysical.FieldOffset, SourcePresenceBit: sourcePhysical.PresenceBit})
	}
	return result, nil
}

func objectLayoutPropertiesByKey(properties []ObjectLayoutProperty) map[string]ObjectLayoutProperty {
	result := make(map[string]ObjectLayoutProperty, len(properties))
	for _, property := range properties {
		result[property.Key] = property
	}
	return result
}
func equalObjectViewMappings(left, right []ObjectViewPropertyMapping) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].TargetPropertyKey != right[i].TargetPropertyKey || left[i].SourcePropertyKey != right[i].SourcePropertyKey || left[i].Kind != right[i].Kind || left[i].SourceFieldOffset != right[i].SourceFieldOffset || left[i].SourcePresenceBit != right[i].SourcePresenceBit || !slices.Equal(left[i].ReadRelationPath, right[i].ReadRelationPath) {
			return false
		}
	}
	return true
}

func cloneObjectViewMappings(mappings []ObjectViewPropertyMapping) []ObjectViewPropertyMapping {
	result := slices.Clone(mappings)
	for index := range result {
		result[index].ReadRelationPath = slices.Clone(result[index].ReadRelationPath)
	}
	return result
}
