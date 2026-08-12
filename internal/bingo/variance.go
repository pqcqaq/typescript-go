package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const VarianceContractSchemaVersion uint32 = 1
const maxVarianceContractBytes = 96 << 10

type VariancePolarity string
type VarianceAnnotation string
type VarianceOccurrenceKind string
type VarianceHint string

const (
	VarianceUnused   VariancePolarity = "unused"
	VariancePositive VariancePolarity = "positive"
	VarianceNegative VariancePolarity = "negative"
	VarianceBoth     VariancePolarity = "both"
	VarianceUnknown  VariancePolarity = "unknown"

	VarianceAnnotationNone  VarianceAnnotation = "none"
	VarianceAnnotationOut   VarianceAnnotation = "out"
	VarianceAnnotationIn    VarianceAnnotation = "in"
	VarianceAnnotationInOut VarianceAnnotation = "in-out"

	VarianceReadonlyProperty  VarianceOccurrenceKind = "readonly-property"
	VarianceWritableProperty  VarianceOccurrenceKind = "writable-property"
	VarianceGetterResult      VarianceOccurrenceKind = "getter-result"
	VarianceSetterInput       VarianceOccurrenceKind = "setter-input"
	VarianceFunctionParameter VarianceOccurrenceKind = "function-parameter"
	VarianceFunctionReturn    VarianceOccurrenceKind = "function-return"
	VarianceMutableElement    VarianceOccurrenceKind = "mutable-element"
	VarianceReadonlyElement   VarianceOccurrenceKind = "readonly-element"
	VarianceInOut             VarianceOccurrenceKind = "inout"
	VarianceResidual          VarianceOccurrenceKind = "residual"
	VarianceDynamic           VarianceOccurrenceKind = "dynamic"
	VarianceExternOpaque      VarianceOccurrenceKind = "extern-opaque"

	VarianceHintIndependent   VarianceHint = "independent"
	VarianceHintCovariant     VarianceHint = "covariant"
	VarianceHintContravariant VarianceHint = "contravariant"
	VarianceHintInvariant     VarianceHint = "invariant"
	VarianceHintBivariant     VarianceHint = "bivariant"
	VarianceHintUnmeasurable  VarianceHint = "unmeasurable"
	VarianceHintUnreliable    VarianceHint = "unreliable"
)

const (
	VarianceReasonDirectCovariant     = "variance.direct_covariant"
	VarianceReasonDirectContravariant = "variance.direct_contravariant"
	VarianceReasonUnused              = "variance.unused"
	VarianceReasonInvariant           = "variance.invariant"
	VarianceReasonAnnotationInvariant = "variance.annotation_invariant"
	VarianceReasonAnnotationConflict  = "variance.annotation_conflict"
	VarianceReasonUnknown             = "variance.unknown"
	VarianceReasonHintBivariant       = "variance.hint_bivariant"
	VarianceReasonHintUnmeasurable    = "variance.hint_unmeasurable"
	VarianceReasonHintUnreliable      = "variance.hint_unreliable"
)

type VarianceParameter struct {
	ID         uint32             `json:"id"`
	Name       string             `json:"name"`
	Annotation VarianceAnnotation `json:"annotation"`
	TsgoHint   VarianceHint       `json:"tsgoHint"`
}

type VarianceOccurrence struct {
	ID          uint32                 `json:"id"`
	ParameterID uint32                 `json:"parameterId"`
	Kind        VarianceOccurrenceKind `json:"kind"`
	SourceOrder uint32                 `json:"sourceOrder"`
	Path        string                 `json:"path"`
}

type VarianceProof struct {
	ParameterID    uint32           `json:"parameterId"`
	Inferred       VariancePolarity `json:"inferred"`
	DirectABIReuse bool             `json:"directAbiReuse"`
	Reason         string           `json:"reason"`
}

type VarianceContract struct {
	SchemaVersion  uint32               `json:"schemaVersion"`
	DeclarationKey string               `json:"declarationKey"`
	Parameters     []VarianceParameter  `json:"parameters"`
	Occurrences    []VarianceOccurrence `json:"occurrences"`
	Proofs         []VarianceProof      `json:"proofs"`
	ContentHash    string               `json:"contentHash"`
}

