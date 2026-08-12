package bingo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	jsonx "github.com/microsoft/typescript-go/internal/json"
)

// ObjectLayoutSchemaVersion identifies the target-dependent object ABI.
const ObjectLayoutSchemaVersion uint32 = 1

const (
	ObjectLayoutX8664Triple   = "x86_64-unknown-linux-gnu"
	ObjectLayoutAArch64Triple = "aarch64-unknown-linux-gnu"
	ObjectLayoutX8664Data     = "e-m:e-p270:32:32-p271:32:32-p272:64:64-i64:64-i128:128-f80:128-n8:16:32:64-S128"
	ObjectLayoutAArch64Data   = "e-m:e-p270:32:32-p271:32:32-p272:64:64-i8:8:32-i16:16:32-i64:64-i128:128-n32:64-S128-Fn32"
)

// ObjectLayoutTarget records the complete observed target facts used by layout.
type ObjectLayoutTarget struct {
	Triple          string `json:"triple"`
	DataLayout      string `json:"dataLayout"`
	DataLayoutHash  string `json:"dataLayoutHash"`
	PointerBits     uint32 `json:"pointerBits"`
	PointerABIAlign uint32 `json:"pointerABIAlign"`
	LittleEndian    bool   `json:"littleEndian"`
}

// ObjectLayoutField records one ABI field offset and representation extent.
type ObjectLayoutField struct {
	Name   string `json:"name"`
	Offset uint32 `json:"offset"`
	Size   uint32 `json:"size"`
	Align  uint32 `json:"align"`
}

// ObjectLayoutBlock is a named C/LLVM/Rust-compatible block layout.
type ObjectLayoutBlock struct {
	Size   uint32              `json:"size"`
	Align  uint32              `json:"align"`
	Fields []ObjectLayoutField `json:"fields"`
}

// ObjectLayoutPropertyInput is the closed-shape input to PlanObjectLayout.
type ObjectLayoutPropertyInput struct {
	Key            string             `json:"key"`
	Kind           ObjectPropertyKind `json:"kind"`
	Representation string             `json:"representation,omitempty"`
	Optional       bool               `json:"optional"`
	Reference      bool               `json:"reference"`
}

// ObjectLayoutProperty is the frozen physical mapping of one data/accessor property.
type ObjectLayoutProperty struct {
	Key              string             `json:"key"`
	Kind             ObjectPropertyKind `json:"kind"`
	FieldOffset      uint32             `json:"fieldOffset,omitempty"`
	PresenceBit      int32              `json:"presenceBit"`
	EnumerationOrder uint32             `json:"enumerationOrder"`
	Representation   string             `json:"representation,omitempty"`
}

// ObjectLayoutContract binds a closed shape to one observed target layout.
type ObjectLayoutContract struct {
	SchemaVersion uint32                 `json:"schemaVersion"`
	SchemaHash    string                 `json:"schemaHash"`
	TypeKey       string                 `json:"typeKey"`
	Target        ObjectLayoutTarget     `json:"target"`
	Header        ObjectLayoutBlock      `json:"header"`
	Shape         ObjectLayoutBlock      `json:"shape"`
	Property      ObjectLayoutBlock      `json:"property"`
	Trace         ObjectLayoutBlock      `json:"trace"`
	PresenceWords uint32                 `json:"presenceWords"`
	Properties    []ObjectLayoutProperty `json:"properties"`
	TraceOffsets  []uint32               `json:"traceOffsets,omitempty"`
	ObjectSize    uint32                 `json:"objectSize"`
	ObjectAlign   uint32                 `json:"objectAlign"`
	LayoutHash    string                 `json:"layoutHash"`
	ContentHash   string                 `json:"contentHash"`
}

// CanonicalObjectLayoutSchemaHash returns the versioned ABI schema identity.
func CanonicalObjectLayoutSchemaHash() string {
	return ObjectLayoutABISchemaHash
}

// CanonicalObjectLayoutTarget returns the observed v1 target record.
func CanonicalObjectLayoutTarget(triple string) (ObjectLayoutTarget, error) {
	var data string
	switch triple {
	case ObjectLayoutX8664Triple:
		data = ObjectLayoutX8664Data
	case ObjectLayoutAArch64Triple:
		data = ObjectLayoutAArch64Data
	default:
		return ObjectLayoutTarget{}, fmt.Errorf("unsupported object layout target %q", triple)
	}
	return ObjectLayoutTarget{Triple: triple, DataLayout: data, DataLayoutHash: digestString(data), PointerBits: 64, PointerABIAlign: 8, LittleEndian: true}, nil
}

