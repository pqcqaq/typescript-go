package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

// ObjectSemanticContractSchemaVersion identifies the target-independent Phase
// 3 object identity, property, and conversion contract.
const ObjectSemanticContractSchemaVersion uint32 = 1

// ObjectIdentitySemantics describes how aliases identify an object.
type ObjectIdentitySemantics string

// ObjectEqualitySemantics describes the equality operation for object values.
type ObjectEqualitySemantics string

const (
	// ObjectIdentityReference requires assignment and views to retain the source reference identity.
	ObjectIdentityReference ObjectIdentitySemantics = "reference"
	// ObjectEqualityReference compares object reference identity, not structural property values.
	ObjectEqualityReference ObjectEqualitySemantics = "reference"
)

// ObjectPropertyKind distinguishes stored data from receiver-bound accessors.
type ObjectPropertyKind string

const (
	// ObjectPropertyData is a stored semantic property. Physical storage is not selected by OBJ-000a.
	ObjectPropertyData ObjectPropertyKind = "data"
	// ObjectPropertyAccessor invokes a getter or setter with the source object as receiver.
	ObjectPropertyAccessor ObjectPropertyKind = "accessor"
)

// ObjectPropertyContract records the read/write surface of one canonical property.
type ObjectPropertyContract struct {
	// Key is the canonical string/symbol/private property key and participates in ordering.
	Key string `json:"key"`
	// Kind distinguishes data from receiver-bound accessor semantics.
	Kind ObjectPropertyKind `json:"kind"`
	// ReadTypeKey identifies the readable source type, or is empty for setter-only properties.
	ReadTypeKey string `json:"readTypeKey,omitempty"`
	// WriteTypeKey identifies the writable input type, or is empty when no write entry exists.
	WriteTypeKey string `json:"writeTypeKey,omitempty"`
	// Optional preserves property absence independently of a present undefined value.
	Optional bool `json:"optional"`
	// Readonly records a shallow compile-time write restriction.
	Readonly bool `json:"readonly"`
	// Visibility is public, protected, or private.
	Visibility string `json:"visibility"`
	// PrivateIdentity is required for private properties and must match nominally across views.
	PrivateIdentity string `json:"privateIdentity,omitempty"`
}

// ObjectSemanticContract is the canonical target-independent contract for one object type.
type ObjectSemanticContract struct {
	// SchemaVersion identifies the object semantic wire contract.
	SchemaVersion uint32 `json:"schemaVersion"`
	// TypeKey is the canonical source type digest and contains no target layout information.
	TypeKey string `json:"typeKey"`
	// Identity fixes aliasing to reference identity.
	Identity ObjectIdentitySemantics `json:"identity"`
	// Equality fixes object equality to reference equality.
	Equality ObjectEqualitySemantics `json:"equality"`
	// Properties are strictly ordered by Key and contain no duplicates.
	Properties []ObjectPropertyContract `json:"properties"`
	// ContentHash binds the complete semantic contract while excluding itself.
	ContentHash string `json:"contentHash"`
}

// ObjectTypeRelation records one reliable source-to-target read assignability proof.
// The proof is produced by the verified source type planner; this contract never
// asks the tsgo checker or trusts an unbound runtime claim.
type ObjectTypeRelation struct {
	// SourceTypeKey is the canonical readable source type digest.
	SourceTypeKey string `json:"sourceTypeKey"`
	// TargetTypeKey is the canonical target read type digest.
	TargetTypeKey string `json:"targetTypeKey"`
	// Reliable is false for a checker proof that requires conservative rejection.
	Reliable bool `json:"reliable"`
}

// ObjectConversionMode identifies an explicit semantic conversion request.
type ObjectConversionMode string

const (
	// ObjectConversionImplicit requests an identity-preserving static conversion.
	ObjectConversionImplicit ObjectConversionMode = "implicit"
	// ObjectConversionExplicitCopy requests a new object identity through an explicit copy.
	ObjectConversionExplicitCopy ObjectConversionMode = "explicit-copy"
	// ObjectConversionDynamicBoundary requests an explicit dynamic/interop boundary.
	ObjectConversionDynamicBoundary ObjectConversionMode = "dynamic-boundary"
)

// ObjectSemanticProfile identifies whether dynamic object semantics are enabled.
type ObjectSemanticProfile string

