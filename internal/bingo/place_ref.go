package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

// PlaceRefSchemaVersion identifies the target-independent property-place
// contract introduced for VERT-011.
const PlaceRefSchemaVersion uint32 = 1

const maxPlaceRefContractBytes = 256 << 10

// PlaceID is dense within one PlaceRefContract.
type PlaceID uint32

// PlaceAccessSyntax distinguishes a direct property name from one computed
// key value that has already been evaluated and saved.
type PlaceAccessSyntax string

const (
	PlaceAccessDirect   PlaceAccessSyntax = "direct"
	PlaceAccessComputed PlaceAccessSyntax = "computed"
)

// PlaceAccessPlan selects semantic data access or receiver-bound accessors.
// Target layout and concrete calling convention are intentionally absent.
type PlaceAccessPlan string

const (
	PlaceAccessStaticData PlaceAccessPlan = "static-data"
	PlaceAccessAccessor   PlaceAccessPlan = "accessor"
)

// PlaceMutability records whether StorePlace is semantically admissible.
type PlaceMutability string

const (
	PlaceMutable  PlaceMutability = "mutable"
	PlaceReadonly PlaceMutability = "readonly"
)

// PropertyPlaceRef saves the receiver and optional computed key used by later
// load/store operations. Values are HIR SSA identities, never source nodes or
// target addresses.
type PropertyPlaceRef struct {
	ID                       PlaceID           `json:"id"`
	Receiver                 ValueID           `json:"receiver"`
	Key                      ValueID           `json:"key,omitempty"`
	AccessSyntax             PlaceAccessSyntax `json:"accessSyntax"`
	AccessPlan               PlaceAccessPlan   `json:"accessPlan"`
	ObjectTypeKey            string            `json:"objectTypeKey"`
	PropertyKey              string            `json:"propertyKey"`
	PropertySymbolKey        string            `json:"propertySymbolKey"`
	ReadTypeKey              string            `json:"readTypeKey"`
	WriteTypeKey             string            `json:"writeTypeKey,omitempty"`
	ReadType                 TypeKind          `json:"readType"`
	WriteType                TypeKind          `json:"writeType,omitempty"`
	Mutability               PlaceMutability   `json:"mutability"`
	Required                 bool              `json:"required"`
	GetterSymbolKey          string            `json:"getterSymbolKey,omitempty"`
	SetterSymbolKey          string            `json:"setterSymbolKey,omitempty"`
	BackingPropertyKey       string            `json:"backingPropertyKey,omitempty"`
	BackingPropertySymbolKey string            `json:"backingPropertySymbolKey,omitempty"`
	LoadEffects              []Effect          `json:"loadEffects"`
	StoreEffects             []Effect          `json:"storeEffects"`
	Origin                   Origin            `json:"origin"`
}

// PlaceRefContract is the canonical module-level table consumed by VERT-011
// HIR. EvaluationOrder lists each place's saved receiver and key in source
// order and lets the enclosing HIR verifier bind them to dominating values.
type PlaceRefContract struct {
	SchemaVersion   uint32                   `json:"schemaVersion"`
	ObjectContracts []ObjectSemanticContract `json:"objectContracts"`
	EvaluationOrder []ValueID                `json:"evaluationOrder"`
	Places          []PropertyPlaceRef       `json:"places"`
	ContentHash     string                   `json:"contentHash"`
}

