// Package firstslicelink owns the narrow BE-004a link/run boundary.
//
// It consumes only verified LLVM emission bytes and a validated first-slice
// runtime manifest. The response file deliberately contains stable basenames;
// temporary workspace paths stay outside the artifact identity.
package firstslicelink

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/internal/llvmbackend"
	"github.com/microsoft/typescript-go/internal/targetcontext"
)

const LinkArtifactSchemaVersion uint32 = 1

var ErrLinkUnavailable = errors.New("first-slice linker requires Linux x86-64 LLVM 20 tools")

type LinkRequest struct {
	Emission llvmbackend.FirstSliceEmission
	Runtime  targetcontext.RuntimeManifest
	// EntryPoint selects the ABI harness or application startup and must match the sole function in
	// the verified LLVM emission. The empty value is retained for the legacy
	// add path and is normalized to "add".
	EntryPoint       string
	RuntimeDirectory string
	// RuntimeArchivePath is explicit because Cargo keeps the archive under its
	// target/profile directory while startup/harness objects stay at the root.
	// The manifest still authenticates the basename, size, and bytes.
	RuntimeArchivePath string
	OutputPath         string
	Clang              string
	LLD                string
}

type LinkArtifact struct {
	SchemaVersion       uint32 `json:"schemaVersion"`
	EmissionContentHash string `json:"emissionContentHash"`
	RuntimeManifestHash string `json:"runtimeManifestHash"`
	TargetTriple        string `json:"targetTriple"`
	ClangVersion        string `json:"clangVersion"`
	LLDVersion          string `json:"lldVersion"`
	ResponseFileHash    string `json:"responseFileHash"`
	LinkMapHash         string `json:"linkMapHash"`
	ExecutableHash      string `json:"executableHash"`
	ContentHash         string `json:"contentHash"`
	ResponseFile        []byte `json:"-"`
	LinkMap             []byte `json:"-"`
	Executable          []byte `json:"-"`
}

type RunResult struct {
	Arguments  []string `json:"arguments"`
	Output     []byte   `json:"-"`
	OutputHash string   `json:"outputHash"`
}

// LinkFirstSlice validates all inputs, invokes the locked Clang/LLD pair, and
// publishes a new executable only after the link and artifact checks pass.
func LinkFirstSlice(ctx context.Context, request LinkRequest) (LinkArtifact, error) {
	if ctx == nil {
		return LinkArtifact{}, fmt.Errorf("link context is nil")
	}
	if err := validateRequest(request); err != nil {
		return LinkArtifact{}, err
	}
	entryPoint := normalizedEntryPoint(request.EntryPoint)
	if err := llvmbackend.VerifyFirstSliceEmission(request.Emission); err != nil {
		return LinkArtifact{}, fmt.Errorf("verify LLVM emission: %w", err)
	}
	if request.Emission.TargetTriple != request.Runtime.Target.Triple {
		return LinkArtifact{}, fmt.Errorf("emission target %q does not match runtime target %q", request.Emission.TargetTriple, request.Runtime.Target.Triple)
	}
	if err := targetcontext.ValidateRuntimeManifest(request.Runtime); err != nil {
		return LinkArtifact{}, fmt.Errorf("verify runtime manifest: %w", err)
	}
	if err := requireNewOutput(request.OutputPath); err != nil {
		return LinkArtifact{}, err
	}

	workspace, err := os.MkdirTemp("", "ts2bin-first-slice-link-")
	if err != nil {
		return LinkArtifact{}, fmt.Errorf("create link workspace: %w", err)
	}
	defer os.RemoveAll(workspace)
	if err := materializeLinkInputs(workspace, request, entryPoint); err != nil {
		return LinkArtifact{}, err
	}
	responseFile := responseFileBytes(entryPoint)
	responsePath := filepath.Join(workspace, "ts2bin-first-slice.rsp")
	if err := os.WriteFile(responsePath, responseFile, 0o600); err != nil {
		return LinkArtifact{}, fmt.Errorf("write linker response file: %w", err)
	}
	clangVersion, err := toolVersion(ctx, request.Clang)
	if err != nil {
		return LinkArtifact{}, fmt.Errorf("query clang: %w", err)
	}
	if !strings.Contains(clangVersion, llvmbackend.LockedLLVMVersion) {
		return LinkArtifact{}, fmt.Errorf("unsupported clang version %q", clangVersion)
	}
	lldVersion, err := toolVersion(ctx, request.LLD)
	if err != nil {
		return LinkArtifact{}, fmt.Errorf("query lld: %w", err)
	}
	if !strings.Contains(lldVersion, llvmbackend.LockedLLVMVersion) {
		return LinkArtifact{}, fmt.Errorf("unsupported lld version %q", lldVersion)
	}

	outputName := "ts2bin-first-slice"
	command := exec.CommandContext(ctx, request.Clang, "@ts2bin-first-slice.rsp")
	command.Dir = workspace
	command.Env = linkerEnvironment(request.LLD)
	output, err := command.CombinedOutput()
	if err != nil {
		return LinkArtifact{}, fmt.Errorf("link first-slice executable: %w: %s", err, strings.TrimSpace(string(output)))
	}
	linkedPath := filepath.Join(workspace, outputName)
	executable, err := os.ReadFile(linkedPath)
	if err != nil {
		return LinkArtifact{}, fmt.Errorf("read linked executable: %w", err)
	}
	linkMap, err := os.ReadFile(filepath.Join(workspace, "ts2bin-first-slice.map"))
	if err != nil {
		return LinkArtifact{}, fmt.Errorf("read linker map: %w", err)
	}
	if err := verifyLinkMap(responseFile, linkMap); err != nil {
		return LinkArtifact{}, err
	}
	artifact, err := newLinkArtifact(request, clangVersion, lldVersion, responseFile, linkMap, executable)
	if err != nil {
		return LinkArtifact{}, err
	}
	if err := publishNewFile(request.OutputPath, executable, 0o755); err != nil {
		return LinkArtifact{}, err
	}
	return artifact, nil
}

