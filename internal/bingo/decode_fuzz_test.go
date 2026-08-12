package bingo

import (
	"strings"
	"testing"
)

func FuzzDecodePhase2HIR(f *testing.F) {
	hir := validPhase2ChooseHIR()
	encoded, hash, err := CanonicalPhase2HIR(hir)
	if err != nil {
		f.Fatal(err)
	}
	hir.ContentHash = hash
	encoded, _, err = CanonicalPhase2HIR(hir)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		module, err := DecodePhase2HIR(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalPhase2HIR(*module)
		if err != nil {
			t.Fatalf("accepted HIR is not canonical: %v", err)
		}
		if _, err := DecodePhase2HIR(canonical); err != nil {
			t.Fatalf("canonical HIR does not round trip: %v", err)
		}
	})
}

func FuzzDecodeStructuralFirstSliceMIR(f *testing.F) {
	hir := validPhase2ChooseHIR()
	_, hirHash, err := CanonicalPhase2HIR(hir)
	if err != nil {
		f.Fatal(err)
	}
	hir.ContentHash = hirHash
	plan, err := NewRepresentationPlanForHIR(phase2ChooseProvenance(hir), hir)
	if err != nil {
		f.Fatal(err)
	}
	mir, err := LowerFirstSliceMIR(hir, plan)
	if err != nil {
		f.Fatal(err)
	}
	encoded, err := mir.CanonicalStructuralBytes()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		module, err := DecodeStructuralFirstSliceMIR(data)
		if err != nil {
			return
		}
		canonical, err := module.CanonicalStructuralBytes()
		if err != nil {
			t.Fatalf("accepted MIR is not canonical: %v", err)
		}
		if _, err := DecodeStructuralFirstSliceMIR(canonical); err != nil {
			t.Fatalf("canonical MIR does not round trip: %v", err)
		}
	})
}

func FuzzDecodeObjectSemanticContract(f *testing.F) {
	contract := baseObjectContract("object-a", []ObjectPropertyContract{{
		Key: "value", Kind: ObjectPropertyData, ReadTypeKey: typeKeyA, Readonly: true, Visibility: "public",
	}})
	encoded, _, err := CanonicalObjectSemanticContract(contract)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<18 {
			return
		}
		contract, err := DecodeObjectSemanticContract(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalObjectSemanticContract(*contract)
		if err != nil {
			t.Fatalf("accepted object contract is not canonical: %v", err)
		}
		if _, err := DecodeObjectSemanticContract(canonical); err != nil {
			t.Fatalf("canonical object contract does not round trip: %v", err)
		}
	})
}

func FuzzDecodeCheckedObjectCast(f *testing.F) {
	cast := testCheckedObjectCast(f)
	encoded, _, err := CanonicalCheckedObjectCast(cast)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxCheckedObjectCastBytes {
			return
		}
		cast, err := DecodeCheckedObjectCast(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalCheckedObjectCast(*cast)
		if err != nil {
			t.Fatalf("accepted checked object cast is not canonical: %v", err)
		}
		if _, err := DecodeCheckedObjectCast(canonical); err != nil {
			t.Fatalf("canonical checked object cast does not round trip: %v", err)
		}
	})
}

func FuzzDecodeCheckedObjectCastBound(f *testing.F) {
	bound := testCheckedObjectCastBound(f)
	encoded, _, err := CanonicalCheckedObjectCastBound(bound)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxCheckedObjectCastBoundBytes {
			return
		}
		bound, err := DecodeCheckedObjectCastBound(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalCheckedObjectCastBound(*bound)
		if err != nil {
			t.Fatalf("accepted checked object cast bound is not canonical: %v", err)
		}
		if _, err := DecodeCheckedObjectCastBound(canonical); err != nil {
			t.Fatalf("canonical checked object cast bound does not round trip: %v", err)
		}
	})
}

func FuzzDecodeClosureContract(f *testing.F) {
	contract := testClosureContract()
	encoded, _, err := CanonicalClosureContract(contract)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<18 {
			return
		}
		contract, err := DecodeClosureContract(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalClosureContract(*contract)
		if err != nil {
			t.Fatalf("accepted closure contract is not canonical: %v", err)
		}
		if _, err := DecodeClosureContract(canonical); err != nil {
			t.Fatalf("canonical closure contract does not round trip: %v", err)
		}
	})
}

