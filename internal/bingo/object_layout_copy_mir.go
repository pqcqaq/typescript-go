package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const ObjectLayoutCopyMIRSchemaVersion uint32 = 1
const ObjectLayoutCopyBoundSchemaVersion uint32 = 1
const maxObjectLayoutCopyMIRBytes = 3 << 20
const maxObjectLayoutCopyBoundBytes = 4 << 20

type ObjectLayoutCopyMIRInstruction struct {
	ID             ValueID   `json:"id"`
	Operation      string    `json:"operation"`
	Operands       []ValueID `json:"operands"`
	Representation string    `json:"representation"`
	PropertyKey    string    `json:"propertyKey,omitempty"`
	FieldOffset    uint32    `json:"fieldOffset,omitempty"`
	RelationPath   []string  `json:"relationPath"`
	Effects        []Effect  `json:"effects"`
	MaySafepoint   bool      `json:"maySafepoint"`
}

type ObjectLayoutCopyMIRArtifact struct {
	SchemaVersion  uint32                           `json:"schemaVersion"`
	HIRHash        string                           `json:"hirHash"`
	HIR            ObjectLayoutCopyHIRArtifact      `json:"hir"`
	TargetTriple   string                           `json:"targetTriple"`
	DataLayoutHash string                           `json:"dataLayoutHash"`
	Instructions   []ObjectLayoutCopyMIRInstruction `json:"instructions"`
	ReturnValueID  ValueID                          `json:"returnValueId"`
	ContentHash    string                           `json:"contentHash"`
}

type ObjectLayoutCopyBoundArtifact struct {
	SchemaVersion     uint32                      `json:"schemaVersion"`
	MIRHash           string                      `json:"mirHash"`
	MIR               ObjectLayoutCopyMIRArtifact `json:"mir"`
	TargetContextHash string                      `json:"targetContextHash"`
	CatalogHash       string                      `json:"catalogHash"`
	Bindings          []BoundCapability           `json:"bindings"`
	ContentHash       string                      `json:"contentHash"`
}

func LowerObjectLayoutCopyMIR(hir ObjectLayoutCopyHIRArtifact) (ObjectLayoutCopyMIRArtifact, error) {
	module := ObjectLayoutCopyMIRArtifact{SchemaVersion: ObjectLayoutCopyMIRSchemaVersion, HIRHash: hir.ContentHash, HIR: hir, TargetTriple: hir.Copy.TargetLayout.Target.Triple, DataLayoutHash: hir.Copy.TargetLayout.Target.DataLayoutHash}
	instructions, returnValue, err := deriveObjectLayoutCopyMIR(module)
	if err != nil {
		return ObjectLayoutCopyMIRArtifact{}, err
	}
	module.Instructions, module.ReturnValueID = instructions, returnValue
	_, hash, err := CanonicalObjectLayoutCopyMIR(module)
	module.ContentHash = hash
	return module, err
}

func CanonicalObjectLayoutCopyMIR(module ObjectLayoutCopyMIRArtifact) ([]byte, string, error) {
	module.ContentHash = ""
	if module.SchemaVersion != ObjectLayoutCopyMIRSchemaVersion || module.HIRHash != module.HIR.ContentHash {
		return nil, "", fmt.Errorf("invalid object layout copy MIR header")
	}
	wanted, returnValue, err := deriveObjectLayoutCopyMIR(module)
	if err != nil {
		return nil, "", err
	}
	target := module.HIR.Copy.TargetLayout.Target
	if module.TargetTriple != target.Triple || module.DataLayoutHash != target.DataLayoutHash || module.ReturnValueID != returnValue || !equalObjectLayoutCopyMIRInstructions(module.Instructions, wanted) {
		return nil, "", fmt.Errorf("object layout copy MIR does not match canonical lowering")
	}
	encoded, err := jsonx.Marshal(module)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	module.ContentHash = hash
	encoded, err = jsonx.Marshal(module)
	return encoded, hash, err
}

func VerifyCanonicalObjectLayoutCopyMIR(module ObjectLayoutCopyMIRArtifact) error {
	claimed := module.ContentHash
	_, wanted, err := CanonicalObjectLayoutCopyMIR(module)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != wanted {
		return fmt.Errorf("object layout copy MIR content hash mismatch")
	}
	return nil
}

func DecodeObjectLayoutCopyMIR(data []byte) (*ObjectLayoutCopyMIRArtifact, error) {
	if len(data) > maxObjectLayoutCopyMIRBytes {
		return nil, fmt.Errorf("object layout copy MIR exceeds %d bytes", maxObjectLayoutCopyMIRBytes)
	}
	var module ObjectLayoutCopyMIRArtifact
	if err := jsonx.Unmarshal(data, &module, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode object layout copy MIR: %w", err)
	}
	if err := VerifyCanonicalObjectLayoutCopyMIR(module); err != nil {
		return nil, err
	}
	return &module, nil
}

