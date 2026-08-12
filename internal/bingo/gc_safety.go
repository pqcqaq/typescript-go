package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

// GCSafetySchemaVersion identifies the post-effect-freeze root/barrier contract.
const GCSafetySchemaVersion uint32 = 1

type GCValueID uint32
type GCRootSlotID uint32
type GCInstructionID uint32

const (
	GCReasonCFGInvalid             = "gc.cfg_invalid"
	GCReasonLivenessMismatch       = "gc.liveness_mismatch"
	GCReasonFrameLinkMissing       = "gc.frame_link_missing"
	GCReasonFrameUnlinkMissing     = "gc.frame_unlink_missing"
	GCReasonRootSlotInvalid        = "gc.root_slot_invalid"
	GCReasonRootPublicationMissing = "gc.root_publication_missing"
	GCReasonRootPublicationInexact = "gc.root_publication_inexact"
	GCReasonDeadSlotNotCleared     = "gc.dead_slot_not_cleared"
	GCReasonReloadMissing          = "gc.reload_missing"
	GCReasonBarrierMissing         = "gc.barrier_missing"
	GCReasonBarrierSpurious        = "gc.barrier_spurious"
	GCReasonEffectAfterFreeze      = "gc.effect_after_freeze"
)

const (
	GCOpRefDef      = "ref.def"
	GCOpRefUse      = "ref.use"
	GCOpPhi         = "ref.phi"
	GCOpFrameLink   = "frame.link"
	GCOpFrameUnlink = "frame.unlink"
	GCOpRootStore   = "root.store"
	GCOpRootClear   = "root.clear"
	GCOpRootPublish = "root.publish"
	GCOpSafepoint   = "safepoint"
	GCOpRootReload  = "root.reload"
	GCOpFieldStore  = "field.store"
)

// GCRootSlot records one fixed shadow-stack slot and its trace layout identity.
type GCRootSlot struct {
	ID              GCRootSlotID `json:"id"`
	TraceLayoutHash string       `json:"traceLayoutHash"`
}

// GCPhiIncoming records an edge-specific reference use.
type GCPhiIncoming struct {
	Block BlockID   `json:"block"`
	Value GCValueID `json:"value"`
}

// GCInstruction is one semantic reference or GC publication event.
type GCInstruction struct {
	ID             GCInstructionID `json:"id"`
	Kind           string          `json:"kind"`
	Value          GCValueID       `json:"value,omitempty"`
	Uses           []GCValueID     `json:"uses,omitempty"`
	PhiIncoming    []GCPhiIncoming `json:"phiIncoming,omitempty"`
	Slot           GCRootSlotID    `json:"slot,omitempty"`
	ActiveSlots    []GCRootSlotID  `json:"activeSlots,omitempty"`
	SafepointKind  string          `json:"safepointKind,omitempty"`
	MayAllocate    bool            `json:"mayAllocate"`
	MaySuspend     bool            `json:"maySuspend"`
	MayBlock       bool            `json:"mayBlock"`
	ReferenceStore bool            `json:"referenceStore"`
	OwnerPublished bool            `json:"ownerPublished"`
	Barrier        bool            `json:"barrier"`
}

// GCSafetyBlock records CFG edges and serialized liveness evidence.
type GCSafetyBlock struct {
	ID           BlockID         `json:"id"`
	Instructions []GCInstruction `json:"instructions"`
	Successors   []BlockID       `json:"successors,omitempty"`
	Terminator   string          `json:"terminator"`
	LiveIn       []GCValueID     `json:"liveIn,omitempty"`
	LiveOut      []GCValueID     `json:"liveOut,omitempty"`
}

// GCSafetyPlan is the canonical safety proof for one post-freeze function.
type GCSafetyPlan struct {
	SchemaVersion      uint32            `json:"schemaVersion"`
	FunctionKey        string            `json:"functionKey"`
	EffectsFrozen      bool              `json:"effectsFrozen"`
	Slots              []GCRootSlot      `json:"slots"`
	Blocks             []GCSafetyBlock   `json:"blocks"`
	FrozenEffectEvents []GCInstructionID `json:"frozenEffectEvents"`
	ContentHash        string            `json:"contentHash"`
}

