package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const ObjectViewHIRArtifactSchemaVersion uint32 = 1
const maxObjectViewHIRArtifactBytes = 2 << 20

// ObjectViewHIRArtifact is an additive reader major. Historical HIR readers
// remain frozen; this artifact owns the verified base HIR and one explicit
// readonly view operation.
type ObjectViewHIRArtifact struct {
	SchemaVersion uint32              `json:"schemaVersion"`
	BaseHIRHash   string              `json:"baseHirHash"`
	Gate          ObjectViewHIRGate   `json:"gate"`
	Operation     ObjectViewOperation `json:"operation"`
	ContentHash   string              `json:"contentHash"`
}

func BuildObjectViewHIRArtifact(gate ObjectViewHIRGate) (ObjectViewHIRArtifact, error) {
	if err := VerifyCanonicalObjectViewHIRGate(gate); err != nil {
		return ObjectViewHIRArtifact{}, err
	}
	operation, err := BuildObjectViewOperation(gate)
	if err != nil {
		return ObjectViewHIRArtifact{}, err
	}
	artifact := ObjectViewHIRArtifact{SchemaVersion: ObjectViewHIRArtifactSchemaVersion, BaseHIRHash: gate.HIR.ContentHash, Gate: gate, Operation: operation}
	_, hash, err := CanonicalObjectViewHIRArtifact(artifact)
	artifact.ContentHash = hash
	return artifact, err
}

func CanonicalObjectViewHIRArtifact(artifact ObjectViewHIRArtifact) ([]byte, string, error) {
	artifact.ContentHash = ""
	if artifact.SchemaVersion != ObjectViewHIRArtifactSchemaVersion {
		return nil, "", fmt.Errorf("unsupported ObjectView HIR artifact schema %d", artifact.SchemaVersion)
	}
	if err := VerifyCanonicalObjectViewHIRGate(artifact.Gate); err != nil {
		return nil, "", err
	}
	if err := VerifyCanonicalObjectViewOperation(artifact.Operation); err != nil {
		return nil, "", err
	}
	if artifact.BaseHIRHash == "" || artifact.BaseHIRHash != artifact.Gate.HIR.ContentHash {
		return nil, "", fmt.Errorf("ObjectView base HIR hash mismatch")
	}
	if artifact.Operation.FunctionID != artifact.Gate.FunctionID || artifact.Operation.SourceValueID != artifact.Gate.SourceValueID || artifact.Operation.View.ContentHash != artifact.Gate.View.ContentHash {
		return nil, "", fmt.Errorf("ObjectView HIR operation/gate binding mismatch")
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

func VerifyCanonicalObjectViewHIRArtifact(artifact ObjectViewHIRArtifact) error {
	claimed := artifact.ContentHash
	_, want, err := CanonicalObjectViewHIRArtifact(artifact)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("ObjectView HIR artifact content hash mismatch")
	}
	return nil
}
func DecodeObjectViewHIRArtifact(data []byte) (*ObjectViewHIRArtifact, error) {
	if len(data) > maxObjectViewHIRArtifactBytes {
		return nil, fmt.Errorf("ObjectView HIR artifact exceeds %d bytes", maxObjectViewHIRArtifactBytes)
	}
	var artifact ObjectViewHIRArtifact
	if err := jsonx.Unmarshal(data, &artifact, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode ObjectView HIR artifact: %w", err)
	}
	if err := VerifyCanonicalObjectViewHIRArtifact(artifact); err != nil {
		return nil, err
	}
	return &artifact, nil
}
