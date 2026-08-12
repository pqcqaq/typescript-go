package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const PropertyAccessAdmissionSchemaVersion uint32 = 1
const maxPropertyAccessAdmissionBytes = 256 << 10

type PropertyKeyDomain string

const (
	PropertyKeyDirect       PropertyKeyDomain = "direct"
	PropertyKeyLiteral      PropertyKeyDomain = "literal"
	PropertyKeyLiteralUnion PropertyKeyDomain = "literal-union"
	PropertyKeyUnknown      PropertyKeyDomain = "unknown"
)

type PropertyAccessProfile string

const (
	PropertyAccessStatic  PropertyAccessProfile = "static"
	PropertyAccessInterop PropertyAccessProfile = "interop"
)

type PropertyAccessDecision string

const (
	PropertyAccessPlaceRef        PropertyAccessDecision = "place-ref"
	PropertyAccessFiniteDispatch  PropertyAccessDecision = "finite-dispatch"
	PropertyAccessDynamicBoundary PropertyAccessDecision = "dynamic-boundary"
	PropertyAccessReject          PropertyAccessDecision = "reject"
)

const PropertyAccessReasonUnknownKeyStatic = "object.unknown_key_static_profile"

type PropertyAccessAdmission struct {
	SchemaVersion uint32                         `json:"schemaVersion"`
	ObjectTypeKey string                         `json:"objectTypeKey"`
	KeyDomain     PropertyKeyDomain              `json:"keyDomain"`
	Keys          []string                       `json:"keys"`
	Profile       PropertyAccessProfile          `json:"profile"`
	Decision      PropertyAccessDecision         `json:"decision"`
	Effects       []Effect                       `json:"effects"`
	Boundary      *DynamicObjectBoundaryArtifact `json:"boundary,omitempty"`
	Reason        string                         `json:"reason,omitempty"`
	ContentHash   string                         `json:"contentHash"`
}

func BuildPropertyAccessAdmission(objectTypeKey string, domain PropertyKeyDomain, keys []string, profile PropertyAccessProfile, sourceID string) (PropertyAccessAdmission, error) {
	admission := PropertyAccessAdmission{SchemaVersion: PropertyAccessAdmissionSchemaVersion, ObjectTypeKey: objectTypeKey, KeyDomain: domain, Keys: slices.Clone(keys), Profile: profile}
	decision, effects, boundary, reason, err := derivePropertyAccessAdmission(admission, sourceID)
	if err != nil {
		return PropertyAccessAdmission{}, err
	}
	admission.Decision, admission.Effects, admission.Boundary, admission.Reason = decision, effects, boundary, reason
	_, hash, err := CanonicalPropertyAccessAdmission(admission)
	admission.ContentHash = hash
	return admission, err
}

func CanonicalPropertyAccessAdmission(admission PropertyAccessAdmission) ([]byte, string, error) {
	admission.ContentHash = ""
	if admission.SchemaVersion != PropertyAccessAdmissionSchemaVersion || !validObjectSemanticTypeKey(admission.ObjectTypeKey) {
		return nil, "", fmt.Errorf("invalid property access admission header")
	}
	sourceID := ""
	if admission.Boundary != nil {
		sourceID = admission.Boundary.SourceID
	}
	decision, effects, boundary, reason, err := derivePropertyAccessAdmission(admission, sourceID)
	if err != nil {
		return nil, "", err
	}
	if admission.Decision != decision || !slices.Equal(admission.Effects, effects) || admission.Reason != reason || !equalDynamicBoundary(admission.Boundary, boundary) {
		return nil, "", fmt.Errorf("property access admission does not match canonical decision")
	}
	encoded, err := jsonx.Marshal(admission)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	admission.ContentHash = hash
	encoded, err = jsonx.Marshal(admission)
	return encoded, hash, err
}

func VerifyCanonicalPropertyAccessAdmission(admission PropertyAccessAdmission) error {
	claimed := admission.ContentHash
	_, want, err := CanonicalPropertyAccessAdmission(admission)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("property access admission content hash mismatch")
	}
	return nil
}

func DecodePropertyAccessAdmission(data []byte) (*PropertyAccessAdmission, error) {
	if len(data) > maxPropertyAccessAdmissionBytes {
		return nil, fmt.Errorf("property access admission exceeds %d bytes", maxPropertyAccessAdmissionBytes)
	}
	var admission PropertyAccessAdmission
	if err := jsonx.Unmarshal(data, &admission, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode property access admission: %w", err)
	}
	if err := VerifyCanonicalPropertyAccessAdmission(admission); err != nil {
		return nil, err
	}
	return &admission, nil
}

func derivePropertyAccessAdmission(admission PropertyAccessAdmission, sourceID string) (PropertyAccessDecision, []Effect, *DynamicObjectBoundaryArtifact, string, error) {
	if admission.Profile != PropertyAccessStatic && admission.Profile != PropertyAccessInterop {
		return "", nil, nil, "", fmt.Errorf("invalid property access profile")
	}
	for i, key := range admission.Keys {
		if strings.TrimSpace(key) == "" || (i > 0 && admission.Keys[i-1] >= key) {
			return "", nil, nil, "", fmt.Errorf("property access keys are not canonical")
		}
	}
	switch admission.KeyDomain {
	case PropertyKeyDirect, PropertyKeyLiteral:
		if len(admission.Keys) != 1 || sourceID != "" {
			return "", nil, nil, "", fmt.Errorf("single property access key is invalid")
		}
		return PropertyAccessPlaceRef, []Effect{EffectRead}, nil, "", nil
	case PropertyKeyLiteralUnion:
		if len(admission.Keys) < 2 || sourceID != "" {
			return "", nil, nil, "", fmt.Errorf("finite property key domain is invalid")
		}
		return PropertyAccessFiniteDispatch, []Effect{EffectRead}, nil, "", nil
	case PropertyKeyUnknown:
		if len(admission.Keys) != 0 {
			return "", nil, nil, "", fmt.Errorf("unknown property key carries finite keys")
		}
		if admission.Profile == PropertyAccessStatic {
			if sourceID != "" {
				return "", nil, nil, "", fmt.Errorf("static rejection carries dynamic source")
			}
			return PropertyAccessReject, []Effect{}, nil, PropertyAccessReasonUnknownKeyStatic, nil
		}
		if strings.TrimSpace(sourceID) == "" {
			return "", nil, nil, "", fmt.Errorf("dynamic property access source is missing")
		}
		boundary := DynamicObjectBoundaryArtifact{SchemaVersion: DynamicObjectBoundarySchemaVersion, Kind: "dynamic-input", SourceID: sourceID}
		_, hash, err := CanonicalDynamicObjectBoundary(boundary)
		if err != nil {
			return "", nil, nil, "", err
		}
		boundary.ContentHash = hash
		return PropertyAccessDynamicBoundary, []Effect{EffectCall, EffectRead, EffectThrow}, &boundary, "", nil
	default:
		return "", nil, nil, "", fmt.Errorf("invalid property key domain")
	}
}

func equalDynamicBoundary(left, right *DynamicObjectBoundaryArtifact) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
