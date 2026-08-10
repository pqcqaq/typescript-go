// Package buildplan contains the checker-free canonical backend request wire.
// It intentionally depends only on frontendwire value types and never imports
// the live TypeScript frontend.
package buildplan

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/internal/frontendwire"
	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const SchemaVersion uint32 = 1

type BackendRequest struct {
	Target      string                       `json:"target"`
	CPU         string                       `json:"cpu"`
	Features    []string                     `json:"features,omitempty"`
	Runtime     string                       `json:"runtime"`
	GC          frontendwire.GCMode          `json:"gc"`
	Exceptions  frontendwire.ExceptionMode   `json:"exceptions"`
	Overflow    frontendwire.OverflowMode    `json:"overflow"`
	BoundsCheck frontendwire.BoundsCheckMode `json:"boundsCheck"`
	Emit        []frontendwire.EmitArtifact  `json:"emit"`
	LLVMMajor   int                          `json:"llvmMajor"`
}

type Plan struct {
	SchemaVersion uint32               `json:"schemaVersion"`
	FrontendHash  string               `json:"frontendHash"`
	Profile       frontendwire.Profile `json:"profile"`
	Backend       BackendRequest       `json:"backend"`
	ContentHash   string               `json:"contentHash"`
}

type hashInput struct {
	SchemaVersion uint32               `json:"schemaVersion"`
	FrontendHash  string               `json:"frontendHash"`
	Profile       frontendwire.Profile `json:"profile"`
	Backend       BackendRequest       `json:"backend"`
}

func New(frontendHash string, profile frontendwire.Profile, backend BackendRequest) (Plan, error) {
	plan := Plan{SchemaVersion: SchemaVersion, FrontendHash: frontendHash, Profile: profile, Backend: cloneBackend(backend)}
	digest, err := ContentHash(plan)
	if err != nil {
		return Plan{}, err
	}
	plan.ContentHash = digest
	if err := Validate(plan); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func (plan Plan) CanonicalBytes() ([]byte, error) {
	if err := Validate(plan); err != nil {
		return nil, err
	}
	return jsonx.MarshalIndent(plan, "", "  ")
}

func Decode(data []byte) (*Plan, error) {
	var plan Plan
	if err := jsonx.Unmarshal(data, &plan, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode build plan: %w", err)
	}
	if err := Validate(plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

func Validate(plan Plan) error {
	if plan.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported build plan schema %d", plan.SchemaVersion)
	}
	if !isDigest(plan.FrontendHash) {
		return fmt.Errorf("invalid build plan frontend hash %q", plan.FrontendHash)
	}
	if !isDigest(plan.ContentHash) {
		return fmt.Errorf("invalid build plan content hash %q", plan.ContentHash)
	}
	switch plan.Profile {
	case frontendwire.ProfileStatic, frontendwire.ProfileInterop, frontendwire.ProfileUnsafe:
	case frontendwire.ProfileDynamic:
		return fmt.Errorf("build plan profile %q is unavailable", plan.Profile)
	default:
		return fmt.Errorf("build plan profile %q is invalid", plan.Profile)
	}
	if err := validateCanonicalBackend(plan.Backend); err != nil {
		return err
	}
	want, err := ContentHash(plan)
	if err != nil {
		return err
	}
	if plan.ContentHash != want {
		return fmt.Errorf("build plan content hash mismatch: got %s, want %s", plan.ContentHash, want)
	}
	return nil
}

func ContentHash(plan Plan) (string, error) {
	encoded, err := jsonx.Marshal(hashInput{plan.SchemaVersion, plan.FrontendHash, plan.Profile, plan.Backend}, jsonx.Deterministic(true))
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func EqualBackendRequest(left, right BackendRequest) bool {
	return left.Target == right.Target && left.CPU == right.CPU && slices.Equal(left.Features, right.Features) &&
		left.Runtime == right.Runtime && left.GC == right.GC && left.Exceptions == right.Exceptions && left.Overflow == right.Overflow &&
		left.BoundsCheck == right.BoundsCheck && slices.Equal(left.Emit, right.Emit) && left.LLVMMajor == right.LLVMMajor
}

func validateCanonicalBackend(backend BackendRequest) error {
	if !canonicalOptionalString(backend.Target) {
		return fmt.Errorf("build plan backend request is not canonical: target %q", backend.Target)
	}
	if !canonicalString(backend.CPU) {
		return fmt.Errorf("build plan backend request is not canonical: CPU %q", backend.CPU)
	}
	if !canonicalString(backend.Runtime) || backend.LLVMMajor == 0 {
		return fmt.Errorf("build plan backend has missing runtime, CPU, or LLVM major")
	}
	if backend.Features != nil && !canonicalStringSet(backend.Features) {
		return fmt.Errorf("build plan features are not canonical")
	}
	if !canonicalEmitSet(backend.Emit) {
		return fmt.Errorf("build plan emit set is not canonical")
	}
	switch backend.GC {
	case frontendwire.GCTracing, frontendwire.GCArc, frontendwire.GCArena:
	default:
		return fmt.Errorf("build plan GC mode %q is invalid", backend.GC)
	}
	switch backend.Exceptions {
	case frontendwire.ExceptionsNone:
	case frontendwire.ExceptionsLLVMEH:
		return fmt.Errorf("build plan exception mode %q is unavailable", backend.Exceptions)
	default:
		return fmt.Errorf("build plan exception mode %q is invalid", backend.Exceptions)
	}
	if backend.Overflow != frontendwire.OverflowJSNumber {
		return fmt.Errorf("build plan overflow mode %q is invalid", backend.Overflow)
	}
	switch backend.BoundsCheck {
	case frontendwire.BoundsCheckOn, frontendwire.BoundsCheckOff:
	default:
		return fmt.Errorf("build plan bounds-check mode %q is invalid", backend.BoundsCheck)
	}
	return nil
}

func canonicalStringSet(values []string) bool {
	for index, value := range values {
		if strings.TrimSpace(value) != value || strings.ToLower(value) != value || value == "" || index > 0 && values[index-1] >= value {
			return false
		}
	}
	return true
}

func canonicalEmitSet(values []frontendwire.EmitArtifact) bool {
	if values == nil {
		return false
	}
	if len(values) == 0 {
		return true
	}
	order := map[frontendwire.EmitArtifact]int{frontendwire.EmitHIR: 0, frontendwire.EmitMIR: 1, frontendwire.EmitLLVM: 2, frontendwire.EmitObject: 3}
	previous := -1
	seen := make(map[frontendwire.EmitArtifact]struct{}, len(values))
	for _, value := range values {
		position, ok := order[value]
		if !ok || position <= previous {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
		previous = position
	}
	return true
}

func canonicalString(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && strings.ToLower(value) == value
}

func canonicalOptionalString(value string) bool {
	return value == "" || canonicalString(value)
}

func cloneBackend(input BackendRequest) BackendRequest {
	input.Features = slices.Clone(input.Features)
	input.Emit = slices.Clone(input.Emit)
	return input
}

func isDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}
