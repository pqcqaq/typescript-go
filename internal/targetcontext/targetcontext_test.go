package targetcontext

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

	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/buildplan"
	"github.com/microsoft/typescript-go/internal/frontendwire"
	"github.com/microsoft/typescript-go/internal/llvmbackend"
)

func TestRuntimeManifestStrictIdentityAndHashes(t *testing.T) {
	data := runtimeManifestFixture(t)
	manifest, err := DecodeRuntimeManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := manifest.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(canonical, bytes.TrimSpace(data)) {
		t.Fatalf("runtime manifest canonical bytes changed: %s", canonical)
	}
	for name, mutate := range map[string]func(*RuntimeManifest){
		"source hash":          func(value *RuntimeManifest) { value.SourceHash = strings.Repeat("0", 64) },
		"ABI schema hash":      func(value *RuntimeManifest) { value.ABISchemaHash = strings.Repeat("1", 64) },
		"implementation hash":  func(value *RuntimeManifest) { value.Capabilities[0].ImplementationHash = strings.Repeat("2", 64) },
		"signature hash":       func(value *RuntimeManifest) { value.Capabilities[0].SignatureHash = strings.Repeat("3", 64) },
		"harness hash":         func(value *RuntimeManifest) { value.Artifacts.HarnessObject.SHA256 = strings.Repeat("4", 64) },
		"compute harness hash": func(value *RuntimeManifest) { value.Artifacts.ComputeHarnessObject.SHA256 = strings.Repeat("4", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := *manifest
			candidate.Artifacts = manifest.Artifacts
			if manifest.Artifacts.ComputeHarnessObject != nil {
				computeHarness := *manifest.Artifacts.ComputeHarnessObject
				candidate.Artifacts.ComputeHarnessObject = &computeHarness
			}
			candidate.Capabilities = append([]RuntimeCapability(nil), manifest.Capabilities...)
			candidate.Capabilities[0] = manifest.Capabilities[0]
			mutate(&candidate)
			if _, err := DecodeRuntimeManifest(mustJSON(t, candidate)); err == nil {
				t.Fatal("tampered runtime manifest was accepted")
			}
		})
	}
	rehashed := *manifest
	rehashed.Capabilities = append([]RuntimeCapability(nil), manifest.Capabilities...)
	rehashed.Capabilities[0] = manifest.Capabilities[0]
	rehashed.Artifacts.UmbrellaArchive.SHA256 = strings.Repeat("4", 64)
	rehashed.Capabilities[0].ImplementationHash = rehashed.Artifacts.UmbrellaArchive.SHA256
	rehashed.ContentHash, err = runtimeManifestContentHash(rehashed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeRuntimeManifest(mustJSON(t, rehashed)); err == nil || !strings.Contains(err.Error(), "not locked") {
		t.Fatalf("rehashed substituted runtime error = %v", err)
	}
	unknown := strings.Replace(string(data), `"contentHash":`, `"unknown":true,"contentHash":`, 1)
	if _, err := DecodeRuntimeManifest([]byte(unknown)); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown runtime field error = %v", err)
	}
}

func TestResolveTargetContextBindsRealManifestFacts(t *testing.T) {
	plan := validBuildPlan()
	toolchain := validToolchainManifest()
	runtime := runtimeManifestFixture(t)
	resolution, err := resolveTargetContext(plan, toolchain, runtime)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Context.FrontendHash != plan.FrontendHash || resolution.Context.RequestHash != plan.ContentHash {
		t.Fatalf("request provenance = %#v", resolution.Context)
	}
	if resolution.Context.LLVMDataLayout != llvmbackend.FirstSliceDataLayout || resolution.Context.DataLayoutHash != toolchain.DataLayout.ContentHash {
		t.Fatalf("LLVM data layout provenance = %#v", resolution.Context)
	}
	if len(resolution.Catalog.Capabilities) != 1 || resolution.Catalog.Capabilities[0].LogicalName != "rt.abi.version" {
		t.Fatalf("available catalog = %#v", resolution.Catalog)
	}
	if resolution.Context.AvailableCapabilityCatalogHash != resolution.Catalog.ContentHash {
		t.Fatalf("catalog hash was not bound into context")
	}
}