func FuzzDecodeObjectLayoutContract(f *testing.F) {
	layout, err := PlanObjectLayout(typeKeyA, objectLayoutFuzzTarget(), []ObjectLayoutPropertyInput{{Key: "value", Kind: ObjectPropertyData, Representation: "f64"}})
	if err != nil {
		f.Fatal(err)
	}
	encoded, _, err := CanonicalObjectLayoutContract(layout)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<18 {
			return
		}
		layout, err := DecodeObjectLayoutContract(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalObjectLayoutContract(*layout)
		if err != nil {
			t.Fatalf("accepted object layout is not canonical: %v", err)
		}
		if _, err := DecodeObjectLayoutContract(canonical); err != nil {
			t.Fatalf("canonical object layout does not round trip: %v", err)
		}
	})
}

func FuzzDecodePlaceRefContract(f *testing.F) {
	encoded, _, err := CanonicalPlaceRefContract(testPlaceRefContract())
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxPlaceRefContractBytes {
			return
		}
		contract, err := DecodePlaceRefContract(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalPlaceRefContract(*contract)
		if err != nil {
			t.Fatalf("accepted PlaceRef contract is not canonical: %v", err)
		}
		if _, err := DecodePlaceRefContract(canonical); err != nil {
			t.Fatalf("canonical PlaceRef contract does not round trip: %v", err)
		}
	})
}

func objectLayoutFuzzTarget() ObjectLayoutTarget {
	target, _ := CanonicalObjectLayoutTarget(ObjectLayoutX8664Triple)
	return target
}

func FuzzDecodeGCSafetyPlan(f *testing.F) {
	plan, err := FinalizeGCSafetyPlan(GCSafetyPlan{
		FunctionKey: typeKeyA,
		Slots:       []GCRootSlot{{ID: 1, TraceLayoutHash: typeKeyB}},
		Blocks: []GCSafetyBlock{{ID: 1, Instructions: []GCInstruction{
			{ID: 1, Kind: GCOpFrameLink},
			{ID: 2, Kind: GCOpRefDef, Value: 1},
			{ID: 3, Kind: GCOpRootStore, Slot: 1, Value: 1},
			{ID: 4, Kind: GCOpRootPublish, ActiveSlots: []GCRootSlotID{1}},
			{ID: 5, Kind: GCOpSafepoint, SafepointKind: "allocation", MayAllocate: true},
			{ID: 6, Kind: GCOpRootReload, Slot: 1, Value: 1},
			{ID: 7, Kind: GCOpRefUse, Uses: []GCValueID{1}},
			{ID: 8, Kind: GCOpFrameUnlink},
		}, Terminator: "return"}},
	})
	if err != nil {
		f.Fatal(err)
	}
	encoded, _, err := CanonicalGCSafetyPlan(plan)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<18 {
			return
		}
		plan, err := DecodeGCSafetyPlan(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalGCSafetyPlan(*plan)
		if err != nil {
			t.Fatalf("accepted GC plan is not canonical: %v", err)
		}
		if _, err := DecodeGCSafetyPlan(canonical); err != nil {
			t.Fatalf("canonical GC plan does not round trip: %v", err)
		}
	})
}

func FuzzDecodeVERT010ObjectHIR(f *testing.F) {
	module := testVERT010Module()
	encoded, hash, err := CanonicalVERT010ObjectHIR(module)
	if err != nil {
		f.Fatal(err)
	}
	module.ContentHash = hash
	encoded, _, err = CanonicalVERT010ObjectHIR(module)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<18 {
			return
		}
		module, err := DecodeVERT010ObjectHIR(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalVERT010ObjectHIR(*module)
		if err != nil {
			t.Fatalf("accepted VERT-010 HIR is not canonical: %v", err)
		}
		if _, err := DecodeVERT010ObjectHIR(canonical); err != nil {
			t.Fatalf("canonical VERT-010 HIR does not round trip: %v", err)
		}
	})
}

func FuzzDecodeVERT011PlaceHIR(f *testing.F) {
	module := testVERT011HIR(f)
	encoded, _, err := CanonicalVERT011PlaceHIR(module)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<18 {
			return
		}
		module, err := DecodeVERT011PlaceHIR(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalVERT011PlaceHIR(*module)
		if err != nil {
			t.Fatalf("accepted VERT-011 HIR is not canonical: %v", err)
		}
		if _, err := DecodeVERT011PlaceHIR(canonical); err != nil {
			t.Fatalf("canonical VERT-011 HIR does not round trip: %v", err)
		}
	})
}

