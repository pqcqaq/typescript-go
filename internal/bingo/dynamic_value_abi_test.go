package bingo

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDynamicValueABIRoundTripAndTampering(t *testing.T) {
	contract, err := BuildDynamicValueABIContract()
	if err != nil {
		t.Fatal(err)
	}
	encoded, _, err := CanonicalDynamicValueABIContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeDynamicValueABIContract(encoded); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*DynamicValueABIContract){"size": func(v *DynamicValueABIContract) { v.SizeBytes = 8 }, "tag": func(v *DynamicValueABIContract) { v.TagBits = 8 }, "payload": func(v *DynamicValueABIContract) { v.PayloadBits = 32 }, "object": func(v *DynamicValueABIContract) { v.ObjectPayload = "gc-ref" }, "symbol": func(v *DynamicValueABIContract) { v.Symbol = "other" }, "signature": func(v *DynamicValueABIContract) { v.Signature = "u32()" }, "signature-hash": func(v *DynamicValueABIContract) { v.SignatureHash = strings.Repeat("f", 64) }, "status": func(v *DynamicValueABIContract) { v.StatusChecked = false }, "exception-status": func(v *DynamicValueABIContract) { v.ExceptionStatus = 1 }, "exception-result": func(v *DynamicValueABIContract) { v.ExceptionResult = "unspecified" }, "exception-carrier": func(v *DynamicValueABIContract) { v.ExceptionCarrier = "opaque-handle" }, "allocate": func(v *DynamicValueABIContract) { v.Allocates = true }} {
		t.Run(name, func(t *testing.T) {
			value := contract
			mutate(&value)
			if _, _, err := CanonicalDynamicValueABIContract(value); err == nil {
				t.Fatal("accepted tampering")
			}
		})
	}
	var raw map[string]any
	_ = json.Unmarshal(encoded, &raw)
	raw["unknown"] = true
	unknown, _ := json.Marshal(raw)
	if _, err := DecodeDynamicValueABIContract(unknown); err == nil {
		t.Fatal("accepted unknown")
	}
}

func TestPropertyAccessMIRAndBoundArtifact(t *testing.T) {
	hir := propertyAccessHIRFixture(t)
	abi, err := BuildDynamicValueABIContract()
	if err != nil {
		t.Fatal(err)
	}
	mir, err := LowerPropertyAccessMIR(hir, "x86_64-unknown-linux-gnu", strings.Repeat("a", 64), abi)
	if err != nil {
		t.Fatal(err)
	}
	if len(mir.LogicalCapabilityRequirements) != 1 || mir.LogicalCapabilityRequirements[0] != DynamicPropertyLoadCapability || len(mir.Functions) != 4 {
		t.Fatalf("invalid MIR: %#v", mir)
	}
	encoded, _, err := CanonicalPropertyAccessMIR(mir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePropertyAccessMIR(encoded); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*PropertyAccessMIRArtifact){"HIR": func(v *PropertyAccessMIRArtifact) { v.HIRHash = strings.Repeat("b", 64) }, "target": func(v *PropertyAccessMIRArtifact) { v.TargetTriple = "aarch64-unknown-linux-gnu" }, "layout": func(v *PropertyAccessMIRArtifact) { v.DataLayoutHash = strings.Repeat("b", 64) }, "requirement": func(v *PropertyAccessMIRArtifact) { v.LogicalCapabilityRequirements = nil }, "function": func(v *PropertyAccessMIRArtifact) { v.Functions[0].Name = "other" }} {
		t.Run(name, func(t *testing.T) {
			value, err := DecodePropertyAccessMIR(encoded)
			if err != nil {
				t.Fatal(err)
			}
			mutate(value)
			if err := VerifyCanonicalPropertyAccessMIR(*value); err == nil {
				t.Fatal("accepted stale/tampered MIR")
			}
		})
	}
	bound, err := NewPropertyAccessBoundMIR(mir, strings.Repeat("c", 64), strings.Repeat("d", 64), BoundCapability{LogicalName: DynamicPropertyLoadCapability, SymbolName: DynamicPropertyLoadSymbol, SignatureHash: abi.SignatureHash})
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyCanonicalPropertyAccessBoundMIR(bound); err != nil {
		t.Fatal(err)
	}
	boundBytes, _, err := CanonicalPropertyAccessBoundMIR(bound)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePropertyAccessBoundMIR(boundBytes); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*PropertyAccessBoundMIR){"context": func(v *PropertyAccessBoundMIR) { v.TargetContextHash = strings.Repeat("f", 64) }, "catalog": func(v *PropertyAccessBoundMIR) { v.CatalogHash = strings.Repeat("a", 64) }, "logical": func(v *PropertyAccessBoundMIR) { v.Binding.LogicalName = "other" }, "symbol": func(v *PropertyAccessBoundMIR) { v.Binding.SymbolName = "other" }, "signature": func(v *PropertyAccessBoundMIR) { v.Binding.SignatureHash = "bad" }} {
		t.Run(name, func(t *testing.T) {
			value := bound
			mutate(&value)
			if err := VerifyCanonicalPropertyAccessBoundMIR(value); err == nil {
				t.Fatal("accepted bound tampering")
			}
		})
	}
}

func FuzzDecodePropertyAccessBoundMIR(f *testing.F) {
	hir := propertyAccessHIRFixture(f)
	abi, err := BuildDynamicValueABIContract()
	if err != nil {
		f.Fatal(err)
	}
	mir, err := LowerPropertyAccessMIR(hir, "x86_64-unknown-linux-gnu", strings.Repeat("a", 64), abi)
	if err != nil {
		f.Fatal(err)
	}
	bound, err := NewPropertyAccessBoundMIR(mir, strings.Repeat("b", 64), strings.Repeat("c", 64), BoundCapability{LogicalName: DynamicPropertyLoadCapability, SymbolName: DynamicPropertyLoadSymbol, SignatureHash: abi.SignatureHash})
	if err != nil {
		f.Fatal(err)
	}
	encoded, _, err := CanonicalPropertyAccessBoundMIR(bound)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Fuzz(func(t *testing.T, data []byte) { _, _ = DecodePropertyAccessBoundMIR(data) })
}

func FuzzDecodeDynamicValueABIContract(f *testing.F) {
	value, err := BuildDynamicValueABIContract()
	if err != nil {
		f.Fatal(err)
	}
	encoded, _, err := CanonicalDynamicValueABIContract(value)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Fuzz(func(t *testing.T, data []byte) { _, _ = DecodeDynamicValueABIContract(data) })
}
func FuzzDecodePropertyAccessMIR(f *testing.F) {
	hir := propertyAccessHIRFixture(f)
	abi, err := BuildDynamicValueABIContract()
	if err != nil {
		f.Fatal(err)
	}
	mir, err := LowerPropertyAccessMIR(hir, "x86_64-unknown-linux-gnu", strings.Repeat("a", 64), abi)
	if err != nil {
		f.Fatal(err)
	}
	encoded, _, err := CanonicalPropertyAccessMIR(mir)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Fuzz(func(t *testing.T, data []byte) { _, _ = DecodePropertyAccessMIR(data) })
}
