package tsfrontend

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/internal/ast"
)

// KindManifestSchemaVersion is the checked-in AST support inventory contract.
const KindManifestSchemaVersion uint32 = 2

// KindSupportLevel is the support-matrix level currently planned for a Kind.
type KindSupportLevel string

const (
	KindSupportS0          KindSupportLevel = "S0"
	KindSupportS1          KindSupportLevel = "S1"
	KindSupportS2          KindSupportLevel = "S2"
	KindSupportCompileOnly KindSupportLevel = "C"
	KindSupportPlanned     KindSupportLevel = "P"
	KindSupportReject      KindSupportLevel = "R"
)

// KindGateDecision is the subset gate's default action before node-specific
// type, assertion, and capability checks refine it.
type KindGateDecision string

const (
	KindDecisionAcceptDirect  KindGateDecision = "accept-direct"
	KindDecisionAcceptErase   KindGateDecision = "accept-erase"
	KindDecisionAcceptDesugar KindGateDecision = "accept-desugar"
	KindDecisionAcceptRuntime KindGateDecision = "accept-runtime"
	KindDecisionDeferFeature  KindGateDecision = "defer-feature"
	KindDecisionReject        KindGateDecision = "reject"
)

// KindManifestEntry is one explicit row in the 0..ast.KindCount inventory.
// GateHandler resolves through the concrete subset-gate registry. LoweringPlan
// is a future implementation plan, not a claim that a lowerer is bound today.
type KindManifestEntry struct {
	Kind            string             `json:"kind"`
	KindValue       int16              `json:"kindValue"`
	Domain          string             `json:"domain"`
	SyntaxGroup     string             `json:"syntaxGroup"`
	PlannedLevels   []KindSupportLevel `json:"plannedLevels"`
	DecisionPolicy  string             `json:"decisionPolicy"`
	DefaultDecision KindGateDecision   `json:"defaultDecision"`
	Feature         string             `json:"feature"`
	Capability      string             `json:"capability"`
	GateHandler     string             `json:"gateHandler"`
	// LoweringPlan names the intended lowering family for accepted Kinds.
	LoweringPlan string `json:"loweringPlan,omitempty"`
}

// KindManifestDocument is the versioned checked-in support inventory.
type KindManifestDocument struct {
	SchemaVersion uint32              `json:"schemaVersion"`
	Kinds         []KindManifestEntry `json:"kinds"`
}

type kindGateHandler func(NodeSnapshot, KindManifestEntry, Profile, subsetFacts) *Diagnostic

type kindGateHandlerDefinition struct {
	Name      string
	Decisions []KindGateDecision
	Handle    kindGateHandler
}

var kindGateHandlerRegistry = []kindGateHandlerDefinition{
	{Name: "gateAssertion", Decisions: []KindGateDecision{KindDecisionAcceptDesugar}, Handle: gateAssertionKind},
	{Name: "gateCapability", Decisions: []KindGateDecision{KindDecisionAcceptRuntime}, Handle: gateCapabilityKind},
	{Name: "gateErase", Decisions: []KindGateDecision{KindDecisionAcceptErase}, Handle: gateEraseKind},
	{Name: "gateFeature", Decisions: []KindGateDecision{KindDecisionDeferFeature, KindDecisionReject}, Handle: gateFeatureKind},
	{Name: "gateRecovery", Decisions: []KindGateDecision{KindDecisionReject}, Handle: gateRecoveryKind},
	{Name: "gateSyntax", Decisions: []KindGateDecision{KindDecisionAcceptDirect, KindDecisionAcceptDesugar}, Handle: gateSyntaxKind},
}

//go:generate go run ./kind_manifest_gen -out kind_manifest.json

//go:embed kind_manifest.json
var embeddedKindManifestJSON []byte

// LoadKindManifest parses and validates a fresh copy of the checked-in
// inventory. It retains no shared mutable slices.
func LoadKindManifest() (KindManifestDocument, error) {
	decoder := json.NewDecoder(bytes.NewReader(embeddedKindManifestJSON))
	decoder.DisallowUnknownFields()
	var document KindManifestDocument
	if err := decoder.Decode(&document); err != nil {
		return KindManifestDocument{}, fmt.Errorf("decode Kind manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return KindManifestDocument{}, fmt.Errorf("decode Kind manifest: multiple JSON values")
		}
		return KindManifestDocument{}, fmt.Errorf("decode Kind manifest: %w", err)
	}
	if err := ValidateKindManifestDocument(document); err != nil {
		return KindManifestDocument{}, err
	}
	return document, nil
}

