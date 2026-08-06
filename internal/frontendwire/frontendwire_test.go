package frontendwire_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/bundled"
	"github.com/microsoft/typescript-go/internal/frontendwire"
	jsonx "github.com/microsoft/typescript-go/internal/json"
	"github.com/microsoft/typescript-go/internal/tsfrontend"
	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

func TestDecodeFrontendSnapshotRejectsUnknownWrapperField(t *testing.T) {
	frontend := buildFrontendSnapshot(t, `export function add(left: number, right: number): number { return left + right; }`)
	encoded, err := frontend.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	broken := bytes.Replace(encoded, []byte(`"schemaVersion":`), []byte(`"unknown":true,"schemaVersion":`), 1)
	if _, err := frontendwire.DecodeFrontendSnapshot(broken); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown wrapper field error = %v", err)
	}
}

func TestDecodeFrontendSnapshotRejectsRehashedSemanticCorruption(t *testing.T) {
	frontend := buildFrontendSnapshot(t, `export function add(left: number, right: number): number { return left + right; }`)
	for index := range frontend.Program.Nodes {
		if frontend.Program.Nodes[index].Kind == "KindBinaryExpression" {
			frontend.Program.Nodes[index].SyntaxPayload.Operator = "KindPlusLikeToken"
			break
		}
	}
	rehashProgram(t, &frontend.Program)
	frontend.ContentHash = frontend.Program.ContentHash
	encoded, err := json.Marshal(frontend)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := frontendwire.DecodeFrontendSnapshot(encoded); err == nil {
		t.Fatal("rehashed semantic corruption was accepted")
	}
}

func TestEmbeddedKindManifestMatchesCaptureManifest(t *testing.T) {
	wire, err := os.ReadFile("kind_manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	capture, err := os.ReadFile(filepath.Join("..", "tsfrontend", "kind_manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(wire, capture) {
		t.Fatal("frontendwire and tsfrontend Kind manifests differ")
	}
	if _, err := frontendwire.LoadKindManifest(); err != nil {
		t.Fatalf("validate embedded frontendwire Kind manifest: %v", err)
	}
}

func TestCapturedPatternEffectsRoundTripThroughWireValidator(t *testing.T) {
	frontend := buildFrontendSnapshot(t, `
let state = 0;
export function assign(input: { nested: { value: number } }): number {
    ({ nested: { value: state } } = input);
    return state;
}
export function iterate(input: Array<[number, number]>): number {
    for ([state, state] of input) {}
    return state;
}
`)

	nodes := make(map[frontendwire.NodeID]frontendwire.NodeSnapshot, len(frontend.Program.Nodes))
	for _, node := range frontend.Program.Nodes {
		nodes[node.ID] = node
	}
	wantFunctions := map[string]bool{"assign": false, "iterate": false}
	for _, signature := range frontend.Program.Signatures {
		declaration := nodes[signature.Declaration]
		name := ""
		for _, child := range declaration.NamedChildren {
			if child.Role == "name" {
				name = nodes[child.Node].SyntaxPayload.Text
				break
			}
		}
		if _, tracked := wantFunctions[name]; !tracked {
			continue
		}
		wantFunctions[name] = true
		if !slices.Equal(signature.EffectProof.DirectEffects, []string{"read", "write"}) || signature.EffectProof.Complete || !slices.Equal(signature.Effects, []string{"unknown"}) {
			t.Fatalf("%s pattern effect proof = %#v / effects=%v", name, signature.EffectProof, signature.Effects)
		}
		if slices.Contains(signature.EffectProof.DirectEffects, "alloc") {
			t.Fatalf("%s destructuring pattern was classified as allocation", name)
		}
	}
	for name, found := range wantFunctions {
		if !found {
			t.Fatalf("captured signature %q is missing", name)
		}
	}

	encoded, err := frontend.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := frontendwire.DecodeFrontendSnapshot(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ContentHash != frontend.ContentHash {
		t.Fatalf("round-trip hash = %q, want %q", decoded.ContentHash, frontend.ContentHash)
	}
}

func buildFrontendSnapshot(t *testing.T, source string) frontendwire.FrontendSnapshot {
	t.Helper()
	fs := vfstest.FromMap(map[string]string{
		"/project/tsconfig.json": `{"compilerOptions":{"strict":true},"files":["main.ts"]}`,
		"/project/main.ts":       source,
	}, true)
	frontend := tsfrontend.NewFrontend(bundled.WrapFS(fs), bundled.LibPath(), tsfrontend.TypeScriptGoCommit, tsfrontend.StandardLibraryHash)
	snapshot, diagnostics := frontend.Build(context.Background(), tsfrontend.BuildRequest{
		ConfigPath: "/project/tsconfig.json", CurrentDirectory: "/project", FileSystem: fs,
	})
	if snapshot == nil || tsfrontend.DiagnosticsHaveErrors(diagnostics) {
		t.Fatalf("snapshot/diagnostics = %#v / %#v", snapshot, diagnostics)
	}
	wrapper, err := frontendwire.NewFrontendSnapshot(*snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return wrapper
}

func rehashProgram(t *testing.T, snapshot *frontendwire.ProgramSnapshot) {
	t.Helper()
	snapshot.ContentHash = ""
	encoded, err := jsonx.Marshal(snapshot, jsonx.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encoded)
	snapshot.ContentHash = hex.EncodeToString(digest[:])
}
