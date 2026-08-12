package bingo

import (
	"strings"
	"testing"
)

func TestGCSafetyPlanStraightLineRootAndBarrier(t *testing.T) {
	plan := validGCSafetyPlan(t)
	block := plan.Blocks[0]
	if len(block.LiveIn) != 0 || len(block.LiveOut) != 0 {
		t.Fatalf("block liveness = in %v, out %v", block.LiveIn, block.LiveOut)
	}
	if err := VerifyGCSafetyPlanStructure(plan); err != nil {
		t.Fatal(err)
	}
	unpublished := cloneGCSafetyPlan(t, plan)
	unpublished.Blocks[0].Instructions[8].OwnerPublished = false
	unpublished.Blocks[0].Instructions[8].Barrier = false
	unpublished.FrozenEffectEvents = gcEffectEvents(unpublished)
	if _, _, err := CanonicalGCSafetyPlan(unpublished); err != nil {
		t.Fatalf("unpublished initialization store was rejected: %v", err)
	}
}

func TestGCSafetyPlanRecomputesPhiAndLoopLiveness(t *testing.T) {
	phiPlan := GCSafetyPlan{FunctionKey: typeKeyA, Slots: []GCRootSlot{}, Blocks: []GCSafetyBlock{
		{ID: 1, Instructions: []GCInstruction{{ID: 1, Kind: GCOpFrameLink}}, Successors: []BlockID{2, 3}, Terminator: "condbranch"},
		{ID: 2, Instructions: []GCInstruction{{ID: 2, Kind: GCOpRefDef, Value: 1}}, Successors: []BlockID{4}, Terminator: "branch"},
		{ID: 3, Instructions: []GCInstruction{{ID: 3, Kind: GCOpRefDef, Value: 2}}, Successors: []BlockID{4}, Terminator: "branch"},
		{ID: 4, Instructions: []GCInstruction{{ID: 4, Kind: GCOpPhi, Value: 3, PhiIncoming: []GCPhiIncoming{{Block: 2, Value: 1}, {Block: 3, Value: 2}}}, {ID: 5, Kind: GCOpRefUse, Uses: []GCValueID{3}}, {ID: 6, Kind: GCOpFrameUnlink}}, Terminator: "return"},
	}}
	phiPlan = finalizedGCSafetyPlan(t, phiPlan)
	if !slicesEqualGCValues(phiPlan.Blocks[1].LiveOut, []GCValueID{1}) || !slicesEqualGCValues(phiPlan.Blocks[2].LiveOut, []GCValueID{2}) {
		t.Fatalf("phi edge liveness = %v / %v", phiPlan.Blocks[1].LiveOut, phiPlan.Blocks[2].LiveOut)
	}

	loopPlan := GCSafetyPlan{FunctionKey: typeKeyB, Slots: []GCRootSlot{}, Blocks: []GCSafetyBlock{
		{ID: 1, Instructions: []GCInstruction{{ID: 1, Kind: GCOpFrameLink}, {ID: 2, Kind: GCOpRefDef, Value: 1}}, Successors: []BlockID{2}, Terminator: "branch"},
		{ID: 2, Instructions: []GCInstruction{{ID: 3, Kind: GCOpPhi, Value: 2, PhiIncoming: []GCPhiIncoming{{Block: 1, Value: 1}, {Block: 3, Value: 3}}}, {ID: 4, Kind: GCOpRefUse, Uses: []GCValueID{2}}}, Successors: []BlockID{3, 4}, Terminator: "condbranch"},
		{ID: 3, Instructions: []GCInstruction{{ID: 5, Kind: GCOpRefDef, Value: 3}}, Successors: []BlockID{2}, Terminator: "branch"},
		{ID: 4, Instructions: []GCInstruction{{ID: 6, Kind: GCOpFrameUnlink}}, Terminator: "return"},
	}}
	loopPlan = finalizedGCSafetyPlan(t, loopPlan)
	if !slicesEqualGCValues(loopPlan.Blocks[0].LiveOut, []GCValueID{1}) || !slicesEqualGCValues(loopPlan.Blocks[2].LiveOut, []GCValueID{3}) {
		t.Fatalf("loop fixed-point liveness = %v / %v", loopPlan.Blocks[0].LiveOut, loopPlan.Blocks[2].LiveOut)
	}
}