// KindManifest returns a detached numeric-order inventory.
func KindManifest() ([]KindManifestEntry, error) {
	document, err := LoadKindManifest()
	if err != nil {
		return nil, err
	}
	return cloneKindManifestEntries(document.Kinds), nil
}

// ValidateKindManifest checks the embedded manifest against this tsgo checkout.
func ValidateKindManifest() error {
	_, err := LoadKindManifest()
	return err
}

// ValidateKindManifestDocument enforces schema, freshness, handler, decision,
// and test-case invariants on a caller-provided detached document.
func ValidateKindManifestDocument(document KindManifestDocument) error {
	if document.SchemaVersion != KindManifestSchemaVersion {
		return fmt.Errorf("unsupported Kind manifest schema %d", document.SchemaVersion)
	}
	if err := validateKindGateHandlerRegistry(kindGateHandlerRegistry); err != nil {
		return fmt.Errorf("Kind gate handler registry: %w", err)
	}
	if got, want := len(document.Kinds), int(ast.KindCount); got != want {
		return fmt.Errorf("Kind manifest contains %d rows, want %d", got, want)
	}
	seenKinds := make(map[string]struct{}, len(document.Kinds))
	seenValues := make(map[int16]struct{}, len(document.Kinds))
	for index, entry := range document.Kinds {
		expectedKind := ast.Kind(index)
		if entry.KindValue != int16(expectedKind) {
			return fmt.Errorf("Kind manifest row %d has value %d, want %d", index, entry.KindValue, expectedKind)
		}
		if entry.Kind != expectedKind.String() {
			return fmt.Errorf("Kind manifest row %d is %q, want %q", index, entry.Kind, expectedKind.String())
		}
		if _, duplicate := seenKinds[entry.Kind]; duplicate {
			return fmt.Errorf("duplicate Kind manifest name %q", entry.Kind)
		}
		seenKinds[entry.Kind] = struct{}{}
		if _, duplicate := seenValues[entry.KindValue]; duplicate {
			return fmt.Errorf("duplicate Kind manifest value %d", entry.KindValue)
		}
		seenValues[entry.KindValue] = struct{}{}
		if err := validateKindManifestEntry(entry); err != nil {
			return fmt.Errorf("Kind manifest %s: %w", entry.Kind, err)
		}
	}
	return nil
}

// CanonicalKindManifestJSON emits stable, LF-terminated JSON for golden and
// provenance checks.
func CanonicalKindManifestJSON() ([]byte, error) {
	document, err := LoadKindManifest()
	if err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode Kind manifest: %w", err)
	}
	return append(encoded, '\n'), nil
}

// KindManifestDigest is the SHA-256 of the validated canonical inventory.
// An empty result denotes a checked-in manifest invariant failure; callers also
// receive the corresponding stable subset diagnostic from RunSubsetGate.
func KindManifestDigest() string {
	encoded, err := CanonicalKindManifestJSON()
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func validateKindManifestEntry(entry KindManifestEntry) error {
	for name, value := range map[string]string{
		"domain": entry.Domain, "syntaxGroup": entry.SyntaxGroup, "decisionPolicy": entry.DecisionPolicy,
		"feature": entry.Feature, "capability": entry.Capability, "gateHandler": entry.GateHandler,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is empty", name)
		}
	}
	if len(entry.PlannedLevels) == 0 {
		return fmt.Errorf("plannedLevels is empty")
	}
	seenLevels := make(map[KindSupportLevel]struct{}, len(entry.PlannedLevels))
	for _, level := range entry.PlannedLevels {
		if !isKindSupportLevel(level) {
			return fmt.Errorf("unknown planned level %q", level)
		}
		if _, duplicate := seenLevels[level]; duplicate {
			return fmt.Errorf("duplicate planned level %q", level)
		}
		seenLevels[level] = struct{}{}
	}
	if want := decisionForSupportLevel(entry.PlannedLevels[0]); entry.DefaultDecision != want {
		return fmt.Errorf("decision %q does not match current level %q (want %q)", entry.DefaultDecision, entry.PlannedLevels[0], want)
	}
	if entry.DefaultDecision != KindDecisionReject && entry.DefaultDecision != KindDecisionDeferFeature && strings.TrimSpace(entry.LoweringPlan) == "" {
		return fmt.Errorf("accepted Kind has no lowering plan")
	}
	if (entry.DefaultDecision == KindDecisionReject || entry.DefaultDecision == KindDecisionDeferFeature) && entry.LoweringPlan != "" {
		return fmt.Errorf("rejected/deferred Kind unexpectedly has lowering plan %q", entry.LoweringPlan)
	}
	handler, ok := lookupKindGateHandler(entry.GateHandler)
	if !ok {
		return fmt.Errorf("unknown gate handler %q", entry.GateHandler)
	}
	if !slices.Contains(handler.Decisions, entry.DefaultDecision) {
		return fmt.Errorf("gate handler %q does not support decision %q", entry.GateHandler, entry.DefaultDecision)
	}
	return nil
}