func FuzzDecodeVERT011MIR(f *testing.F) {
	module := testVERT011MIR(f)
	encoded, _, err := CanonicalVERT011MIR(module)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<18 {
			return
		}
		module, err := DecodeVERT011MIR(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalVERT011MIR(*module)
		if err != nil {
			t.Fatalf("accepted VERT-011 MIR is not canonical: %v", err)
		}
		if _, err := DecodeVERT011MIR(canonical); err != nil {
			t.Fatalf("canonical VERT-011 MIR does not round trip: %v", err)
		}
	})
}

func FuzzDecodeVERT012MIR(f *testing.F) {
	module := testVERT012MIR(f)
	encoded, _, err := CanonicalVERT012MIR(module)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<18 {
			return
		}
		module, err := DecodeVERT012MIR(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalVERT012MIR(*module)
		if err != nil {
			t.Fatalf("accepted VERT-012 MIR is not canonical: %v", err)
		}
		if _, err := DecodeVERT012MIR(canonical); err != nil {
			t.Fatalf("canonical VERT-012 MIR does not round trip: %v", err)
		}
	})
}

func FuzzDecodeVERT010MIR(f *testing.F) {
	module := testVERT010MIRForFuzz(f)
	encoded, _, err := CanonicalVERT010MIR(module)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<18 {
			return
		}
		module, err := DecodeVERT010MIR(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalVERT010MIR(*module)
		if err != nil {
			t.Fatalf("accepted VERT-010 MIR is not canonical: %v", err)
		}
		if _, err := DecodeVERT010MIR(canonical); err != nil {
			t.Fatalf("canonical VERT-010 MIR does not round trip: %v", err)
		}
	})
}

func FuzzDecodeVERT010BoundMIR(f *testing.F) {
	module := testVERT010MIRForFuzz(f)
	bindings := make([]BoundCapability, len(module.LogicalCapabilityRequirements))
	for index, requirement := range module.LogicalCapabilityRequirements {
		bindings[index] = BoundCapability{LogicalName: requirement, SymbolName: "fuzz_" + string(requirement), SignatureHash: typeKeyA}
	}
	bound, err := NewVERT010BoundMIR(module, typeKeyB, typeKeyC, bindings)
	if err != nil {
		f.Fatal(err)
	}
	encoded, _, err := CanonicalVERT010BoundMIR(bound)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<18 {
			return
		}
		bound, err := DecodeVERT010BoundMIR(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalVERT010BoundMIR(*bound)
		if err != nil {
			t.Fatalf("accepted VERT-010 bound MIR is not canonical: %v", err)
		}
		if _, err := DecodeVERT010BoundMIR(canonical); err != nil {
			t.Fatalf("canonical VERT-010 bound MIR does not round trip: %v", err)
		}
	})
}

func testVERT010MIRForFuzz(f *testing.F) VERT010MIRModule {
	f.Helper()
	module := testVERT010MIR(f)
	_, hash, err := CanonicalVERT010MIR(module)
	if err != nil {
		f.Fatal(err)
	}
	module.ContentHash = hash
	return module
}

func FuzzDecodeClassContract(f *testing.F) {
	contract := testClassContract()
	encoded, _, err := CanonicalClassContract(contract)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxClassContractBytes+1 {
			return
		}
		contract, err := DecodeClassContract(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalClassContract(*contract)
		if err != nil {
			t.Fatalf("accepted class contract is not canonical: %v", err)
		}
		if _, err := DecodeClassContract(canonical); err != nil {
			t.Fatalf("canonical class contract does not round trip: %v", err)
		}
	})
}

func FuzzDecodeVERT013aMIR(f *testing.F) {
	module := testVERT013aMIR(f)
	encoded, _, err := CanonicalVERT013aMIR(module)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<18 {
			return
		}
		module, err := DecodeVERT013aMIR(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalVERT013aMIR(*module)
		if err != nil {
			t.Fatalf("accepted VERT-013a MIR is not canonical: %v", err)
		}
		if _, err := DecodeVERT013aMIR(canonical); err != nil {
			t.Fatalf("canonical VERT-013a MIR does not round trip: %v", err)
		}
	})
}