// PlanObjectLayout computes a deterministic closed-shape layout without allocation.
func PlanObjectLayout(typeKey string, target ObjectLayoutTarget, properties []ObjectLayoutPropertyInput) (ObjectLayoutContract, error) {
	if !validObjectSemanticTypeKey(typeKey) {
		return ObjectLayoutContract{}, fmt.Errorf("invalid object layout type key %q", typeKey)
	}
	if err := verifyObjectLayoutTarget(target); err != nil {
		return ObjectLayoutContract{}, err
	}
	seenKeys := make(map[string]struct{}, len(properties))
	for _, property := range properties {
		if strings.TrimSpace(property.Key) == "" {
			return ObjectLayoutContract{}, fmt.Errorf("object layout property has an empty key")
		}
		if _, duplicate := seenKeys[property.Key]; duplicate {
			return ObjectLayoutContract{}, fmt.Errorf("object layout property key %q is duplicated", property.Key)
		}
		seenKeys[property.Key] = struct{}{}
		if property.Kind != ObjectPropertyData && property.Kind != ObjectPropertyAccessor {
			return ObjectLayoutContract{}, fmt.Errorf("object layout property %q has invalid kind %q", property.Key, property.Kind)
		}
		if property.Kind == ObjectPropertyAccessor && (property.Representation != "" || property.Reference) {
			return ObjectLayoutContract{}, fmt.Errorf("object accessor %q cannot carry field representation", property.Key)
		}
		if property.Kind == ObjectPropertyAccessor && property.Optional {
			return ObjectLayoutContract{}, fmt.Errorf("object accessor %q cannot carry data-field presence", property.Key)
		}
		if property.Kind == ObjectPropertyData && property.Representation == "" {
			return ObjectLayoutContract{}, fmt.Errorf("object data property %q has no representation", property.Key)
		}
		if property.Kind == ObjectPropertyData && property.Reference != (property.Representation == "gc-ref") {
			return ObjectLayoutContract{}, fmt.Errorf("object data property %q has inconsistent reference representation", property.Key)
		}
	}

	header, err := fixedObjectHeader(target)
	if err != nil {
		return ObjectLayoutContract{}, err
	}
	shape, err := fixedObjectShape(target)
	if err != nil {
		return ObjectLayoutContract{}, err
	}
	propertyBlock, err := fixedObjectProperty(target)
	if err != nil {
		return ObjectLayoutContract{}, err
	}
	trace, err := fixedObjectTrace(target)
	if err != nil {
		return ObjectLayoutContract{}, err
	}
	pointerBits := target.PointerBits
	presenceCount := 0
	for _, property := range properties {
		if property.Optional && property.Kind == ObjectPropertyData {
			presenceCount++
		}
	}
	presenceWords := uint32((presenceCount + int(pointerBits) - 1) / int(pointerBits))
	cursor := uint32(header.Size) + presenceWords*uint32(pointerBits/8)
	maxAlign := header.Align
	result := make([]ObjectLayoutProperty, 0, len(properties))
	traceOffsets := make([]uint32, 0)
	nextPresenceBit := int32(0)
	for order, input := range properties {
		mapping := ObjectLayoutProperty{Key: input.Key, Kind: input.Kind, PresenceBit: -1, EnumerationOrder: uint32(order), Representation: input.Representation}
		if input.Kind == ObjectPropertyData {
			size, align, ok := objectRepresentationFacts(input.Representation, target)
			if !ok {
				return ObjectLayoutContract{}, fmt.Errorf("unsupported object representation %q", input.Representation)
			}
			cursor = alignObjectOffset(cursor, align)
			mapping.FieldOffset = cursor
			cursor += size
			if align > maxAlign {
				maxAlign = align
			}
			if input.Reference {
				traceOffsets = append(traceOffsets, mapping.FieldOffset)
			}
			if input.Optional {
				mapping.PresenceBit = nextPresenceBit
				nextPresenceBit++
			}
		}
		result = append(result, mapping)
	}
	objectSize := alignObjectOffset(cursor, maxAlign)
	contract := ObjectLayoutContract{SchemaVersion: ObjectLayoutSchemaVersion, SchemaHash: CanonicalObjectLayoutSchemaHash(), TypeKey: typeKey, Target: target, Header: header, Shape: shape, Property: propertyBlock, Trace: trace, PresenceWords: presenceWords, Properties: result, TraceOffsets: traceOffsets, ObjectSize: objectSize, ObjectAlign: maxAlign}
	contract.LayoutHash = objectPhysicalLayoutHash(contract)
	_, hash, err := CanonicalObjectLayoutContract(contract)
	if err != nil {
		return ObjectLayoutContract{}, err
	}
	contract.ContentHash = hash
	return contract, nil
}

