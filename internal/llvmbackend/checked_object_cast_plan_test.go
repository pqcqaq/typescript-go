package llvmbackend

import (
	"testing"

	"github.com/microsoft/typescript-go/internal/bingo"
)

func backendCheckedObjectCastBound(t testing.TB) bingo.CheckedObjectCastBoundContract {
	t.Helper()
	target := backendObjectContract(t, typeKey64("a"), typeKey64("b"), true)
	layoutTarget, err := bingo.CanonicalObjectLayoutTarget(bingo.ObjectLayoutX8664Triple)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := bingo.PlanObjectLayout(target.TypeKey, layoutTarget, []bingo.ObjectLayoutPropertyInput{{Key: "value", Kind: bingo.ObjectPropertyData, Representation: "f64"}})
	if err != nil {
		t.Fatal(err)
	}
	boundary := bingo.DynamicObjectBoundaryArtifact{SchemaVersion: bingo.DynamicObjectBoundarySchemaVersion, Kind: "ffi-import", SourceID: "ffi.payload"}
	_, h, err := bingo.CanonicalDynamicObjectBoundary(boundary)
	if err != nil {
		t.Fatal(err)
	}
	boundary.ContentHash = h
	cast := bingo.CheckedObjectCastContract{SchemaVersion: bingo.CheckedObjectCastSchemaVersion, Boundary: boundary, SourceTypeKey: typeKey64("c"), Target: target, TargetLayout: layout, Properties: []string{"value"}, PreservesIdentity: true, ReadonlyResult: true}
	_, h, err = bingo.CanonicalCheckedObjectCast(cast)
	if err != nil {
		t.Fatal(err)
	}
	cast.ContentHash = h
	bound, err := bingo.NewCheckedObjectCastBound(cast, typeKey64("d"), typeKey64("e"), bingo.BoundCapability{LogicalName: bingo.CheckedObjectCastCapability, SymbolName: bingo.CheckedObjectCastSymbol, SignatureHash: typeKey64("f")})
	if err != nil {
		t.Fatal(err)
	}
	return bound
}

func typeKey64(value string) string {
	result := ""
	for len(result) < 64 {
		result += value
	}
	return result[:64]
}

func TestCheckedObjectCastBackendPlanRoundTrip(t *testing.T) {
	plan, err := BuildCheckedObjectCastBackendPlan(backendCheckedObjectCastBound(t))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Allocates || plan.Copies || plan.InvokesAccessors || !plan.PreservesIdentity || len(plan.RuntimeCalls) != 1 {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	b, _, err := CanonicalCheckedObjectCastBackendPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeCheckedObjectCastBackendPlan(b); err != nil {
		t.Fatal(err)
	}
}

func TestCheckedObjectCastBackendPlanRejectsTamper(t *testing.T) {
	base, err := BuildCheckedObjectCastBackendPlan(backendCheckedObjectCastBound(t))
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*CheckedObjectCastBackendPlan){
		"bound":    func(p *CheckedObjectCastBackendPlan) { p.BoundHash = typeKey64("0") },
		"symbol":   func(p *CheckedObjectCastBackendPlan) { p.FunctionName = "forged" },
		"layout":   func(p *CheckedObjectCastBackendPlan) { p.TargetLayoutContentHash = typeKey64("0") },
		"count":    func(p *CheckedObjectCastBackendPlan) { p.PropertyCount++ },
		"status":   func(p *CheckedObjectCastBackendPlan) { p.StatusChecked = false },
		"match":    func(p *CheckedObjectCastBackendPlan) { p.MatchDomain = "any" },
		"identity": func(p *CheckedObjectCastBackendPlan) { p.PreservesIdentity = false },
		"allocate": func(p *CheckedObjectCastBackendPlan) { p.Allocates = true },
		"copy":     func(p *CheckedObjectCastBackendPlan) { p.Copies = true },
		"accessor": func(p *CheckedObjectCastBackendPlan) { p.InvokesAccessors = true },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			p := base
			p.RuntimeCalls = append([]string(nil), base.RuntimeCalls...)
			mutate(&p)
			if err := VerifyCanonicalCheckedObjectCastBackendPlan(p); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func FuzzDecodeCheckedObjectCastBackendPlan(f *testing.F) {
	plan, err := BuildCheckedObjectCastBackendPlan(backendCheckedObjectCastBound(f))
	if err != nil {
		f.Fatal(err)
	}
	b, _, err := CanonicalCheckedObjectCastBackendPlan(plan)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(b)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxCheckedObjectCastBackendPlanBytes {
			return
		}
		plan, err := DecodeCheckedObjectCastBackendPlan(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalCheckedObjectCastBackendPlan(*plan)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeCheckedObjectCastBackendPlan(canonical); err != nil {
			t.Fatal(err)
		}
	})
}
