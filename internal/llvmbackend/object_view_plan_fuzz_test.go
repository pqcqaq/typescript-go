package llvmbackend

import "testing"

func FuzzDecodeObjectViewBackendPlan(f *testing.F) {
	plan, err := BuildObjectViewBackendPlan(backendObjectViewMIR(f))
	if err != nil {
		f.Fatal(err)
	}
	encoded, _, err := CanonicalObjectViewBackendPlan(plan)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	accessor, err := BuildObjectViewBackendPlan(backendObjectAccessorViewMIR(f))
	if err != nil {
		f.Fatal(err)
	}
	accessorEncoded, _, err := CanonicalObjectViewBackendPlan(accessor)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(accessorEncoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxObjectViewBackendPlanBytes+1 {
			return
		}
		plan, err := DecodeObjectViewBackendPlan(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalObjectViewBackendPlan(*plan)
		if err != nil {
			t.Fatalf("accepted ObjectView backend plan is not canonical: %v", err)
		}
		if _, err := DecodeObjectViewBackendPlan(canonical); err != nil {
			t.Fatalf("canonical ObjectView backend plan does not round trip: %v", err)
		}
	})
}