func deriveObjectLayoutCopyMIR(module ObjectLayoutCopyMIRArtifact) ([]ObjectLayoutCopyMIRInstruction, ValueID, error) {
	if err := VerifyCanonicalObjectLayoutCopyHIRArtifact(module.HIR); err != nil {
		return nil, 0, err
	}
	instructions := []ObjectLayoutCopyMIRInstruction{{ID: 2, Operation: "gc.alloc.target", Operands: []ValueID{}, Representation: "gc-ref", RelationPath: []string{}, Effects: []Effect{EffectAllocate}, MaySafepoint: true}}
	for index, mapping := range module.HIR.Copy.Mappings {
		loadID := ValueID(3 + index*2)
		instructions = append(instructions,
			ObjectLayoutCopyMIRInstruction{ID: loadID, Operation: "field.load.source", Operands: []ValueID{1}, Representation: mapping.SourceRepresentation, PropertyKey: mapping.PropertyKey, FieldOffset: mapping.SourceFieldOffset, RelationPath: slices.Clone(mapping.ReadRelationPath), Effects: []Effect{EffectRead}},
			ObjectLayoutCopyMIRInstruction{ID: loadID + 1, Operation: "field.store.target", Operands: []ValueID{2, loadID}, Representation: mapping.TargetRepresentation, PropertyKey: mapping.PropertyKey, FieldOffset: mapping.TargetFieldOffset, RelationPath: slices.Clone(mapping.ReadRelationPath), Effects: []Effect{EffectWrite}},
		)
	}
	return instructions, 2, nil
}

func equalObjectLayoutCopyMIRInstructions(left, right []ObjectLayoutCopyMIRInstruction) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].ID != right[index].ID || left[index].Operation != right[index].Operation || left[index].Representation != right[index].Representation || left[index].PropertyKey != right[index].PropertyKey || left[index].FieldOffset != right[index].FieldOffset || left[index].MaySafepoint != right[index].MaySafepoint || !slices.Equal(left[index].Operands, right[index].Operands) || !slices.Equal(left[index].RelationPath, right[index].RelationPath) || !slices.Equal(left[index].Effects, right[index].Effects) {
			return false
		}
	}
	return true
}

func ObjectLayoutCopyCapabilityRequirements() []RuntimeCapabilityID {
	return []RuntimeCapabilityID{"rt.gc.alloc", "rt.gc.frame.link", "rt.gc.frame.unlink", "rt.gc.root.publish", "rt.gc.root.reload", "rt.gc.root.store"}
}

func NewObjectLayoutCopyBoundArtifact(module ObjectLayoutCopyMIRArtifact, contextHash, catalogHash string, bindings []BoundCapability) (ObjectLayoutCopyBoundArtifact, error) {
	bound := ObjectLayoutCopyBoundArtifact{SchemaVersion: ObjectLayoutCopyBoundSchemaVersion, MIRHash: module.ContentHash, MIR: module, TargetContextHash: contextHash, CatalogHash: catalogHash, Bindings: slices.Clone(bindings)}
	_, hash, err := CanonicalObjectLayoutCopyBoundArtifact(bound)
	bound.ContentHash = hash
	return bound, err
}

func CanonicalObjectLayoutCopyBoundArtifact(bound ObjectLayoutCopyBoundArtifact) ([]byte, string, error) {
	bound.ContentHash = ""
	if bound.SchemaVersion != ObjectLayoutCopyBoundSchemaVersion || bound.MIRHash != bound.MIR.ContentHash || !validSHA256Hex(bound.TargetContextHash) || !validSHA256Hex(bound.CatalogHash) {
		return nil, "", fmt.Errorf("invalid object layout copy bound header")
	}
	if err := VerifyCanonicalObjectLayoutCopyMIR(bound.MIR); err != nil {
		return nil, "", err
	}
	requirements := ObjectLayoutCopyCapabilityRequirements()
	if len(bound.Bindings) != len(requirements) {
		return nil, "", fmt.Errorf("invalid object layout copy capability closure")
	}
	for index, requirement := range requirements {
		binding := bound.Bindings[index]
		if binding.LogicalName != requirement || binding.SymbolName == "" || !validSHA256Hex(binding.SignatureHash) {
			return nil, "", fmt.Errorf("invalid object layout copy capability binding %q", requirement)
		}
	}
	encoded, err := jsonx.Marshal(bound)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	bound.ContentHash = hash
	encoded, err = jsonx.Marshal(bound)
	return encoded, hash, err
}

func VerifyCanonicalObjectLayoutCopyBoundArtifact(bound ObjectLayoutCopyBoundArtifact) error {
	claimed := bound.ContentHash
	_, wanted, err := CanonicalObjectLayoutCopyBoundArtifact(bound)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != wanted {
		return fmt.Errorf("object layout copy bound content hash mismatch")
	}
	return nil
}

func DecodeObjectLayoutCopyBoundArtifact(data []byte) (*ObjectLayoutCopyBoundArtifact, error) {
	if len(data) > maxObjectLayoutCopyBoundBytes {
		return nil, fmt.Errorf("object layout copy bound exceeds %d bytes", maxObjectLayoutCopyBoundBytes)
	}
	var bound ObjectLayoutCopyBoundArtifact
	if err := jsonx.Unmarshal(data, &bound, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode object layout copy bound: %w", err)
	}
	if err := VerifyCanonicalObjectLayoutCopyBoundArtifact(bound); err != nil {
		return nil, err
	}
	return &bound, nil
}
