package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const ObjectLayoutCopySchemaVersion uint32 = 1
const maxObjectLayoutCopyBytes = 768 << 10

// ObjectLayoutCopyMapping freezes one explicit source-load to target-store.
type ObjectLayoutCopyMapping struct {
	PropertyKey          string   `json:"propertyKey"`
	ReadRelationPath     []string `json:"readRelationPath"`
	SourceFieldOffset    uint32   `json:"sourceFieldOffset"`
	SourceRepresentation string   `json:"sourceRepresentation"`
	TargetFieldOffset    uint32   `json:"targetFieldOffset"`
	TargetRepresentation string   `json:"targetRepresentation"`
}

// ObjectLayoutCopyContract authorizes an explicit allocation and field copy.
// It never authorizes aliasing, accessors, or an in-place representation cast.
type ObjectLayoutCopyContract struct {
	SchemaVersion     uint32                    `json:"schemaVersion"`
	Source            ObjectSemanticContract    `json:"source"`
	Target            ObjectSemanticContract    `json:"target"`
	Relations         TypeRelationGraph         `json:"relations"`
	SourceLayout      ObjectLayoutContract      `json:"sourceLayout"`
	TargetLayout      ObjectLayoutContract      `json:"targetLayout"`
	Mappings          []ObjectLayoutCopyMapping `json:"mappings"`
	AllocatesTarget   bool                      `json:"allocatesTarget"`
	PreservesIdentity bool                      `json:"preservesIdentity"`
	InvokesAccessors  bool                      `json:"invokesAccessors"`
	ContentHash       string                    `json:"contentHash"`
}

func BuildObjectLayoutCopyContract(source, target ObjectSemanticContract, relations TypeRelationGraph, sourceLayout, targetLayout ObjectLayoutContract) (ObjectLayoutCopyContract, error) {
	contract := ObjectLayoutCopyContract{SchemaVersion: ObjectLayoutCopySchemaVersion, Source: source, Target: target, Relations: relations, SourceLayout: sourceLayout, TargetLayout: targetLayout, AllocatesTarget: true}
	mappings, err := deriveObjectLayoutCopyMappings(contract)
	if err != nil {
		return ObjectLayoutCopyContract{}, err
	}
	contract.Mappings = mappings
	_, hash, err := CanonicalObjectLayoutCopyContract(contract)
	if err != nil {
		return ObjectLayoutCopyContract{}, err
	}
	contract.ContentHash = hash
	return contract, nil
}

func CanonicalObjectLayoutCopyContract(contract ObjectLayoutCopyContract) ([]byte, string, error) {
	contract.ContentHash = ""
	if contract.SchemaVersion != ObjectLayoutCopySchemaVersion || !contract.AllocatesTarget || contract.PreservesIdentity || contract.InvokesAccessors {
		return nil, "", fmt.Errorf("invalid explicit object layout copy policy")
	}
	wanted, err := deriveObjectLayoutCopyMappings(contract)
	if err != nil {
		return nil, "", err
	}
	if !equalObjectLayoutCopyMappings(contract.Mappings, wanted) {
		return nil, "", fmt.Errorf("object layout copy mappings are not canonical")
	}
	encoded, err := jsonx.Marshal(contract)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	contract.ContentHash = hash
	encoded, err = jsonx.Marshal(contract)
	return encoded, hash, err
}

func VerifyCanonicalObjectLayoutCopyContract(contract ObjectLayoutCopyContract) error {
	claimed := contract.ContentHash
	_, wanted, err := CanonicalObjectLayoutCopyContract(contract)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != wanted {
		return fmt.Errorf("object layout copy content hash mismatch")
	}
	return nil
}

func DecodeObjectLayoutCopyContract(data []byte) (*ObjectLayoutCopyContract, error) {
	if len(data) > maxObjectLayoutCopyBytes {
		return nil, fmt.Errorf("object layout copy exceeds %d bytes", maxObjectLayoutCopyBytes)
	}
	var contract ObjectLayoutCopyContract
	if err := jsonx.Unmarshal(data, &contract, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode object layout copy: %w", err)
	}
	if err := VerifyCanonicalObjectLayoutCopyContract(contract); err != nil {
		return nil, err
	}
	return &contract, nil
}