// CanonicalObjectLayoutContract serializes and hashes a verified layout contract.
func CanonicalObjectLayoutContract(contract ObjectLayoutContract) ([]byte, string, error) {
	contract.ContentHash = ""
	if err := VerifyObjectLayoutContractStructure(contract); err != nil {
		return nil, "", err
	}
	encoded, err := jsonx.Marshal(contract)
	if err != nil {
		return nil, "", err
	}
	hash := digestBytes(encoded)
	contract.ContentHash = hash
	encoded, err = jsonx.Marshal(contract)
	if err != nil {
		return nil, "", err
	}
	return encoded, hash, nil
}

// DecodeObjectLayoutContract strictly decodes and verifies a layout contract.
func DecodeObjectLayoutContract(data []byte) (*ObjectLayoutContract, error) {
	var contract ObjectLayoutContract
	if err := jsonx.Unmarshal(data, &contract, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode object layout contract: %w", err)
	}
	claimed := contract.ContentHash
	_, want, err := CanonicalObjectLayoutContract(contract)
	if err != nil {
		return nil, err
	}
	if claimed == "" || claimed != want {
		return nil, fmt.Errorf("object layout content hash mismatch: got %q, want %q", claimed, want)
	}
	return &contract, nil
}

// VerifyObjectLayoutContractStructure checks schema, target, offsets, and hashes without mutating input.
func VerifyObjectLayoutContractStructure(contract ObjectLayoutContract) error {
	if contract.SchemaVersion != ObjectLayoutSchemaVersion || contract.SchemaHash != CanonicalObjectLayoutSchemaHash() {
		return fmt.Errorf("object layout schema mismatch")
	}
	if !validObjectSemanticTypeKey(contract.TypeKey) {
		return fmt.Errorf("invalid object layout type key %q", contract.TypeKey)
	}
	if err := verifyObjectLayoutTarget(contract.Target); err != nil {
		return err
	}
	header, err := fixedObjectHeader(contract.Target)
	if err != nil {
		return err
	}
	shape, err := fixedObjectShape(contract.Target)
	if err != nil {
		return err
	}
	propertyBlock, err := fixedObjectProperty(contract.Target)
	if err != nil {
		return err
	}
	trace, err := fixedObjectTrace(contract.Target)
	if err != nil {
		return err
	}
	if !equalObjectLayoutBlock(contract.Header, header) || !equalObjectLayoutBlock(contract.Shape, shape) || !equalObjectLayoutBlock(contract.Property, propertyBlock) || !equalObjectLayoutBlock(contract.Trace, trace) {
		return fmt.Errorf("object layout fixed ABI block mismatch")
	}
	seenKeys := make(map[string]struct{}, len(contract.Properties))
	optionalCount := 0
	for i, property := range contract.Properties {
		if strings.TrimSpace(property.Key) == "" || property.EnumerationOrder != uint32(i) {
			return fmt.Errorf("object layout properties are not in canonical declaration order")
		}
		if _, duplicate := seenKeys[property.Key]; duplicate {
			return fmt.Errorf("object layout property key %q is duplicated", property.Key)
		}
		seenKeys[property.Key] = struct{}{}
		if property.Kind != ObjectPropertyData && property.Kind != ObjectPropertyAccessor {
			return fmt.Errorf("object layout property %q has invalid kind", property.Key)
		}
		if property.Kind == ObjectPropertyAccessor {
			if property.FieldOffset != 0 || property.Representation != "" || property.PresenceBit != -1 {
				return fmt.Errorf("accessor %q carries data-field layout", property.Key)
			}
			continue
		}
		if _, _, ok := objectRepresentationFacts(property.Representation, contract.Target); !ok {
			return fmt.Errorf("property %q has unsupported representation", property.Key)
		}
		if property.PresenceBit < -1 {
			return fmt.Errorf("property %q has invalid presence bit", property.Key)
		}
		if property.PresenceBit >= 0 {
			optionalCount++
		}
	}
	wantPresenceWords := uint32((optionalCount + int(contract.Target.PointerBits) - 1) / int(contract.Target.PointerBits))
	if contract.PresenceWords != wantPresenceWords {
		return fmt.Errorf("object layout presence word count mismatch")
	}
	cursor := contract.Header.Size + contract.PresenceWords*(contract.Target.PointerBits/8)
	maxAlign := contract.Header.Align
	nextPresenceBit := int32(0)
	wantTraceOffsets := make([]uint32, 0)
	for _, property := range contract.Properties {
		if property.Kind != ObjectPropertyData {
			continue
		}
		size, align, _ := objectRepresentationFacts(property.Representation, contract.Target)
		cursor = alignObjectOffset(cursor, align)
		if property.FieldOffset != cursor {
			return fmt.Errorf("property %q field offset mismatch: got %d, want %d", property.Key, property.FieldOffset, cursor)
		}
		cursor += size
		if align > maxAlign {
			maxAlign = align
		}
		if property.PresenceBit >= 0 {
			if property.PresenceBit != nextPresenceBit {
				return fmt.Errorf("property %q presence bit mismatch", property.Key)
			}
			nextPresenceBit++
		}
		if property.Representation == "gc-ref" {
			wantTraceOffsets = append(wantTraceOffsets, property.FieldOffset)
		}
	}
	if !slices.Equal(contract.TraceOffsets, wantTraceOffsets) {
		return fmt.Errorf("object trace offsets mismatch")
	}
	wantSize := alignObjectOffset(cursor, maxAlign)
	if contract.ObjectAlign != maxAlign || contract.ObjectSize != wantSize {
		return fmt.Errorf("object layout size/alignment mismatch: got %d/%d, want %d/%d", contract.ObjectSize, contract.ObjectAlign, wantSize, maxAlign)
	}
	if contract.ContentHash != "" && !validObjectSemanticTypeKey(contract.ContentHash) {
		return fmt.Errorf("invalid object layout content hash")
	}
	if contract.LayoutHash != objectPhysicalLayoutHash(contract) {
		return fmt.Errorf("object layout hash mismatch")
	}
	return nil
}