func TestGCSafetyPlanAllowsMultipleNormalReturns(t *testing.T) {
	plan := finalizedGCSafetyPlan(t, GCSafetyPlan{FunctionKey: typeKeyA, Blocks: []GCSafetyBlock{
		{ID: 1, Instructions: []GCInstruction{{ID: 1, Kind: GCOpFrameLink}}, Successors: []BlockID{2, 3}, Terminator: "condbranch"},
		{ID: 2, Instructions: []GCInstruction{{ID: 2, Kind: GCOpFrameUnlink}}, Terminator: "return"},
		{ID: 3, Instructions: []GCInstruction{{ID: 3, Kind: GCOpFrameUnlink}}, Terminator: "return"},
	}})
	if err := VerifyGCSafetyPlanStructure(plan); err != nil {
		t.Fatal(err)
	}
}

func TestGCSafetyPlanCanonicalRoundTrip(t *testing.T) {
	plan := validGCSafetyPlan(t)
	encoded, hash, err := CanonicalGCSafetyPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeGCSafetyPlan(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ContentHash != hash || decoded.FunctionKey != plan.FunctionKey {
		t.Fatalf("decoded plan = %#v", decoded)
	}
}

func TestGCSafetyPlanRejectsMalformedProofs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*GCSafetyPlan)
		want   string
	}{
		{"liveness", func(plan *GCSafetyPlan) { plan.Blocks[0].LiveOut = []GCValueID{1} }, GCReasonLivenessMismatch},
		{"frame link", func(plan *GCSafetyPlan) { plan.Blocks[0].Instructions = plan.Blocks[0].Instructions[1:] }, GCReasonFrameLinkMissing},
		{"frame unlink", func(plan *GCSafetyPlan) {
			plan.Blocks[0].Instructions = plan.Blocks[0].Instructions[:len(plan.Blocks[0].Instructions)-1]
		}, GCReasonFrameUnlinkMissing},
		{"dead slot", func(plan *GCSafetyPlan) {
			plan.Blocks[0].Instructions = append(plan.Blocks[0].Instructions[:3], plan.Blocks[0].Instructions[4:]...)
		}, GCReasonDeadSlotNotCleared},
		{"active set", func(plan *GCSafetyPlan) { plan.Blocks[0].Instructions[4].ActiveSlots = []GCRootSlotID{1, 2} }, GCReasonRootPublicationInexact},
		{"reload", func(plan *GCSafetyPlan) {
			plan.Blocks[0].Instructions[6].Kind = GCOpRefUse
			plan.Blocks[0].Instructions[6].Uses = []GCValueID{1}
		}, GCReasonReloadMissing},
		{"barrier", func(plan *GCSafetyPlan) { plan.Blocks[0].Instructions[8].Barrier = false }, GCReasonBarrierMissing},
		{"spurious barrier", func(plan *GCSafetyPlan) { plan.Blocks[0].Instructions[8].OwnerPublished = false }, GCReasonBarrierSpurious},
		{"effect freeze", func(plan *GCSafetyPlan) {
			plan.FrozenEffectEvents = plan.FrozenEffectEvents[:len(plan.FrozenEffectEvents)-1]
		}, GCReasonEffectAfterFreeze},
		{"slot", func(plan *GCSafetyPlan) { plan.Slots[0].TraceLayoutHash = "bad" }, GCReasonRootSlotInvalid},
		{"duplicate instruction", func(plan *GCSafetyPlan) { plan.Blocks[0].Instructions[9].ID = 9 }, GCReasonCFGInvalid},
		{"orphan root event", func(plan *GCSafetyPlan) {
			plan.Blocks[0].Instructions[9].ID = 11
			plan.Blocks[0].Instructions = append(plan.Blocks[0].Instructions[:9], append([]GCInstruction{{ID: 10, Kind: GCOpRootClear, Slot: 1}}, plan.Blocks[0].Instructions[9:]...)...)
		}, GCReasonRootPublicationMissing},
		{"duplicate active value", func(plan *GCSafetyPlan) {
			plan.Blocks[0].Instructions[3] = GCInstruction{ID: 4, Kind: GCOpRootStore, Slot: 2, Value: 1}
			plan.Blocks[0].Instructions[4].ActiveSlots = []GCRootSlotID{1, 2}
		}, GCReasonRootPublicationInexact},
		{"unknown instruction", func(plan *GCSafetyPlan) { plan.Blocks[0].Instructions[7].Kind = "gc.future" }, GCReasonCFGInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := cloneGCSafetyPlan(t, validGCSafetyPlan(t))
			test.mutate(&plan)
			if test.name != "liveness" && test.name != "effect freeze" {
				plan.FrozenEffectEvents = gcEffectEvents(plan)
			}
			if _, _, err := CanonicalGCSafetyPlan(plan); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %s", err, test.want)
			}
		})
	}
}

