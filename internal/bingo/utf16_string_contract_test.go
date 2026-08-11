package bingo

import "testing"

func TestUTF16StringContractAndCodeUnitsAreCanonical(t *testing.T) {
	contract := CanonicalUTF16StringContract()
	if err := ValidateUTF16StringContract(contract); err != nil {
		t.Fatal(err)
	}
	if err := ValidateUTF16CodeUnits("0041d83dde00d800"); err != nil {
		t.Fatalf("UTF-16 code units were rejected: %v", err)
	}
	for _, mutate := range []func(*UTF16StringContract){
		func(value *UTF16StringContract) { value.Ownership = "owned" },
		func(value *UTF16StringContract) { value.BitWidth = 64 },
		func(value *UTF16StringContract) { value.EmptyDataIsNil = false },
	} {
		candidate := contract
		mutate(&candidate)
		if err := ValidateUTF16StringContract(candidate); err == nil {
			t.Fatal("noncanonical UTF-16 string contract was accepted")
		}
	}
	for _, value := range []string{"0", "004", "004A", "zzzz"} {
		if err := ValidateUTF16CodeUnits(value); err == nil {
			t.Fatalf("noncanonical UTF-16 code units %q were accepted", value)
		}
	}
}
