package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const PropertyAccessHIRSchemaVersion uint32 = 1
const maxPropertyAccessHIRBytes = 1 << 20

type PropertyAccessHIRInput struct {
	FunctionName     string                  `json:"functionName"`
	AccessNodeID     string                  `json:"accessNodeId"`
	ReceiverTypeHash string                  `json:"receiverTypeHash"`
	KeyTypeHash      string                  `json:"keyTypeHash"`
	Admission        PropertyAccessAdmission `json:"admission"`
}

type PropertyAccessHIROperation struct {
	ID            ValueID   `json:"id"`
	Kind          string    `json:"kind"`
	Operands      []ValueID `json:"operands"`
	PropertyKey   string    `json:"propertyKey,omitempty"`
	DispatchKeys  []string  `json:"dispatchKeys"`
	AdmissionHash string    `json:"admissionHash"`
	BoundaryHash  string    `json:"boundaryHash,omitempty"`
	Effects       []Effect  `json:"effects"`
}

type PropertyAccessHIRFunction struct {
	ID            FunctionID                   `json:"id"`
	Name          string                       `json:"name"`
	AccessNodeID  string                       `json:"accessNodeId"`
	Operations    []PropertyAccessHIROperation `json:"operations"`
	ReturnValueID ValueID                      `json:"returnValueId"`
}

type PropertyAccessHIRArtifact struct {
	SchemaVersion        uint32                      `json:"schemaVersion"`
	FrontendSnapshotHash string                      `json:"frontendSnapshotHash"`
	ReplayHash           string                      `json:"replayHash"`
	Inputs               []PropertyAccessHIRInput    `json:"inputs"`
	Functions            []PropertyAccessHIRFunction `json:"functions"`
	ContentHash          string                      `json:"contentHash"`
}

func BuildPropertyAccessHIRArtifact(frontendHash, replayHash string, inputs []PropertyAccessHIRInput) (PropertyAccessHIRArtifact, error) {
	artifact := PropertyAccessHIRArtifact{SchemaVersion: PropertyAccessHIRSchemaVersion, FrontendSnapshotHash: frontendHash, ReplayHash: replayHash, Inputs: slices.Clone(inputs)}
	functions, err := derivePropertyAccessHIR(artifact)
	if err != nil {
		return PropertyAccessHIRArtifact{}, err
	}
	artifact.Functions = functions
	_, hash, err := CanonicalPropertyAccessHIRArtifact(artifact)
	artifact.ContentHash = hash
	return artifact, err
}

