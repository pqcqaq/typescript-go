package frontendwire

import "testing"

func TestSnapshotEffectMetadataRecognizesPortableTypeKinds(t *testing.T) {
	tests := []struct {
		kind       string
		definitely bool
		possibly   bool
	}{
		{kind: "KindNumberKeyword", definitely: true, possibly: true},
		{kind: "KindTypeLiteral", definitely: true, possibly: true},
		{kind: "KindImportType", definitely: true, possibly: true},
		{kind: "KindExpressionWithTypeArguments", possibly: true},
		{kind: "KindParameter", possibly: true},
	}
	for _, test := range tests {
		metadata, ok := snapshotEffectMetadataByKind[test.kind]
		if !ok {
			t.Fatalf("Kind metadata is missing %s", test.kind)
		}
		if metadata.definitelyType != test.definitely || metadata.possiblyType != test.possibly {
			t.Errorf("%s type metadata = definitely:%t possibly:%t, want definitely:%t possibly:%t", test.kind, metadata.definitelyType, metadata.possiblyType, test.definitely, test.possibly)
		}
	}
}
