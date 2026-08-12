package bingo

import "testing"

func testClassAccessLayout(t testing.TB) ClassAccessLayoutContract {
	t.Helper()
	mir := testClassAccessMIR(t)
	target, err := CanonicalObjectLayoutTarget(ObjectLayoutX8664Triple)
	if err != nil {
		t.Fatal(err)
	}
	mir.Target.Triple = target.Triple
	mir.Target.LLVMDataLayoutHash = target.DataLayoutHash
	_, hash, err := CanonicalClassAccessMIR(mir)
	if err != nil {
		t.Fatal(err)
	}
	mir.ContentHash = hash
	layout, err := PlanClassAccessLayout(mir, target)
	if err != nil {
		t.Fatal(err)
	}
	return layout
}

func TestClassAccessLayoutPreservesPrivateProtectedPrefix(t *testing.T) {
	layout := testClassAccessLayout(t)
	if err := VerifyCanonicalClassAccessLayout(layout); err != nil {
		t.Fatal(err)
	}
	if len(layout.Base.Properties) != 2 || len(layout.Derived.Properties) != 2 {
		t.Fatal("access layout does not contain exactly two inherited fields")
	}
	for index := range layout.Base.Properties {
		if layout.Base.Properties[index].FieldOffset != layout.Derived.Properties[index].FieldOffset {
			t.Fatalf("field %d moved in derived layout", index+1)
		}
	}
	if layout.Base.Properties[0].FieldOffset >= layout.Base.Properties[1].FieldOffset {
		t.Fatal("private/protected declaration order was not preserved")
	}
}

func TestClassAccessLayoutRoundTripAndTamperRejection(t *testing.T) {
	layout := testClassAccessLayout(t)
	encoded, hash, err := CanonicalClassAccessLayout(layout)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeClassAccessLayout(encoded)
	if err != nil || decoded.ContentHash != hash {
		t.Fatalf("access layout decode = %#v / %v", decoded, err)
	}
	for name, mutate := range map[string]func(*ClassAccessLayoutContract){
		"MIR hash":         func(l *ClassAccessLayoutContract) { l.MIRHash = typeKeyC },
		"base class":       func(l *ClassAccessLayoutContract) { l.BaseClassID = 2 },
		"base type":        func(l *ClassAccessLayoutContract) { l.Base.TypeKey = typeKeyC },
		"derived type":     func(l *ClassAccessLayoutContract) { l.Derived.TypeKey = typeKeyA },
		"private symbol":   func(l *ClassAccessLayoutContract) { l.Base.Properties[0].Key = "other" },
		"protected offset": func(l *ClassAccessLayoutContract) { l.Derived.Properties[1].FieldOffset += 8 },
		"representation":   func(l *ClassAccessLayoutContract) { l.Base.Properties[0].Representation = "gc-ref" },
		"trace offset": func(l *ClassAccessLayoutContract) {
			l.Derived.TraceOffsets = []uint32{l.Derived.Properties[0].FieldOffset}
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := testClassAccessLayout(t)
			mutate(&candidate)
			if err := VerifyClassAccessLayout(candidate); err == nil {
				t.Fatal("tampered access layout accepted")
			}
		})
	}
}
