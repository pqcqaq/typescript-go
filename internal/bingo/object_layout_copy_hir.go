package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const ObjectLayoutCopyHIRSchemaVersion uint32 = 1
const maxObjectLayoutCopyHIRBytes = 2 << 20

type ObjectLayoutCopyHIROperation struct {
	ID                   ValueID   `json:"id"`
	Kind                 string    `json:"kind"`
	Operands             []ValueID `json:"operands"`
	PropertyKey          string    `json:"propertyKey,omitempty"`
	SourceFieldOffset    uint32    `json:"sourceFieldOffset,omitempty"`
	SourceRepresentation string    `json:"sourceRepresentation,omitempty"`
	TargetFieldOffset    uint32    `json:"targetFieldOffset,omitempty"`
	TargetRepresentation string    `json:"targetRepresentation,omitempty"`
	RelationPath         []string  `json:"relationPath"`
	Effects              []Effect  `json:"effects"`
}

type ObjectLayoutCopyHIRArtifact struct {
	SchemaVersion      uint32                         `json:"schemaVersion"`
	Copy               ObjectLayoutCopyContract       `json:"copy"`
	FunctionID         FunctionID                     `json:"functionId"`
	SourceValueID      ValueID                        `json:"sourceValueId"`
	Operations         []ObjectLayoutCopyHIROperation `json:"operations"`
	ReturnValueID      ValueID                        `json:"returnValueId"`
	RequiredCapability RuntimeCapabilityID            `json:"requiredCapability"`
	GCSafety           GCSafetyPlan                   `json:"gcSafety"`
	ContentHash        string                         `json:"contentHash"`
}

func BuildObjectLayoutCopyHIRArtifact(copyContract ObjectLayoutCopyContract) (ObjectLayoutCopyHIRArtifact, error) {
	artifact := ObjectLayoutCopyHIRArtifact{SchemaVersion: ObjectLayoutCopyHIRSchemaVersion, Copy: copyContract, FunctionID: 1, SourceValueID: 1, RequiredCapability: "rt.gc.alloc"}
	operations, returnValue, gc, err := deriveObjectLayoutCopyHIR(artifact)
	if err != nil {
		return ObjectLayoutCopyHIRArtifact{}, err
	}
	artifact.Operations, artifact.ReturnValueID, artifact.GCSafety = operations, returnValue, gc
	_, hash, err := CanonicalObjectLayoutCopyHIRArtifact(artifact)
	artifact.ContentHash = hash
	return artifact, err
}

func CanonicalObjectLayoutCopyHIRArtifact(artifact ObjectLayoutCopyHIRArtifact) ([]byte, string, error) {
	artifact.ContentHash = ""
	if artifact.SchemaVersion != ObjectLayoutCopyHIRSchemaVersion || artifact.FunctionID != 1 || artifact.SourceValueID != 1 || artifact.RequiredCapability != "rt.gc.alloc" {
		return nil, "", fmt.Errorf("invalid object layout copy HIR header")
	}
	wantedOperations, wantedReturn, wantedGC, err := deriveObjectLayoutCopyHIR(artifact)
	if err != nil {
		return nil, "", err
	}
	if artifact.ReturnValueID != wantedReturn || !equalObjectLayoutCopyHIROperations(artifact.Operations, wantedOperations) {
		return nil, "", fmt.Errorf("object layout copy HIR does not match canonical lowering")
	}
	leftGC, _ := jsonx.Marshal(artifact.GCSafety)
	rightGC, _ := jsonx.Marshal(wantedGC)
	if !slices.Equal(leftGC, rightGC) {
		return nil, "", fmt.Errorf("object layout copy HIR GC safety does not match canonical lowering")
	}
	encoded, err := jsonx.Marshal(artifact)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	artifact.ContentHash = hash
	encoded, err = jsonx.Marshal(artifact)
	return encoded, hash, err
}

func VerifyCanonicalObjectLayoutCopyHIRArtifact(artifact ObjectLayoutCopyHIRArtifact) error {
	claimed := artifact.ContentHash
	_, wanted, err := CanonicalObjectLayoutCopyHIRArtifact(artifact)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != wanted {
		return fmt.Errorf("object layout copy HIR content hash mismatch")
	}
	return nil
}

