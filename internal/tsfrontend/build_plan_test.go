package tsfrontend

import (
	"context"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/internal/vfs/vfstest"
)

func TestFrontendSnapshotKeyIgnoresBackendChoices(t *testing.T) {
	base := DefaultBingoOptions()
	base.TargetTriple = "x86_64-unknown-linux-gnu"
	base.CPU = "generic"

	variants := []BingoOptions{
		base,
		{
			Profile:      base.Profile,
			TargetTriple: "x86_64-unknown-linux-gnu",
			CPU:          "znver4",
			Features:     []string{"+avx2"},
			GC:           GCArc,
			Exceptions:   ExceptionsNone,
			Overflow:     OverflowJSNumber,
			BoundsCheck:  BoundsCheckOff,
			Emit:         []EmitArtifact{EmitLLVM},
			Runtime:      "core-esnext",
			LLVMMajor:    20,
		},
		{
			Profile:      base.Profile,
			TargetTriple: "x86_64-pc-windows-msvc",
			CPU:          "generic",
			GC:           GCArena,
			Exceptions:   ExceptionsNone,
			Overflow:     OverflowJSNumber,
			BoundsCheck:  BoundsCheckOn,
			Emit:         []EmitArtifact{EmitObject},
			Runtime:      "core-es2020",
			LLVMMajor:    20,
		},
	}

	var frontendKey string
	var rawBytes []byte
	var firstPlan BuildPlan
	for index, options := range variants {
		snapshot := buildPlanTestSnapshot(t, options)
		bytes, err := snapshot.CanonicalBytes()
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			rawBytes = bytes
		} else if string(bytes) != string(rawBytes) {
			t.Fatalf("backend variant changed raw frontend snapshot")
		}
		if snapshot.Config.Bingo.Runtime != "" || snapshot.Config.Bingo.TargetTriple != "" || len(snapshot.Config.Bingo.Emit) != 0 {
			t.Fatalf("raw snapshot retained backend Bingo options: %#v", snapshot.Config.Bingo)
		}
		frontend, err := NewFrontendSnapshot(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		if frontend.ContentHash != frontend.Program.ContentHash {
			t.Fatalf("frontend wrapper hash = %q, program hash = %q", frontend.ContentHash, frontend.Program.ContentHash)
		}
		if index == 0 {
			frontendKey = frontend.ContentHash
		} else if frontend.ContentHash != frontendKey {
			t.Fatalf("backend variant changed frontend key: got %q, want %q", frontend.ContentHash, frontendKey)
		}

		plan, err := ResolveBuildPlan(frontend, options)
		if err != nil {
			t.Fatal(err)
		}
		if plan.SchemaVersion != BuildPlanSchemaVersion || plan.FrontendHash != frontendKey {
			t.Fatalf("plan provenance = %#v", plan)
		}
		if index == 0 {
			firstPlan = plan
		} else if plan.ContentHash == firstPlan.ContentHash {
			t.Fatalf("backend variant did not invalidate build plan: %#v", plan)
		}
	}
}

func TestValidateProgramSnapshotRejectsBackendContamination(t *testing.T) {
	snapshot := buildPlanTestSnapshot(t, DefaultBingoOptions())
	snapshot.Config.Bingo.TargetTriple = "x86_64-unknown-linux-gnu"
	digest, err := hashCanonical(struct {
		Schema int          `json:"schema"`
		Bingo  BingoOptions `json:"bingo"`
	}{OptionsSchemaVersion, snapshot.Config.Bingo})
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Config.BingoDigest = digest
	if err := finalizeSnapshot(&snapshot); err != nil {
		t.Fatal(err)
	}
	if err := ValidateProgramSnapshot(snapshot); err == nil || !strings.Contains(err.Error(), "backend-only") {
		t.Fatalf("backend-contaminated snapshot error = %v", err)
	}
}