func CanonicalPropertyAccessHIRArtifact(artifact PropertyAccessHIRArtifact) ([]byte, string, error) {
	artifact.ContentHash = ""
	if artifact.SchemaVersion != PropertyAccessHIRSchemaVersion || !validSHA256Hex(artifact.FrontendSnapshotHash) || !validSHA256Hex(artifact.ReplayHash) {
		return nil, "", fmt.Errorf("invalid property access HIR header")
	}
	want, err := derivePropertyAccessHIR(artifact)
	if err != nil {
		return nil, "", err
	}
	left, _ := jsonx.Marshal(artifact.Functions)
	right, _ := jsonx.Marshal(want)
	if !slices.Equal(left, right) {
		return nil, "", fmt.Errorf("property access HIR does not match canonical lowering")
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

func VerifyCanonicalPropertyAccessHIRArtifact(artifact PropertyAccessHIRArtifact) error {
	claimed := artifact.ContentHash
	_, want, err := CanonicalPropertyAccessHIRArtifact(artifact)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("property access HIR content hash mismatch")
	}
	return nil
}

func DecodePropertyAccessHIRArtifact(data []byte) (*PropertyAccessHIRArtifact, error) {
	if len(data) > maxPropertyAccessHIRBytes {
		return nil, fmt.Errorf("property access HIR exceeds %d bytes", maxPropertyAccessHIRBytes)
	}
	var artifact PropertyAccessHIRArtifact
	if err := jsonx.Unmarshal(data, &artifact, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode property access HIR: %w", err)
	}
	if err := VerifyCanonicalPropertyAccessHIRArtifact(artifact); err != nil {
		return nil, err
	}
	return &artifact, nil
}

func derivePropertyAccessHIR(artifact PropertyAccessHIRArtifact) ([]PropertyAccessHIRFunction, error) {
	if len(artifact.Inputs) != 4 {
		return nil, fmt.Errorf("property access HIR requires four inputs")
	}
	wantNames := []string{"direct", "dynamic", "finite", "literal"}
	functions := make([]PropertyAccessHIRFunction, len(artifact.Inputs))
	for i, input := range artifact.Inputs {
		if input.FunctionName != wantNames[i] || strings.TrimSpace(input.AccessNodeID) == "" || !validSHA256Hex(input.ReceiverTypeHash) || !validSHA256Hex(input.KeyTypeHash) {
			return nil, fmt.Errorf("invalid property access HIR input")
		}
		if err := VerifyCanonicalPropertyAccessAdmission(input.Admission); err != nil {
			return nil, err
		}
		ops, ret, err := propertyAccessHIROperations(input)
		if err != nil {
			return nil, err
		}
		functions[i] = PropertyAccessHIRFunction{ID: FunctionID(i + 1), Name: input.FunctionName, AccessNodeID: input.AccessNodeID, Operations: ops, ReturnValueID: ret}
	}
	return functions, nil
}

func propertyAccessHIROperations(input PropertyAccessHIRInput) ([]PropertyAccessHIROperation, ValueID, error) {
	hash := input.Admission.ContentHash
	pure := []Effect{EffectPure}
	read := []Effect{EffectRead}
	switch input.Admission.Decision {
	case PropertyAccessPlaceRef:
		key := input.Admission.Keys[0]
		return []PropertyAccessHIROperation{{ID: 1, Kind: "receiver.eval", AdmissionHash: hash, DispatchKeys: []string{}, Effects: pure}, {ID: 2, Kind: "place.make", Operands: []ValueID{1}, PropertyKey: key, AdmissionHash: hash, DispatchKeys: []string{}, Effects: pure}, {ID: 3, Kind: "place.load", Operands: []ValueID{2}, PropertyKey: key, AdmissionHash: hash, DispatchKeys: []string{}, Effects: read}}, 3, nil
	case PropertyAccessFiniteDispatch:
		keys := slices.Clone(input.Admission.Keys)
		return []PropertyAccessHIROperation{{ID: 1, Kind: "receiver.eval", AdmissionHash: hash, DispatchKeys: []string{}, Effects: pure}, {ID: 2, Kind: "key.eval", AdmissionHash: hash, DispatchKeys: keys, Effects: pure}, {ID: 3, Kind: "key.dispatch", Operands: []ValueID{2}, AdmissionHash: hash, DispatchKeys: keys, Effects: pure}, {ID: 4, Kind: "place.load.case", Operands: []ValueID{1, 3}, PropertyKey: keys[0], AdmissionHash: hash, DispatchKeys: []string{}, Effects: read}, {ID: 5, Kind: "place.load.case", Operands: []ValueID{1, 3}, PropertyKey: keys[1], AdmissionHash: hash, DispatchKeys: []string{}, Effects: read}, {ID: 6, Kind: "phi", Operands: []ValueID{4, 5}, AdmissionHash: hash, DispatchKeys: keys, Effects: pure}}, 6, nil
	case PropertyAccessDynamicBoundary:
		if input.Admission.Boundary == nil {
			return nil, 0, fmt.Errorf("dynamic HIR input has no boundary")
		}
		boundary := input.Admission.Boundary.ContentHash
		return []PropertyAccessHIROperation{{ID: 1, Kind: "host.call", AdmissionHash: hash, BoundaryHash: boundary, DispatchKeys: []string{}, Effects: []Effect{EffectCall}}, {ID: 2, Kind: "key.eval", AdmissionHash: hash, BoundaryHash: boundary, DispatchKeys: []string{}, Effects: pure}, {ID: 3, Kind: "dynamic.boundary.enter", Operands: []ValueID{1}, AdmissionHash: hash, BoundaryHash: boundary, DispatchKeys: []string{}, Effects: []Effect{EffectCall}}, {ID: 4, Kind: "dynamic.property.load", Operands: []ValueID{3, 2}, AdmissionHash: hash, BoundaryHash: boundary, DispatchKeys: []string{}, Effects: slices.Clone(input.Admission.Effects)}}, 4, nil
	default:
		return nil, 0, fmt.Errorf("property access HIR cannot lower decision %q", input.Admission.Decision)
	}
}
