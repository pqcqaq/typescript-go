package bingo

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// PassArtifactEnvelopeSchemaVersion is the wire contract for the immutable
// sidecar artifact store carried by PassState.
const PassArtifactEnvelopeSchemaVersion uint32 = 1

// PassArtifactName identifies one proof-bearing value in a pass envelope.
// Names describe semantic roles; Schema identifies the concrete wire type.
type PassArtifactName string

const (
	PassArtifactTypedHIR                   PassArtifactName = "typed-hir"
	PassArtifactBuildPlan                  PassArtifactName = "canonical-build-plan"
	PassArtifactRuntimeManifest            PassArtifactName = "runtime-manifest"
	PassArtifactToolchainManifest          PassArtifactName = "toolchain-manifest"
	PassArtifactTargetContext              PassArtifactName = "target-context"
	PassArtifactDataLayout                 PassArtifactName = "data-layout"
	PassArtifactAvailableCapabilityCatalog PassArtifactName = "available-capability-catalog"
	PassArtifactRepresentationPlan         PassArtifactName = "representation-plan"
)

// PassArtifactRequirement declares the exact named wire type a pass consumes.
type PassArtifactRequirement struct {
	Name   PassArtifactName `json:"name"`
	Schema string           `json:"schema"`
}

// PassArtifactWrite declares one immutable artifact a pass must add. When
// FromPrimary is true, the executor binds the pass's canonical primary JSON
// artifact under Name instead of trusting the handler to duplicate it.
type PassArtifactWrite struct {
	Name        PassArtifactName `json:"name"`
	Schema      string           `json:"schema"`
	FromPrimary bool             `json:"fromPrimary,omitempty"`
}

// PassArtifact is one canonical, named and typed JSON value. Digest binds the
// name and schema as well as Payload, preventing role or schema substitution.
type PassArtifact struct {
	Name    PassArtifactName `json:"name"`
	Schema  string           `json:"schema"`
	Payload json.RawMessage  `json:"payload"`
	Digest  string           `json:"digest"`
}

// PassArtifactEnvelope retains independent pass inputs and outputs without
// treating string Facts as provenance. Artifacts are name-sorted and immutable.
type PassArtifactEnvelope struct {
	SchemaVersion uint32         `json:"schemaVersion"`
	Artifacts     []PassArtifact `json:"artifacts"`
	Digest        string         `json:"digest"`
}

// NewPassArtifact canonicalizes payload and computes its role-bound digest.
func NewPassArtifact(name PassArtifactName, schema string, payload json.RawMessage) (PassArtifact, error) {
	artifact, err := canonicalPassArtifactFields(PassArtifact{Name: name, Schema: schema, Payload: payload})
	if err != nil {
		return PassArtifact{}, err
	}
	digest, err := passArtifactDigest(artifact)
	if err != nil {
		return PassArtifact{}, err
	}
	artifact.Digest = digest
	return artifact, nil
}

// CanonicalBytes verifies the artifact digest and returns its stable wire form.
func (a PassArtifact) CanonicalBytes() ([]byte, error) {
	artifact, err := normalizePassArtifact(a)
	if err != nil {
		return nil, err
	}
	return json.Marshal(artifact)
}

// NewPassArtifactEnvelope verifies and sorts artifacts before computing the
// envelope digest. A name may occur only once, regardless of schema.
func NewPassArtifactEnvelope(artifacts ...PassArtifact) (PassArtifactEnvelope, error) {
	normalized, err := normalizePassArtifacts(artifacts)
	if err != nil {
		return PassArtifactEnvelope{}, err
	}
	if len(normalized) == 0 {
		return PassArtifactEnvelope{}, fmt.Errorf("pass artifact envelope is empty")
	}
	envelope := PassArtifactEnvelope{
		SchemaVersion: PassArtifactEnvelopeSchemaVersion,
		Artifacts:     normalized,
	}
	digest, err := passArtifactEnvelopeDigest(envelope)
	if err != nil {
		return PassArtifactEnvelope{}, err
	}
	envelope.Digest = digest
	return envelope, nil
}

// CanonicalBytes verifies every nested digest and the envelope digest before
// returning the stable wire representation.
func (e PassArtifactEnvelope) CanonicalBytes() ([]byte, error) {
	envelope, err := normalizePassArtifactEnvelope(e)
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope)
}

// Artifact returns a detached artifact with the requested semantic name.
func (e PassArtifactEnvelope) Artifact(name PassArtifactName) (PassArtifact, bool) {
	for _, artifact := range e.Artifacts {
		if artifact.Name == name {
			return clonePassArtifact(artifact), true
		}
	}
	return PassArtifact{}, false
}

// WithNamedArtifacts adds immutable artifacts to a detached PassState copy.
// Replacing an existing name is deliberately unsupported.
func (s PassState) WithNamedArtifacts(artifacts ...PassArtifact) (PassState, error) {
	result := clonePassState(s)
	all := make([]PassArtifact, 0, len(artifacts))
	if result.Artifacts != nil {
		envelope, err := normalizePassArtifactEnvelope(*result.Artifacts)
		if err != nil {
			return PassState{}, fmt.Errorf("existing pass artifact envelope: %w", err)
		}
		all = append(all, envelope.Artifacts...)
	}
	all = append(all, artifacts...)
	envelope, err := NewPassArtifactEnvelope(all...)
	if err != nil {
		return PassState{}, err
	}
	result.Artifacts = &envelope
	return result, nil
}