func TestFrontendSnapshotStrictDiskRoundTrip(t *testing.T) {
	frontend := buildPlanTestFrontend(t, DefaultBingoOptions())
	data, err := frontend.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeFrontendSnapshot(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ContentHash != frontend.ContentHash || decoded.Program.ContentHash != frontend.Program.ContentHash {
		t.Fatalf("frontend disk round trip changed identity: %#v", decoded)
	}
	unknown := strings.Replace(string(data), `"contentHash":`, `"unexpected":true,"contentHash":`, 1)
	if _, err := DecodeFrontendSnapshot([]byte(unknown)); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown frontend field error = %v", err)
	}
	backend := strings.Replace(string(data), `"profile": "static"`, `"profile": "static", "targetTriple": "x86_64-unknown-linux-gnu"`, 1)
	if _, err := DecodeFrontendSnapshot([]byte(backend)); err == nil || !strings.Contains(err.Error(), "backend-only") {
		t.Fatalf("backend frontend field error = %v", err)
	}
}

func TestFrontendSnapshotCanonicalBytesRejectsMutatedWrapper(t *testing.T) {
	frontend := buildPlanTestFrontend(t, DefaultBingoOptions())
	frontend.ContentHash = strings.Repeat("0", 64)

	if _, err := frontend.CanonicalBytes(); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("CanonicalBytes mutated-wrapper error = %v", err)
	}
}

