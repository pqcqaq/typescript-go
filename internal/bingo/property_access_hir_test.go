package bingo

import (
	"encoding/json"
	"strings"
	"testing"
)

func propertyAccessHIRFixture(t testing.TB) PropertyAccessHIRArtifact {
	t.Helper()
	typeKey := strings.Repeat("a", 64)
	inputs := make([]PropertyAccessHIRInput, 0, 4)
	for index, item := range []struct {
		name   string
		domain PropertyKeyDomain
		keys   []string
		source string
	}{{"direct", PropertyKeyDirect, []string{"left"}, ""}, {"dynamic", PropertyKeyUnknown, nil, "node/dynamic"}, {"finite", PropertyKeyLiteralUnion, []string{"left", "right"}, ""}, {"literal", PropertyKeyLiteral, []string{"right"}, ""}} {
		admission, err := BuildPropertyAccessAdmission(typeKey, item.domain, item.keys, PropertyAccessInterop, item.source)
		if err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, PropertyAccessHIRInput{FunctionName: item.name, AccessNodeID: "node/" + item.name, ReceiverTypeHash: typeKey, KeyTypeHash: strings.Repeat(string("bcde"[index]), 64), Admission: admission})
	}
	artifact, err := BuildPropertyAccessHIRArtifact(strings.Repeat("1", 64), strings.Repeat("2", 64), inputs)
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}

func TestPropertyAccessHIRMaterializesFiniteAndDynamicControl(t *testing.T) {
	artifact := propertyAccessHIRFixture(t)
	finite, dynamic := artifact.Functions[2], artifact.Functions[1]
	if finite.ReturnValueID != 6 || len(finite.Operations) != 6 || finite.Operations[2].Kind != "key.dispatch" || finite.Operations[5].Kind != "phi" || len(finite.Operations[5].Operands) != 2 {
		t.Fatalf("invalid finite HIR: %#v", finite)
	}
	if dynamic.ReturnValueID != 4 || len(dynamic.Operations) != 4 || dynamic.Operations[2].Kind != "dynamic.boundary.enter" || dynamic.Operations[3].Kind != "dynamic.property.load" || dynamic.Operations[3].BoundaryHash == "" {
		t.Fatalf("invalid dynamic HIR: %#v", dynamic)
	}
	encoded, _, err := CanonicalPropertyAccessHIRArtifact(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePropertyAccessHIRArtifact(encoded); err != nil {
		t.Fatal(err)
	}
}

func TestPropertyAccessHIRRejectsTampering(t *testing.T) {
	base := propertyAccessHIRFixture(t)
	encoded, _, err := CanonicalPropertyAccessHIRArtifact(base)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*PropertyAccessHIRArtifact){
		"function":  func(v *PropertyAccessHIRArtifact) { v.Functions[0].Name = "other" },
		"operation": func(v *PropertyAccessHIRArtifact) { v.Functions[2].Operations[2].Kind = "dynamic.dispatch" },
		"operand":   func(v *PropertyAccessHIRArtifact) { v.Functions[2].Operations[5].Operands[0] = 5 },
		"keys": func(v *PropertyAccessHIRArtifact) {
			v.Functions[2].Operations[2].DispatchKeys = []string{"right", "left"}
		},
		"boundary": func(v *PropertyAccessHIRArtifact) {
			v.Functions[1].Operations[3].BoundaryHash = strings.Repeat("5", 64)
		},
		"effects": func(v *PropertyAccessHIRArtifact) { v.Functions[1].Operations[3].Effects = []Effect{EffectRead} },
		"return":  func(v *PropertyAccessHIRArtifact) { v.Functions[2].ReturnValueID = 5 },
	} {
		t.Run(name, func(t *testing.T) {
			value, err := DecodePropertyAccessHIRArtifact(encoded)
			if err != nil {
				t.Fatal(err)
			}
			mutate(value)
			if _, _, err := CanonicalPropertyAccessHIRArtifact(*value); err == nil {
				t.Fatal("accepted tampering")
			}
		})
	}
	for name, mutate := range map[string]func(*PropertyAccessHIRArtifact){
		"frontend": func(v *PropertyAccessHIRArtifact) { v.FrontendSnapshotHash = strings.Repeat("3", 64) },
		"replay":   func(v *PropertyAccessHIRArtifact) { v.ReplayHash = strings.Repeat("4", 64) },
	} {
		t.Run("stale "+name, func(t *testing.T) {
			stale, err := DecodePropertyAccessHIRArtifact(encoded)
			if err != nil {
				t.Fatal(err)
			}
			mutate(stale)
			if err := VerifyCanonicalPropertyAccessHIRArtifact(*stale); err == nil {
				t.Fatal("accepted stale provenance identity")
			}
		})
	}
	var raw map[string]any
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	raw["unknown"] = true
	unknown, _ := json.Marshal(raw)
	if _, err := DecodePropertyAccessHIRArtifact(unknown); err == nil {
		t.Fatal("accepted unknown member")
	}
	if _, err := DecodePropertyAccessHIRArtifact(make([]byte, maxPropertyAccessHIRBytes+1)); err == nil {
		t.Fatal("accepted oversized HIR")
	}
}

func FuzzDecodePropertyAccessHIRArtifact(f *testing.F) {
	artifact := propertyAccessHIRFixture(f)
	encoded, _, err := CanonicalPropertyAccessHIRArtifact(artifact)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Fuzz(func(t *testing.T, data []byte) { _, _ = DecodePropertyAccessHIRArtifact(data) })
}