func DecodeObjectLayoutCopyHIRArtifact(data []byte) (*ObjectLayoutCopyHIRArtifact, error) {
	if len(data) > maxObjectLayoutCopyHIRBytes {
		return nil, fmt.Errorf("object layout copy HIR exceeds %d bytes", maxObjectLayoutCopyHIRBytes)
	}
	var artifact ObjectLayoutCopyHIRArtifact
	if err := jsonx.Unmarshal(data, &artifact, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode object layout copy HIR: %w", err)
	}
	if err := VerifyCanonicalObjectLayoutCopyHIRArtifact(artifact); err != nil {
		return nil, err
	}
	return &artifact, nil
}

func deriveObjectLayoutCopyHIR(artifact ObjectLayoutCopyHIRArtifact) ([]ObjectLayoutCopyHIROperation, ValueID, GCSafetyPlan, error) {
	if err := VerifyCanonicalObjectLayoutCopyContract(artifact.Copy); err != nil {
		return nil, 0, GCSafetyPlan{}, err
	}
	operations := []ObjectLayoutCopyHIROperation{{ID: 2, Kind: "object.copy.target.alloc", Operands: []ValueID{}, RelationPath: []string{}, Effects: []Effect{EffectAllocate}}}
	nextID := ValueID(3)
	for _, mapping := range artifact.Copy.Mappings {
		if mapping.SourceRepresentation != "f64" || mapping.TargetRepresentation != "f64" {
			return nil, 0, GCSafetyPlan{}, fmt.Errorf("object layout copy HIR currently requires f64 data mappings")
		}
		loadID := nextID
		operations = append(operations,
			ObjectLayoutCopyHIROperation{ID: loadID, Kind: "object.copy.source.load", Operands: []ValueID{artifact.SourceValueID}, PropertyKey: mapping.PropertyKey, SourceFieldOffset: mapping.SourceFieldOffset, SourceRepresentation: mapping.SourceRepresentation, RelationPath: slices.Clone(mapping.ReadRelationPath), Effects: []Effect{EffectRead}},
			ObjectLayoutCopyHIROperation{ID: loadID + 1, Kind: "object.copy.target.store", Operands: []ValueID{2, loadID}, PropertyKey: mapping.PropertyKey, TargetFieldOffset: mapping.TargetFieldOffset, TargetRepresentation: mapping.TargetRepresentation, RelationPath: slices.Clone(mapping.ReadRelationPath), Effects: []Effect{EffectWrite}},
		)
		nextID += 2
	}
	gc, err := buildObjectLayoutCopyGCSafety(artifact.Copy)
	if err != nil {
		return nil, 0, GCSafetyPlan{}, err
	}
	return operations, 2, gc, nil
}

func buildObjectLayoutCopyGCSafety(copyContract ObjectLayoutCopyContract) (GCSafetyPlan, error) {
	return FinalizeGCSafetyPlan(GCSafetyPlan{
		FunctionKey: copyContract.ContentHash,
		Slots:       []GCRootSlot{{ID: 1, TraceLayoutHash: copyContract.SourceLayout.ContentHash}},
		Blocks: []GCSafetyBlock{{ID: 1, Terminator: "return", Instructions: []GCInstruction{
			{ID: 1, Kind: GCOpFrameLink},
			{ID: 2, Kind: GCOpRefDef, Value: 1},
			{ID: 3, Kind: GCOpRootStore, Slot: 1, Value: 1},
			{ID: 4, Kind: GCOpRootPublish, ActiveSlots: []GCRootSlotID{1}},
			{ID: 5, Kind: GCOpSafepoint, SafepointKind: "allocation", MayAllocate: true},
			{ID: 6, Kind: GCOpRootReload, Slot: 1, Value: 1},
			{ID: 7, Kind: GCOpRefDef, Value: 2},
			{ID: 8, Kind: GCOpRefUse, Uses: []GCValueID{1}},
			{ID: 9, Kind: GCOpFrameUnlink},
		}}},
	})
}

func equalObjectLayoutCopyHIROperations(left, right []ObjectLayoutCopyHIROperation) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].ID != right[index].ID || left[index].Kind != right[index].Kind || left[index].PropertyKey != right[index].PropertyKey || left[index].SourceFieldOffset != right[index].SourceFieldOffset || left[index].SourceRepresentation != right[index].SourceRepresentation || left[index].TargetFieldOffset != right[index].TargetFieldOffset || left[index].TargetRepresentation != right[index].TargetRepresentation || !slices.Equal(left[index].Operands, right[index].Operands) || !slices.Equal(left[index].RelationPath, right[index].RelationPath) || !slices.Equal(left[index].Effects, right[index].Effects) {
			return false
		}
	}
	return true
}