func TestFrontendProfileChangesFrontendAndPlanProvenance(t *testing.T) {
	staticOptions := DefaultBingoOptions()
	staticSnapshot := buildPlanTestSnapshot(t, staticOptions)
	staticFrontend, err := NewFrontendSnapshot(staticSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	staticPlan, err := ResolveBuildPlan(staticFrontend, staticOptions)
	if err != nil {
		t.Fatal(err)
	}

	interopOptions := staticOptions
	interopOptions.Profile = ProfileInterop
	interopSnapshot := buildPlanTestSnapshot(t, interopOptions)
	interopFrontend, err := NewFrontendSnapshot(interopSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	interopPlan, err := ResolveBuildPlan(interopFrontend, interopOptions)
	if err != nil {
		t.Fatal(err)
	}
	if interopFrontend.ContentHash == staticFrontend.ContentHash {
		t.Fatal("profile override did not change frontend key")
	}
	if interopPlan.ContentHash == staticPlan.ContentHash || interopPlan.Profile != ProfileInterop {
		t.Fatalf("profile override did not change plan provenance: static=%#v interop=%#v", staticPlan, interopPlan)
	}
}

func TestResolveBuildPlanRejectsUnboundDigestAndMismatchedWrapper(t *testing.T) {
	options := DefaultBingoOptions()
	if _, err := ResolveBuildPlan(FrontendSnapshot{SchemaVersion: SnapshotSchemaVersion, ContentHash: strings.Repeat("0", 64)}, options); err == nil {
		t.Fatal("unbound digest was accepted without a verified program")
	}
	frontend := buildPlanTestFrontend(t, options)
	frontend.ContentHash = strings.Repeat("0", 64)
	if _, err := ResolveBuildPlan(frontend, options); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatched wrapper error = %v", err)
	}
}

func TestResolveBuildPlanRejectsProfileRelabeling(t *testing.T) {
	staticOptions := DefaultBingoOptions()
	frontend := buildPlanTestFrontend(t, staticOptions)
	interopOptions := staticOptions
	interopOptions.Profile = ProfileInterop
	if _, err := ResolveBuildPlan(frontend, interopOptions); err == nil || !strings.Contains(err.Error(), "does not match frontend snapshot profile") {
		t.Fatalf("profile relabeling error = %v", err)
	}
}

func TestResolveBuildPlanRejectsInvalidBackendOptions(t *testing.T) {
	base := DefaultBingoOptions()
	frontend := buildPlanTestFrontend(t, base)
	tests := []struct {
		name string
		edit func(*BingoOptions)
		want string
	}{
		{name: "dynamic profile", edit: func(options *BingoOptions) { options.Profile = ProfileDynamic }, want: "unavailable"},
		{name: "llvm major", edit: func(options *BingoOptions) { options.LLVMMajor = 19 }, want: "LLVM major"},
		{name: "gc", edit: func(options *BingoOptions) { options.GC = GCMode("invalid") }, want: "GC mode"},
		{name: "unavailable LLVM EH", edit: func(options *BingoOptions) { options.Exceptions = ExceptionsLLVMEH }, want: "unavailable"},
		{name: "exceptions", edit: func(options *BingoOptions) { options.Exceptions = ExceptionMode("invalid") }, want: "exception mode"},
		{name: "overflow", edit: func(options *BingoOptions) { options.Overflow = OverflowMode("invalid") }, want: "overflow mode"},
		{name: "bounds", edit: func(options *BingoOptions) { options.BoundsCheck = BoundsCheckMode("invalid") }, want: "bounds-check mode"},
		{name: "emit", edit: func(options *BingoOptions) { options.Emit = []EmitArtifact{"invalid"} }, want: "emit artifact"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := base
			test.edit(&options)
			if _, err := ResolveBuildPlan(frontend, options); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("invalid build plan error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestBuildPlanStrictDiskRoundTrip(t *testing.T) {
	options := DefaultBingoOptions()
	options.TargetTriple = "x86_64-unknown-linux-gnu"
	options.Features = []string{"+avx2", "+sse2"}
	frontend := buildPlanTestFrontend(t, options)
	plan, err := ResolveBuildPlan(frontend, options)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Backend.Exceptions != ExceptionsNone {
		t.Fatalf("build plan exceptions = %q, want %q", plan.Backend.Exceptions, ExceptionsNone)
	}
	if err := ValidateBuildPlan(plan); err != nil {
		t.Fatal(err)
	}

	data, err := plan.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeBuildPlan(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.ContentHash != plan.ContentHash || decoded.FrontendHash != frontend.ContentHash || !equalBackendRequest(decoded.Backend, plan.Backend) {
		t.Fatalf("build plan disk round trip changed identity: %#v", decoded)
	}

	unknownPlan := strings.Replace(string(data), `"contentHash":`, `"unexpected":true,"contentHash":`, 1)
	if _, err := DecodeBuildPlan([]byte(unknownPlan)); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown build plan field error = %v", err)
	}
	unknownBackend := strings.Replace(string(data), `"target":`, `"unexpected":true,"target":`, 1)
	if _, err := DecodeBuildPlan([]byte(unknownBackend)); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown backend field error = %v", err)
	}
	tampered := strings.Replace(string(data), `"cpu": "generic"`, `"cpu": "znver4"`, 1)
	if _, err := DecodeBuildPlan([]byte(tampered)); err == nil || !strings.Contains(err.Error(), "content hash mismatch") {
		t.Fatalf("tampered build plan error = %v", err)
	}
}

func TestValidateBuildPlanRejectsMalformedOrNonCanonicalPlan(t *testing.T) {
	options := DefaultBingoOptions()
	frontend := buildPlanTestFrontend(t, options)
	base, err := ResolveBuildPlan(frontend, options)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		mutate func(*BuildPlan)
		rehash bool
		want   string
	}{
		{name: "schema", mutate: func(plan *BuildPlan) { plan.SchemaVersion++ }, rehash: true, want: "unsupported build plan schema"},
		{name: "frontend hash", mutate: func(plan *BuildPlan) { plan.FrontendHash = "invalid" }, rehash: true, want: "invalid build plan frontend hash"},
		{name: "content hash", mutate: func(plan *BuildPlan) { plan.ContentHash = strings.Repeat("0", 64) }, want: "content hash mismatch"},
		{name: "backend tampering", mutate: func(plan *BuildPlan) { plan.Backend.Target = "x86_64-pc-windows-msvc" }, want: "content hash mismatch"},
		{name: "noncanonical CPU", mutate: func(plan *BuildPlan) { plan.Backend.CPU = " GENERIC " }, rehash: true, want: "not canonical"},
		{name: "noncanonical features", mutate: func(plan *BuildPlan) { plan.Backend.Features = []string{"+sse2", "+avx2", "+sse2"} }, rehash: true, want: "not canonical"},
		{name: "invalid GC", mutate: func(plan *BuildPlan) { plan.Backend.GC = GCMode("invalid") }, rehash: true, want: "GC mode"},
		{name: "unavailable LLVM EH", mutate: func(plan *BuildPlan) { plan.Backend.Exceptions = ExceptionsLLVMEH }, rehash: true, want: "unavailable"},
		{name: "missing emit", mutate: func(plan *BuildPlan) { plan.Backend.Emit = nil }, rehash: true, want: "not canonical"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := base
			plan.Backend.Features = append([]string(nil), base.Backend.Features...)
			plan.Backend.Emit = append([]EmitArtifact(nil), base.Backend.Emit...)
			test.mutate(&plan)
			if test.rehash {
				hash, err := buildPlanContentHash(plan)
				if err != nil {
					t.Fatal(err)
				}
				plan.ContentHash = hash
			}
			if err := ValidateBuildPlan(plan); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("malformed build plan error = %v, want %q", err, test.want)
			}
			if _, err := plan.CanonicalBytes(); err == nil {
				t.Fatal("CanonicalBytes accepted malformed build plan")
			}
		})
	}
}

