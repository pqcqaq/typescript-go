package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

// CheckedObjectCastSchemaVersion is the first explicit dynamic-boundary cast contract.
const CheckedObjectCastSchemaVersion uint32 = 1
const DynamicObjectBoundarySchemaVersion uint32 = 1
const CheckedObjectCastBoundSchemaVersion uint32 = 1

const CheckedObjectCastCapability RuntimeCapabilityID = "rt.object.shape_matches"
const CheckedObjectCastSymbol = "bingo_shape_matches_v1"

type DynamicObjectBoundaryArtifact struct {
	SchemaVersion uint32 `json:"schemaVersion"`
	Kind          string `json:"kind"`
	SourceID      string `json:"sourceId"`
	ContentHash   string `json:"contentHash"`
}

// CheckedObjectCastContract describes a runtime shape check without changing identity.
// It is deliberately narrower than TypeScript assertions: only required public data
// properties may be admitted, and the source must be an explicit dynamic boundary.
type CheckedObjectCastContract struct {
	SchemaVersion     uint32                        `json:"schemaVersion"`
	Boundary          DynamicObjectBoundaryArtifact `json:"boundary"`
	SourceTypeKey     string                        `json:"sourceTypeKey"`
	Target            ObjectSemanticContract        `json:"target"`
	TargetLayout      ObjectLayoutContract          `json:"targetLayout"`
	Properties        []string                      `json:"properties"`
	PreservesIdentity bool                          `json:"preservesIdentity"`
	ReadonlyResult    bool                          `json:"readonlyResult"`
	ContentHash       string                        `json:"contentHash"`
}

type CheckedObjectCastBoundContract struct {
	SchemaVersion                  uint32                    `json:"schemaVersion"`
	Cast                           CheckedObjectCastContract `json:"cast"`
	TargetContextHash              string                    `json:"targetContextHash"`
	AvailableCapabilityCatalogHash string                    `json:"availableCapabilityCatalogHash"`
	Binding                        BoundCapability           `json:"binding"`
	ContentHash                    string                    `json:"contentHash"`
}

const maxCheckedObjectCastBytes = 256 << 10
const maxCheckedObjectCastBoundBytes = 512 << 10

func CanonicalCheckedObjectCast(c CheckedObjectCastContract) ([]byte, string, error) {
	c.ContentHash = ""
	if err := verifyCheckedObjectCast(c); err != nil {
		return nil, "", err
	}
	b, err := jsonx.Marshal(c)
	if err != nil {
		return nil, "", err
	}
	d := sha256.Sum256(b)
	h := hex.EncodeToString(d[:])
	c.ContentHash = h
	b, err = jsonx.Marshal(c)
	return b, h, err
}

func VerifyCanonicalCheckedObjectCast(c CheckedObjectCastContract) error {
	claimed := c.ContentHash
	_, want, err := CanonicalCheckedObjectCast(c)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("checked object cast content hash mismatch")
	}
	return nil
}

func DecodeCheckedObjectCast(data []byte) (*CheckedObjectCastContract, error) {
	if len(data) > maxCheckedObjectCastBytes {
		return nil, fmt.Errorf("checked object cast exceeds %d bytes", maxCheckedObjectCastBytes)
	}
	var c CheckedObjectCastContract
	if err := jsonx.Unmarshal(data, &c, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode checked object cast: %w", err)
	}
	if err := VerifyCanonicalCheckedObjectCast(c); err != nil {
		return nil, err
	}
	return &c, nil
}

func CanonicalDynamicObjectBoundary(boundary DynamicObjectBoundaryArtifact) ([]byte, string, error) {
	boundary.ContentHash = ""
	if boundary.SchemaVersion != DynamicObjectBoundarySchemaVersion || (boundary.Kind != "ffi-import" && boundary.Kind != "dynamic-input") || boundary.SourceID == "" {
		return nil, "", fmt.Errorf("invalid dynamic object boundary")
	}
	b, err := jsonx.Marshal(boundary)
	if err != nil {
		return nil, "", err
	}
	d := sha256.Sum256(b)
	h := hex.EncodeToString(d[:])
	boundary.ContentHash = h
	b, err = jsonx.Marshal(boundary)
	return b, h, err
}

func VerifyCanonicalDynamicObjectBoundary(boundary DynamicObjectBoundaryArtifact) error {
	claimed := boundary.ContentHash
	_, want, err := CanonicalDynamicObjectBoundary(boundary)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("dynamic object boundary content hash mismatch")
	}
	return nil
}

func NewCheckedObjectCastBound(cast CheckedObjectCastContract, targetContextHash, catalogHash string, binding BoundCapability) (CheckedObjectCastBoundContract, error) {
	bound := CheckedObjectCastBoundContract{SchemaVersion: CheckedObjectCastBoundSchemaVersion, Cast: cast, TargetContextHash: targetContextHash, AvailableCapabilityCatalogHash: catalogHash, Binding: binding}
	_, hash, err := CanonicalCheckedObjectCastBound(bound)
	bound.ContentHash = hash
	return bound, err
}

