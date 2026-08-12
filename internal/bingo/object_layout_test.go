package bingo

import (
	"strings"
	"testing"
)

func TestCanonicalObjectLayoutTargetsAreIndependent(t *testing.T) {
	x86 := objectLayoutTarget(t, ObjectLayoutX8664Triple)
	arm := objectLayoutTarget(t, ObjectLayoutAArch64Triple)
	if x86.DataLayout == arm.DataLayout || x86.DataLayoutHash == arm.DataLayoutHash {
		t.Fatal("authoritative target DataLayouts must have independent identities")
	}
	layoutHashes := make([]string, 0, 2)
	for _, target := range []ObjectLayoutTarget{x86, arm} {
		layout, err := PlanObjectLayout(typeKeyA, target, nil)
		if err != nil {
			t.Fatal(err)
		}
		assertObjectABIBlock(t, layout.Header, 24, 8, []uint32{0, 8, 16})
		assertObjectABIBlock(t, layout.Shape, 48, 8, []uint32{0, 4, 8, 16, 24, 28, 32, 40})
		assertObjectABIBlock(t, layout.Property, 40, 8, []uint32{0, 8, 9, 10, 12, 16, 20, 24, 32})
		assertObjectABIBlock(t, layout.Trace, 40, 8, []uint32{0, 4, 8, 16, 20, 24, 32})
		layoutHashes = append(layoutHashes, layout.LayoutHash)
	}
	if layoutHashes[0] == layoutHashes[1] {
		t.Fatal("equal extents on different DataLayouts must retain independent layout identities")
	}
}

