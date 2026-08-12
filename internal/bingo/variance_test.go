package bingo

import (
	"bytes"
	"strings"
	"testing"
)

func TestVarianceContractCanonicalInference(t *testing.T) {
	contract, err := BuildVarianceContract("module.BoxConsumerCell", []VarianceParameter{
		{ID: 1, Name: "Out", Annotation: VarianceAnnotationOut, TsgoHint: VarianceHintCovariant},
		{ID: 2, Name: "In", Annotation: VarianceAnnotationIn, TsgoHint: VarianceHintContravariant},
		{ID: 3, Name: "Cell", Annotation: VarianceAnnotationNone, TsgoHint: VarianceHintCovariant},
		{ID: 4, Name: "Residual", Annotation: VarianceAnnotationNone, TsgoHint: VarianceHintUnmeasurable},
	}, []VarianceOccurrence{
		{ID: 1, ParameterID: 1, Kind: VarianceReadonlyProperty, SourceOrder: 1, Path: "Box.value"},
		{ID: 2, ParameterID: 2, Kind: VarianceFunctionParameter, SourceOrder: 2, Path: "Consumer.consume.parameter[0]"},
		{ID: 3, ParameterID: 3, Kind: VarianceWritableProperty, SourceOrder: 3, Path: "Cell.value"},
		{ID: 4, ParameterID: 4, Kind: VarianceResidual, SourceOrder: 4, Path: "Residual.mapped"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []VarianceProof{
		{ParameterID: 1, Inferred: VariancePositive, DirectABIReuse: true, Reason: VarianceReasonDirectCovariant},
		{ParameterID: 2, Inferred: VarianceNegative, DirectABIReuse: true, Reason: VarianceReasonDirectContravariant},
		{ParameterID: 3, Inferred: VarianceBoth, Reason: VarianceReasonInvariant},
		{ParameterID: 4, Inferred: VarianceUnknown, Reason: VarianceReasonHintUnmeasurable},
	}
	if !equalVarianceProofs(contract.Proofs, want) {
		t.Fatalf("proofs = %#v, want %#v", contract.Proofs, want)
	}
	encoded, hash, err := CanonicalVarianceContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeVarianceContract(encoded)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, rehash, err := CanonicalVarianceContract(*decoded)
	if err != nil {
		t.Fatal(err)
	}
	if hash != rehash || !bytes.Equal(encoded, reencoded) {
		t.Fatal("variance contract round trip is not canonical")
	}
}

func TestVarianceContractRejectsForgedAndNonCanonicalEvidence(t *testing.T) {
	base, err := BuildVarianceContract("module.Box", []VarianceParameter{{ID: 1, Name: "T", Annotation: VarianceAnnotationOut, TsgoHint: VarianceHintCovariant}}, []VarianceOccurrence{{ID: 1, ParameterID: 1, Kind: VarianceReadonlyProperty, SourceOrder: 1, Path: "Box.value"}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _, err := CanonicalVarianceContract(base)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"unknown member": []byte(strings.Replace(string(encoded), `"contentHash":`, `"unknown":true,"contentHash":`, 1)),
		"stale hash":     []byte(strings.Replace(string(encoded), base.ContentHash, strings.Repeat("0", 64), 1)),
		"forged proof":   []byte(strings.Replace(string(encoded), `"directAbiReuse":true`, `"directAbiReuse":false`, 1)),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeVarianceContract(data); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}

	conflict := base
	conflict.Occurrences[0].Kind = VarianceFunctionParameter
	conflict.Proofs[0] = VarianceProof{ParameterID: 1, Inferred: VarianceNegative, Reason: VarianceReasonAnnotationConflict}
	if _, _, err := CanonicalVarianceContract(conflict); err != nil {
		t.Fatal(err)
	}
	if conflict.Proofs[0].DirectABIReuse {
		t.Fatal("annotation conflict must not admit direct ABI reuse")
	}

	reordered := base
	reordered.Occurrences = []VarianceOccurrence{
		{ID: 1, ParameterID: 1, Kind: VarianceReadonlyProperty, SourceOrder: 2, Path: "Box.z"},
		{ID: 2, ParameterID: 1, Kind: VarianceReadonlyProperty, SourceOrder: 1, Path: "Box.a"},
	}
	reordered.Proofs = []VarianceProof{{ParameterID: 1, Inferred: VariancePositive, DirectABIReuse: true, Reason: VarianceReasonDirectCovariant}}
	if _, _, err := CanonicalVarianceContract(reordered); err == nil {
		t.Fatal("expected non-canonical occurrence rejection")
	}
}

func TestVarianceHintBivarianceNeverAdmitsDirectReuse(t *testing.T) {
	contract, err := BuildVarianceContract("module.Method", []VarianceParameter{{ID: 1, Name: "T", Annotation: VarianceAnnotationIn, TsgoHint: VarianceHintBivariant}}, []VarianceOccurrence{{ID: 1, ParameterID: 1, Kind: VarianceFunctionParameter, SourceOrder: 1, Path: "Method.call.parameter[0]"}})
	if err != nil {
		t.Fatal(err)
	}
	if contract.Proofs[0].DirectABIReuse || contract.Proofs[0].Reason != VarianceReasonHintBivariant {
		t.Fatalf("unexpected bivariant proof: %#v", contract.Proofs[0])
	}
}

func TestDecodeVarianceContractRejectsOversizedInput(t *testing.T) {
	if _, err := DecodeVarianceContract(make([]byte, maxVarianceContractBytes+1)); err == nil {
		t.Fatal("expected oversized input rejection")
	}
}