// CanonicalPlaceRefContract verifies, serializes, and hashes a PlaceRef table.
func CanonicalPlaceRefContract(contract PlaceRefContract) ([]byte, string, error) {
	contract.ContentHash = ""
	if err := verifyPlaceRefContract(contract); err != nil {
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

// VerifyCanonicalPlaceRefContract verifies structure and the claimed hash.
func VerifyCanonicalPlaceRefContract(contract PlaceRefContract) error {
	claimed := contract.ContentHash
	_, want, err := CanonicalPlaceRefContract(contract)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("PlaceRef content hash mismatch: got %q, want %q", claimed, want)
	}
	return nil
}

// DecodePlaceRefContract strictly decodes one bounded canonical contract.
func DecodePlaceRefContract(data []byte) (*PlaceRefContract, error) {
	if len(data) > maxPlaceRefContractBytes {
		return nil, fmt.Errorf("PlaceRef contract exceeds %d-byte limit", maxPlaceRefContractBytes)
	}
	var contract PlaceRefContract
	if err := jsonx.Unmarshal(data, &contract, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode PlaceRef contract: %w", err)
	}
	if err := VerifyCanonicalPlaceRefContract(contract); err != nil {
		return nil, err
	}
	return &contract, nil
}

func verifyPlaceRefContract(contract PlaceRefContract) error {
	if contract.SchemaVersion != PlaceRefSchemaVersion {
		return fmt.Errorf("unsupported PlaceRef schema %d", contract.SchemaVersion)
	}
	if len(contract.Places) == 0 {
		return fmt.Errorf("PlaceRef contract requires at least one place")
	}
	objects := make(map[string]ObjectSemanticContract, len(contract.ObjectContracts))
	previousTypeKey := ""
	for index, object := range contract.ObjectContracts {
		if err := VerifyCanonicalObjectSemanticContract(object); err != nil {
			return fmt.Errorf("PlaceRef object contract %d: %w", index, err)
		}
		if _, duplicate := objects[object.TypeKey]; duplicate {
			return fmt.Errorf("duplicate PlaceRef object type %q", object.TypeKey)
		}
		if previousTypeKey != "" && object.TypeKey <= previousTypeKey {
			return fmt.Errorf("PlaceRef object contracts are not in canonical type-key order")
		}
		objects[object.TypeKey] = object
		previousTypeKey = object.TypeKey
	}
	if len(objects) == 0 {
		return fmt.Errorf("PlaceRef contract requires an object semantic contract")
	}
	wantOrder := make([]ValueID, 0, len(contract.Places)*2)
	for index, place := range contract.Places {
		if place.ID != PlaceID(index+1) {
			return fmt.Errorf("PlaceRef ID %d is not dense at index %d", place.ID, index)
		}
		if err := verifyPropertyPlaceRef(place); err != nil {
			return fmt.Errorf("PlaceRef %d: %w", place.ID, err)
		}
		object, ok := objects[place.ObjectTypeKey]
		if !ok {
			return fmt.Errorf("PlaceRef %d object type %q is not bound", place.ID, place.ObjectTypeKey)
		}
		if err := verifyPlaceObjectProperty(place, object); err != nil {
			return fmt.Errorf("PlaceRef %d: %w", place.ID, err)
		}
		for _, value := range []ValueID{place.Receiver, place.Key} {
			if value == 0 {
				continue
			}
			wantOrder = append(wantOrder, value)
		}
	}
	if !slices.Equal(contract.EvaluationOrder, wantOrder) {
		return fmt.Errorf("PlaceRef evaluation order mismatch: got %v, want %v", contract.EvaluationOrder, wantOrder)
	}
	return nil
}

func verifyPropertyPlaceRef(place PropertyPlaceRef) error {
	if place.Receiver == 0 || !validOrigin(place.Origin) {
		return fmt.Errorf("receiver or origin is invalid")
	}
	if !validObjectSemanticTypeKey(place.ObjectTypeKey) || strings.TrimSpace(place.PropertyKey) == "" || strings.TrimSpace(place.PropertySymbolKey) == "" {
		return fmt.Errorf("object or property identity is invalid")
	}
	if !validSHA256Hex(place.ReadTypeKey) || !validPlaceReadType(place.ReadType) || !place.Required {
		return fmt.Errorf("read type or presence contract is invalid")
	}
	switch place.AccessSyntax {
	case PlaceAccessDirect:
		if place.Key != 0 {
			return fmt.Errorf("direct property place carries a computed key")
		}
	case PlaceAccessComputed:
		if place.Key == 0 || place.Key == place.Receiver {
			return fmt.Errorf("computed property place has no distinct saved key")
		}
	default:
		return fmt.Errorf("unsupported property access syntax %q", place.AccessSyntax)
	}
	switch place.Mutability {
	case PlaceMutable:
		if !validSHA256Hex(place.WriteTypeKey) || place.WriteType != TypeNumber {
			return fmt.Errorf("mutable place write type is invalid")
		}
	case PlaceReadonly:
		if place.WriteTypeKey != "" || place.WriteType != "" || len(place.StoreEffects) != 0 || place.SetterSymbolKey != "" {
			return fmt.Errorf("readonly place exposes a store")
		}
	default:
		return fmt.Errorf("unsupported place mutability %q", place.Mutability)
	}
	switch place.AccessPlan {
	case PlaceAccessStaticData:
		if place.Mutability == PlaceMutable && (place.ReadTypeKey != place.WriteTypeKey || place.ReadType != place.WriteType) {
			return fmt.Errorf("static data place read/write type mismatch")
		}
		if place.GetterSymbolKey != "" || place.SetterSymbolKey != "" || place.BackingPropertyKey != "" || place.BackingPropertySymbolKey != "" || !slices.Equal(place.LoadEffects, []Effect{EffectRead}) {
			return fmt.Errorf("static data place has invalid load contract")
		}
		if place.Mutability == PlaceMutable && !slices.Equal(place.StoreEffects, []Effect{EffectWrite}) {
			return fmt.Errorf("static data place has invalid store contract")
		}
	case PlaceAccessAccessor:
		if strings.TrimSpace(place.GetterSymbolKey) == "" || strings.TrimSpace(place.BackingPropertyKey) == "" || strings.TrimSpace(place.BackingPropertySymbolKey) == "" || !slices.Equal(place.LoadEffects, []Effect{EffectCall, EffectRead, EffectThrow}) {
			return fmt.Errorf("accessor place has invalid getter contract")
		}
		if place.Mutability == PlaceMutable && (strings.TrimSpace(place.SetterSymbolKey) == "" || !slices.Equal(place.StoreEffects, []Effect{EffectCall, EffectThrow, EffectWrite})) {
			return fmt.Errorf("accessor place has invalid setter contract")
		}
	default:
		return fmt.Errorf("unsupported property access plan %q", place.AccessPlan)
	}
	return nil
}

func validPlaceReadType(value TypeKind) bool {
	return value == TypeNumber || value == TypeNullableNumber
}

func verifyPlaceObjectProperty(place PropertyPlaceRef, object ObjectSemanticContract) error {
	index := slices.IndexFunc(object.Properties, func(property ObjectPropertyContract) bool { return property.Key == place.PropertyKey })
	if index < 0 {
		return fmt.Errorf("property %q is not in object semantic contract", place.PropertyKey)
	}
	property := object.Properties[index]
	wantKind := ObjectPropertyData
	if place.AccessPlan == PlaceAccessAccessor {
		wantKind = ObjectPropertyAccessor
	}
	if property.Kind != wantKind || property.ReadTypeKey != place.ReadTypeKey || property.WriteTypeKey != place.WriteTypeKey || property.Optional || property.Readonly != (place.Mutability == PlaceReadonly) {
		return fmt.Errorf("property %q semantic contract mismatch", place.PropertyKey)
	}
	if place.AccessPlan == PlaceAccessAccessor {
		backingIndex := slices.IndexFunc(object.Properties, func(property ObjectPropertyContract) bool { return property.Key == place.BackingPropertyKey })
		if backingIndex < 0 {
			return fmt.Errorf("accessor backing property %q is not in object semantic contract", place.BackingPropertyKey)
		}
		backing := object.Properties[backingIndex]
		if backing.Kind != ObjectPropertyData || backing.ReadTypeKey != place.ReadTypeKey || backing.WriteTypeKey != place.ReadTypeKey || backing.Optional || backing.Readonly {
			return fmt.Errorf("accessor backing property %q contract mismatch", place.BackingPropertyKey)
		}
	}
	return nil
}
