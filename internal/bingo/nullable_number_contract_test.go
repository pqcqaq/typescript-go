package bingo

import "testing"

func TestNullableNumberContractIsCanonicalAndDistinct(t *testing.T) {
	contract := CanonicalNullableNumberContract()
	if err := ValidateNullableNumberContract(contract); err != nil {
		t.Fatal(err)
	}
	if contract.NumberTag == contract.NullTag || contract.NullTag == contract.UndefinedTag || contract.NumberTag == contract.UndefinedTag {
		t.Fatalf("nullable tags are not distinct: %#v", contract)
	}
	for _, mutate := range []func(*NullableNumberContract){
		func(value *NullableNumberContract) { value.NullTag = value.NumberTag },
		func(value *NullableNumberContract) { value.BitWidth = 64 },
		func(value *NullableNumberContract) { value.PayloadOffset = 0 },
	} {
		candidate := contract
		mutate(&candidate)
		if err := ValidateNullableNumberContract(candidate); err == nil {
			t.Fatal("noncanonical nullable ABI contract was accepted")
		}
	}
}