// RunFirstSlice executes the fixed C ABI harness and accepts only one
// canonical IEEE-754 binary64 output line.
func RunFirstSlice(ctx context.Context, executable string, left, right string) (RunResult, error) {
	return runHarness(ctx, executable, "add", "", left, right)
}

// RunClassify executes the one-argument binary64 classify C ABI harness.
func RunClassify(ctx context.Context, executable, value string) (RunResult, error) {
	if ctx == nil {
		return RunResult{}, fmt.Errorf("run context is nil")
	}
	if err := validateBits(value); err != nil {
		return RunResult{}, fmt.Errorf("value argument: %w", err)
	}
	if strings.TrimSpace(executable) == "" {
		return RunResult{}, fmt.Errorf("executable path is empty")
	}
	command := exec.CommandContext(ctx, executable, value)
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	output, err := command.CombinedOutput()
	if err != nil {
		return RunResult{}, fmt.Errorf("run classify executable: %w: %s", err, strings.TrimSpace(string(output)))
	}
	trimmed := strings.TrimSuffix(string(output), "\n")
	if strings.Contains(trimmed, "\n") || len(trimmed) != 16 || validateBits(trimmed) != nil {
		return RunResult{}, fmt.Errorf("classify output is not one binary64 hex line: %q", string(output))
	}
	return RunResult{Arguments: []string{value}, Output: slices.Clone(output), OutputHash: hashBytes(output)}, nil
}

// RunCompute executes the local-binding/direct-call C ABI harness.
func RunCompute(ctx context.Context, executable string, left, right string) (RunResult, error) {
	return runHarness(ctx, executable, "compute", "", left, right)
}

// RunChoose executes the choose C ABI harness with a canonical boolean byte.
func RunChoose(ctx context.Context, executable string, flag bool, left, right string) (RunResult, error) {
	flagByte := "00"
	if flag {
		flagByte = "01"
	}
	return runHarness(ctx, executable, "choose", flagByte, left, right)
}

// RunCoalesce executes the nullable-number ABI harness. The semantic tag is
// encoded as the canonical external byte while the payload remains binary64.
func RunCoalesce(ctx context.Context, executable, tag, value, fallback string) (RunResult, error) {
	tagByte, err := nullableTagByte(tag)
	if err != nil {
		return RunResult{}, err
	}
	return runHarness(ctx, executable, "coalesce", tagByte, value, fallback)
}

