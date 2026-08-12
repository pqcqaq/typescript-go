package llvmbackend

import (
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/bingo"
)

func propertyAccessBackendPlanFixture(t testing.TB) PropertyAccessBackendPlan {
	return propertyAccessBackendPlanForLayout(t, strings.Repeat("a", 64))
}

func propertyAccessBackendPlanForLayout(t testing.TB, dataLayoutHash string) PropertyAccessBackendPlan {
	t.Helper()
	hir := propertyAccessHIRFixtureForBackend(t)
	abi, err := bingo.BuildDynamicValueABIContract()
	if err != nil {
		t.Fatal(err)
	}
	mir, err := bingo.LowerPropertyAccessMIR(hir, FirstSliceTriple, dataLayoutHash, abi)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := bingo.NewPropertyAccessBoundMIR(mir, strings.Repeat("b", 64), strings.Repeat("c", 64), bingo.BoundCapability{LogicalName: bingo.DynamicPropertyLoadCapability, SymbolName: bingo.DynamicPropertyLoadSymbol, SignatureHash: abi.SignatureHash})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPropertyAccessBackendPlan(bound)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func propertyAccessHIRFixtureForBackend(t testing.TB) bingo.PropertyAccessHIRArtifact {
	t.Helper()
	inputs := make([]bingo.PropertyAccessHIRInput, 0, 4)
	for _, item := range []struct {
		name   string
		domain bingo.PropertyKeyDomain
		keys   []string
		source string
	}{{"direct", bingo.PropertyKeyDirect, []string{"left"}, ""}, {"dynamic", bingo.PropertyKeyUnknown, nil, "source"}, {"finite", bingo.PropertyKeyLiteralUnion, []string{"left", "right"}, ""}, {"literal", bingo.PropertyKeyLiteral, []string{"right"}, ""}} {
		admission, err := bingo.BuildPropertyAccessAdmission(strings.Repeat("1", 64), item.domain, item.keys, bingo.PropertyAccessInterop, item.source)
		if err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, bingo.PropertyAccessHIRInput{FunctionName: item.name, AccessNodeID: item.name, ReceiverTypeHash: strings.Repeat("1", 64), KeyTypeHash: strings.Repeat("2", 64), Admission: admission})
	}
	hir, err := bingo.BuildPropertyAccessHIRArtifact(strings.Repeat("3", 64), strings.Repeat("4", 64), inputs)
	if err != nil {
		t.Fatal(err)
	}
	return hir
}

func TestPropertyAccessBackendPlanRoundTripAndTampering(t *testing.T) {
	plan := propertyAccessBackendPlanFixture(t)
	encoded, _, err := CanonicalPropertyAccessBackendPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodePropertyAccessBackendPlan(encoded); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*PropertyAccessBackendPlan){
		"bound":     func(v *PropertyAccessBackendPlan) { v.BoundMIRHash = strings.Repeat("e", 64) },
		"value":     func(v *PropertyAccessBackendPlan) { v.DynamicValueLLVM = "{i64,i64}" },
		"key":       func(v *PropertyAccessBackendPlan) { v.UTF16ViewLLVM = "ptr" },
		"symbol":    func(v *PropertyAccessBackendPlan) { v.RuntimeSymbol = "other" },
		"entry":     func(v *PropertyAccessBackendPlan) { v.EntrySymbol = "other" },
		"status":    func(v *PropertyAccessBackendPlan) { v.ChecksStatus = false },
		"clear":     func(v *PropertyAccessBackendPlan) { v.ClearsFailureResult = false },
		"exception": func(v *PropertyAccessBackendPlan) { v.ExceptionStatus = 1 },
		"allocate":  func(v *PropertyAccessBackendPlan) { v.Allocates = true },
	} {
		t.Run(name, func(t *testing.T) {
			value := plan
			mutate(&value)
			if err := VerifyCanonicalPropertyAccessBackendPlan(value); err == nil {
				t.Fatal("accepted tampering")
			}
		})
	}
}

func FuzzDecodePropertyAccessBackendPlan(f *testing.F) {
	plan := propertyAccessBackendPlanFixture(f)
	encoded, _, err := CanonicalPropertyAccessBackendPlan(plan)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Fuzz(func(t *testing.T, data []byte) { _, _ = DecodePropertyAccessBackendPlan(data) })
}