// FinalizeGCSafetyPlan recomputes liveness, freezes effect events, and hashes a plan.
func FinalizeGCSafetyPlan(plan GCSafetyPlan) (GCSafetyPlan, error) {
	plan.SchemaVersion = GCSafetySchemaVersion
	plan.EffectsFrozen = true
	liveIn, liveOut, err := computeGCLiveness(plan)
	if err != nil {
		return GCSafetyPlan{}, err
	}
	for index := range plan.Blocks {
		plan.Blocks[index].LiveIn = liveIn[plan.Blocks[index].ID]
		plan.Blocks[index].LiveOut = liveOut[plan.Blocks[index].ID]
	}
	plan.FrozenEffectEvents = gcEffectEvents(plan)
	_, hash, err := CanonicalGCSafetyPlan(plan)
	if err != nil {
		return GCSafetyPlan{}, err
	}
	plan.ContentHash = hash
	return plan, nil
}

// CanonicalGCSafetyPlan verifies, serializes, and hashes a GC safety plan.
func CanonicalGCSafetyPlan(plan GCSafetyPlan) ([]byte, string, error) {
	plan.ContentHash = ""
	if err := VerifyGCSafetyPlanStructure(plan); err != nil {
		return nil, "", err
	}
	encoded, err := jsonx.Marshal(plan)
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	plan.ContentHash = hash
	encoded, err = jsonx.Marshal(plan)
	if err != nil {
		return nil, "", err
	}
	return encoded, hash, nil
}

// DecodeGCSafetyPlan strictly decodes and verifies a canonical GC safety plan.
func DecodeGCSafetyPlan(data []byte) (*GCSafetyPlan, error) {
	var plan GCSafetyPlan
	if err := jsonx.Unmarshal(data, &plan, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode GC safety plan: %w", err)
	}
	claimed := plan.ContentHash
	_, want, err := CanonicalGCSafetyPlan(plan)
	if err != nil {
		return nil, err
	}
	if claimed == "" || claimed != want {
		return nil, fmt.Errorf("GC safety content hash mismatch: got %q, want %q", claimed, want)
	}
	return &plan, nil
}

// VerifyGCSafetyPlanStructure independently verifies liveness and every GC event sequence.
func VerifyGCSafetyPlanStructure(plan GCSafetyPlan) error {
	if plan.SchemaVersion != GCSafetySchemaVersion || !validObjectSemanticTypeKey(plan.FunctionKey) || !plan.EffectsFrozen {
		return fmt.Errorf("invalid GC safety plan envelope")
	}
	if err := verifyGCRootSlots(plan.Slots); err != nil {
		return err
	}
	liveIn, liveOut, err := computeGCLiveness(plan)
	if err != nil {
		return err
	}
	if len(plan.Blocks) == 0 || len(plan.Blocks[0].Instructions) == 0 || plan.Blocks[0].Instructions[0].Kind != GCOpFrameLink {
		return fmt.Errorf("%s", GCReasonFrameLinkMissing)
	}
	if !slices.Equal(plan.FrozenEffectEvents, gcEffectEvents(plan)) {
		return fmt.Errorf("%s", GCReasonEffectAfterFreeze)
	}
	linkCount := 0
	for _, block := range plan.Blocks {
		if !slices.Equal(block.LiveIn, liveIn[block.ID]) || !slices.Equal(block.LiveOut, liveOut[block.ID]) {
			return fmt.Errorf("%s: block %d", GCReasonLivenessMismatch, block.ID)
		}
		if block.Terminator == "return" && (len(block.Instructions) == 0 || block.Instructions[len(block.Instructions)-1].Kind != GCOpFrameUnlink) {
			return fmt.Errorf("%s: block %d", GCReasonFrameUnlinkMissing, block.ID)
		}
		for index, instruction := range block.Instructions {
			if instruction.Kind == GCOpFrameLink {
				linkCount++
				if block.ID != plan.Blocks[0].ID || index != 0 {
					return fmt.Errorf("%s", GCReasonFrameLinkMissing)
				}
			}
			if instruction.Kind == GCOpFrameUnlink && (block.Terminator != "return" || index != len(block.Instructions)-1) {
				return fmt.Errorf("%s: block %d", GCReasonFrameUnlinkMissing, block.ID)
			}
		}
		if err := verifyGCBlockEvents(plan.Slots, block, liveOut[block.ID]); err != nil {
			return fmt.Errorf("block %d: %w", block.ID, err)
		}
	}
	if linkCount != 1 {
		return fmt.Errorf("%s", GCReasonFrameLinkMissing)
	}
	return nil
}