// RunCoalesceAssign executes the nullable logical-assignment ABI harness.
func RunCoalesceAssign(ctx context.Context, executable, tag, value, fallback string) (RunResult, error) {
	tagByte, err := nullableTagByte(tag)
	if err != nil {
		return RunResult{}, err
	}
	return runHarness(ctx, executable, "coalesceAssign", tagByte, value, fallback)
}

// RunStringLength executes the UTF-16 code-unit view ABI harness. The empty
// string is represented by an empty argument and maps to {NULL, 0}.
func RunStringLength(ctx context.Context, executable, codeUnits string) (RunResult, error) {
	if ctx == nil {
		return RunResult{}, fmt.Errorf("run context is nil")
	}
	if err := validateUTF16CodeUnits(codeUnits); err != nil {
		return RunResult{}, fmt.Errorf("UTF-16 argument: %w", err)
	}
	if strings.TrimSpace(executable) == "" {
		return RunResult{}, fmt.Errorf("executable path is empty")
	}
	command := exec.CommandContext(ctx, executable, codeUnits)
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	output, err := command.CombinedOutput()
	if err != nil {
		return RunResult{}, fmt.Errorf("run UTF-16 string length executable: %w: %s", err, strings.TrimSpace(string(output)))
	}
	trimmed := strings.TrimSuffix(string(output), "\n")
	if strings.Contains(trimmed, "\n") || validateBits(trimmed) != nil {
		return RunResult{}, fmt.Errorf("UTF-16 string length output is not one binary64 hex line: %q", string(output))
	}
	return RunResult{Arguments: []string{codeUnits}, Output: slices.Clone(output), OutputHash: hashBytes(output)}, nil
}

// RejectNonCanonicalChoose executes choose with an invalid ABI byte and
// requires the strict entry-point trap to reject the process.
func RejectNonCanonicalChoose(ctx context.Context, executable, left, right string) (RunResult, error) {
	return runHarnessExpectFailure(ctx, executable, "choose", "02", left, right)
}

// RejectNonCanonicalCoalesce requires the generated entry point to trap an
// unknown nullable tag before observing its payload.
func RejectNonCanonicalCoalesce(ctx context.Context, executable, value, fallback string) (RunResult, error) {
	return runHarnessExpectFailure(ctx, executable, "coalesce", "03", value, fallback)
}

// RejectNonCanonicalCoalesceAssign rejects an unknown nullable tag at the
// logical-assignment entry point.
func RejectNonCanonicalCoalesceAssign(ctx context.Context, executable, value, fallback string) (RunResult, error) {
	return runHarnessExpectFailure(ctx, executable, "coalesceAssign", "03", value, fallback)
}

// RejectNonCanonicalStringLength proves that a malformed {NULL, nonzero}
// borrowed view cannot cross the native ABI boundary.
func RejectNonCanonicalStringLength(ctx context.Context, executable string) (RunResult, error) {
	if ctx == nil {
		return RunResult{}, fmt.Errorf("run context is nil")
	}
	if strings.TrimSpace(executable) == "" {
		return RunResult{}, fmt.Errorf("executable path is empty")
	}
	command := exec.CommandContext(ctx, executable, "--invalid-null")
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	output, err := command.CombinedOutput()
	if err == nil {
		return RunResult{}, fmt.Errorf("noncanonical UTF-16 view was accepted")
	}
	if len(output) != 0 {
		return RunResult{}, fmt.Errorf("noncanonical UTF-16 view emitted output: %q", output)
	}
	return RunResult{Arguments: []string{"--invalid-null"}, Output: slices.Clone(output), OutputHash: hashBytes(output)}, nil
}