func FuzzDecodeVERT013bClassContract(f *testing.F) {
	contract := testVERT013bContract(f)
	encoded, _, err := CanonicalVERT013bClassContract(contract)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxVERT013bClassContractBytes+1 {
			return
		}
		contract, err := DecodeVERT013bClassContract(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalVERT013bClassContract(*contract)
		if err != nil {
			t.Fatalf("accepted VERT-013b contract is not canonical: %v", err)
		}
		if _, err := DecodeVERT013bClassContract(canonical); err != nil {
			t.Fatalf("canonical VERT-013b contract does not round trip: %v", err)
		}
	})
}

func FuzzDecodeClassAccessContract(f *testing.F) {
	contract := testClassAccessContract()
	encoded, _, err := CanonicalClassAccessContract(contract)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxClassAccessContractBytes+1 {
			return
		}
		contract, err := DecodeClassAccessContract(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalClassAccessContract(*contract)
		if err != nil {
			t.Fatalf("accepted class access contract is not canonical: %v", err)
		}
		if _, err := DecodeClassAccessContract(canonical); err != nil {
			t.Fatalf("canonical class access contract does not round trip: %v", err)
		}
	})
}

func FuzzDecodeClassAccessHIR(f *testing.F) {
	contract := testClassAccessContract()
	_, hash, err := CanonicalClassAccessContract(contract)
	if err != nil {
		f.Fatal(err)
	}
	contract.ContentHash = hash
	module, err := NewClassAccessHIR(testHIRProvenance(ClassAccessLogicalCapabilities()), contract)
	if err != nil {
		f.Fatal(err)
	}
	encoded, _, err := CanonicalClassAccessHIR(module)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxClassAccessHIRBytes+1 {
			return
		}
		module, err := DecodeClassAccessHIR(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalClassAccessHIR(*module)
		if err != nil {
			t.Fatalf("accepted class access HIR is not canonical: %v", err)
		}
		if _, err := DecodeClassAccessHIR(canonical); err != nil {
			t.Fatalf("canonical class access HIR does not round trip: %v", err)
		}
	})
}

func FuzzDecodeClassAccessExecution(f *testing.F) {
	execution := testClassAccessExecution(f)
	encoded, _, err := CanonicalClassAccessExecution(execution)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxClassAccessExecutionContractBytes+1 {
			return
		}
		execution, err := DecodeClassAccessExecution(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalClassAccessExecution(*execution)
		if err != nil {
			t.Fatalf("accepted class access execution is not canonical: %v", err)
		}
		if _, err := DecodeClassAccessExecution(canonical); err != nil {
			t.Fatalf("canonical class access execution does not round trip: %v", err)
		}
	})
}

func FuzzDecodeClassAccessMIR(f *testing.F) {
	module := testClassAccessMIR(f)
	encoded, _, err := CanonicalClassAccessMIR(module)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxClassAccessMIRBytes+1 {
			return
		}
		module, err := DecodeClassAccessMIR(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalClassAccessMIR(*module)
		if err != nil {
			t.Fatalf("accepted class access MIR is not canonical: %v", err)
		}
		if _, err := DecodeClassAccessMIR(canonical); err != nil {
			t.Fatalf("canonical class access MIR does not round trip: %v", err)
		}
	})
}

func FuzzDecodeClassAccessLayout(f *testing.F) {
	layout := testClassAccessLayout(f)
	encoded, _, err := CanonicalClassAccessLayout(layout)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxClassAccessLayoutBytes+1 {
			return
		}
		layout, err := DecodeClassAccessLayout(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalClassAccessLayout(*layout)
		if err != nil {
			t.Fatalf("accepted class access layout is not canonical: %v", err)
		}
		if _, err := DecodeClassAccessLayout(canonical); err != nil {
			t.Fatalf("canonical class access layout does not round trip: %v", err)
		}
	})
}