func TestResolveTargetContextRejectsUnsupportedRequests(t *testing.T) {
	base := validBuildPlan()
	toolchain := validToolchainManifest()
	runtime := runtimeManifestFixture(t)
	tests := []struct {
		name   string
		mutate func(*buildplan.Plan)
	}{
		{name: "target", mutate: func(plan *buildplan.Plan) { plan.Backend.Target = "x86_64-pc-windows-msvc" }},
		{name: "cpu", mutate: func(plan *buildplan.Plan) { plan.Backend.CPU = "znver4" }},
		{name: "feature", mutate: func(plan *buildplan.Plan) { plan.Backend.Features = []string{"+avx2"} }},
		{name: "runtime", mutate: func(plan *buildplan.Plan) { plan.Backend.Runtime = "core-esnext" }},
		{name: "gc", mutate: func(plan *buildplan.Plan) { plan.Backend.GC = frontendwire.GCArc }},
		{name: "bounds", mutate: func(plan *buildplan.Plan) { plan.Backend.BoundsCheck = frontendwire.BoundsCheckOff }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := base
			test.mutate(&plan)
			rehashBuildPlan(&plan)
			if _, err := resolveTargetContext(plan, toolchain, runtime); err == nil {
				t.Fatal("unsupported request was accepted")
			}
		})
	}
}