func runHarness(ctx context.Context, executable, entryPoint, flag, left, right string) (RunResult, error) {
	if ctx == nil {
		return RunResult{}, fmt.Errorf("run context is nil")
	}
	if err := validateBits(left); err != nil {
		return RunResult{}, fmt.Errorf("left argument: %w", err)
	}
	if err := validateBits(right); err != nil {
		return RunResult{}, fmt.Errorf("right argument: %w", err)
	}
	if strings.TrimSpace(executable) == "" {
		return RunResult{}, fmt.Errorf("executable path is empty")
	}
	if entryPoint != "add" && entryPoint != "compute" && entryPoint != "choose" && entryPoint != "coalesce" && entryPoint != "coalesceAssign" {
		return RunResult{}, fmt.Errorf("unsupported harness entry point %q", entryPoint)
	}
	arguments := []string{left, right}
	if entryPoint == "choose" || entryPoint == "coalesce" || entryPoint == "coalesceAssign" {
		if entryPoint == "choose" && flag != "00" && flag != "01" {
			return RunResult{}, fmt.Errorf("choose flag must be canonical 00 or 01")
		}
		if (entryPoint == "coalesce" || entryPoint == "coalesceAssign") && flag != "00" && flag != "01" && flag != "02" {
			return RunResult{}, fmt.Errorf("coalesce tag must be canonical 00, 01, or 02")
		}
		arguments = []string{flag, left, right}
	}
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	output, err := command.CombinedOutput()
	if err != nil {
		return RunResult{}, fmt.Errorf("run first-slice executable: %w: %s", err, strings.TrimSpace(string(output)))
	}
	trimmed := strings.TrimSuffix(string(output), "\n")
	if strings.Contains(trimmed, "\n") || len(trimmed) != 16 || validateBits(trimmed) != nil {
		return RunResult{}, fmt.Errorf("first-slice output is not one binary64 hex line: %q", string(output))
	}
	return RunResult{Arguments: slices.Clone(arguments), Output: slices.Clone(output), OutputHash: hashBytes(output)}, nil
}

func runHarnessExpectFailure(ctx context.Context, executable, entryPoint, flag, left, right string) (RunResult, error) {
	if ctx == nil {
		return RunResult{}, fmt.Errorf("run context is nil")
	}
	if (entryPoint != "choose" && entryPoint != "coalesce" && entryPoint != "coalesceAssign") || entryPoint == "choose" && (flag == "00" || flag == "01") || (entryPoint == "coalesce" || entryPoint == "coalesceAssign") && (flag == "00" || flag == "01" || flag == "02") {
		return RunResult{}, fmt.Errorf("invalid noncanonical choose invocation")
	}
	if err := validateBits(left); err != nil {
		return RunResult{}, fmt.Errorf("left argument: %w", err)
	}
	if err := validateBits(right); err != nil {
		return RunResult{}, fmt.Errorf("right argument: %w", err)
	}
	command := exec.CommandContext(ctx, executable, flag, left, right)
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	output, err := command.CombinedOutput()
	if err == nil {
		return RunResult{}, fmt.Errorf("noncanonical choose byte was accepted")
	}
	if len(output) != 0 {
		return RunResult{}, fmt.Errorf("noncanonical choose emitted output: %q", output)
	}
	return RunResult{Arguments: []string{flag, left, right}, Output: slices.Clone(output), OutputHash: hashBytes(output)}, nil
}

func nullableTagByte(tag string) (string, error) {
	switch tag {
	case "number":
		return "00", nil
	case "null":
		return "01", nil
	case "undefined":
		return "02", nil
	default:
		return "", fmt.Errorf("unsupported nullable tag %q", tag)
	}
}

