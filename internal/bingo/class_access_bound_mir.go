package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const ClassAccessBoundMIRSchemaVersion uint32 = 1
const maxClassAccessBoundMIRBytes = 768 << 10

type ClassAccessBoundMIR struct {
	SchemaVersion     uint32                    `json:"schemaVersion"`
	TargetContextHash string                    `json:"targetContextHash"`
	LayoutHash        string                    `json:"layoutHash"`
	Layout            ClassAccessLayoutContract `json:"layout"`
	GCSafety          GCSafetyPlan              `json:"gcSafety"`
	Closure           BoundCapabilityClosure    `json:"closure"`
	ContentHash       string                    `json:"contentHash"`
}

func NewClassAccessBoundMIR(layout ClassAccessLayoutContract, targetContextHash, catalogHash string, bindings []BoundCapability) (ClassAccessBoundMIR, error) {
	if err := VerifyCanonicalClassAccessLayout(layout); err != nil {
		return ClassAccessBoundMIR{}, err
	}
	if !validSHA256Hex(targetContextHash) || targetContextHash != layout.MIR.Target.TargetContextHash || !validSHA256Hex(catalogHash) {
		return ClassAccessBoundMIR{}, fmt.Errorf("invalid OBJ-003b bound target identity")
	}
	requirements := layout.MIR.HIR.LogicalCapabilityRequirements
	digest, err := LogicalCapabilityRequirementsDigest(requirements)
	if err != nil {
		return ClassAccessBoundMIR{}, err
	}
	closure := BoundCapabilityClosure{SchemaVersion: BoundCapabilitySchemaVersion, AvailableCapabilityCatalogHash: catalogHash, LogicalCapabilityRequirementsDigest: digest, Bindings: slices.Clone(bindings)}
	closure.ContentHash, err = boundCapabilityContentHash(closure)
	if err != nil {
		return ClassAccessBoundMIR{}, err
	}
	gc, err := buildVERT013bGCSafety(layout.MIR.HIR.ClassAccess.Classes[1].InstanceTypeKey, layout.Derived.ContentHash)
	if err != nil {
		return ClassAccessBoundMIR{}, err
	}
	bound := ClassAccessBoundMIR{SchemaVersion: ClassAccessBoundMIRSchemaVersion, TargetContextHash: targetContextHash, LayoutHash: layout.ContentHash, Layout: layout, GCSafety: gc, Closure: closure}
	_, hash, err := CanonicalClassAccessBoundMIR(bound)
	if err != nil {
		return ClassAccessBoundMIR{}, err
	}
	bound.ContentHash = hash
	return bound, nil
}

func VerifyClassAccessBoundMIR(bound ClassAccessBoundMIR) error {
	if bound.SchemaVersion != ClassAccessBoundMIRSchemaVersion || !validSHA256Hex(bound.TargetContextHash) || bound.TargetContextHash != bound.Layout.MIR.Target.TargetContextHash || bound.LayoutHash != bound.Layout.ContentHash {
		return fmt.Errorf("invalid OBJ-003b bound MIR envelope")
	}
	if err := VerifyCanonicalClassAccessLayout(bound.Layout); err != nil {
		return err
	}
	wantGC, err := buildVERT013bGCSafety(bound.Layout.MIR.HIR.ClassAccess.Classes[1].InstanceTypeKey, bound.Layout.Derived.ContentHash)
	if err != nil {
		return err
	}
	left, _ := jsonx.Marshal(bound.GCSafety)
	right, _ := jsonx.Marshal(wantGC)
	if !slices.Equal(left, right) {
		return fmt.Errorf("OBJ-003b bound MIR GC safety mismatch")
	}
	requirements := bound.Layout.MIR.HIR.LogicalCapabilityRequirements
	digest, err := LogicalCapabilityRequirementsDigest(requirements)
	closure := bound.Closure
	if err != nil || closure.SchemaVersion != BoundCapabilitySchemaVersion || !validSHA256Hex(closure.AvailableCapabilityCatalogHash) || closure.LogicalCapabilityRequirementsDigest != digest || len(closure.Bindings) != len(requirements) {
		return fmt.Errorf("OBJ-003b bound capability closure mismatch")
	}
	for index, requirement := range requirements {
		binding := closure.Bindings[index]
		if binding.LogicalName != requirement || binding.SymbolName == "" || !validSHA256Hex(binding.SignatureHash) {
			return fmt.Errorf("OBJ-003b capability %q is not exactly bound", requirement)
		}
	}
	wantClosure, err := boundCapabilityContentHash(closure)
	if err != nil || closure.ContentHash != wantClosure {
		return fmt.Errorf("OBJ-003b bound capability hash mismatch")
	}
	return nil
}

func CanonicalClassAccessBoundMIR(bound ClassAccessBoundMIR) ([]byte, string, error) {
	bound.ContentHash = ""
	if err := VerifyClassAccessBoundMIR(bound); err != nil {
		return nil, "", err
	}
	encoded, err := jsonx.Marshal(bound)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	bound.ContentHash = hex.EncodeToString(digest[:])
	encoded, err = jsonx.Marshal(bound)
	return encoded, bound.ContentHash, err
}

func VerifyCanonicalClassAccessBoundMIR(bound ClassAccessBoundMIR) error {
	claimed := bound.ContentHash
	_, want, err := CanonicalClassAccessBoundMIR(bound)
	if err != nil || claimed == "" || claimed != want {
		return fmt.Errorf("OBJ-003b bound MIR content hash mismatch")
	}
	return nil
}

func DecodeClassAccessBoundMIR(data []byte) (*ClassAccessBoundMIR, error) {
	if len(data) > maxClassAccessBoundMIRBytes {
		return nil, fmt.Errorf("OBJ-003b bound MIR exceeds %d bytes", maxClassAccessBoundMIRBytes)
	}
	var bound ClassAccessBoundMIR
	if err := jsonx.Unmarshal(data, &bound, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, err
	}
	if err := VerifyCanonicalClassAccessBoundMIR(bound); err != nil {
		return nil, err
	}
	return &bound, nil
}
