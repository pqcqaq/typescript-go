package bingo

import "testing"

func testVERT013bLayout(t testing.TB) VERT013bLayoutContract {
	t.Helper()
	contract := testVERT013bContract(t)
	_, hash, err := CanonicalVERT013bClassContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	contract.ContentHash = hash
	target, err := CanonicalObjectLayoutTarget(ObjectLayoutX8664Triple)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := PlanVERT013bLayout(contract, target)
	if err != nil {
		t.Fatal(err)
	}
	return layout
}

func TestVERT013bLayoutPreservesBasePrefix(t *testing.T) {
	contract := testVERT013bContract(t)
	_, hash, err := CanonicalVERT013bClassContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	contract.ContentHash = hash
	layout := testVERT013bLayout(t)
	if err := VerifyCanonicalVERT013bLayout(layout, contract); err != nil {
		t.Fatal(err)
	}
	if layout.Base.Properties[0].FieldOffset != layout.Derived.Properties[0].FieldOffset {
		t.Fatal("base offset moved in derived layout")
	}
	if layout.Derived.Properties[1].FieldOffset <= layout.Derived.Properties[0].FieldOffset {
		t.Fatal("derived field is not a suffix")
	}
}

func TestVERT013bLayoutRejectsTampering(t *testing.T) {
	for name, mutate := range map[string]func(*VERT013bLayoutContract){
		"base class":     func(l *VERT013bLayoutContract) { l.BaseClassID = 2 },
		"base type":      func(l *VERT013bLayoutContract) { l.Base.TypeKey = typeKeyC },
		"derived type":   func(l *VERT013bLayoutContract) { l.Derived.TypeKey = typeKeyA },
		"base field":     func(l *VERT013bLayoutContract) { l.Base.Properties[0].Key = "other" },
		"derived prefix": func(l *VERT013bLayoutContract) { l.Derived.Properties[0].Key = "other" },
		"trace offset": func(l *VERT013bLayoutContract) {
			l.Derived.TraceOffsets = []uint32{l.Derived.Properties[0].FieldOffset}
		},
	} {
		t.Run(name, func(t *testing.T) {
			contract := testVERT013bContract(t)
			_, hash, err := CanonicalVERT013bClassContract(contract)
			if err != nil {
				t.Fatal(err)
			}
			contract.ContentHash = hash
			layout := testVERT013bLayout(t)
			mutate(&layout)
			if err := VerifyVERT013bLayout(layout, contract); err == nil {
				t.Fatal("tampered layout accepted")
			}
		})
	}
}