// NamedArtifact returns a detached proof artifact from the state envelope.
func (s PassState) NamedArtifact(name PassArtifactName) (PassArtifact, bool) {
	if s.Artifacts == nil {
		return PassArtifact{}, false
	}
	return s.Artifacts.Artifact(name)
}

func canonicalPassArtifactFields(input PassArtifact) (PassArtifact, error) {
	name := PassArtifactName(strings.TrimSpace(string(input.Name)))
	if name == "" {
		return PassArtifact{}, fmt.Errorf("pass artifact name is empty")
	}
	schema := strings.TrimSpace(input.Schema)
	if schema == "" {
		return PassArtifact{}, fmt.Errorf("pass artifact %q schema is empty", name)
	}
	payload, err := canonicalJSON(input.Payload)
	if err != nil {
		return PassArtifact{}, fmt.Errorf("pass artifact %q payload: %w", name, err)
	}
	return PassArtifact{Name: name, Schema: schema, Payload: payload}, nil
}

func normalizePassArtifact(input PassArtifact) (PassArtifact, error) {
	artifact, err := canonicalPassArtifactFields(input)
	if err != nil {
		return PassArtifact{}, err
	}
	want, err := passArtifactDigest(artifact)
	if err != nil {
		return PassArtifact{}, err
	}
	if input.Digest == "" || input.Digest != want {
		return PassArtifact{}, fmt.Errorf("pass artifact %q digest mismatch: got %q, want %q", artifact.Name, input.Digest, want)
	}
	artifact.Digest = want
	return artifact, nil
}

func normalizePassArtifacts(input []PassArtifact) ([]PassArtifact, error) {
	result := make([]PassArtifact, len(input))
	for index, artifact := range input {
		normalized, err := normalizePassArtifact(artifact)
		if err != nil {
			return nil, fmt.Errorf("artifact %d: %w", index, err)
		}
		result[index] = normalized
	}
	slices.SortFunc(result, func(left, right PassArtifact) int {
		if order := strings.Compare(string(left.Name), string(right.Name)); order != 0 {
			return order
		}
		return strings.Compare(left.Schema, right.Schema)
	})
	for index := 1; index < len(result); index++ {
		if result[index-1].Name == result[index].Name {
			return nil, fmt.Errorf("duplicate pass artifact %q", result[index].Name)
		}
	}
	return result, nil
}

func normalizePassArtifactEnvelope(input PassArtifactEnvelope) (PassArtifactEnvelope, error) {
	if input.SchemaVersion != PassArtifactEnvelopeSchemaVersion {
		return PassArtifactEnvelope{}, fmt.Errorf("unsupported pass artifact envelope schema %d", input.SchemaVersion)
	}
	artifacts, err := normalizePassArtifacts(input.Artifacts)
	if err != nil {
		return PassArtifactEnvelope{}, err
	}
	if len(artifacts) == 0 {
		return PassArtifactEnvelope{}, fmt.Errorf("pass artifact envelope is empty")
	}
	envelope := PassArtifactEnvelope{SchemaVersion: input.SchemaVersion, Artifacts: artifacts}
	want, err := passArtifactEnvelopeDigest(envelope)
	if err != nil {
		return PassArtifactEnvelope{}, err
	}
	if input.Digest == "" || input.Digest != want {
		return PassArtifactEnvelope{}, fmt.Errorf("pass artifact envelope digest mismatch: got %q, want %q", input.Digest, want)
	}
	envelope.Digest = want
	return envelope, nil
}

func normalizePassArtifactEnvelopePointer(input *PassArtifactEnvelope) (*PassArtifactEnvelope, error) {
	if input == nil {
		return nil, nil
	}
	envelope, err := normalizePassArtifactEnvelope(*input)
	if err != nil {
		return nil, err
	}
	return &envelope, nil
}

func passArtifactDigest(artifact PassArtifact) (string, error) {
	payload := struct {
		Name    PassArtifactName `json:"name"`
		Schema  string           `json:"schema"`
		Payload json.RawMessage  `json:"payload"`
	}{Name: artifact.Name, Schema: artifact.Schema, Payload: artifact.Payload}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func passArtifactEnvelopeDigest(envelope PassArtifactEnvelope) (string, error) {
	payload := struct {
		SchemaVersion uint32         `json:"schemaVersion"`
		Artifacts     []PassArtifact `json:"artifacts"`
	}{SchemaVersion: envelope.SchemaVersion, Artifacts: envelope.Artifacts}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func equalPassArtifact(left, right PassArtifact) bool {
	return left.Name == right.Name && left.Schema == right.Schema && left.Digest == right.Digest &&
		bytes.Equal(left.Payload, right.Payload)
}

func equalPassArtifactEnvelopes(left, right *PassArtifactEnvelope) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	if left.SchemaVersion != right.SchemaVersion || left.Digest != right.Digest || len(left.Artifacts) != len(right.Artifacts) {
		return false
	}
	for index := range left.Artifacts {
		if !equalPassArtifact(left.Artifacts[index], right.Artifacts[index]) {
			return false
		}
	}
	return true
}

func clonePassArtifact(input PassArtifact) PassArtifact {
	input.Payload = slices.Clone(input.Payload)
	return input
}

func clonePassArtifactEnvelope(input *PassArtifactEnvelope) *PassArtifactEnvelope {
	if input == nil {
		return nil
	}
	result := *input
	result.Artifacts = make([]PassArtifact, len(input.Artifacts))
	for index, artifact := range input.Artifacts {
		result.Artifacts[index] = clonePassArtifact(artifact)
	}
	return &result
}
