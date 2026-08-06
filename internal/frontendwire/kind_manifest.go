package frontendwire

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
)

const KindManifestSchemaVersion uint32 = 2

type KindManifestEntry struct {
	Kind            string   `json:"kind"`
	KindValue       int16    `json:"kindValue"`
	Domain          string   `json:"domain"`
	SyntaxGroup     string   `json:"syntaxGroup"`
	PlannedLevels   []string `json:"plannedLevels"`
	DecisionPolicy  string   `json:"decisionPolicy"`
	DefaultDecision string   `json:"defaultDecision"`
	Feature         string   `json:"feature"`
	Capability      string   `json:"capability"`
	GateHandler     string   `json:"gateHandler"`
	LoweringPlan    string   `json:"loweringPlan,omitempty"`
}

type KindManifestDocument struct {
	SchemaVersion uint32              `json:"schemaVersion"`
	Kinds         []KindManifestEntry `json:"kinds"`
}

//go:embed kind_manifest.json
var embeddedKindManifestJSON []byte

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
	if document.SchemaVersion != KindManifestSchemaVersion {
		return KindManifestDocument{}, fmt.Errorf("unsupported Kind manifest schema %d", document.SchemaVersion)
	}
	seen := make(map[string]struct{}, len(document.Kinds))
	for index, entry := range document.Kinds {
		if entry.Kind == "" || entry.KindValue != int16(index) {
			return KindManifestDocument{}, fmt.Errorf("Kind manifest entry %d has invalid identity %q/%d", index, entry.Kind, entry.KindValue)
		}
		if _, duplicate := seen[entry.Kind]; duplicate {
			return KindManifestDocument{}, fmt.Errorf("Kind manifest contains duplicate Kind %q", entry.Kind)
		}
		seen[entry.Kind] = struct{}{}
	}
	return document, nil
}