func validateRequest(request LinkRequest) error {
	if filepath.IsAbs(request.RuntimeDirectory) == false || strings.TrimSpace(request.RuntimeDirectory) == "" {
		return fmt.Errorf("runtime artifact directory must be an absolute path")
	}
	if strings.TrimSpace(request.RuntimeArchivePath) != "" && !filepath.IsAbs(request.RuntimeArchivePath) {
		return fmt.Errorf("runtime archive path must be absolute when provided")
	}
	if filepath.IsAbs(request.OutputPath) == false || strings.TrimSpace(request.OutputPath) == "" {
		return fmt.Errorf("output path must be an absolute path")
	}
	if strings.TrimSpace(request.Clang) == "" || strings.TrimSpace(request.LLD) == "" {
		return ErrLinkUnavailable
	}
	if request.Runtime.Target.Triple != llvmbackend.FirstSliceTriple || request.Runtime.Target.CPU != llvmbackend.FirstSliceCPU || request.Runtime.Target.ObjectFormat != "elf" {
		return fmt.Errorf("unsupported first-slice runtime target: %#v", request.Runtime.Target)
	}
	entryPoint := normalizedEntryPoint(request.EntryPoint)
	if entryPoint != "add" && entryPoint != "choose" && entryPoint != "classify" && entryPoint != "compute" && entryPoint != "coalesce" && entryPoint != "coalesceAssign" && entryPoint != "stringLength" && entryPoint != "main" {
		return fmt.Errorf("unsupported first-slice entry point %q", request.EntryPoint)
	}
	llvmEntryPoint := entryPoint
	if entryPoint == "main" {
		llvmEntryPoint = "bingo_program_main_v1"
	}
	if !strings.Contains(string(request.Emission.LLVMIR), "define double @"+llvmEntryPoint+"(") {
		return fmt.Errorf("LLVM emission does not contain entry point %q", entryPoint)
	}
	return nil
}

