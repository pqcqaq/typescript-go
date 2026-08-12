package bingo

import (
	"strings"
	"testing"
)

func testCheckedObjectCast(t testing.TB) CheckedObjectCastContract {
	t.Helper()
	target := baseObjectContract("object-a", []ObjectPropertyContract{{Key: "value", Kind: ObjectPropertyData, ReadTypeKey: typeKeyA, Readonly: true, Visibility: "public"}})
	_, targetHash, err := CanonicalObjectSemanticContract(target)
	if err != nil {
		t.Fatal(err)
	}
	target.ContentHash = targetHash
	layout, err := PlanObjectLayout(target.TypeKey, objectLayoutFuzzTarget(), []ObjectLayoutPropertyInput{{Key: "value", Kind: ObjectPropertyData, Representation: "f64"}})
	if err != nil {
		t.Fatal(err)
	}
	boundary := DynamicObjectBoundaryArtifact{SchemaVersion: DynamicObjectBoundarySchemaVersion, Kind: "ffi-import", SourceID: "ffi.import.payload"}
	_, boundaryHash, err := CanonicalDynamicObjectBoundary(boundary)
	if err != nil {
		t.Fatal(err)
	}
	boundary.ContentHash = boundaryHash
	c := CheckedObjectCastContract{SchemaVersion: CheckedObjectCastSchemaVersion, Boundary: boundary, SourceTypeKey: typeKeyB, Target: target, TargetLayout: layout, Properties: []string{"value"}, PreservesIdentity: true, ReadonlyResult: true}
	_, h, err := CanonicalCheckedObjectCast(c)
	if err != nil {
		t.Fatal(err)
	}
	c.ContentHash = h
	return c
}

func TestCheckedObjectCastCanonicalRoundTrip(t *testing.T) {
	c := testCheckedObjectCast(t)
	b, _, err := CanonicalCheckedObjectCast(c)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeCheckedObjectCast(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.Boundary.SourceID != c.Boundary.SourceID || !got.PreservesIdentity || !got.ReadonlyResult {
		t.Fatalf("unexpected cast: %#v", got)
	}
}

func TestCheckedObjectCastRejectsSubstitution(t *testing.T) {
	base := testCheckedObjectCast(t)
	tests := map[string]func(*CheckedObjectCastContract){
		"static source":         func(c *CheckedObjectCastContract) { c.Boundary = DynamicObjectBoundaryArtifact{} },
		"assertion source":      func(c *CheckedObjectCastContract) { c.Boundary.Kind = "typescript-assertion" },
		"boundary substitution": func(c *CheckedObjectCastContract) { c.Boundary.SourceID = "other" },
		"write exposure":        func(c *CheckedObjectCastContract) { c.ReadonlyResult = false },
		"identity change":       func(c *CheckedObjectCastContract) { c.PreservesIdentity = false },
		"optional":              func(c *CheckedObjectCastContract) { c.Target.Properties[0].Optional = true },
		"accessor":              func(c *CheckedObjectCastContract) { c.Target.Properties[0].Kind = ObjectPropertyAccessor },
		"private": func(c *CheckedObjectCastContract) {
			c.Target.Properties[0].Visibility = "private"
			c.Target.Properties[0].PrivateIdentity = typeKeyC
		},
		"writable": func(c *CheckedObjectCastContract) {
			c.Target.Properties[0].Readonly = false
			c.Target.Properties[0].WriteTypeKey = typeKeyA
		},
		"layout type":           func(c *CheckedObjectCastContract) { c.TargetLayout.TypeKey = typeKeyC },
		"layout hash":           func(c *CheckedObjectCastContract) { c.TargetLayout.ContentHash = typeKeyC },
		"property substitution": func(c *CheckedObjectCastContract) { c.Properties[0] = "other" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			c := base
			c.Properties = append([]string(nil), base.Properties...)
			c.Target.Properties = append([]ObjectPropertyContract(nil), base.Target.Properties...)
			mutate(&c)
			if _, _, err := CanonicalCheckedObjectCast(c); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestCheckedObjectCastStrictDecode(t *testing.T) {
	b, _, err := CanonicalCheckedObjectCast(testCheckedObjectCast(t))
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"unknown": []byte(strings.Replace(string(b), `"contentHash":`, `"unknown":true,"contentHash":`, 1)), "stale schema": []byte(strings.Replace(string(b), `"schemaVersion":1`, `"schemaVersion":2`, 1)), "oversize": make([]byte, maxCheckedObjectCastBytes+1)} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeCheckedObjectCast(data); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func testCheckedObjectCastBound(t testing.TB) CheckedObjectCastBoundContract {
	t.Helper()
	bound, err := NewCheckedObjectCastBound(testCheckedObjectCast(t), typeKeyA, typeKeyB, BoundCapability{LogicalName: CheckedObjectCastCapability, SymbolName: CheckedObjectCastSymbol, SignatureHash: typeKeyC})
	if err != nil {
		t.Fatal(err)
	}
	return bound
}

func TestCheckedObjectCastBoundRoundTripAndTamper(t *testing.T) {
	bound := testCheckedObjectCastBound(t)
	b, _, err := CanonicalCheckedObjectCastBound(bound)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeCheckedObjectCastBound(b); err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*CheckedObjectCastBoundContract){
		"context":   func(v *CheckedObjectCastBoundContract) { v.TargetContextHash = typeKeyC },
		"catalog":   func(v *CheckedObjectCastBoundContract) { v.AvailableCapabilityCatalogHash = typeKeyC },
		"logical":   func(v *CheckedObjectCastBoundContract) { v.Binding.LogicalName = "rt.gc.alloc" },
		"symbol":    func(v *CheckedObjectCastBoundContract) { v.Binding.SymbolName = "forged" },
		"signature": func(v *CheckedObjectCastBoundContract) { v.Binding.SignatureHash = "bad" },
		"cast":      func(v *CheckedObjectCastBoundContract) { v.Cast.ReadonlyResult = false },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			v := bound
			mutate(&v)
			if err := VerifyCanonicalCheckedObjectCastBound(v); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}
