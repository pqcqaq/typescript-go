package llvmbackend

import "testing"

func FuzzDecodeFunctionThunkBackendPlan(f *testing.F) {
	plan, err := BuildFunctionThunkBackendPlan(backendFunctionThunkMIR(f, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	if err != nil {
		f.Fatal(err)
	}
	encoded, _, err := CanonicalFunctionThunkBackendPlan(plan)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Fuzz(func(t *testing.T, data []byte) { _, _ = DecodeFunctionThunkBackendPlan(data) })
}