func TestPlanObjectLayoutRejectsInconsistentInputs(t *testing.T) {
	target := objectLayoutTarget(t, ObjectLayoutX8664Triple)
	tests := map[string]ObjectLayoutPropertyInput{
		"duplicate key":     {Key: "x", Kind: ObjectPropertyData, Representation: "u8"},
		"accessor presence": {Key: "accessor", Kind: ObjectPropertyAccessor, Optional: true},
		"false reference":   {Key: "value", Kind: ObjectPropertyData, Representation: "f64", Reference: true},
	}
	for name, property := range tests {
		t.Run(name, func(t *testing.T) {
			properties := []ObjectLayoutPropertyInput{property}
			if name == "duplicate key" {
				properties = append(properties, property)
			}
			if _, err := PlanObjectLayout(typeKeyA, target, properties); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestPlanObjectLayoutMixedClosedShape(t *testing.T) {
	properties := []ObjectLayoutPropertyInput{
		{Key: "z", Kind: ObjectPropertyData, Representation: "u8"},
		{Key: "optional", Kind: ObjectPropertyData, Representation: "f64", Optional: true},
		{Key: "child", Kind: ObjectPropertyData, Representation: "gc-ref", Reference: true},
		{Key: "computed", Kind: ObjectPropertyAccessor},
	}
	layout, err := PlanObjectLayout(typeKeyA, objectLayoutTarget(t, ObjectLayoutX8664Triple), properties)
	if err != nil {
		t.Fatal(err)
	}
	if layout.PresenceWords != 1 || layout.ObjectSize != 56 || layout.ObjectAlign != 8 {
		t.Fatalf("object extent = %d/%d, presence words = %d", layout.ObjectSize, layout.ObjectAlign, layout.PresenceWords)
	}
	wantOffsets := []uint32{32, 40, 48, 0}
	for i, property := range layout.Properties {
		if property.FieldOffset != wantOffsets[i] || property.EnumerationOrder != uint32(i) {
			t.Fatalf("property %d = %#v", i, property)
		}
	}
	if layout.Properties[1].PresenceBit != 0 || layout.Properties[0].PresenceBit != -1 || layout.Properties[3].PresenceBit != -1 {
		t.Fatalf("presence mapping = %#v", layout.Properties)
	}
	if len(layout.TraceOffsets) != 1 || layout.TraceOffsets[0] != 48 {
		t.Fatalf("trace offsets = %v", layout.TraceOffsets)
	}
}

func TestPlanObjectLayoutNullableNumberBacking(t *testing.T) {
	layout, err := PlanObjectLayout(typeKeyA, objectLayoutTarget(t, ObjectLayoutX8664Triple), []ObjectLayoutPropertyInput{
		{Key: "backing", Kind: ObjectPropertyData, Representation: "nullable-f64"},
		{Key: "result", Kind: ObjectPropertyAccessor},
	})
	if err != nil {
		t.Fatal(err)
	}
	if layout.ObjectSize != 40 || layout.ObjectAlign != 8 || len(layout.TraceOffsets) != 0 {
		t.Fatalf("nullable object extent = %d/%d, trace = %v", layout.ObjectSize, layout.ObjectAlign, layout.TraceOffsets)
	}
	if backing := layout.Properties[0]; backing.FieldOffset != 24 || backing.Representation != "nullable-f64" || backing.PresenceBit != -1 {
		t.Fatalf("nullable backing layout = %#v", backing)
	}
	if accessor := layout.Properties[1]; accessor.FieldOffset != 0 || accessor.Representation != "" || accessor.PresenceBit != -1 {
		t.Fatalf("accessor layout = %#v", accessor)
	}
}

func TestObjectLayoutCanonicalRoundTrip(t *testing.T) {
	layout := validObjectLayout(t, typeKeyA)
	encoded, hash, err := CanonicalObjectLayoutContract(layout)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeObjectLayoutContract(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ContentHash != hash || decoded.LayoutHash != layout.LayoutHash {
		t.Fatalf("decoded layout = %#v", decoded)
	}
}

func TestObjectLayoutRejectsTampering(t *testing.T) {
	base := validObjectLayout(t, typeKeyA)
	tests := map[string]func(*ObjectLayoutContract){
		"schema":             func(value *ObjectLayoutContract) { value.SchemaHash = typeKeyB },
		"data layout":        func(value *ObjectLayoutContract) { value.Target.DataLayout = ObjectLayoutAArch64Data },
		"layout hash":        func(value *ObjectLayoutContract) { value.LayoutHash = typeKeyB },
		"fixed ABI":          func(value *ObjectLayoutContract) { value.Header.Fields[1].Offset++ },
		"field overlap":      func(value *ObjectLayoutContract) { value.Properties[1].FieldOffset = value.Properties[0].FieldOffset },
		"presence collision": func(value *ObjectLayoutContract) { value.Properties[1].PresenceBit = 1 },
		"accessor storage":   func(value *ObjectLayoutContract) { value.Properties[2].FieldOffset = 8 },
		"trace offset":       func(value *ObjectLayoutContract) { value.TraceOffsets[0]++ },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			value := cloneObjectLayout(t, base)
			mutate(&value)
			if _, _, err := CanonicalObjectLayoutContract(value); err == nil {
				t.Fatal("expected structural rejection")
			}
		})
	}
}

func TestDecodeObjectLayoutRejectsUnknownAndHashTamper(t *testing.T) {
	layout := validObjectLayout(t, typeKeyA)
	encoded, _, err := CanonicalObjectLayoutContract(layout)
	if err != nil {
		t.Fatal(err)
	}
	unknown := []byte(strings.Replace(string(encoded), `"contentHash":`, `"unknown":true,"contentHash":`, 1))
	if _, err := DecodeObjectLayoutContract(unknown); err == nil {
		t.Fatal("expected unknown member rejection")
	}
	tampered := []byte(strings.Replace(string(encoded), layout.ContentHash, typeKeyB, 1))
	if _, err := DecodeObjectLayoutContract(tampered); err == nil {
		t.Fatal("expected content hash rejection")
	}
}

func TestVerifyObjectLayoutMutableAlias(t *testing.T) {
	source := validObjectLayout(t, typeKeyA)
	target := validObjectLayout(t, typeKeyB)
	if source.ContentHash == target.ContentHash || source.LayoutHash != target.LayoutHash {
		t.Fatalf("semantic and physical hashes are not separated: source=%s/%s target=%s/%s", source.ContentHash, source.LayoutHash, target.ContentHash, target.LayoutHash)
	}
	if err := VerifyObjectLayoutMutableAlias(source, target); err != nil {
		t.Fatal(err)
	}
	target = cloneObjectLayout(t, target)
	target.Properties[0].Representation = "u32"
	target.Properties[0].FieldOffset = 32
	target.Properties[1].FieldOffset = 40
	target.ObjectSize = 48
	target.LayoutHash = objectPhysicalLayoutHash(target)
	_, hash, err := CanonicalObjectLayoutContract(target)
	if err != nil {
		t.Fatal(err)
	}
	target.ContentHash = hash
	if err := VerifyObjectLayoutMutableAlias(source, target); err == nil || !strings.Contains(err.Error(), "layout_mismatch") {
		t.Fatalf("expected layout mismatch, got %v", err)
	}
}

func validObjectLayout(t *testing.T, typeKey string) ObjectLayoutContract {
	t.Helper()
	layout, err := PlanObjectLayout(typeKey, objectLayoutTarget(t, ObjectLayoutX8664Triple), []ObjectLayoutPropertyInput{
		{Key: "value", Kind: ObjectPropertyData, Representation: "f64"},
		{Key: "child", Kind: ObjectPropertyData, Representation: "gc-ref", Optional: true, Reference: true},
		{Key: "accessor", Kind: ObjectPropertyAccessor},
	})
	if err != nil {
		t.Fatal(err)
	}
	return layout
}

func objectLayoutTarget(t *testing.T, triple string) ObjectLayoutTarget {
	t.Helper()
	target, err := CanonicalObjectLayoutTarget(triple)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func assertObjectABIBlock(t *testing.T, block ObjectLayoutBlock, size, align uint32, offsets []uint32) {
	t.Helper()
	if block.Size != size || block.Align != align || len(block.Fields) != len(offsets) {
		t.Fatalf("ABI block = %#v", block)
	}
	for i, offset := range offsets {
		if block.Fields[i].Offset != offset {
			t.Fatalf("field %d offset = %d, want %d", i, block.Fields[i].Offset, offset)
		}
	}
}

func cloneObjectLayout(t *testing.T, value ObjectLayoutContract) ObjectLayoutContract {
	t.Helper()
	encoded, _, err := CanonicalObjectLayoutContract(value)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeObjectLayoutContract(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return *decoded
}