func materializeLinkInputs(workspace string, request LinkRequest, entryPoint string) error {
	harness := request.Runtime.Artifacts.HarnessObject
	if entryPoint == "main" {
		harness = request.Runtime.Artifacts.ApplicationStartupObject
	} else if entryPoint == "choose" {
		if request.Runtime.Artifacts.ChooseHarnessObject == nil {
			return fmt.Errorf("runtime manifest has no choose harness object")
		}
		harness = *request.Runtime.Artifacts.ChooseHarnessObject
	} else if entryPoint == "compute" {
		if request.Runtime.Artifacts.ComputeHarnessObject == nil {
			return fmt.Errorf("runtime manifest has no compute harness object")
		}
		harness = *request.Runtime.Artifacts.ComputeHarnessObject
	} else if entryPoint == "coalesce" {
		if request.Runtime.Artifacts.CoalesceHarnessObject == nil {
			return fmt.Errorf("runtime manifest has no coalesce harness object")
		}
		harness = *request.Runtime.Artifacts.CoalesceHarnessObject
	} else if entryPoint == "coalesceAssign" {
		if request.Runtime.Artifacts.CoalesceAssignHarnessObject == nil {
			return fmt.Errorf("runtime manifest has no coalesce assignment harness object")
		}
		harness = *request.Runtime.Artifacts.CoalesceAssignHarnessObject
	} else if entryPoint == "stringLength" {
		if request.Runtime.Artifacts.StringLengthHarnessObject == nil {
			return fmt.Errorf("runtime manifest has no string length harness object")
		}
		harness = *request.Runtime.Artifacts.StringLengthHarnessObject
	}
	if entryPoint == "classify" {
		if request.Runtime.Artifacts.ClassifyHarnessObject == nil {
			return fmt.Errorf("runtime manifest has no classify harness object")
		}
		harness = *request.Runtime.Artifacts.ClassifyHarnessObject
	}
	inputs := []struct {
		artifact targetcontext.RuntimeArtifact
		name     string
	}{
		{request.Runtime.Artifacts.UmbrellaArchive, "libbingo_runtime.a"},
		{request.Runtime.Artifacts.StartupObject, "bingo_startup_empty.o"},
		{harness, harness.File},
	}
	for _, input := range inputs {
		path := filepath.Join(request.RuntimeDirectory, input.artifact.File)
		if input.name == request.Runtime.Artifacts.UmbrellaArchive.File && strings.TrimSpace(request.RuntimeArchivePath) != "" {
			path = request.RuntimeArchivePath
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read runtime artifact %s: %w", input.artifact.File, err)
		}
		if uint64(len(data)) != input.artifact.Bytes || hashBytes(data) != input.artifact.SHA256 {
			return fmt.Errorf("runtime artifact %s does not match locked identity", input.artifact.File)
		}
		if err := os.WriteFile(filepath.Join(workspace, input.name), data, 0o644); err != nil {
			return fmt.Errorf("materialize runtime artifact %s: %w", input.name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(workspace, "ts2bin-first-slice.o"), request.Emission.Object, 0o644); err != nil {
		return fmt.Errorf("materialize LLVM object: %w", err)
	}
	return nil
}

func responseFileBytes(entryPoints ...string) []byte {
	entryPoint := "add"
	if len(entryPoints) != 0 {
		entryPoint = normalizedEntryPoint(entryPoints[0])
	}
	harness := "bingo_add_harness.o"
	if entryPoint == "choose" {
		harness = "bingo_choose_harness.o"
	} else if entryPoint == "compute" {
		harness = "bingo_compute_harness.o"
	} else if entryPoint == "coalesce" {
		harness = "bingo_coalesce_harness.o"
	} else if entryPoint == "coalesceAssign" {
		harness = "bingo_coalesce_assign_harness.o"
	} else if entryPoint == "classify" {
		harness = "bingo_classify_harness.o"
	} else if entryPoint == "stringLength" {
		harness = "bingo_string_length_harness.o"
	} else if entryPoint == "main" {
		harness = "bingo_application_startup.o"
	}
	return []byte(strings.Join([]string{
		"--target=x86_64-unknown-linux-gnu",
		"-fuse-ld=lld",
		"-no-pie",
		"-Wl,--build-id=none",
		"-Wl,--no-undefined",
		"-Wl,--fatal-warnings",
		"-Wl,-Map=ts2bin-first-slice.map",
		"-o",
		"ts2bin-first-slice",
		"ts2bin-first-slice.o",
		"bingo_startup_empty.o",
		harness,
		"libbingo_runtime.a",
		"-ldl",
		"-lpthread",
		"-lm",
	}, "\n") + "\n")
}

func normalizedEntryPoint(entryPoint string) string {
	if strings.TrimSpace(entryPoint) == "" {
		return "add"
	}
	return entryPoint
}

func validateUTF16CodeUnits(value string) error {
	if len(value)%4 != 0 {
		return fmt.Errorf("code-unit hex length %d is not divisible by four", len(value))
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return fmt.Errorf("code-unit hex is not lowercase hexadecimal")
		}
	}
	return nil
}

func verifyLinkMap(responseFile, data []byte) error {
	if strings.Count(string(responseFile), "libbingo_runtime.a") != 1 {
		return fmt.Errorf("response file must contain exactly one umbrella runtime input")
	}
	text := string(data)
	if len(data) == 0 || !strings.Contains(text, "libbingo_runtime.a") {
		return fmt.Errorf("linker map does not prove umbrella runtime input")
	}
	archives := make(map[string]struct{})
	for _, field := range strings.Fields(text) {
		if index := strings.Index(field, "libbingo_runtime.a("); index >= 0 {
			archives[field[:index+len("libbingo_runtime.a")]] = struct{}{}
		}
	}
	if len(archives) != 1 {
		return fmt.Errorf("linker map contains multiple umbrella runtime inputs")
	}
	if !strings.Contains(text, "bingo_rt_abi_version_v1") {
		return fmt.Errorf("linker map does not contain the runtime ABI version symbol")
	}
	return nil
}

func newLinkArtifact(request LinkRequest, clangVersion, lldVersion string, responseFile, linkMap, executable []byte) (LinkArtifact, error) {
	artifact := LinkArtifact{
		SchemaVersion:       LinkArtifactSchemaVersion,
		EmissionContentHash: request.Emission.ContentHash,
		RuntimeManifestHash: request.Runtime.ContentHash,
		TargetTriple:        request.Runtime.Target.Triple,
		ClangVersion:        clangVersion,
		LLDVersion:          lldVersion,
		ResponseFileHash:    hashBytes(responseFile),
		LinkMapHash:         hashBytes(linkMap),
		ExecutableHash:      hashBytes(executable),
		ResponseFile:        slices.Clone(responseFile),
		LinkMap:             slices.Clone(linkMap),
		Executable:          slices.Clone(executable),
	}
	var err error
	artifact.ContentHash, err = linkContentHash(artifact)
	if err != nil {
		return LinkArtifact{}, err
	}
	if err := VerifyLinkArtifact(artifact); err != nil {
		return LinkArtifact{}, err
	}
	return artifact, nil
}

func VerifyLinkArtifact(artifact LinkArtifact) error {
	if artifact.SchemaVersion != LinkArtifactSchemaVersion || artifact.TargetTriple != llvmbackend.FirstSliceTriple {
		return fmt.Errorf("unsupported first-slice link artifact identity")
	}
	for _, value := range []struct {
		name   string
		digest string
	}{
		{"emission", artifact.EmissionContentHash},
		{"runtime manifest", artifact.RuntimeManifestHash},
		{"response file", artifact.ResponseFileHash},
		{"link map", artifact.LinkMapHash},
		{"executable", artifact.ExecutableHash},
		{"content", artifact.ContentHash},
	} {
		if !isDigest(value.digest) {
			return fmt.Errorf("invalid %s digest %q", value.name, value.digest)
		}
	}
	if len(artifact.ResponseFile) == 0 || hashBytes(artifact.ResponseFile) != artifact.ResponseFileHash {
		return fmt.Errorf("response file bytes do not match link identity")
	}
	if len(artifact.LinkMap) == 0 || hashBytes(artifact.LinkMap) != artifact.LinkMapHash {
		return fmt.Errorf("link map bytes do not match link identity")
	}
	if len(artifact.Executable) == 0 || hashBytes(artifact.Executable) != artifact.ExecutableHash {
		return fmt.Errorf("executable bytes do not match link identity")
	}
	want, err := linkContentHash(artifact)
	if err != nil {
		return err
	}
	if artifact.ContentHash != want {
		return fmt.Errorf("link content hash mismatch: got %s want %s", artifact.ContentHash, want)
	}
	return nil
}

func linkContentHash(artifact LinkArtifact) (string, error) {
	return canonicalHash(struct {
		SchemaVersion       uint32 `json:"schemaVersion"`
		EmissionContentHash string `json:"emissionContentHash"`
		RuntimeManifestHash string `json:"runtimeManifestHash"`
		TargetTriple        string `json:"targetTriple"`
		ClangVersion        string `json:"clangVersion"`
		LLDVersion          string `json:"lldVersion"`
		ResponseFileHash    string `json:"responseFileHash"`
		LinkMapHash         string `json:"linkMapHash"`
		ExecutableHash      string `json:"executableHash"`
	}{artifact.SchemaVersion, artifact.EmissionContentHash, artifact.RuntimeManifestHash, artifact.TargetTriple, artifact.ClangVersion, artifact.LLDVersion, artifact.ResponseFileHash, artifact.LinkMapHash, artifact.ExecutableHash})
}

func canonicalHash(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func hashBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func isDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func toolVersion(ctx context.Context, name string) (string, error) {
	command := exec.CommandContext(ctx, name, "--version")
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	line := strings.SplitN(string(output), "\n", 2)[0]
	if strings.TrimSpace(line) == "" {
		return "", fmt.Errorf("tool %q returned an empty version", name)
	}
	return strings.TrimSpace(line), nil
}

func linkerEnvironment(lld string) []string {
	environment := append([]string(nil), os.Environ()...)
	directory := filepath.Dir(lld)
	for index, value := range environment {
		if strings.HasPrefix(value, "PATH=") {
			environment[index] = "PATH=" + directory + string(os.PathListSeparator) + strings.TrimPrefix(value, "PATH=")
			return append(environment, "LC_ALL=C", "TZ=UTC")
		}
	}
	return append(environment, "PATH="+directory, "LC_ALL=C", "TZ=UTC")
}

func validateBits(value string) error {
	if len(value) != 16 {
		return fmt.Errorf("want exactly 16 hexadecimal characters")
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') && (char < 'A' || char > 'F') {
			return fmt.Errorf("contains non-hexadecimal character")
		}
	}
	return nil
}

func requireNewOutput(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("output path already exists: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat output path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	return nil
}

func publishNewFile(path string, data []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".ts2bin-output-*")
	if err != nil {
		return fmt.Errorf("create output staging file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return fmt.Errorf("chmod staged output: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write staged output: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close staged output: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish executable: %w", err)
	}
	return nil
}