func TestGCSafetyPlanRejectsMalformedCFGEdges(t *testing.T) {
	tests := []struct {
		name string
		plan GCSafetyPlan
	}{
		{"unreachable", GCSafetyPlan{FunctionKey: typeKeyA, Blocks: []GCSafetyBlock{
			{ID: 1, Instructions: []GCInstruction{{ID: 1, Kind: GCOpFrameLink}, {ID: 2, Kind: GCOpFrameUnlink}}, Terminator: "return"},
			{ID: 2, Instructions: []GCInstruction{{ID: 3, Kind: GCOpFrameUnlink}}, Terminator: "return"},
		}}},
		{"phi predecessor order", GCSafetyPlan{FunctionKey: typeKeyA, Blocks: []GCSafetyBlock{
			{ID: 1, Instructions: []GCInstruction{{ID: 1, Kind: GCOpFrameLink}}, Successors: []BlockID{2, 3}, Terminator: "condbranch"},
			{ID: 2, Instructions: []GCInstruction{{ID: 2, Kind: GCOpRefDef, Value: 1}}, Successors: []BlockID{4}, Terminator: "branch"},
			{ID: 3, Instructions: []GCInstruction{{ID: 3, Kind: GCOpRefDef, Value: 2}}, Successors: []BlockID{4}, Terminator: "branch"},
			{ID: 4, Instructions: []GCInstruction{{ID: 4, Kind: GCOpPhi, Value: 3, PhiIncoming: []GCPhiIncoming{{Block: 3, Value: 2}, {Block: 2, Value: 1}}}, {ID: 5, Kind: GCOpFrameUnlink}}, Terminator: "return"},
		}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := FinalizeGCSafetyPlan(test.plan); err == nil || !strings.Contains(err.Error(), GCReasonCFGInvalid) {
				t.Fatalf("error = %v, want %s", err, GCReasonCFGInvalid)
			}
		})
	}
}

func TestDecodeGCSafetyPlanRejectsUnknownAndHashTamper(t *testing.T) {
	plan := validGCSafetyPlan(t)
	encoded, _, err := CanonicalGCSafetyPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	unknown := []byte(strings.Replace(string(encoded), `"contentHash":`, `"unknown":true,"contentHash":`, 1))
	if _, err := DecodeGCSafetyPlan(unknown); err == nil {
		t.Fatal("expected unknown-member rejection")
	}
	tampered := []byte(strings.Replace(string(encoded), plan.ContentHash, typeKeyC, 1))
	if _, err := DecodeGCSafetyPlan(tampered); err == nil {
		t.Fatal("expected content-hash rejection")
	}
}

func validGCSafetyPlan(t *testing.T) GCSafetyPlan {
	t.Helper()
	return finalizedGCSafetyPlan(t, GCSafetyPlan{
		FunctionKey: typeKeyA,
		Slots:       []GCRootSlot{{ID: 1, TraceLayoutHash: typeKeyB}, {ID: 2, TraceLayoutHash: typeKeyB}},
		Blocks: []GCSafetyBlock{{
			ID: 1,
			Instructions: []GCInstruction{
				{ID: 1, Kind: GCOpFrameLink},
				{ID: 2, Kind: GCOpRefDef, Value: 1},
				{ID: 3, Kind: GCOpRootStore, Slot: 1, Value: 1},
				{ID: 4, Kind: GCOpRootClear, Slot: 2},
				{ID: 5, Kind: GCOpRootPublish, ActiveSlots: []GCRootSlotID{1}},
				{ID: 6, Kind: GCOpSafepoint, SafepointKind: "allocation", MayAllocate: true},
				{ID: 7, Kind: GCOpRootReload, Slot: 1, Value: 1},
				{ID: 8, Kind: GCOpRefUse, Uses: []GCValueID{1}},
				{ID: 9, Kind: GCOpFieldStore, ReferenceStore: true, OwnerPublished: true, Barrier: true},
				{ID: 10, Kind: GCOpFrameUnlink},
			},
			Terminator: "return",
		}},
	})
}

func finalizedGCSafetyPlan(t *testing.T, plan GCSafetyPlan) GCSafetyPlan {
	t.Helper()
	result, err := FinalizeGCSafetyPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func cloneGCSafetyPlan(t *testing.T, plan GCSafetyPlan) GCSafetyPlan {
	t.Helper()
	encoded, _, err := CanonicalGCSafetyPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeGCSafetyPlan(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return *decoded
}

func slicesEqualGCValues(left, right []GCValueID) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