func verifyGCRootSlots(slots []GCRootSlot) error {
	for index, slot := range slots {
		if slot.ID != GCRootSlotID(index+1) || !validObjectSemanticTypeKey(slot.TraceLayoutHash) {
			return fmt.Errorf("%s: slot %d", GCReasonRootSlotInvalid, slot.ID)
		}
	}
	return nil
}

func computeGCLiveness(plan GCSafetyPlan) (map[BlockID][]GCValueID, map[BlockID][]GCValueID, error) {
	blocks := make(map[BlockID]GCSafetyBlock, len(plan.Blocks))
	pred := make(map[BlockID][]BlockID, len(plan.Blocks))
	definitions := make(map[GCValueID]struct{})
	instructionIDs := make(map[GCInstructionID]struct{})
	for index, block := range plan.Blocks {
		if block.ID == 0 || (index > 0 && block.ID <= plan.Blocks[index-1].ID) {
			return nil, nil, fmt.Errorf("%s: block order", GCReasonCFGInvalid)
		}
		if block.Terminator != "branch" && block.Terminator != "condbranch" && block.Terminator != "return" {
			return nil, nil, fmt.Errorf("%s: block %d terminator", GCReasonCFGInvalid, block.ID)
		}
		if block.Terminator == "return" && len(block.Successors) != 0 || block.Terminator == "branch" && len(block.Successors) != 1 || block.Terminator == "condbranch" && len(block.Successors) != 2 {
			return nil, nil, fmt.Errorf("%s: block %d successors", GCReasonCFGInvalid, block.ID)
		}
		if !sortedUniqueBlockIDs(block.Successors) {
			return nil, nil, fmt.Errorf("%s: block %d successor order", GCReasonCFGInvalid, block.ID)
		}
		blocks[block.ID] = block
		for _, instruction := range block.Instructions {
			if !knownGCInstructionKind(instruction.Kind) {
				return nil, nil, fmt.Errorf("%s: unknown instruction %q", GCReasonCFGInvalid, instruction.Kind)
			}
			if _, duplicate := instructionIDs[instruction.ID]; instruction.ID == 0 || duplicate {
				return nil, nil, fmt.Errorf("%s: duplicate instruction %d", GCReasonCFGInvalid, instruction.ID)
			}
			instructionIDs[instruction.ID] = struct{}{}
			if instruction.Kind == GCOpRefDef || instruction.Kind == GCOpPhi {
				if instruction.Value == 0 {
					return nil, nil, fmt.Errorf("%s: zero definition", GCReasonCFGInvalid)
				}
				if _, duplicate := definitions[instruction.Value]; duplicate {
					return nil, nil, fmt.Errorf("%s: duplicate value %d", GCReasonCFGInvalid, instruction.Value)
				}
				definitions[instruction.Value] = struct{}{}
			}
		}
	}
	for _, block := range plan.Blocks {
		for _, successor := range block.Successors {
			if _, ok := blocks[successor]; !ok {
				return nil, nil, fmt.Errorf("%s: missing successor %d", GCReasonCFGInvalid, successor)
			}
			pred[successor] = append(pred[successor], block.ID)
		}
	}
	reachable := map[BlockID]struct{}{plan.Blocks[0].ID: {}}
	worklist := []BlockID{plan.Blocks[0].ID}
	for len(worklist) != 0 {
		blockID := worklist[len(worklist)-1]
		worklist = worklist[:len(worklist)-1]
		for _, successor := range blocks[blockID].Successors {
			if _, seen := reachable[successor]; seen {
				continue
			}
			reachable[successor] = struct{}{}
			worklist = append(worklist, successor)
		}
	}
	if len(reachable) != len(blocks) {
		return nil, nil, fmt.Errorf("%s: unreachable block", GCReasonCFGInvalid)
	}
	blockUse := make(map[BlockID]map[GCValueID]struct{}, len(blocks))
	blockDef := make(map[BlockID]map[GCValueID]struct{}, len(blocks))
	phiDef := make(map[BlockID]map[GCValueID]struct{}, len(blocks))
	phiUse := make(map[BlockID]map[BlockID]map[GCValueID]struct{}, len(blocks))
	for _, block := range plan.Blocks {
		uses, defs, phis := map[GCValueID]struct{}{}, map[GCValueID]struct{}{}, map[GCValueID]struct{}{}
		seenNonPhi := false
		lastInstruction := GCInstructionID(0)
		for _, instruction := range block.Instructions {
			if instruction.ID == 0 || instruction.ID <= lastInstruction {
				return nil, nil, fmt.Errorf("%s: instruction order", GCReasonCFGInvalid)
			}
			lastInstruction = instruction.ID
			if instruction.Kind == GCOpPhi {
				if seenNonPhi || len(instruction.PhiIncoming) != len(pred[block.ID]) {
					return nil, nil, fmt.Errorf("%s: phi in block %d", GCReasonCFGInvalid, block.ID)
				}
				incomingBlocks := make([]BlockID, len(instruction.PhiIncoming))
				for index, incoming := range instruction.PhiIncoming {
					incomingBlocks[index] = incoming.Block
					if _, ok := definitions[incoming.Value]; !ok {
						return nil, nil, fmt.Errorf("%s: phi missing value %d", GCReasonCFGInvalid, incoming.Value)
					}
					if phiUse[block.ID] == nil {
						phiUse[block.ID] = make(map[BlockID]map[GCValueID]struct{})
					}
					if phiUse[block.ID][incoming.Block] == nil {
						phiUse[block.ID][incoming.Block] = make(map[GCValueID]struct{})
					}
					phiUse[block.ID][incoming.Block][incoming.Value] = struct{}{}
				}
				if !slices.Equal(incomingBlocks, pred[block.ID]) {
					return nil, nil, fmt.Errorf("%s: phi predecessor mismatch", GCReasonCFGInvalid)
				}
				defs[instruction.Value], phis[instruction.Value] = struct{}{}, struct{}{}
				continue
			}
			seenNonPhi = true
			if instruction.Kind == GCOpRefUse {
				for _, value := range instruction.Uses {
					if _, ok := definitions[value]; !ok {
						return nil, nil, fmt.Errorf("%s: missing use %d", GCReasonCFGInvalid, value)
					}
					if _, defined := defs[value]; !defined {
						uses[value] = struct{}{}
					}
				}
			}
			if instruction.Kind == GCOpRefDef {
				defs[instruction.Value] = struct{}{}
			}
		}
		blockUse[block.ID], blockDef[block.ID], phiDef[block.ID] = uses, defs, phis
	}
	liveInSet, liveOutSet := make(map[BlockID]map[GCValueID]struct{}), make(map[BlockID]map[GCValueID]struct{})
	for _, block := range plan.Blocks {
		liveInSet[block.ID], liveOutSet[block.ID] = map[GCValueID]struct{}{}, map[GCValueID]struct{}{}
	}
	for changed := true; changed; {
		changed = false
		for index := len(plan.Blocks) - 1; index >= 0; index-- {
			block := plan.Blocks[index]
			out := map[GCValueID]struct{}{}
			for _, successor := range block.Successors {
				for value := range liveInSet[successor] {
					if _, isPhi := phiDef[successor][value]; !isPhi {
						out[value] = struct{}{}
					}
				}
				for value := range phiUse[successor][block.ID] {
					out[value] = struct{}{}
				}
			}
			in := cloneGCValueSet(blockUse[block.ID])
			for value := range out {
				if _, defined := blockDef[block.ID][value]; !defined {
					in[value] = struct{}{}
				}
			}
			if !equalGCValueSet(out, liveOutSet[block.ID]) || !equalGCValueSet(in, liveInSet[block.ID]) {
				liveOutSet[block.ID], liveInSet[block.ID], changed = out, in, true
			}
		}
	}
	liveIn, liveOut := make(map[BlockID][]GCValueID), make(map[BlockID][]GCValueID)
	for _, block := range plan.Blocks {
		liveIn[block.ID], liveOut[block.ID] = sortedGCValues(liveInSet[block.ID]), sortedGCValues(liveOutSet[block.ID])
	}
	return liveIn, liveOut, nil
}