const (
	// ObjectProfileStatic rejects all dynamic object boundaries.
	ObjectProfileStatic ObjectSemanticProfile = "static"
	// ObjectProfileDynamic admits an explicit dynamic boundary.
	ObjectProfileDynamic ObjectSemanticProfile = "dynamic"
)

// ObjectConversionDecision is the semantic result selected by PlanObjectConversion.
type ObjectConversionDecision string

const (
	// ObjectDecisionIdentity preserves the source reference without a view.
	ObjectDecisionIdentity ObjectConversionDecision = "identity"
	// ObjectDecisionReadonlyView preserves identity and exposes no target writes.
	ObjectDecisionReadonlyView ObjectConversionDecision = "readonly-view"
	// ObjectDecisionMutableView preserves identity but requires a later physical layout proof.
	ObjectDecisionMutableView ObjectConversionDecision = "mutable-view"
	// ObjectDecisionCopyNewIdentity explicitly creates a distinct object identity.
	ObjectDecisionCopyNewIdentity ObjectConversionDecision = "copy-new-identity"
	// ObjectDecisionDynamicBoundary crosses an explicit dynamic/interop boundary.
	ObjectDecisionDynamicBoundary ObjectConversionDecision = "dynamic-boundary"
	// ObjectDecisionReject denies a conversion without changing source semantics.
	ObjectDecisionReject ObjectConversionDecision = "reject"
)

const (
	// ObjectReasonPropertyMissing reports a target property with no source property contract.
	ObjectReasonPropertyMissing = "object.property_missing"
	// ObjectReasonPropertyKindMismatch preserves receiver-bound accessor semantics.
	ObjectReasonPropertyKindMismatch = "object.property_kind_mismatch"
	// ObjectReasonReadTypeUnproven reports a missing or unreliable source-to-target read proof.
	ObjectReasonReadTypeUnproven = "object.read_type_unproven"
	// ObjectReasonMutableAliasTypeMismatch reports non-identical read/write types on a writable alias.
	ObjectReasonMutableAliasTypeMismatch = "object.mutable_alias_type_mismatch"
	// ObjectReasonMutableAliasOptionalMismatch reports incompatible writable presence semantics.
	ObjectReasonMutableAliasOptionalMismatch = "object.mutable_alias_optional_mismatch"
	// ObjectReasonMutableAliasRequiresLayoutProof marks a semantic candidate blocked on OBJ-000b.
	ObjectReasonMutableAliasRequiresLayoutProof = "object.mutable_alias_requires_layout_proof"
	// ObjectReasonPrivateIdentityMismatch reports a nominal private-property mismatch.
	ObjectReasonPrivateIdentityMismatch = "object.private_identity_mismatch"
	// ObjectReasonDynamicBoundaryStaticProfile reports dynamic behavior requested by a static profile.
	ObjectReasonDynamicBoundaryStaticProfile = "object.dynamic_boundary_static_profile"
	// ObjectReasonEscapeDowngrade reports a non-monotonic escape transition.
	ObjectReasonEscapeDowngrade = "object.escape_downgrade"
)

// ObjectConversionRequest supplies verified relation proofs and an explicit profile boundary.
type ObjectConversionRequest struct {
	// Mode distinguishes implicit aliasing, explicit copying, and dynamic boundaries.
	Mode ObjectConversionMode
	// Profile controls whether a dynamic boundary is admissible.
	Profile ObjectSemanticProfile
	// ReadRelations are strictly ordered reliable/unreliable covariance proofs.
	ReadRelations []ObjectTypeRelation
}

// ObjectConversionPlan records identity and write exposure without selecting a layout.
type ObjectConversionPlan struct {
	// Decision is the unique semantic conversion decision.
	Decision ObjectConversionDecision
	// PreservesIdentity is true for aliases and views and false for explicit copies/rejection.
	PreservesIdentity bool
	// ExposesWrites is true when the target surface can mutate the source identity.
	ExposesWrites bool
	// RequiresLayoutProof prevents a mutable semantic candidate from reaching lowering before OBJ-000b.
	RequiresLayoutProof bool
	// Reason is a stable rejection or pending-proof reason.
	Reason string
}

// ObjectEscapeCategory records the strongest lifetime boundary crossed by an object value.
type ObjectEscapeCategory string