func FuzzDecodeClassAccessBoundMIR(f *testing.F) {
	layout := testClassAccessLayout(f)
	bindings := make([]BoundCapability, 0, len(layout.MIR.HIR.LogicalCapabilityRequirements))
	for _, requirement := range layout.MIR.HIR.LogicalCapabilityRequirements {
		bindings = append(bindings, BoundCapability{LogicalName: requirement, SymbolName: "rt_" + string(requirement), SignatureHash: typeKeyA})
	}
	bound, err := NewClassAccessBoundMIR(layout, layout.MIR.Target.TargetContextHash, typeKeyB, bindings)
	if err != nil {
		f.Fatal(err)
	}
	encoded, _, err := CanonicalClassAccessBoundMIR(bound)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxClassAccessBoundMIRBytes+1 {
			return
		}
		bound, err := DecodeClassAccessBoundMIR(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalClassAccessBoundMIR(*bound)
		if err != nil {
			t.Fatalf("accepted class access bound MIR is not canonical: %v", err)
		}
		if _, err := DecodeClassAccessBoundMIR(canonical); err != nil {
			t.Fatalf("canonical class access bound MIR does not round trip: %v", err)
		}
	})
}

func FuzzDecodeVarianceContract(f *testing.F) {
	contract, err := BuildVarianceContract("fuzz.Box", []VarianceParameter{{ID: 1, Name: "T", Annotation: VarianceAnnotationOut, TsgoHint: VarianceHintCovariant}}, []VarianceOccurrence{{ID: 1, ParameterID: 1, Kind: VarianceReadonlyProperty, SourceOrder: 1, Path: "Box.value"}})
	if err != nil {
		f.Fatal(err)
	}
	encoded, _, err := CanonicalVarianceContract(contract)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxVarianceContractBytes+1 {
			return
		}
		contract, err := DecodeVarianceContract(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalVarianceContract(*contract)
		if err != nil {
			t.Fatalf("accepted variance contract is not canonical: %v", err)
		}
		if _, err := DecodeVarianceContract(canonical); err != nil {
			t.Fatalf("canonical variance contract does not round trip: %v", err)
		}
	})
}

func FuzzDecodeVarianceGraph(f *testing.F) {
	contract, err := BuildVarianceContract("fuzz.Tree", []VarianceParameter{{ID: 1, Name: "T", Annotation: VarianceAnnotationNone, TsgoHint: VarianceHintCovariant}}, []VarianceOccurrence{{ID: 1, ParameterID: 1, Kind: VarianceReadonlyProperty, SourceOrder: 1, Path: "Tree.value"}})
	if err != nil {
		f.Fatal(err)
	}
	graph, err := BuildVarianceGraph([]VarianceContract{contract}, []VarianceDependencyEdge{{ID: 1, OwnerNodeID: 1, DependencyNodeID: 1, Transform: VarianceTransformPositive, Path: "Tree.next"}})
	if err != nil {
		f.Fatal(err)
	}
	encoded, _, err := CanonicalVarianceGraph(graph)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxVarianceGraphBytes+1 {
			return
		}
		graph, err := DecodeVarianceGraph(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalVarianceGraph(*graph)
		if err != nil {
			t.Fatalf("accepted variance graph is not canonical: %v", err)
		}
		if _, err := DecodeVarianceGraph(canonical); err != nil {
			t.Fatalf("canonical variance graph does not round trip: %v", err)
		}
	})
}

func FuzzDecodeTypeRelationGraph(f *testing.F) {
	graph, err := BuildTypeRelationGraph([]TypeRelationNode{{TypeKey: typeKeyA, DeclarationKey: typeKeyA}, {TypeKey: typeKeyB, DeclarationKey: typeKeyB}}, []TypeRelationEdge{{SubTypeKey: typeKeyB, SuperTypeKey: typeKeyA, Path: "B extends A"}})
	if err != nil {
		f.Fatal(err)
	}
	encoded, _, err := CanonicalTypeRelationGraph(graph)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxTypeRelationGraphBytes+1 {
			return
		}
		graph, err := DecodeTypeRelationGraph(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalTypeRelationGraph(*graph)
		if err != nil {
			t.Fatalf("accepted type relation graph is not canonical: %v", err)
		}
		if _, err := DecodeTypeRelationGraph(canonical); err != nil {
			t.Fatalf("canonical type relation graph does not round trip: %v", err)
		}
	})
}