func knownGCInstructionKind(kind string) bool {
	switch kind {
	case GCOpRefDef, GCOpRefUse, GCOpPhi, GCOpFrameLink, GCOpFrameUnlink,
		GCOpRootStore, GCOpRootClear, GCOpRootPublish, GCOpSafepoint,
		GCOpRootReload, GCOpFieldStore:
		return true
	default:
		return false
	}
}

func verifyGCBlockEvents(slots []GCRootSlot, block GCSafetyBlock, liveOut []GCValueID) error {
	live := gcValueSliceSet(liveOut)
	liveAfter := make([]map[GCValueID]struct{}, len(block.Instructions))
	for index := len(block.Instructions) - 1; index >= 0; index-- {
		liveAfter[index] = cloneGCValueSet(live)
		instruction := block.Instructions[index]
		if instruction.Kind == GCOpRefUse {
			for _, value := range instruction.Uses {
				live[value] = struct{}{}
			}
		}
		if instruction.Kind == GCOpRefDef || instruction.Kind == GCOpPhi {
			delete(live, instruction.Value)
		}
	}
	for index, instruction := range block.Instructions {
		switch instruction.Kind {
		case GCOpSafepoint:
			if strings.TrimSpace(instruction.SafepointKind) == "" || !(instruction.MayAllocate || instruction.MaySuspend || instruction.MayBlock || instruction.SafepointKind == "poll") {
				return fmt.Errorf("%s: invalid safepoint %d", GCReasonCFGInvalid, instruction.ID)
			}
			if index == 0 || block.Instructions[index-1].Kind != GCOpRootPublish {
				return fmt.Errorf("%s: safepoint %d", GCReasonRootPublicationMissing, instruction.ID)
			}
			publish := block.Instructions[index-1]
			active := make(map[GCRootSlotID]GCValueID)
			start := index - 2
			for start >= 0 && (block.Instructions[start].Kind == GCOpRootStore || block.Instructions[start].Kind == GCOpRootClear) {
				start--
			}
			preparation := block.Instructions[start+1 : index-1]
			if len(preparation) != len(slots) {
				return fmt.Errorf("%s: safepoint %d", GCReasonDeadSlotNotCleared, instruction.ID)
			}
			for slotIndex, event := range preparation {
				wantSlot := GCRootSlotID(slotIndex + 1)
				if event.Slot != wantSlot {
					return fmt.Errorf("%s: safepoint %d slot order", GCReasonRootSlotInvalid, instruction.ID)
				}
				if event.Kind == GCOpRootStore {
					active[event.Slot] = event.Value
				} else if event.Kind != GCOpRootClear {
					return fmt.Errorf("%s", GCReasonRootPublicationInexact)
				}
			}
			wantLive := liveAfter[index]
			if len(active) != len(wantLive) {
				return fmt.Errorf("%s: safepoint %d", GCReasonRootPublicationInexact, instruction.ID)
			}
			activeValues := make(map[GCValueID]struct{}, len(active))
			for _, value := range active {
				if _, ok := wantLive[value]; !ok {
					return fmt.Errorf("%s: safepoint %d", GCReasonRootPublicationInexact, instruction.ID)
				}
				activeValues[value] = struct{}{}
			}
			if !equalGCValueSet(activeValues, wantLive) {
				return fmt.Errorf("%s: safepoint %d", GCReasonRootPublicationInexact, instruction.ID)
			}
			wantActiveSlots := make([]GCRootSlotID, 0, len(active))
			for slot := range active {
				wantActiveSlots = append(wantActiveSlots, slot)
			}
			slices.Sort(wantActiveSlots)
			if !slices.Equal(publish.ActiveSlots, wantActiveSlots) {
				return fmt.Errorf("%s: safepoint %d active set", GCReasonRootPublicationInexact, instruction.ID)
			}
			if index+len(active) >= len(block.Instructions) {
				return fmt.Errorf("%s: safepoint %d", GCReasonReloadMissing, instruction.ID)
			}
			for reloadIndex, slot := range wantActiveSlots {
				reload := block.Instructions[index+1+reloadIndex]
				if reload.Kind != GCOpRootReload || reload.Slot != slot || reload.Value != active[slot] {
					return fmt.Errorf("%s: safepoint %d", GCReasonReloadMissing, instruction.ID)
				}
			}
		case GCOpFieldStore:
			if instruction.ReferenceStore && instruction.OwnerPublished && !instruction.Barrier {
				return fmt.Errorf("%s: store %d", GCReasonBarrierMissing, instruction.ID)
			}
			if (!instruction.ReferenceStore || !instruction.OwnerPublished) && instruction.Barrier {
				return fmt.Errorf("%s: store %d", GCReasonBarrierSpurious, instruction.ID)
			}
		case GCOpRootPublish:
			if index+1 >= len(block.Instructions) || block.Instructions[index+1].Kind != GCOpSafepoint {
				return fmt.Errorf("%s: publication %d", GCReasonRootPublicationMissing, instruction.ID)
			}
		case GCOpRootStore, GCOpRootClear:
			if index+1 >= len(block.Instructions) || (block.Instructions[index+1].Kind != GCOpRootStore && block.Instructions[index+1].Kind != GCOpRootClear && block.Instructions[index+1].Kind != GCOpRootPublish) {
				return fmt.Errorf("%s: root event %d", GCReasonRootPublicationMissing, instruction.ID)
			}
		case GCOpRootReload:
			previous := index - 1
			for previous >= 0 && block.Instructions[previous].Kind == GCOpRootReload {
				previous--
			}
			if previous < 0 || block.Instructions[previous].Kind != GCOpSafepoint {
				return fmt.Errorf("%s: reload %d", GCReasonReloadMissing, instruction.ID)
			}
		}
	}
	return nil
}