func deriveObjectLayoutCopyMappings(contract ObjectLayoutCopyContract) ([]ObjectLayoutCopyMapping, error) {
	if err := VerifyCanonicalObjectSemanticContract(contract.Source); err != nil {
		return nil, fmt.Errorf("source copy semantics: %w", err)
	}
	if err := VerifyCanonicalObjectSemanticContract(contract.Target); err != nil {
		return nil, fmt.Errorf("target copy semantics: %w", err)
	}
	if err := VerifyCanonicalTypeRelationGraph(contract.Relations); err != nil {
		return nil, err
	}
	readRelations := make([]ObjectTypeRelation, 0, len(contract.Target.Properties))
	sourceProperties := objectPropertiesByKey(contract.Source.Properties)
	for _, target := range contract.Target.Properties {
		source, ok := sourceProperties[target.Key]
		if !ok {
			return nil, fmt.Errorf("%s", ObjectReasonPropertyMissing)
		}
		targetInput := target.WriteTypeKey
		if targetInput == "" {
			targetInput = target.ReadTypeKey
		}
		if source.ReadTypeKey != "" && targetInput != "" {
			if _, err := FindTypeRelationPath(contract.Relations, source.ReadTypeKey, targetInput); err == nil {
				readRelations = append(readRelations, ObjectTypeRelation{SourceTypeKey: source.ReadTypeKey, TargetTypeKey: targetInput, Reliable: true})
			}
		}
	}
	slices.SortFunc(readRelations, func(left, right ObjectTypeRelation) int {
		leftKey := left.SourceTypeKey + "\x00" + left.TargetTypeKey
		rightKey := right.SourceTypeKey + "\x00" + right.TargetTypeKey
		if leftKey < rightKey {
			return -1
		}
		if leftKey > rightKey {
			return 1
		}
		return 0
	})
	readRelations = slices.CompactFunc(readRelations, func(left, right ObjectTypeRelation) bool {
		return left.SourceTypeKey == right.SourceTypeKey && left.TargetTypeKey == right.TargetTypeKey
	})
	plan, err := PlanObjectConversion(contract.Source, contract.Target, ObjectConversionRequest{Mode: ObjectConversionExplicitCopy, Profile: ObjectProfileStatic, ReadRelations: readRelations})
	if err != nil || plan.Decision != ObjectDecisionCopyNewIdentity || plan.PreservesIdentity {
		return nil, fmt.Errorf("object layout copy lacks explicit-copy semantic admission: %v", err)
	}
	if contract.SourceLayout.TypeKey != contract.Source.TypeKey || contract.TargetLayout.TypeKey != contract.Target.TypeKey {
		return nil, fmt.Errorf("object layout copy semantic/layout type binding mismatch")
	}
	if contract.SourceLayout.Target != contract.TargetLayout.Target {
		return nil, fmt.Errorf("object layout copy uses different target contexts")
	}
	if err := verifyObjectLayoutContractHash(contract.SourceLayout); err != nil {
		return nil, err
	}
	if err := verifyObjectLayoutContractHash(contract.TargetLayout); err != nil {
		return nil, err
	}
	sourceLayout := objectLayoutPropertiesByKey(contract.SourceLayout.Properties)
	targetLayout := objectLayoutPropertiesByKey(contract.TargetLayout.Properties)
	mappings := make([]ObjectLayoutCopyMapping, 0, len(contract.Target.Properties))
	for _, target := range contract.Target.Properties {
		if target.Kind != ObjectPropertyData || target.Optional || target.Visibility != "public" || target.PrivateIdentity != "" {
			return nil, fmt.Errorf("copy target property %q is not required public data", target.Key)
		}
		source, ok := sourceProperties[target.Key]
		if !ok {
			return nil, fmt.Errorf("%s", ObjectReasonPropertyMissing)
		}
		if source.Kind != ObjectPropertyData || source.Optional || source.Visibility != "public" || source.PrivateIdentity != "" {
			return nil, fmt.Errorf("copy source property %q is not required public data", source.Key)
		}
		targetInput := target.WriteTypeKey
		if targetInput == "" {
			targetInput = target.ReadTypeKey
		}
		if source.ReadTypeKey == "" || targetInput == "" {
			return nil, fmt.Errorf("%s", ObjectReasonReadTypeUnproven)
		}
		path, err := FindTypeRelationPath(contract.Relations, source.ReadTypeKey, targetInput)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", ObjectReasonReadTypeUnproven, err)
		}
		sourcePhysical, sourceOK := sourceLayout[source.Key]
		targetPhysical, targetOK := targetLayout[target.Key]
		if !sourceOK || !targetOK || sourcePhysical.Kind != ObjectPropertyData || targetPhysical.Kind != ObjectPropertyData || sourcePhysical.PresenceBit != -1 || targetPhysical.PresenceBit != -1 || sourcePhysical.Representation == "" || targetPhysical.Representation == "" {
			return nil, fmt.Errorf("copy property %q has incompatible physical layout", target.Key)
		}
		mappings = append(mappings, ObjectLayoutCopyMapping{PropertyKey: target.Key, ReadRelationPath: path, SourceFieldOffset: sourcePhysical.FieldOffset, SourceRepresentation: sourcePhysical.Representation, TargetFieldOffset: targetPhysical.FieldOffset, TargetRepresentation: targetPhysical.Representation})
	}
	return mappings, nil
}

func equalObjectLayoutCopyMappings(left, right []ObjectLayoutCopyMapping) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].PropertyKey != right[index].PropertyKey || left[index].SourceFieldOffset != right[index].SourceFieldOffset || left[index].SourceRepresentation != right[index].SourceRepresentation || left[index].TargetFieldOffset != right[index].TargetFieldOffset || left[index].TargetRepresentation != right[index].TargetRepresentation || !slices.Equal(left[index].ReadRelationPath, right[index].ReadRelationPath) {
			return false
		}
	}
	return true
}