func TestBuildPlanEmptyEmitRoundTrip(t *testing.T) {
	options := DefaultBingoOptions()
	options.Emit = []EmitArtifact{}
	frontend := buildPlanTestFrontend(t, options)
	plan, err := ResolveBuildPlan(frontend, options)
	if err != nil {
		t.Fatal(err)
	}
	data, err := plan.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"emit": []`) {
		t.Fatalf("empty emit request was omitted from build plan wire:\n%s", data)
	}
	decoded, err := DecodeBuildPlan(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Backend.Emit) != 0 {
		t.Fatalf("decoded emit request = %#v, want empty", decoded.Backend.Emit)
	}
}

func TestFrontendCaptureDefersBackendValidationToBuildPlan(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*BingoOptions)
		want   string
	}{
		{name: "llvm major", mutate: func(options *BingoOptions) { options.LLVMMajor = 19 }, want: "LLVM major"},
		{name: "gc", mutate: func(options *BingoOptions) { options.GC = GCMode("invalid") }, want: "GC mode"},
		{name: "unavailable LLVM EH", mutate: func(options *BingoOptions) { options.Exceptions = ExceptionsLLVMEH }, want: "unavailable"},
		{name: "exceptions", mutate: func(options *BingoOptions) { options.Exceptions = ExceptionMode("invalid") }, want: "exception mode"},
		{name: "overflow", mutate: func(options *BingoOptions) { options.Overflow = OverflowMode("invalid") }, want: "overflow mode"},
		{name: "bounds", mutate: func(options *BingoOptions) { options.BoundsCheck = BoundsCheckMode("invalid") }, want: "bounds-check mode"},
		{name: "emit", mutate: func(options *BingoOptions) { options.Emit = []EmitArtifact{"invalid"} }, want: "emit artifact"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := DefaultBingoOptions()
			test.mutate(&options)
			snapshot := buildPlanTestSnapshot(t, options)
			if DiagnosticsHaveErrors(snapshot.Diagnostics) {
				t.Fatalf("backend-only option blocked frontend capture: %#v", snapshot.Diagnostics)
			}
			frontend, err := NewFrontendSnapshot(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ResolveBuildPlan(frontend, options); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("deferred build-plan error = %v, want %q", err, test.want)
			}
		})
	}
}

func buildPlanTestFrontend(t *testing.T, options BingoOptions) FrontendSnapshot {
	t.Helper()
	snapshot := buildPlanTestSnapshot(t, options)
	frontend, err := NewFrontendSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return frontend
}

func buildPlanTestSnapshot(t *testing.T, options BingoOptions) ProgramSnapshot {
	t.Helper()
	request := BuildRequest{
		ConfigPath:           "/project/tsconfig.json",
		CurrentDirectory:     "/project",
		ProjectRoot:          "/project",
		BingoOptions:         options,
		BingoOptionsOverride: true,
		FileSystem: vfstest.FromMap(map[string]string{
			"/project/tsconfig.json": `{"compilerOptions":{"strict":true,"noEmit":true},"files":["main.ts"]}`,
			"/project/main.ts":       `export function add(a: number, b: number): number { return a + b; }`,
		}, true),
	}
	snapshot, diagnostics := NewFrontend(nil, "", TypeScriptGoCommit, StandardLibraryHash).Build(context.Background(), request)
	if snapshot == nil {
		t.Fatalf("Build returned nil snapshot: %#v", diagnostics)
	}
	return *snapshot
}