func gcEffectEvents(plan GCSafetyPlan) []GCInstructionID {
	result := make([]GCInstructionID, 0)
	for _, block := range plan.Blocks {
		for _, instruction := range block.Instructions {
			switch instruction.Kind {
			case GCOpFrameLink, GCOpFrameUnlink, GCOpRootStore, GCOpRootClear, GCOpRootPublish, GCOpSafepoint, GCOpRootReload:
				result = append(result, instruction.ID)
			case GCOpFieldStore:
				if instruction.ReferenceStore {
					result = append(result, instruction.ID)
				}
			}
		}
	}
	return result
}
func sortedUniqueBlockIDs(values []BlockID) bool {
	for i, value := range values {
		if value == 0 || i > 0 && value <= values[i-1] {
			return false
		}
	}
	return true
}
func cloneGCValueSet(source map[GCValueID]struct{}) map[GCValueID]struct{} {
	result := make(map[GCValueID]struct{}, len(source))
	for value := range source {
		result[value] = struct{}{}
	}
	return result
}
func equalGCValueSet(left, right map[GCValueID]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for value := range left {
		if _, ok := right[value]; !ok {
			return false
		}
	}
	return true
}
func sortedGCValues(values map[GCValueID]struct{}) []GCValueID {
	result := make([]GCValueID, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}
func gcValueSliceSet(values []GCValueID) map[GCValueID]struct{} {
	result := make(map[GCValueID]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
