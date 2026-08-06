package frontendwire

import (
	"encoding/json"
	"fmt"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

// FrontendSnapshot is the target-independent disk handoff consumed by replay.
type FrontendSnapshot struct {
	SchemaVersion uint32          `json:"schemaVersion"`
	Program       ProgramSnapshot `json:"program"`
	ContentHash   string          `json:"contentHash"`
}

// CanonicalBytes returns the validated deterministic disk representation.
func (s FrontendSnapshot) CanonicalBytes() ([]byte, error) {
	if err := ValidateFrontendSnapshot(s); err != nil {
		return nil, err
	}
	return json.MarshalIndent(s, "", "  ")
}

// DecodeFrontendSnapshot rejects unknown fields and validates the complete
// nested program before returning it to a lowering consumer.
func DecodeFrontendSnapshot(data []byte) (*FrontendSnapshot, error) {
	var snapshot FrontendSnapshot
	if err := jsonx.Unmarshal(data, &snapshot, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode frontend snapshot: %w", err)
	}
	if err := ValidateFrontendSnapshot(snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func NewFrontendSnapshot(snapshot ProgramSnapshot) (FrontendSnapshot, error) {
	if err := ValidateProgramSnapshot(snapshot); err != nil {
		return FrontendSnapshot{}, err
	}
	return FrontendSnapshot{SchemaVersion: SnapshotSchemaVersion, Program: snapshot, ContentHash: snapshot.ContentHash}, nil
}

func ValidateFrontendSnapshot(frontend FrontendSnapshot) error {
	if frontend.SchemaVersion != SnapshotSchemaVersion {
		return fmt.Errorf("unsupported frontend snapshot schema %d", frontend.SchemaVersion)
	}
	if !isDigest(frontend.ContentHash) {
		return fmt.Errorf("invalid frontend snapshot hash %q", frontend.ContentHash)
	}
	if err := ValidateProgramSnapshot(frontend.Program); err != nil {
		return fmt.Errorf("validate frontend program: %w", err)
	}
	if frontend.ContentHash != frontend.Program.ContentHash {
		return fmt.Errorf("frontend snapshot hash does not match verified program projection")
	}
	return nil
}