const (
	// ObjectEscapeLocal does not outlive the current activation.
	ObjectEscapeLocal ObjectEscapeCategory = "local"
	// ObjectEscapeCaller transfers the reference to a caller or a proven non-storing call.
	ObjectEscapeCaller ObjectEscapeCategory = "caller"
	// ObjectEscapeHeap stores or captures the reference beyond the current activation.
	ObjectEscapeHeap ObjectEscapeCategory = "heap"
	// ObjectEscapeDynamic crosses an explicit dynamic or FFI boundary.
	ObjectEscapeDynamic ObjectEscapeCategory = "dynamic"
)

// CanonicalObjectSemanticContract verifies and serializes a contract and returns its content hash.
func CanonicalObjectSemanticContract(contract ObjectSemanticContract) ([]byte, string, error) {
	contract.ContentHash = ""
	if err := verifyObjectSemanticStructure(contract); err != nil {
		return nil, "", err
	}
	encoded, err := jsonx.Marshal(contract)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	contract.ContentHash = hash
	encoded, err = jsonx.Marshal(contract)
	if err != nil {
		return nil, "", err
	}
	return encoded, hash, nil
}

// VerifyCanonicalObjectSemanticContract verifies structure and the claimed content hash.
func VerifyCanonicalObjectSemanticContract(contract ObjectSemanticContract) error {
	claimed := contract.ContentHash
	_, want, err := CanonicalObjectSemanticContract(contract)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("object semantic content hash mismatch: got %q, want %q", claimed, want)
	}
	return nil
}

// DecodeObjectSemanticContract strictly decodes and verifies a canonical contract.
func DecodeObjectSemanticContract(data []byte) (*ObjectSemanticContract, error) {
	var contract ObjectSemanticContract
	if err := jsonx.Unmarshal(data, &contract, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode object semantic contract: %w", err)
	}
	if err := VerifyCanonicalObjectSemanticContract(contract); err != nil {
		return nil, err
	}
	return &contract, nil
}

// PlanObjectConversion selects a conservative identity-preserving conversion or a stable rejection.
func PlanObjectConversion(source, target ObjectSemanticContract, request ObjectConversionRequest) (ObjectConversionPlan, error) {
	if err := VerifyCanonicalObjectSemanticContract(source); err != nil {
		return ObjectConversionPlan{}, fmt.Errorf("source object contract: %w", err)
	}
	if err := VerifyCanonicalObjectSemanticContract(target); err != nil {
		return ObjectConversionPlan{}, fmt.Errorf("target object contract: %w", err)
	}
	if err := verifyObjectConversionRequest(request); err != nil {
		return ObjectConversionPlan{}, err
	}

	switch request.Mode {
	case ObjectConversionDynamicBoundary:
		if request.Profile != ObjectProfileDynamic {
			return rejectedObjectConversion(ObjectReasonDynamicBoundaryStaticProfile), nil
		}
		return ObjectConversionPlan{Decision: ObjectDecisionDynamicBoundary, PreservesIdentity: true, ExposesWrites: true}, nil
	case ObjectConversionExplicitCopy:
		if reason := verifyCopyObjectMapping(source, target, request.ReadRelations); reason != "" {
			return rejectedObjectConversion(reason), nil
		}
		return ObjectConversionPlan{Decision: ObjectDecisionCopyNewIdentity, ExposesWrites: objectContractExposesWrites(target)}, nil
	case ObjectConversionImplicit:
	default:
		return ObjectConversionPlan{}, fmt.Errorf("unsupported object conversion mode %q", request.Mode)
	}

	if source.TypeKey == target.TypeKey {
		if source.ContentHash != target.ContentHash {
			return ObjectConversionPlan{}, fmt.Errorf("object type key %q has conflicting semantic contracts", source.TypeKey)
		}
		return ObjectConversionPlan{Decision: ObjectDecisionIdentity, PreservesIdentity: true, ExposesWrites: objectContractExposesWrites(target)}, nil
	}
	if reason := verifyReadableObjectMapping(source, target, request.ReadRelations); reason != "" {
		return rejectedObjectConversion(reason), nil
	}
	if !objectContractExposesWrites(target) {
		return ObjectConversionPlan{Decision: ObjectDecisionReadonlyView, PreservesIdentity: true}, nil
	}
	if reason := verifyWritableObjectMapping(source, target); reason != "" {
		return rejectedObjectConversion(reason), nil
	}
	return ObjectConversionPlan{
		Decision:            ObjectDecisionMutableView,
		PreservesIdentity:   true,
		ExposesWrites:       true,
		RequiresLayoutProof: true,
		Reason:              ObjectReasonMutableAliasRequiresLayoutProof,
	}, nil
}

