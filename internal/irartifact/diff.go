package irartifact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strconv"
)

const DiffReportSchemaVersion uint32 = 1

type Kind string

const (
	KindHIR Kind = "hir"
	KindMIR Kind = "mir"
)

type Difference struct {
	Path  string `json:"path"`
	Left  any    `json:"left,omitempty"`
	Right any    `json:"right,omitempty"`
}

// DiffReport makes compatibility explicit before listing structural changes.
// Consumers must not treat an incompatible schema/provenance comparison as a
// normal content-only diff.
type DiffReport struct {
	SchemaVersion   uint32       `json:"schemaVersion"`
	Kind            Kind         `json:"kind"`
	LeftIRSchema    uint32       `json:"leftIRSchema"`
	RightIRSchema   uint32       `json:"rightIRSchema"`
	SchemaEqual     bool         `json:"schemaEqual"`
	ProvenanceEqual bool         `json:"provenanceEqual"`
	Compatible      bool         `json:"compatible"`
	Equal           bool         `json:"equal"`
	Differences     []Difference `json:"differences"`
}

func Diff(kind Kind, left, right []byte) (DiffReport, error) {
	switch kind {
	case KindHIR:
		leftModule, err := DecodeHIR(left)
		if err != nil {
			return DiffReport{}, fmt.Errorf("decode left HIR: %w", err)
		}
		rightModule, err := DecodeHIR(right)
		if err != nil {
			return DiffReport{}, fmt.Errorf("decode right HIR: %w", err)
		}
		return buildDiff(kind, leftModule.SchemaVersion, rightModule.SchemaVersion, leftModule.Provenance, rightModule.Provenance, left, right)
	case KindMIR:
		leftModule, err := DecodeMIR(left)
		if err != nil {
			return DiffReport{}, fmt.Errorf("decode left MIR: %w", err)
		}
		rightModule, err := DecodeMIR(right)
		if err != nil {
			return DiffReport{}, fmt.Errorf("decode right MIR: %w", err)
		}
		return buildDiff(kind, leftModule.SchemaVersion, rightModule.SchemaVersion, leftModule.Provenance, rightModule.Provenance, left, right)
	default:
		return DiffReport{}, fmt.Errorf("unsupported IR kind %q", kind)
	}
}

func (report DiffReport) CanonicalBytes() ([]byte, error) {
	if report.SchemaVersion != DiffReportSchemaVersion || report.Kind != KindHIR && report.Kind != KindMIR || report.Differences == nil {
		return nil, fmt.Errorf("invalid IR diff report")
	}
	if report.SchemaEqual != (report.LeftIRSchema == report.RightIRSchema) || report.Compatible != (report.SchemaEqual && report.ProvenanceEqual) || report.Equal != (report.Compatible && len(report.Differences) == 0) {
		return nil, fmt.Errorf("inconsistent IR diff report flags")
	}
	return json.Marshal(report)
}

func RenderDiffText(report DiffReport) (string, error) {
	if _, err := report.CanonicalBytes(); err != nil {
		return "", err
	}
	var out bytes.Buffer
	fmt.Fprintf(&out, "%s schema=%d/%d compatible=%t equal=%t\n", report.Kind, report.LeftIRSchema, report.RightIRSchema, report.Compatible, report.Equal)
	for _, difference := range report.Differences {
		left, err := json.Marshal(difference.Left)
		if err != nil {
			return "", err
		}
		right, err := json.Marshal(difference.Right)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&out, "%s: %s -> %s\n", difference.Path, left, right)
	}
	return out.String(), nil
}

func buildDiff(kind Kind, leftSchema, rightSchema uint32, leftProvenance, rightProvenance any, left, right []byte) (DiffReport, error) {
	report := DiffReport{
		SchemaVersion:   DiffReportSchemaVersion,
		Kind:            kind,
		LeftIRSchema:    leftSchema,
		RightIRSchema:   rightSchema,
		SchemaEqual:     leftSchema == rightSchema,
		ProvenanceEqual: reflect.DeepEqual(leftProvenance, rightProvenance),
		Differences:     []Difference{},
	}
	report.Compatible = report.SchemaEqual && report.ProvenanceEqual

	leftValue, err := decodeJSON(left)
	if err != nil {
		return DiffReport{}, err
	}
	rightValue, err := decodeJSON(right)
	if err != nil {
		return DiffReport{}, err
	}

	leftObject := leftValue.(map[string]any)
	rightObject := rightValue.(map[string]any)
	if !report.SchemaEqual {
		report.Differences = append(report.Differences, Difference{Path: "$.schemaVersion", Left: leftSchema, Right: rightSchema})
	}
	if !report.ProvenanceEqual {
		appendJSONDifferences(&report.Differences, "$.provenance", leftObject["provenance"], rightObject["provenance"])
	}
	leftStructural := cloneWithoutMetadata(leftObject)
	rightStructural := cloneWithoutMetadata(rightObject)
	appendJSONDifferences(&report.Differences, "$", leftStructural, rightStructural)
	report.Equal = report.Compatible && len(report.Differences) == 0
	return report, nil
}

func cloneWithoutMetadata(input map[string]any) map[string]any {
	result := make(map[string]any, len(input)-2)
	for key, value := range input {
		if key != "schemaVersion" && key != "provenance" {
			result[key] = value
		}
	}
	return result
}

func decodeJSON(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("JSON contains multiple values")
		}
		return nil, err
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, fmt.Errorf("IR artifact is not a JSON object")
	}
	return value, nil
}

func appendJSONDifferences(result *[]Difference, path string, left, right any) {
	if reflect.DeepEqual(left, right) {
		return
	}
	leftObject, leftIsObject := left.(map[string]any)
	rightObject, rightIsObject := right.(map[string]any)
	if leftIsObject && rightIsObject {
		keys := make([]string, 0, len(leftObject)+len(rightObject))
		seen := make(map[string]struct{}, len(leftObject)+len(rightObject))
		for key := range leftObject {
			seen[key] = struct{}{}
			keys = append(keys, key)
		}
		for key := range rightObject {
			if _, ok := seen[key]; !ok {
				keys = append(keys, key)
			}
		}
		slices.Sort(keys)
		for _, key := range keys {
			appendJSONDifferences(result, path+"."+key, leftObject[key], rightObject[key])
		}
		return
	}
	leftArray, leftIsArray := left.([]any)
	rightArray, rightIsArray := right.([]any)
	if leftIsArray && rightIsArray {
		limit := max(len(leftArray), len(rightArray))
		for index := 0; index < limit; index++ {
			var leftItem, rightItem any
			if index < len(leftArray) {
				leftItem = leftArray[index]
			}
			if index < len(rightArray) {
				rightItem = rightArray[index]
			}
			appendJSONDifferences(result, path+"["+strconv.Itoa(index)+"]", leftItem, rightItem)
		}
		return
	}
	*result = append(*result, Difference{Path: path, Left: left, Right: right})
}