func validateKindGateHandlerRegistry(registry []kindGateHandlerDefinition) error {
	if len(registry) == 0 {
		return fmt.Errorf("registry is empty")
	}
	for index, definition := range registry {
		if strings.TrimSpace(definition.Name) == "" {
			return fmt.Errorf("handler %d has an empty name", index)
		}
		if index > 0 && registry[index-1].Name >= definition.Name {
			return fmt.Errorf("handler names are not sorted and unique at %q", definition.Name)
		}
		if definition.Handle == nil {
			return fmt.Errorf("handler %q is unbound", definition.Name)
		}
		if len(definition.Decisions) == 0 {
			return fmt.Errorf("handler %q has no decisions", definition.Name)
		}
		seen := make(map[KindGateDecision]struct{}, len(definition.Decisions))
		for _, decision := range definition.Decisions {
			if !isKindGateDecision(decision) {
				return fmt.Errorf("handler %q has unknown decision %q", definition.Name, decision)
			}
			if _, duplicate := seen[decision]; duplicate {
				return fmt.Errorf("handler %q repeats decision %q", definition.Name, decision)
			}
			seen[decision] = struct{}{}
		}
	}
	return nil
}

func lookupKindGateHandler(name string) (kindGateHandlerDefinition, bool) {
	index, ok := slices.BinarySearchFunc(kindGateHandlerRegistry, name, func(definition kindGateHandlerDefinition, name string) int {
		return strings.Compare(definition.Name, name)
	})
	if !ok {
		return kindGateHandlerDefinition{}, false
	}
	return kindGateHandlerRegistry[index], true
}

func isKindSupportLevel(level KindSupportLevel) bool {
	switch level {
	case KindSupportS0, KindSupportS1, KindSupportS2, KindSupportCompileOnly, KindSupportPlanned, KindSupportReject:
		return true
	default:
		return false
	}
}

func isKindGateDecision(decision KindGateDecision) bool {
	switch decision {
	case KindDecisionAcceptDirect, KindDecisionAcceptErase, KindDecisionAcceptDesugar,
		KindDecisionAcceptRuntime, KindDecisionDeferFeature, KindDecisionReject:
		return true
	default:
		return false
	}
}

func decisionForSupportLevel(level KindSupportLevel) KindGateDecision {
	switch level {
	case KindSupportS0:
		return KindDecisionAcceptDirect
	case KindSupportS1:
		return KindDecisionAcceptDesugar
	case KindSupportS2:
		return KindDecisionAcceptRuntime
	case KindSupportCompileOnly:
		return KindDecisionAcceptErase
	case KindSupportPlanned:
		return KindDecisionDeferFeature
	case KindSupportReject:
		return KindDecisionReject
	default:
		return ""
	}
}

func cloneKindManifestEntries(input []KindManifestEntry) []KindManifestEntry {
	result := make([]KindManifestEntry, len(input))
	for index := range input {
		result[index] = input[index]
		result[index].PlannedLevels = slices.Clone(input[index].PlannedLevels)
	}
	return result
}
