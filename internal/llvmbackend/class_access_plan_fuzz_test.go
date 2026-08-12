package llvmbackend

import "testing"

func FuzzDecodeClassAccessBackendPlan(f *testing.F) {
	plan := testClassAccessBackendPlan(f)
	encoded, _, err := CanonicalClassAccessBackendPlan(plan)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxClassAccessBackendPlanBytes+1 {
			return
		}
		plan, err := DecodeClassAccessBackendPlan(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalClassAccessBackendPlan(*plan)
		if err != nil {
			t.Fatalf("accepted backend plan is not canonical: %v", err)
		}
		if _, err := DecodeClassAccessBackendPlan(canonical); err != nil {
			t.Fatalf("canonical backend plan does not round trip: %v", err)
		}
	})
}
