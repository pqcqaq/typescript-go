package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const ObjectViewMIRSchemaVersion uint32 = 2
const maxObjectViewMIRBytes = 2 << 20

type ObjectViewMIRBinding struct {
	ID                uint32  `json:"id"`
	SourceValueID     ValueID `json:"sourceValueId"`
	ResultValueID     ValueID `json:"resultValueId"`
	SourceTypeKey     string  `json:"sourceTypeKey"`
	TargetTypeKey     string  `json:"targetTypeKey"`
	PreservesIdentity bool    `json:"preservesIdentity"`
	Allocates         bool    `json:"allocates"`
}

type ObjectViewMIRRead struct {
	ID                uint32             `json:"id"`
	ViewValueID       ValueID            `json:"viewValueId"`
	PropertyKey       string             `json:"propertyKey"`
	Kind              ObjectPropertyKind `json:"kind"`
	SourceFieldOffset uint32             `json:"sourceFieldOffset"`
	SourcePresenceBit int32              `json:"sourcePresenceBit"`
	Representation    string             `json:"representation"`
	ReceiverValueID   ValueID            `json:"receiverValueId,omitempty"`
	GetterSymbolKey   string             `json:"getterSymbolKey,omitempty"`
	GetterSignature   string             `json:"getterSignature,omitempty"`
	Effects           []Effect           `json:"effects"`
	ReadRelationPath  []string           `json:"readRelationPath"`
}

type ObjectViewMIRModule struct {
	SchemaVersion  uint32                `json:"schemaVersion"`
	HIRHash        string                `json:"hirHash"`
	HIR            ObjectViewHIRArtifact `json:"hir"`
	TargetTriple   string                `json:"targetTriple"`
	DataLayoutHash string                `json:"dataLayoutHash"`
	Binding        ObjectViewMIRBinding  `json:"binding"`
	Reads          []ObjectViewMIRRead   `json:"reads"`
	ContentHash    string                `json:"contentHash"`
}

func LowerObjectViewMIR(hir ObjectViewHIRArtifact) (ObjectViewMIRModule, error) {
	module := ObjectViewMIRModule{SchemaVersion: ObjectViewMIRSchemaVersion, HIRHash: hir.ContentHash, HIR: hir}
	binding, reads, err := deriveObjectViewMIR(module)
	if err != nil {
		return ObjectViewMIRModule{}, err
	}
	module.Binding = binding
	module.Reads = reads
	target := hir.Operation.View.SourceLayout.Target
	module.TargetTriple = target.Triple
	module.DataLayoutHash = target.DataLayoutHash
	_, hash, err := CanonicalObjectViewMIR(module)
	module.ContentHash = hash
	return module, err
}