func CanonicalCheckedObjectCastBound(bound CheckedObjectCastBoundContract) ([]byte, string, error) {
	bound.ContentHash = ""
	if bound.SchemaVersion != CheckedObjectCastBoundSchemaVersion || !validSHA256Hex(bound.TargetContextHash) || !validSHA256Hex(bound.AvailableCapabilityCatalogHash) {
		return nil, "", fmt.Errorf("invalid checked object cast bound header")
	}
	if err := VerifyCanonicalCheckedObjectCast(bound.Cast); err != nil {
		return nil, "", err
	}
	if bound.Binding.LogicalName != CheckedObjectCastCapability || bound.Binding.SymbolName != CheckedObjectCastSymbol || !validSHA256Hex(bound.Binding.SignatureHash) {
		return nil, "", fmt.Errorf("invalid checked object cast runtime binding")
	}
	b, err := jsonx.Marshal(bound)
	if err != nil {
		return nil, "", err
	}
	d := sha256.Sum256(b)
	h := hex.EncodeToString(d[:])
	bound.ContentHash = h
	b, err = jsonx.Marshal(bound)
	return b, h, err
}

func VerifyCanonicalCheckedObjectCastBound(bound CheckedObjectCastBoundContract) error {
	claimed := bound.ContentHash
	_, want, err := CanonicalCheckedObjectCastBound(bound)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("checked object cast bound content hash mismatch")
	}
	return nil
}

func DecodeCheckedObjectCastBound(data []byte) (*CheckedObjectCastBoundContract, error) {
	if len(data) > maxCheckedObjectCastBoundBytes {
		return nil, fmt.Errorf("checked object cast bound exceeds %d bytes", maxCheckedObjectCastBoundBytes)
	}
	var bound CheckedObjectCastBoundContract
	if err := jsonx.Unmarshal(data, &bound, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode checked object cast bound: %w", err)
	}
	if err := VerifyCanonicalCheckedObjectCastBound(bound); err != nil {
		return nil, err
	}
	return &bound, nil
}

func verifyCheckedObjectCast(c CheckedObjectCastContract) error {
	if c.SchemaVersion != CheckedObjectCastSchemaVersion || !validObjectSemanticTypeKey(c.SourceTypeKey) || !c.PreservesIdentity || !c.ReadonlyResult {
		return fmt.Errorf("invalid checked object cast header")
	}
	if err := VerifyCanonicalDynamicObjectBoundary(c.Boundary); err != nil {
		return err
	}
	if err := VerifyCanonicalObjectSemanticContract(c.Target); err != nil {
		return fmt.Errorf("checked object cast target: %w", err)
	}
	if err := VerifyObjectLayoutContractStructure(c.TargetLayout); err != nil {
		return fmt.Errorf("checked object cast target layout: %w", err)
	}
	claimedLayoutContentHash := c.TargetLayout.ContentHash
	_, wantLayoutContentHash, err := CanonicalObjectLayoutContract(c.TargetLayout)
	if err != nil || claimedLayoutContentHash == "" || claimedLayoutContentHash != wantLayoutContentHash {
		return fmt.Errorf("checked object cast target layout content hash mismatch")
	}
	if err := verifyObjectLayoutContractHash(c.TargetLayout); err != nil {
		return fmt.Errorf("checked object cast target layout: %w", err)
	}
	if c.Target.TypeKey != c.TargetLayout.TypeKey {
		return fmt.Errorf("checked object cast target/layout type mismatch")
	}
	if len(c.Properties) == 0 {
		return fmt.Errorf("checked object cast requires properties")
	}
	for i, p := range c.Properties {
		if p == "" || (i > 0 && c.Properties[i-1] >= p) {
			return fmt.Errorf("checked object cast properties are not strictly ordered")
		}
	}
	if len(c.Properties) != len(c.Target.Properties) || len(c.Properties) != len(c.TargetLayout.Properties) {
		return fmt.Errorf("checked object cast target property count mismatch")
	}
	for i, p := range c.Target.Properties {
		if c.Properties[i] != p.Key || p.Kind != ObjectPropertyData || p.Optional || p.Visibility != "public" || p.ReadTypeKey == "" || p.WriteTypeKey != "" {
			return fmt.Errorf("checked object cast admits only required public readonly data properties")
		}
		layout := c.TargetLayout.Properties[i]
		if layout.Key != p.Key || layout.Kind != ObjectPropertyData || layout.PresenceBit != -1 || layout.Representation == "" {
			return fmt.Errorf("checked object cast target layout property mismatch")
		}
	}
	return nil
}