func FuzzDecodeHIRVarianceConversionProof(f *testing.F) {
	declaration := strings.Repeat("d", 64)
	source := strings.Repeat("e", 64)
	target := strings.Repeat("f", 64)
	contract, err := BuildVarianceContract(declaration, []VarianceParameter{{ID: 1, Name: "T", Annotation: VarianceAnnotationOut, TsgoHint: VarianceHintCovariant}}, []VarianceOccurrence{{ID: 1, ParameterID: 1, Kind: VarianceReadonlyProperty, SourceOrder: 1, Path: "Box.value"}})
	if err != nil {
		f.Fatal(err)
	}
	variance, err := BuildVarianceGraph([]VarianceContract{contract}, nil)
	if err != nil {
		f.Fatal(err)
	}
	relations, err := BuildTypeRelationGraph([]TypeRelationNode{{TypeKey: typeKeyA, DeclarationKey: declaration, ArgumentKeys: []string{source}}, {TypeKey: typeKeyB, DeclarationKey: declaration, ArgumentKeys: []string{target}}, {TypeKey: source, DeclarationKey: source}, {TypeKey: target, DeclarationKey: target}}, []TypeRelationEdge{{SubTypeKey: source, SuperTypeKey: target, Path: "E extends F"}})
	if err != nil {
		f.Fatal(err)
	}
	left, err := PlanObjectLayout(typeKeyA, objectLayoutFuzzTarget(), []ObjectLayoutPropertyInput{{Key: "value", Kind: ObjectPropertyData, Representation: "gc-ref", Reference: true}})
	if err != nil {
		f.Fatal(err)
	}
	right, err := PlanObjectLayout(typeKeyB, objectLayoutFuzzTarget(), []ObjectLayoutPropertyInput{{Key: "value", Kind: ObjectPropertyData, Representation: "gc-ref", Reference: true}})
	if err != nil {
		f.Fatal(err)
	}
	proof, err := BuildHIRVarianceConversionProof(1, declaration, typeKeyA, typeKeyB, variance, relations, left, right)
	if err != nil {
		f.Fatal(err)
	}
	encoded, _, err := CanonicalHIRVarianceConversionProof(proof)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxHIRVarianceConversionBytes+1 {
			return
		}
		proof, err := DecodeHIRVarianceConversionProof(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalHIRVarianceConversionProof(*proof)
		if err != nil {
			t.Fatalf("accepted HIR variance conversion is not canonical: %v", err)
		}
		if _, err := DecodeHIRVarianceConversionProof(canonical); err != nil {
			t.Fatalf("canonical HIR variance conversion does not round trip: %v", err)
		}
	})
}

func FuzzDecodeHIRVarianceGate(f *testing.F) {
	hir := testVERT010Module()
	_, hash, err := CanonicalVERT010ObjectHIR(hir)
	if err != nil {
		f.Fatal(err)
	}
	hir.ContentHash = hash
	gate, err := BuildHIRVarianceGate(hir, 1, testHIRVarianceConversionProof(f))
	if err != nil {
		f.Fatal(err)
	}
	encoded, _, err := CanonicalHIRVarianceGate(gate)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxHIRVarianceGateBytes+1 {
			return
		}
		gate, err := DecodeHIRVarianceGate(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalHIRVarianceGate(*gate)
		if err != nil {
			t.Fatalf("accepted HIR variance gate is not canonical: %v", err)
		}
		if _, err := DecodeHIRVarianceGate(canonical); err != nil {
			t.Fatalf("canonical HIR variance gate does not round trip: %v", err)
		}
	})
}

func FuzzDecodeObjectViewProof(f *testing.F) {
	proof := testObjectViewProof(f)
	encoded, _, err := CanonicalObjectViewProof(proof)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxObjectViewBytes+1 {
			return
		}
		proof, err := DecodeObjectViewProof(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalObjectViewProof(*proof)
		if err != nil {
			t.Fatalf("accepted ObjectView proof is not canonical: %v", err)
		}
		if _, err := DecodeObjectViewProof(canonical); err != nil {
			t.Fatalf("canonical ObjectView proof does not round trip: %v", err)
		}
	})
}

func FuzzDecodeObjectLayoutCopyContract(f *testing.F) {
	contract := testObjectLayoutCopyContract(f)
	encoded, _, err := CanonicalObjectLayoutCopyContract(contract)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxObjectLayoutCopyBytes+1 {
			return
		}
		contract, err := DecodeObjectLayoutCopyContract(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalObjectLayoutCopyContract(*contract)
		if err != nil {
			t.Fatalf("accepted object layout copy is not canonical: %v", err)
		}
		if _, err := DecodeObjectLayoutCopyContract(canonical); err != nil {
			t.Fatalf("canonical object layout copy does not round trip: %v", err)
		}
	})
}