func CanonicalObjectViewMIR(module ObjectViewMIRModule) ([]byte, string, error) {
	module.ContentHash = ""
	if module.SchemaVersion != ObjectViewMIRSchemaVersion {
		return nil, "", fmt.Errorf("unsupported ObjectView MIR schema %d", module.SchemaVersion)
	}
	wantBinding, wantReads, err := deriveObjectViewMIR(module)
	if err != nil {
		return nil, "", err
	}
	target := module.HIR.Operation.View.SourceLayout.Target
	if module.HIRHash != module.HIR.ContentHash || module.TargetTriple != target.Triple || module.DataLayoutHash != target.DataLayoutHash {
		return nil, "", fmt.Errorf("ObjectView MIR provenance mismatch")
	}
	if module.Binding != wantBinding || !equalObjectViewMIRReads(module.Reads, wantReads) {
		return nil, "", fmt.Errorf("ObjectView MIR plan does not match canonical lowering")
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

func VerifyCanonicalObjectViewMIR(module ObjectViewMIRModule) error {
	claimed := module.ContentHash
	_, want, err := CanonicalObjectViewMIR(module)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("ObjectView MIR content hash mismatch")
	}
	return nil
}
func DecodeObjectViewMIR(data []byte) (*ObjectViewMIRModule, error) {
	if len(data) > maxObjectViewMIRBytes {
		return nil, fmt.Errorf("ObjectView MIR exceeds %d bytes", maxObjectViewMIRBytes)
	}
	var module ObjectViewMIRModule
	if err := jsonx.Unmarshal(data, &module, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode ObjectView MIR: %w", err)
	}
	if err := VerifyCanonicalObjectViewMIR(module); err != nil {
		return nil, err
	}
	return &module, nil
}

func deriveObjectViewMIR(module ObjectViewMIRModule) (ObjectViewMIRBinding, []ObjectViewMIRRead, error) {
	if err := VerifyCanonicalObjectViewHIRArtifact(module.HIR); err != nil {
		return ObjectViewMIRBinding{}, nil, err
	}
	operation := module.HIR.Operation
	binding := ObjectViewMIRBinding{ID: 1, SourceValueID: operation.SourceValueID, ResultValueID: operation.ResultValueID, SourceTypeKey: operation.View.Source.TypeKey, TargetTypeKey: operation.TargetTypeKey, PreservesIdentity: true}
	physical := objectLayoutPropertiesByKey(operation.View.SourceLayout.Properties)
	reads := make([]ObjectViewMIRRead, len(operation.Mappings))
	for i, mapping := range operation.Mappings {
		property, ok := physical[mapping.SourcePropertyKey]
		if !ok || property.Kind != mapping.Kind {
			return ObjectViewMIRBinding{}, nil, fmt.Errorf("ObjectView MIR property layout is missing")
		}
		read := ObjectViewMIRRead{ID: uint32(i + 2), ViewValueID: operation.ResultValueID, PropertyKey: mapping.TargetPropertyKey, Kind: mapping.Kind, SourceFieldOffset: mapping.SourceFieldOffset, SourcePresenceBit: mapping.SourcePresenceBit, ReadRelationPath: slices.Clone(mapping.ReadRelationPath)}
		switch mapping.Kind {
		case ObjectPropertyData:
			if property.Representation == "" {
				return ObjectViewMIRBinding{}, nil, fmt.Errorf("ObjectView MIR data representation is missing")
			}
			read.Representation = property.Representation
			read.Effects = []Effect{EffectRead}
		case ObjectPropertyAccessor:
			place, err := objectViewAccessorPlace(module.HIR.Gate.HIR, operation, mapping)
			if err != nil {
				return ObjectViewMIRBinding{}, nil, err
			}
			read.ReceiverValueID = operation.ResultValueID
			read.GetterSymbolKey = place.GetterSymbolKey
			read.GetterSignature = VERT011GetterSignature
			read.Effects = slices.Clone(place.LoadEffects)
			switch place.ReadType {
			case TypeNumber:
				read.Representation = string(VERT011RepF64)
			case TypeNullableNumber:
				read.Representation = string(VERT011RepNullableF64)
			default:
				return ObjectViewMIRBinding{}, nil, fmt.Errorf("ObjectView accessor read representation is unsupported")
			}
		default:
			return ObjectViewMIRBinding{}, nil, fmt.Errorf("ObjectView MIR property kind is unsupported")
		}
		reads[i] = read
	}
	return binding, reads, nil
}

func objectViewAccessorPlace(hir HIRModule, operation ObjectViewOperation, mapping ObjectViewPropertyMapping) (PropertyPlaceRef, error) {
	if hir.PlaceRefs == nil || VerifyCanonicalPlaceRefContract(*hir.PlaceRefs) != nil {
		return PropertyPlaceRef{}, fmt.Errorf("ObjectView accessor requires a canonical PlaceRef contract")
	}
	propertyIndex := slices.IndexFunc(operation.View.Source.Properties, func(property ObjectPropertyContract) bool { return property.Key == mapping.SourcePropertyKey })
	if propertyIndex < 0 || operation.View.Source.Properties[propertyIndex].Kind != ObjectPropertyAccessor {
		return PropertyPlaceRef{}, fmt.Errorf("ObjectView accessor semantic property is missing")
	}
	readTypeKey := operation.View.Source.Properties[propertyIndex].ReadTypeKey
	var found *PropertyPlaceRef
	for i := range hir.PlaceRefs.Places {
		place := &hir.PlaceRefs.Places[i]
		if place.ObjectTypeKey != operation.View.Source.TypeKey || place.PropertyKey != mapping.SourcePropertyKey || place.Receiver != operation.SourceValueID {
			continue
		}
		if found != nil {
			return PropertyPlaceRef{}, fmt.Errorf("ObjectView accessor PlaceRef is ambiguous")
		}
		found = place
	}
	if found == nil || found.AccessPlan != PlaceAccessAccessor || found.GetterSymbolKey == "" || found.ReadTypeKey != readTypeKey || !slices.Equal(found.LoadEffects, []Effect{EffectCall, EffectRead, EffectThrow}) {
		return PropertyPlaceRef{}, fmt.Errorf("ObjectView accessor PlaceRef binding is invalid")
	}
	return *found, nil
}

func equalObjectViewMIRReads(left, right []ObjectViewMIRRead) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].ID != right[i].ID || left[i].ViewValueID != right[i].ViewValueID || left[i].PropertyKey != right[i].PropertyKey || left[i].Kind != right[i].Kind || left[i].SourceFieldOffset != right[i].SourceFieldOffset || left[i].SourcePresenceBit != right[i].SourcePresenceBit || left[i].Representation != right[i].Representation || left[i].ReceiverValueID != right[i].ReceiverValueID || left[i].GetterSymbolKey != right[i].GetterSymbolKey || left[i].GetterSignature != right[i].GetterSignature || !slices.Equal(left[i].Effects, right[i].Effects) || !slices.Equal(left[i].ReadRelationPath, right[i].ReadRelationPath) {
			return false
		}
	}
	return true
}