// VerifyObjectLayoutMutableAlias checks that two semantic types have the same physical layout.
func VerifyObjectLayoutMutableAlias(source, target ObjectLayoutContract) error {
	if err := verifyObjectLayoutContractHash(source); err != nil {
		return fmt.Errorf("source object layout: %w", err)
	}
	if err := verifyObjectLayoutContractHash(target); err != nil {
		return fmt.Errorf("target object layout: %w", err)
	}
	if source.LayoutHash != target.LayoutHash {
		return fmt.Errorf("object.mutable_alias_layout_mismatch")
	}
	return nil
}

func verifyObjectLayoutContractHash(contract ObjectLayoutContract) error {
	claimed := contract.ContentHash
	_, want, err := CanonicalObjectLayoutContract(contract)
	if err != nil {
		return err
	}
	if claimed == "" || claimed != want {
		return fmt.Errorf("object layout content hash mismatch")
	}
	return nil
}

func objectPhysicalLayoutHash(contract ObjectLayoutContract) string {
	value := struct {
		SchemaHash    string                 `json:"schemaHash"`
		Target        ObjectLayoutTarget     `json:"target"`
		Header        ObjectLayoutBlock      `json:"header"`
		Shape         ObjectLayoutBlock      `json:"shape"`
		Property      ObjectLayoutBlock      `json:"property"`
		Trace         ObjectLayoutBlock      `json:"trace"`
		PresenceWords uint32                 `json:"presenceWords"`
		Properties    []ObjectLayoutProperty `json:"properties"`
		TraceOffsets  []uint32               `json:"traceOffsets,omitempty"`
		ObjectSize    uint32                 `json:"objectSize"`
		ObjectAlign   uint32                 `json:"objectAlign"`
	}{contract.SchemaHash, contract.Target, contract.Header, contract.Shape, contract.Property, contract.Trace, contract.PresenceWords, contract.Properties, contract.TraceOffsets, contract.ObjectSize, contract.ObjectAlign}
	bytes, _ := jsonx.Marshal(value)
	return digestBytes(bytes)
}

