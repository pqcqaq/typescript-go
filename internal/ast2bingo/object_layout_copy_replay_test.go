package ast2bingo

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/bingo"
)

func TestObjectLayoutCopyReplayFixture(t *testing.T) {
	data, err := os.ReadFile("../../testdata/ts2bin/objectlayoutcopy/frontend-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	identity := bingo.CompilerBuildIdentity{UpstreamCommit: "86cc4767d4ebadb9b7845d0ab8eb2b05785c3fee", ForkCommit: "1111111111111111111111111111111111111111", LoweringSchema: PrimitiveLoweringSchema, LoweringHash: PrimitiveLoweringHash()}
	result, err := ReplayObjectLayoutCopyFrontendSnapshot(data, identity)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := result.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeObjectLayoutCopyReplay(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ContentHash != result.ContentHash || !decoded.Copy.AllocatesTarget || decoded.Copy.PreservesIdentity {
		t.Fatal("invalid layout copy replay")
	}
}

func TestObjectLayoutCopyReplayRejectsTamper(t *testing.T) {
	data, err := os.ReadFile("../../testdata/ts2bin/objectlayoutcopy/frontend-snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	identity := bingo.CompilerBuildIdentity{UpstreamCommit: "86cc4767d4ebadb9b7845d0ab8eb2b05785c3fee", ForkCommit: "1111111111111111111111111111111111111111", LoweringSchema: PrimitiveLoweringSchema, LoweringHash: PrimitiveLoweringHash()}
	result, err := ReplayObjectLayoutCopyFrontendSnapshot(data, identity)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := result.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	encoded[len(encoded)-2] ^= 1
	if _, err := DecodeObjectLayoutCopyReplay(encoded); err == nil {
		t.Fatal("tampered replay accepted")
	}
	unknown := bytes.Replace(encoded, []byte(`"schemaVersion":1`), []byte(`"schemaVersion":1,"unknown":true`), 1)
	if _, err := DecodeObjectLayoutCopyReplay(unknown); err == nil {
		t.Fatal("unknown replay field accepted")
	}
	if _, err := DecodeObjectLayoutCopyReplay(make([]byte, maxObjectLayoutCopyReplayBytes+1)); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversize error=%v", err)
	}
	hashSubstitution := bytes.Replace(encoded, []byte(result.FrontendSnapshotHash), []byte(strings.Repeat("0", 64)), 1)
	if _, err := DecodeObjectLayoutCopyReplay(hashSubstitution); err == nil {
		t.Fatal("frontend hash substitution accepted")
	}
}

func FuzzDecodeObjectLayoutCopyReplay(f *testing.F) {
	data, err := os.ReadFile("../../testdata/ts2bin/objectlayoutcopy/frontend-snapshot.json")
	if err == nil {
		identity := bingo.CompilerBuildIdentity{UpstreamCommit: "86cc4767d4ebadb9b7845d0ab8eb2b05785c3fee", ForkCommit: "1111111111111111111111111111111111111111", LoweringSchema: PrimitiveLoweringSchema, LoweringHash: PrimitiveLoweringHash()}
		if replay, replayErr := ReplayObjectLayoutCopyFrontendSnapshot(data, identity); replayErr == nil {
			if encoded, encodeErr := replay.CanonicalBytes(); encodeErr == nil {
				f.Add(encoded)
			}
		}
	}
	f.Fuzz(func(t *testing.T, data []byte) { _, _ = DecodeObjectLayoutCopyReplay(data) })
}