// JoinObjectEscape returns the stronger lifetime obligation.
func JoinObjectEscape(left, right ObjectEscapeCategory) (ObjectEscapeCategory, error) {
	leftRank, err := objectEscapeRank(left)
	if err != nil {
		return "", err
	}
	rightRank, err := objectEscapeRank(right)
	if err != nil {
		return "", err
	}
	if rightRank > leftRank {
		return right, nil
	}
	return left, nil
}

// VerifyObjectEscapeTransition rejects an unproven downgrade of a value's lifetime obligation.
func VerifyObjectEscapeTransition(previous, next ObjectEscapeCategory) error {
	previousRank, err := objectEscapeRank(previous)
	if err != nil {
		return err
	}
	nextRank, err := objectEscapeRank(next)
	if err != nil {
		return err
	}
	if nextRank < previousRank {
		return fmt.Errorf("%s: %s to %s", ObjectReasonEscapeDowngrade, previous, next)
	}
	return nil
}

func verifyObjectSemanticStructure(contract ObjectSemanticContract) error {
	if contract.SchemaVersion != ObjectSemanticContractSchemaVersion {
		return fmt.Errorf("unsupported object semantic schema %d", contract.SchemaVersion)
	}
	if !validObjectSemanticTypeKey(contract.TypeKey) {
		return fmt.Errorf("invalid object semantic type key %q", contract.TypeKey)
	}
	if contract.Identity != ObjectIdentityReference || contract.Equality != ObjectEqualityReference {
		return fmt.Errorf("object semantic identity/equality must use reference semantics")
	}
	previousKey := ""
	for index, property := range contract.Properties {
		if strings.TrimSpace(property.Key) == "" || (index != 0 && property.Key <= previousKey) {
			return fmt.Errorf("object property %d key %q is empty, duplicated, or not canonical", index, property.Key)
		}
		previousKey = property.Key
		if property.Kind != ObjectPropertyData && property.Kind != ObjectPropertyAccessor {
			return fmt.Errorf("object property %q has invalid kind %q", property.Key, property.Kind)
		}
		if property.ReadTypeKey == "" && property.WriteTypeKey == "" {
			return fmt.Errorf("object property %q has no read or write contract", property.Key)
		}
		if property.ReadTypeKey != "" && !validObjectSemanticTypeKey(property.ReadTypeKey) {
			return fmt.Errorf("object property %q has invalid read type key", property.Key)
		}
		if property.WriteTypeKey != "" && !validObjectSemanticTypeKey(property.WriteTypeKey) {
			return fmt.Errorf("object property %q has invalid write type key", property.Key)
		}
		if property.Readonly && property.WriteTypeKey != "" {
			return fmt.Errorf("object property %q exposes a write through readonly", property.Key)
		}
		switch property.Visibility {
		case "public", "protected":
			if property.PrivateIdentity != "" {
				return fmt.Errorf("object property %q has private identity with visibility %q", property.Key, property.Visibility)
			}
		case "private":
			if strings.TrimSpace(property.PrivateIdentity) == "" {
				return fmt.Errorf("object property %q has no private identity", property.Key)
			}
		default:
			return fmt.Errorf("object property %q has invalid visibility %q", property.Key, property.Visibility)
		}
	}
	return nil
}

func verifyObjectConversionRequest(request ObjectConversionRequest) error {
	if request.Profile != ObjectProfileStatic && request.Profile != ObjectProfileDynamic {
		return fmt.Errorf("unsupported object semantic profile %q", request.Profile)
	}
	previous := ""
	for index, relation := range request.ReadRelations {
		if !validObjectSemanticTypeKey(relation.SourceTypeKey) || !validObjectSemanticTypeKey(relation.TargetTypeKey) {
			return fmt.Errorf("object read relation %d has invalid type key", index)
		}
		key := relation.SourceTypeKey + "\x00" + relation.TargetTypeKey
		if index != 0 && key <= previous {
			return fmt.Errorf("object read relations are duplicated or not canonical")
		}
		previous = key
	}
	return nil
}