func equalObjectLayoutBlock(left, right ObjectLayoutBlock) bool {
	return left.Size == right.Size && left.Align == right.Align && slices.Equal(left.Fields, right.Fields)
}

func fixedObjectHeader(target ObjectLayoutTarget) (ObjectLayoutBlock, error) {
	return blockFor(target, []representationField{{"descriptor", "ptr"}, {"sizeBytes", "usize"}, {"gcWord", "usize"}})
}
func fixedObjectShape(target ObjectLayoutTarget) (ObjectLayoutBlock, error) {
	return blockFor(target, []representationField{{"schemaVersion", "u32"}, {"flags", "u32"}, {"objectSize", "usize"}, {"objectAlign", "usize"}, {"propertyCount", "u32"}, {"presenceWordCount", "u32"}, {"properties", "ptr"}, {"trace", "ptr"}})
}
func fixedObjectProperty(target ObjectLayoutTarget) (ObjectLayoutBlock, error) {
	return blockFor(target, []representationField{{"key", "ptr"}, {"kind", "u8"}, {"flags", "u8"}, {"reserved", "u16"}, {"fieldOffset", "u32"}, {"presenceBit", "u32"}, {"slot", "u32"}, {"enumerationOrder", "u32"}, {"valueDescriptor", "ptr"}})
}
func fixedObjectTrace(target ObjectLayoutTarget) (ObjectLayoutBlock, error) {
	return blockFor(target, []representationField{{"schemaVersion", "u32"}, {"flags", "u32"}, {"objectSize", "usize"}, {"pointerCount", "u32"}, {"pointerMapWords", "u32"}, {"pointerOffsets", "ptr"}, {"traceCallback", "ptr"}})
}

type representationField struct{ name, representation string }

func blockFor(target ObjectLayoutTarget, fields []representationField) (ObjectLayoutBlock, error) {
	var cursor, max uint32
	result := make([]ObjectLayoutField, 0, len(fields))
	for _, field := range fields {
		size, align, ok := objectRepresentationFacts(field.representation, target)
		if !ok {
			return ObjectLayoutBlock{}, fmt.Errorf("unsupported ABI representation %q", field.representation)
		}
		cursor = alignObjectOffset(cursor, align)
		result = append(result, ObjectLayoutField{Name: field.name, Offset: cursor, Size: size, Align: align})
		cursor += size
		if align > max {
			max = align
		}
	}
	return ObjectLayoutBlock{Size: alignObjectOffset(cursor, max), Align: max, Fields: result}, nil
}
func objectRepresentationFacts(representation string, target ObjectLayoutTarget) (uint32, uint32, bool) {
	pointerBytes := target.PointerBits / 8
	switch representation {
	case "u8":
		return 1, 1, true
	case "u16":
		return 2, 2, true
	case "u32":
		return 4, 4, true
	case "f64":
		return 8, 8, true
	case "nullable-f64":
		// The payload occupies the first eight bytes and an explicit nullish tag
		// occupies the second eight-byte slot. It is non-reference storage.
		return 16, 8, true
	case "usize", "ptr", "gc-ref":
		return pointerBytes, target.PointerABIAlign, true
	default:
		return 0, 0, false
	}
}
func alignObjectOffset(offset, align uint32) uint32 {
	if align <= 1 {
		return offset
	}
	remainder := offset % align
	if remainder == 0 {
		return offset
	}
	return offset + align - remainder
}
func verifyObjectLayoutTarget(target ObjectLayoutTarget) error {
	canonical, err := CanonicalObjectLayoutTarget(target.Triple)
	if err != nil {
		return err
	}
	if target.DataLayout != canonical.DataLayout || target.DataLayoutHash != canonical.DataLayoutHash || target.PointerBits != canonical.PointerBits || target.PointerABIAlign != canonical.PointerABIAlign || target.LittleEndian != canonical.LittleEndian {
		return fmt.Errorf("object layout target facts do not match authoritative target %q", target.Triple)
	}
	return nil
}
func digestBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
func digestString(value string) string { return digestBytes([]byte(value)) }