func TestResolveTargetPassPreservesHIRAndRejectsManifestSubstitution(t *testing.T) {
	plan := validBuildPlan()
	toolchain := validToolchainManifest()
	runtime := runtimeManifestFixture(t)
	inputs, err := newResolverInputArtifacts(plan, toolchain, runtime)
	if err != nil {
		t.Fatal(err)
	}
	hir, err := bingo.NewPassArtifact(bingo.PassArtifactTypedHIR, "hir-v5", []byte(`{"logicalCapabilityRequirements":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := bingo.NewPassArtifactEnvelope(append(inputs, hir)...)
	if err != nil {
		t.Fatal(err)
	}
	spec := resolveTargetPassSpec(t)
	initial := bingo.PassState{Schema: spec.InputSchema, Facts: append([]string(nil), spec.ReadsFacts...), Artifact: hir.Payload, Artifacts: &envelope}
	handler, err := newResolveTargetPassHandler(toolchain)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.PreVerify(context.Background(), spec, 1, initial); err != nil {
		t.Fatal(err)
	}
	result, err := handler.Run(context.Background(), spec, 1, initial)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handler.PostVerify(context.Background(), spec, 1, initial, result.State); err != nil {
		t.Fatal(err)
	}
	if got, ok := result.State.NamedArtifact(bingo.PassArtifactTypedHIR); !ok || !bytes.Equal(got.Payload, hir.Payload) {
		t.Fatal("resolver did not preserve typed HIR sidecar")
	}
	if _, ok := result.State.NamedArtifact(bingo.PassArtifactAvailableCapabilityCatalog); !ok {
		t.Fatal("resolver did not emit available capability catalog")
	}
	if _, ok := result.State.NamedArtifact(bingo.PassArtifactRepresentationPlan); ok {
		t.Fatal("resolver emitted a representation plan before the join pass")
	}
	if slices.Contains(result.State.Facts, "bound-capability-closure") {
		t.Fatal("resolver claimed a MIR-derived bound capability closure")
	}

	otherHIR, err := bingo.NewPassArtifact(bingo.PassArtifactTypedHIR, "hir-v5", []byte(`{"tamperedButOpaque":true}`))
	if err != nil {
		t.Fatal(err)
	}
	otherArtifacts := make([]bingo.PassArtifact, 0, len(envelope.Artifacts))
	for _, artifact := range envelope.Artifacts {
		if artifact.Name == bingo.PassArtifactTypedHIR {
			otherArtifacts = append(otherArtifacts, otherHIR)
		} else {
			otherArtifacts = append(otherArtifacts, artifact)
		}
	}
	otherEnvelope, err := bingo.NewPassArtifactEnvelope(otherArtifacts...)
	if err != nil {
		t.Fatal(err)
	}
	otherInput := initial
	otherInput.Artifact = otherHIR.Payload
	otherInput.Artifacts = &otherEnvelope
	otherResult, err := handler.Run(context.Background(), spec, 1, otherInput)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(otherResult.State.Artifact, result.State.Artifact) {
		t.Fatal("resolver target context changed after opaque HIR payload changed")
	}
	firstCatalog, _ := result.State.NamedArtifact(bingo.PassArtifactAvailableCapabilityCatalog)
	otherCatalog, _ := otherResult.State.NamedArtifact(bingo.PassArtifactAvailableCapabilityCatalog)
	if !bytes.Equal(firstCatalog.Payload, otherCatalog.Payload) {
		t.Fatal("resolver available catalog changed after opaque HIR payload changed")
	}

	tamperedToolchain, err := bingo.NewPassArtifact(bingo.PassArtifactToolchainManifest, "toolchain-manifest-v1", []byte(`{"schemaVersion":1}`))
	if err != nil {
		t.Fatal(err)
	}
	tamperedArtifacts := make([]bingo.PassArtifact, 0, len(envelope.Artifacts))
	for _, artifact := range envelope.Artifacts {
		if artifact.Name == bingo.PassArtifactToolchainManifest {
			tamperedArtifacts = append(tamperedArtifacts, tamperedToolchain)
		} else {
			tamperedArtifacts = append(tamperedArtifacts, artifact)
		}
	}
	tamperedEnvelope, err := bingo.NewPassArtifactEnvelope(tamperedArtifacts...)
	if err != nil {
		t.Fatal(err)
	}
	tamperedInput := initial
	tamperedInput.Artifacts = &tamperedEnvelope
	if err := handler.PreVerify(context.Background(), spec, 1, tamperedInput); err == nil {
		t.Fatal("substituted toolchain manifest was accepted")
	}

	tamperedOutput := result.State
	tamperedContext := append([]byte(nil), result.State.Artifact...)
	tamperedContext = bytes.Replace(tamperedContext, []byte(`"pointerWidth":64`), []byte(`"pointerWidth":32`), 1)
	tamperedTarget, err := bingo.NewPassArtifact(bingo.PassArtifactTargetContext, "target-context-v1", tamperedContext)
	if err != nil {
		t.Fatal(err)
	}
	tamperedOutputArtifacts := make([]bingo.PassArtifact, 0, len(result.State.Artifacts.Artifacts))
	for _, artifact := range result.State.Artifacts.Artifacts {
		if artifact.Name == bingo.PassArtifactTargetContext {
			tamperedOutputArtifacts = append(tamperedOutputArtifacts, tamperedTarget)
		} else {
			tamperedOutputArtifacts = append(tamperedOutputArtifacts, artifact)
		}
	}
	tamperedOutputEnvelope, err := bingo.NewPassArtifactEnvelope(tamperedOutputArtifacts...)
	if err != nil {
		t.Fatal(err)
	}
	tamperedOutput.Artifact = tamperedContext
	tamperedOutput.Artifacts = &tamperedOutputEnvelope
	if _, err := handler.PostVerify(context.Background(), spec, 1, initial, tamperedOutput); err == nil {
		t.Fatal("tampered target context output was accepted")
	}
}

func resolveTargetPassSpec(t *testing.T) bingo.PassSpec {
	t.Helper()
	for _, spec := range bingo.PassSpecs() {
		if spec.ID == bingo.PassResolveTarget {
			return spec
		}
	}
	t.Fatal("missing resolve target pass spec")
	return bingo.PassSpec{}
}

func runtimeManifestFixture(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join("testdata", "runtime-manifest.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func validBuildPlan() buildplan.Plan {
	plan := buildplan.Plan{
		SchemaVersion: buildplan.SchemaVersion,
		FrontendHash:  strings.Repeat("a", 64),
		Profile:       frontendwire.ProfileStatic,
		Backend: buildplan.BackendRequest{
			Target:      llvmbackend.FirstSliceTriple,
			CPU:         llvmbackend.FirstSliceCPU,
			Features:    []string{},
			Runtime:     LockedRuntimeName,
			GC:          frontendwire.GCTracing,
			Exceptions:  frontendwire.ExceptionsNone,
			Overflow:    frontendwire.OverflowJSNumber,
			BoundsCheck: frontendwire.BoundsCheckOn,
			Emit:        []frontendwire.EmitArtifact{frontendwire.EmitHIR},
			LLVMMajor:   llvmbackend.LockedLLVMMajor,
		},
	}
	rehashBuildPlan(&plan)
	return plan
}

func rehashBuildPlan(plan *buildplan.Plan) {
	input := struct {
		SchemaVersion uint32                   `json:"schemaVersion"`
		FrontendHash  string                   `json:"frontendHash"`
		Profile       frontendwire.Profile     `json:"profile"`
		Backend       buildplan.BackendRequest `json:"backend"`
	}{plan.SchemaVersion, plan.FrontendHash, plan.Profile, plan.Backend}
	plan.ContentHash = sha256JSON(input)
}

func validToolchainManifest() llvmbackend.ToolchainManifest {
	layout := llvmbackend.DataLayout{
		SchemaVersion: llvmbackend.DataLayoutSchemaVersion,
		Triple:        llvmbackend.FirstSliceTriple,
		LayoutString:  llvmbackend.FirstSliceDataLayout,
		PointerBits:   64,
		LittleEndian:  true,
		F64Bits:       64,
		F64ABIAlign:   8,
	}
	layout.ContentHash = sha256JSON(struct {
		SchemaVersion uint32 `json:"schemaVersion"`
		Triple        string `json:"triple"`
		LayoutString  string `json:"layoutString"`
		PointerBits   uint32 `json:"pointerBits"`
		LittleEndian  bool   `json:"littleEndian"`
		F64Bits       uint32 `json:"f64Bits"`
		F64ABIAlign   uint32 `json:"f64AbiAlign"`
	}{layout.SchemaVersion, layout.Triple, layout.LayoutString, layout.PointerBits, layout.LittleEndian, layout.F64Bits, layout.F64ABIAlign})
	manifest := llvmbackend.ToolchainManifest{
		SchemaVersion:   llvmbackend.ToolchainManifestSchemaVersion,
		LLVMVersion:     llvmbackend.LockedLLVMVersion,
		LLVMMajor:       llvmbackend.LockedLLVMMajor,
		TargetTriple:    llvmbackend.FirstSliceTriple,
		CPU:             llvmbackend.FirstSliceCPU,
		Features:        []string{},
		ObjectFormat:    "elf",
		RelocationModel: "pic",
		CodeModel:       "small",
		OptLevel:        "none",
		DataLayout:      layout,
	}
	manifest.ContentHash = sha256JSON(struct {
		SchemaVersion   uint32                 `json:"schemaVersion"`
		LLVMVersion     string                 `json:"llvmVersion"`
		LLVMMajor       int                    `json:"llvmMajor"`
		TargetTriple    string                 `json:"targetTriple"`
		CPU             string                 `json:"cpu"`
		Features        []string               `json:"features,omitempty"`
		ObjectFormat    string                 `json:"objectFormat"`
		RelocationModel string                 `json:"relocationModel"`
		CodeModel       string                 `json:"codeModel"`
		OptLevel        string                 `json:"optLevel"`
		DataLayout      llvmbackend.DataLayout `json:"dataLayout"`
	}{manifest.SchemaVersion, manifest.LLVMVersion, manifest.LLVMMajor, manifest.TargetTriple, manifest.CPU, manifest.Features, manifest.ObjectFormat, manifest.RelocationModel, manifest.CodeModel, manifest.OptLevel, manifest.DataLayout})
	return manifest
}

func sha256JSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