func verifyReadableObjectMapping(source, target ObjectSemanticContract, relations []ObjectTypeRelation) string {
	sourceProperties := objectPropertiesByKey(source.Properties)
	for _, targetProperty := range target.Properties {
		sourceProperty, ok := sourceProperties[targetProperty.Key]
		if !ok {
			return ObjectReasonPropertyMissing
		}
		if !matchingPrivateIdentity(sourceProperty, targetProperty) {
			return ObjectReasonPrivateIdentityMismatch
		}
		if sourceProperty.Kind != targetProperty.Kind {
			return ObjectReasonPropertyKindMismatch
		}
		if !targetProperty.Optional && sourceProperty.Optional {
			return ObjectReasonPropertyMissing
		}
		if targetProperty.ReadTypeKey != "" {
			if sourceProperty.ReadTypeKey == "" || !hasReliableObjectReadRelation(sourceProperty.ReadTypeKey, targetProperty.ReadTypeKey, relations) {
				return ObjectReasonReadTypeUnproven
			}
		}
	}
	return ""
}

func verifyWritableObjectMapping(source, target ObjectSemanticContract) string {
	sourceProperties := objectPropertiesByKey(source.Properties)
	for _, targetProperty := range target.Properties {
		if targetProperty.WriteTypeKey == "" {
			continue
		}
		sourceProperty := sourceProperties[targetProperty.Key]
		if sourceProperty.Optional != targetProperty.Optional {
			return ObjectReasonMutableAliasOptionalMismatch
		}
		if sourceProperty.WriteTypeKey == "" || sourceProperty.ReadTypeKey != targetProperty.ReadTypeKey || sourceProperty.WriteTypeKey != targetProperty.WriteTypeKey {
			return ObjectReasonMutableAliasTypeMismatch
		}
	}
	return ""
}

func verifyCopyObjectMapping(source, target ObjectSemanticContract, relations []ObjectTypeRelation) string {
	sourceProperties := objectPropertiesByKey(source.Properties)
	for _, targetProperty := range target.Properties {
		sourceProperty, ok := sourceProperties[targetProperty.Key]
		if !ok {
			return ObjectReasonPropertyMissing
		}
		if !matchingPrivateIdentity(sourceProperty, targetProperty) {
			return ObjectReasonPrivateIdentityMismatch
		}
		if sourceProperty.Kind != targetProperty.Kind {
			return ObjectReasonPropertyKindMismatch
		}
		if !targetProperty.Optional && sourceProperty.Optional {
			return ObjectReasonPropertyMissing
		}
		targetInputType := targetProperty.WriteTypeKey
		if targetInputType == "" {
			targetInputType = targetProperty.ReadTypeKey
		}
		if sourceProperty.ReadTypeKey == "" || targetInputType == "" || !hasReliableObjectReadRelation(sourceProperty.ReadTypeKey, targetInputType, relations) {
			return ObjectReasonReadTypeUnproven
		}
	}
	return ""
}

func objectPropertiesByKey(properties []ObjectPropertyContract) map[string]ObjectPropertyContract {
	result := make(map[string]ObjectPropertyContract, len(properties))
	for _, property := range properties {
		result[property.Key] = property
	}
	return result
}

func matchingPrivateIdentity(source, target ObjectPropertyContract) bool {
	if source.Visibility != "private" && target.Visibility != "private" {
		return true
	}
	return source.Visibility == "private" && target.Visibility == "private" && source.PrivateIdentity == target.PrivateIdentity
}

func hasReliableObjectReadRelation(source, target string, relations []ObjectTypeRelation) bool {
	if source == target {
		return true
	}
	for _, relation := range relations {
		if relation.SourceTypeKey == source && relation.TargetTypeKey == target {
			return relation.Reliable
		}
	}
	return false
}

func objectContractExposesWrites(contract ObjectSemanticContract) bool {
	for _, property := range contract.Properties {
		if property.WriteTypeKey != "" {
			return true
		}
	}
	return false
}

func rejectedObjectConversion(reason string) ObjectConversionPlan {
	return ObjectConversionPlan{Decision: ObjectDecisionReject, Reason: reason}
}

func objectEscapeRank(category ObjectEscapeCategory) (int, error) {
	switch category {
	case ObjectEscapeLocal:
		return 0, nil
	case ObjectEscapeCaller:
		return 1, nil
	case ObjectEscapeHeap:
		return 2, nil
	case ObjectEscapeDynamic:
		return 3, nil
	default:
		return 0, fmt.Errorf("unsupported object escape category %q", category)
	}
}

func validObjectSemanticTypeKey(value string) bool {
	return len(value) == sha256.Size*2 && isLowerHex(value)
}