func BuildVarianceContract(declarationKey string, parameters []VarianceParameter, occurrences []VarianceOccurrence) (VarianceContract, error) {
	contract := VarianceContract{SchemaVersion: VarianceContractSchemaVersion, DeclarationKey: declarationKey, Parameters: parameters, Occurrences: occurrences}
	proofs, err := deriveVarianceProofs(contract)
	if err != nil {
		return VarianceContract{}, err
	}
	contract.Proofs = proofs
	_, hash, err := CanonicalVarianceContract(contract)
	contract.ContentHash = hash
	return contract, err
}

func CanonicalVarianceContract(contract VarianceContract) ([]byte, string, error) {
	contract.ContentHash = ""
	if err := verifyVarianceContractStructure(contract); err != nil {
		return nil, "", err
	}
	wantProofs, err := deriveVarianceProofs(contract)
	if err != nil {
		return nil, "", err
	}
	if !equalVarianceProofs(contract.Proofs, wantProofs) {
		return nil, "", fmt.Errorf("variance proofs do not match canonical inference")
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

func VerifyCanonicalVarianceContract(contract VarianceContract) error {
	claimed := contract.ContentHash
	_, want, err := CanonicalVarianceContract(contract)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("variance contract content hash mismatch")
	}
	return nil
}

func DecodeVarianceContract(data []byte) (*VarianceContract, error) {
	if len(data) > maxVarianceContractBytes {
		return nil, fmt.Errorf("variance contract exceeds %d bytes", maxVarianceContractBytes)
	}
	var contract VarianceContract
	if err := jsonx.Unmarshal(data, &contract, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode variance contract: %w", err)
	}
	if err := VerifyCanonicalVarianceContract(contract); err != nil {
		return nil, err
	}
	return &contract, nil
}

func deriveVarianceProofs(contract VarianceContract) ([]VarianceProof, error) {
	if err := verifyVarianceInputs(contract); err != nil {
		return nil, err
	}
	states := make([]VariancePolarity, len(contract.Parameters))
	for index := range states {
		states[index] = VarianceUnused
	}
	for _, occurrence := range contract.Occurrences {
		polarity, err := varianceOccurrencePolarity(occurrence.Kind)
		if err != nil {
			return nil, err
		}
		states[occurrence.ParameterID-1] = joinVariancePolarity(states[occurrence.ParameterID-1], polarity)
	}
	proofs := make([]VarianceProof, len(contract.Parameters))
	for index, parameter := range contract.Parameters {
		proofs[index] = planVarianceProof(parameter, states[index])
	}
	return proofs, nil
}

func planVarianceProof(parameter VarianceParameter, inferred VariancePolarity) VarianceProof {
	proof := VarianceProof{ParameterID: parameter.ID, Inferred: inferred}
	if parameter.TsgoHint == VarianceHintUnmeasurable {
		proof.Inferred, proof.Reason = VarianceUnknown, VarianceReasonHintUnmeasurable
		return proof
	}
	if parameter.TsgoHint == VarianceHintUnreliable {
		proof.Inferred, proof.Reason = VarianceUnknown, VarianceReasonHintUnreliable
		return proof
	}
	if inferred == VarianceUnknown {
		proof.Reason = VarianceReasonUnknown
		return proof
	}
	if annotationConflicts(parameter.Annotation, inferred) {
		proof.Reason = VarianceReasonAnnotationConflict
		return proof
	}
	if parameter.Annotation == VarianceAnnotationInOut || parameter.Annotation == VarianceAnnotationNone {
		if inferred == VarianceUnused {
			proof.Reason = VarianceReasonUnused
		} else if parameter.Annotation == VarianceAnnotationInOut {
			proof.Reason = VarianceReasonAnnotationInvariant
		} else {
			proof.Reason = VarianceReasonInvariant
		}
		return proof
	}
	if parameter.TsgoHint == VarianceHintBivariant {
		proof.Reason = VarianceReasonHintBivariant
		return proof
	}
	switch inferred {
	case VarianceUnused:
		proof.DirectABIReuse, proof.Reason = true, VarianceReasonUnused
	case VariancePositive:
		proof.DirectABIReuse, proof.Reason = true, VarianceReasonDirectCovariant
	case VarianceNegative:
		proof.DirectABIReuse, proof.Reason = true, VarianceReasonDirectContravariant
	default:
		proof.Reason = VarianceReasonInvariant
	}
	return proof
}

func verifyVarianceContractStructure(contract VarianceContract) error {
	if contract.SchemaVersion != VarianceContractSchemaVersion || strings.TrimSpace(contract.DeclarationKey) == "" || len(contract.Parameters) == 0 {
		return fmt.Errorf("invalid variance contract header")
	}
	if err := verifyVarianceInputs(contract); err != nil {
		return err
	}
	if len(contract.Proofs) != len(contract.Parameters) {
		return fmt.Errorf("variance proof count mismatch")
	}
	return nil
}

func verifyVarianceInputs(contract VarianceContract) error {
	names := make(map[string]struct{}, len(contract.Parameters))
	for index, parameter := range contract.Parameters {
		if parameter.ID != uint32(index+1) || strings.TrimSpace(parameter.Name) == "" {
			return fmt.Errorf("invalid variance parameter %d", index+1)
		}
		if _, exists := names[parameter.Name]; exists {
			return fmt.Errorf("duplicate variance parameter %q", parameter.Name)
		}
		names[parameter.Name] = struct{}{}
		if !validVarianceAnnotation(parameter.Annotation) || !validVarianceHint(parameter.TsgoHint) {
			return fmt.Errorf("invalid variance parameter metadata %d", index+1)
		}
	}
	var previousParameter, previousOrder uint32
	previousPath := ""
	for index, occurrence := range contract.Occurrences {
		if occurrence.ID != uint32(index+1) || occurrence.ParameterID == 0 || int(occurrence.ParameterID) > len(contract.Parameters) || strings.TrimSpace(occurrence.Path) == "" {
			return fmt.Errorf("invalid variance occurrence %d", index+1)
		}
		if _, err := varianceOccurrencePolarity(occurrence.Kind); err != nil {
			return err
		}
		if index != 0 && (occurrence.ParameterID < previousParameter || occurrence.ParameterID == previousParameter && (occurrence.SourceOrder < previousOrder || occurrence.SourceOrder == previousOrder && occurrence.Path <= previousPath)) {
			return fmt.Errorf("variance occurrences are duplicated or not canonical")
		}
		previousParameter, previousOrder, previousPath = occurrence.ParameterID, occurrence.SourceOrder, occurrence.Path
	}
	return nil
}

func varianceOccurrencePolarity(kind VarianceOccurrenceKind) (VariancePolarity, error) {
	switch kind {
	case VarianceReadonlyProperty, VarianceGetterResult, VarianceFunctionReturn, VarianceReadonlyElement:
		return VariancePositive, nil
	case VarianceSetterInput, VarianceFunctionParameter:
		return VarianceNegative, nil
	case VarianceWritableProperty, VarianceMutableElement, VarianceInOut:
		return VarianceBoth, nil
	case VarianceResidual, VarianceDynamic, VarianceExternOpaque:
		return VarianceUnknown, nil
	default:
		return "", fmt.Errorf("unsupported variance occurrence kind %q", kind)
	}
}

func joinVariancePolarity(left, right VariancePolarity) VariancePolarity {
	if left == VarianceUnknown || right == VarianceUnknown {
		return VarianceUnknown
	}
	if left == VarianceUnused {
		return right
	}
	if right == VarianceUnused || left == right {
		return left
	}
	return VarianceBoth
}

func annotationConflicts(annotation VarianceAnnotation, inferred VariancePolarity) bool {
	switch annotation {
	case VarianceAnnotationOut:
		return inferred != VarianceUnused && inferred != VariancePositive
	case VarianceAnnotationIn:
		return inferred != VarianceUnused && inferred != VarianceNegative
	default:
		return false
	}
}

func validVarianceAnnotation(annotation VarianceAnnotation) bool {
	return annotation == VarianceAnnotationNone || annotation == VarianceAnnotationOut || annotation == VarianceAnnotationIn || annotation == VarianceAnnotationInOut
}

func validVarianceHint(hint VarianceHint) bool {
	switch hint {
	case VarianceHintIndependent, VarianceHintCovariant, VarianceHintContravariant, VarianceHintInvariant, VarianceHintBivariant, VarianceHintUnmeasurable, VarianceHintUnreliable:
		return true
	default:
		return false
	}
}

func equalVarianceProofs(left, right []VarianceProof) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