func FuzzDecodeObjectViewHIRGate(f *testing.F) {
	hir := testVERT010Module()
	_, hash, err := CanonicalVERT010ObjectHIR(hir)
	if err != nil {
		f.Fatal(err)
	}
	hir.ContentHash = hash
	gate, err := BuildObjectViewHIRGate(hir, 1, 2, testObjectViewProof(f))
	if err != nil {
		f.Fatal(err)
	}
	encoded, _, err := CanonicalObjectViewHIRGate(gate)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxObjectViewHIRGateBytes+1 {
			return
		}
		gate, err := DecodeObjectViewHIRGate(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalObjectViewHIRGate(*gate)
		if err != nil {
			t.Fatalf("accepted ObjectView HIR gate is not canonical: %v", err)
		}
		if _, err := DecodeObjectViewHIRGate(canonical); err != nil {
			t.Fatalf("canonical ObjectView HIR gate does not round trip: %v", err)
		}
	})
}

func FuzzDecodeObjectViewOperation(f *testing.F) {
	hir := testVERT010Module()
	_, hash, err := CanonicalVERT010ObjectHIR(hir)
	if err != nil {
		f.Fatal(err)
	}
	hir.ContentHash = hash
	gate, err := BuildObjectViewHIRGate(hir, 1, 2, testObjectViewProof(f))
	if err != nil {
		f.Fatal(err)
	}
	operation, err := BuildObjectViewOperation(gate)
	if err != nil {
		f.Fatal(err)
	}
	encoded, _, err := CanonicalObjectViewOperation(operation)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxObjectViewOperationBytes+1 {
			return
		}
		operation, err := DecodeObjectViewOperation(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalObjectViewOperation(*operation)
		if err != nil {
			t.Fatalf("accepted ObjectView operation is not canonical: %v", err)
		}
		if _, err := DecodeObjectViewOperation(canonical); err != nil {
			t.Fatalf("canonical ObjectView operation does not round trip: %v", err)
		}
	})
}

func FuzzDecodeObjectViewHIRArtifact(f *testing.F) {
	hir := testVERT010Module()
	_, hash, err := CanonicalVERT010ObjectHIR(hir)
	if err != nil {
		f.Fatal(err)
	}
	hir.ContentHash = hash
	gate, err := BuildObjectViewHIRGate(hir, 1, 2, testObjectViewProof(f))
	if err != nil {
		f.Fatal(err)
	}
	artifact, err := BuildObjectViewHIRArtifact(gate)
	if err != nil {
		f.Fatal(err)
	}
	encoded, _, err := CanonicalObjectViewHIRArtifact(artifact)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxObjectViewHIRArtifactBytes+1 {
			return
		}
		artifact, err := DecodeObjectViewHIRArtifact(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalObjectViewHIRArtifact(*artifact)
		if err != nil {
			t.Fatalf("accepted ObjectView HIR artifact is not canonical: %v", err)
		}
		if _, err := DecodeObjectViewHIRArtifact(canonical); err != nil {
			t.Fatalf("canonical ObjectView HIR artifact does not round trip: %v", err)
		}
	})
}

func FuzzDecodeObjectViewMIR(f *testing.F) {
	hir := testVERT010Module()
	_, hash, err := CanonicalVERT010ObjectHIR(hir)
	if err != nil {
		f.Fatal(err)
	}
	hir.ContentHash = hash
	gate, err := BuildObjectViewHIRGate(hir, 1, 2, testObjectViewProof(f))
	if err != nil {
		f.Fatal(err)
	}
	artifact, err := BuildObjectViewHIRArtifact(gate)
	if err != nil {
		f.Fatal(err)
	}
	mir, err := LowerObjectViewMIR(artifact)
	if err != nil {
		f.Fatal(err)
	}
	encoded, _, err := CanonicalObjectViewMIR(mir)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encoded)
	accessorEncoded, _, err := CanonicalObjectViewMIR(testObjectViewAccessorMIR(f))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(accessorEncoded)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxObjectViewMIRBytes+1 {
			return
		}
		mir, err := DecodeObjectViewMIR(data)
		if err != nil {
			return
		}
		canonical, _, err := CanonicalObjectViewMIR(*mir)
		if err != nil {
			t.Fatalf("accepted ObjectView MIR is not canonical: %v", err)
		}
		if _, err := DecodeObjectViewMIR(canonical); err != nil {
			t.Fatalf("canonical ObjectView MIR does not round trip: %v", err)
		}
	})
}
