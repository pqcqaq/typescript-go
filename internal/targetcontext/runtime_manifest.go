// Package targetcontext resolves target-independent build requests against
// observed LLVM and locked runtime manifests.
package targetcontext

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/microsoft/typescript-go/internal/bingo"
	"github.com/microsoft/typescript-go/internal/frontendwire"
	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const (
	RuntimeManifestSchemaVersion uint32 = 1
	LockedRuntimeName                   = "core-es2020"
	LockedRuntimeABIVersion      uint32 = 1
	LockedRuntimeSourceHash             = "bb2f8346742a14ce9d6b96446dc007ec9f7be798ea49b18cdb99ab99a57bbc92"
	LockedABISchemaHash                 = "12c5f0fac0559b74dd9365b22f1bba8e532e55d5a9bd77bb94b6a74efd7c6bb3"
	LockedTargetManifestHash            = "d888122a4e26d6ee5c0844ea256e71de625d337795b5584d62f2a1d9b194cc19"
	InteropTargetManifestHash           = "14c3eb2bb6df5fe2febf918cd8996e0c9c4860162d35ccc9650e92c2edc3deb0"
	LockedRuntimeManifestHash           = "7acc709640921bfdc8a9195188c9a41e242c1cd2807b79dc0cca106e7df4b81f"
)

func lockedRuntimeManifestHash(profile string) (string, bool) {
	switch profile {
	case string(frontendwire.ProfileStatic):
		return LockedRuntimeManifestHash, true
	case string(frontendwire.ProfileInterop):
		return "", false
	default:
		return "", false
	}
}

func isSupportedRuntimeProfile(profile string) bool {
	return profile == string(frontendwire.ProfileStatic) || profile == string(frontendwire.ProfileInterop)
}

// RuntimeTarget is the target tuple implemented by one runtime artifact set.
type RuntimeTarget struct {
	Triple       string   `json:"triple"`
	CPU          string   `json:"cpu"`
	Features     []string `json:"features"`
	ObjectFormat string   `json:"objectFormat"`
}

// RuntimeArtifact binds a runtime file name to its size and content digest.
type RuntimeArtifact struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
	Bytes  uint64 `json:"bytes"`
}

// RuntimeArtifacts lists the first-slice link inputs.
type RuntimeArtifacts struct {
	StartupObject                      RuntimeArtifact  `json:"startupObject"`
	ApplicationStartupObject           RuntimeArtifact  `json:"applicationStartupObject"`
	HarnessObject                      RuntimeArtifact  `json:"harnessObject"`
	ComputeHarnessObject               *RuntimeArtifact `json:"computeHarnessObject,omitempty"`
	ChooseHarnessObject                *RuntimeArtifact `json:"chooseHarnessObject,omitempty"`
	ClassifyHarnessObject              *RuntimeArtifact `json:"classifyHarnessObject,omitempty"`
	CoalesceHarnessObject              *RuntimeArtifact `json:"coalesceHarnessObject,omitempty"`
	CoalesceAssignHarnessObject        *RuntimeArtifact `json:"coalesceAssignHarnessObject,omitempty"`
	StringLengthHarnessObject          *RuntimeArtifact `json:"stringLengthHarnessObject,omitempty"`
	ObjectAliasHarnessObject           *RuntimeArtifact `json:"objectAliasHarnessObject,omitempty"`
	PropertyNullishAssignHarnessObject *RuntimeArtifact `json:"propertyNullishAssignHarnessObject,omitempty"`
	ClosureCounterHarnessObject        *RuntimeArtifact `json:"closureCounterHarnessObject,omitempty"`
	ClassCounterHarnessObject          *RuntimeArtifact `json:"classCounterHarnessObject,omitempty"`
	DerivedCounterHarnessObject        *RuntimeArtifact `json:"derivedCounterHarnessObject,omitempty"`
	ClassAccessHarnessObject           *RuntimeArtifact `json:"classAccessHarnessObject,omitempty"`
	CheckedObjectCastHarnessObject     *RuntimeArtifact `json:"checkedObjectCastHarnessObject,omitempty"`
	UmbrellaArchive                    RuntimeArtifact  `json:"umbrellaArchive"`
}

// RuntimeCapability is one available versioned C ABI implementation.
type RuntimeCapability struct {
	LogicalName        bingo.RuntimeCapabilityID `json:"logicalName"`
	SymbolName         string                    `json:"symbolName"`
	ABIVersion         string                    `json:"abiVersion"`
	Signature          string                    `json:"signature"`
	Effects            []bingo.PassEffect        `json:"effects"`
	RequiredFeatures   []string                  `json:"requiredFeatures"`
	SignatureHash      string                    `json:"signatureHash"`
	ImplementationHash string                    `json:"implementationHash"`
}

// RuntimeToolchain records the locked tools used to build the runtime.
type RuntimeToolchain struct {
	Rustc string `json:"rustc"`
	Cargo string `json:"cargo"`
	Clang string `json:"clang"`
}

// RuntimeManifest is the linked runtime identity consumed by target
// resolution. It describes available implementations, not program usage.
type RuntimeManifest struct {
	SchemaVersion      uint32              `json:"schemaVersion"`
	Runtime            string              `json:"runtime"`
	RuntimeABIVersion  uint32              `json:"runtimeABIVersion"`
	Target             RuntimeTarget       `json:"target"`
	Profile            string              `json:"profile"`
	GC                 string              `json:"gc"`
	Exceptions         string              `json:"exceptions"`
	Panic              string              `json:"panic"`
	Capabilities       []RuntimeCapability `json:"capabilities"`
	Artifacts          RuntimeArtifacts    `json:"artifacts"`
	ABISchemaHash      string              `json:"abiSchemaHash"`
	TargetManifestHash string              `json:"targetManifestHash"`
	SourceHash         string              `json:"sourceHash"`
	Toolchain          RuntimeToolchain    `json:"toolchain"`
	ContentHash        string              `json:"contentHash"`
}

type lockedRuntimeCapability struct {
	logicalName bingo.RuntimeCapabilityID
	symbolName  string
	signature   string
	effects     []bingo.PassEffect
}

var staticRuntimeCapabilities = []lockedRuntimeCapability{
	{"rt.abi.version", "bingo_rt_abi_version_v1", "() -> u32", []bingo.PassEffect{}},
	{"rt.gc.alloc", "bingo_gc_alloc_v1", "(*const shape, **object) -> status", []bingo.PassEffect{bingo.PassEffectAllocate, bingo.PassEffectSafepoint}},
	{"rt.gc.collect", "bingo_gc_collect_v1", "() -> status", []bingo.PassEffect{bingo.PassEffectSafepoint}},
	{"rt.gc.frame.link", "bingo_gc_frame_link_v1", "(*frame) -> status", []bingo.PassEffect{bingo.PassEffectRootPublication}},
	{"rt.gc.frame.unlink", "bingo_gc_frame_unlink_v1", "(*frame) -> status", []bingo.PassEffect{bingo.PassEffectRootPublication}},
	{"rt.gc.heap.reset", "bingo_gc_heap_reset_v1", "() -> status", []bingo.PassEffect{}},
	{"rt.gc.root.clear", "bingo_gc_root_clear_v1", "(*frame, u32) -> status", []bingo.PassEffect{bingo.PassEffectRootPublication}},
	{"rt.gc.root.publish", "bingo_gc_root_publish_v1", "(*frame, u64) -> status", []bingo.PassEffect{bingo.PassEffectRootPublication}},
	{"rt.gc.root.reload", "bingo_gc_root_reload_v1", "(*frame, u32, **object) -> status", []bingo.PassEffect{bingo.PassEffectRootPublication}},
	{"rt.gc.root.store", "bingo_gc_root_store_v1", "(*frame, u32, *object) -> status", []bingo.PassEffect{bingo.PassEffectRootPublication}},
	{"rt.gc.safepoint", "bingo_gc_safepoint_v1", "() -> status", []bingo.PassEffect{bingo.PassEffectSafepoint}},
	{"rt.gc.stats", "bingo_gc_stats_v1", "(*stats) -> status", []bingo.PassEffect{bingo.PassEffectRead}},
	{"rt.gc.write_barrier", "bingo_gc_write_barrier_v1", "(*object, u32, *object) -> status", []bingo.PassEffect{bingo.PassEffectWrite}},
}

var interopRuntimeCapability = lockedRuntimeCapability{
	bingo.DynamicPropertyLoadCapability,
	bingo.DynamicPropertyLoadSymbol,
	"u32(dynamic-value-v1,utf16-string-view,dynamic-value-v1*)",
	[]bingo.PassEffect{bingo.PassEffectCall, bingo.PassEffectRead, bingo.PassEffectThrow},
}

func runtimeCapabilitiesForProfile(profile string) ([]lockedRuntimeCapability, bool) {
	capabilities := slices.Clone(staticRuntimeCapabilities)
	switch profile {
	case string(frontendwire.ProfileStatic):
		return capabilities, true
	case string(frontendwire.ProfileInterop):
		capabilities = append(capabilities, interopRuntimeCapability)
		slices.SortFunc(capabilities, func(left, right lockedRuntimeCapability) int {
			return strings.Compare(string(left.logicalName), string(right.logicalName))
		})
		return capabilities, true
	default:
		return nil, false
	}
}

func targetManifestHashForProfile(profile string) (string, bool) {
	switch profile {
	case string(frontendwire.ProfileStatic):
		return LockedTargetManifestHash, true
	case string(frontendwire.ProfileInterop):
		return InteropTargetManifestHash, true
	default:
		return "", false
	}
}

// DecodeRuntimeManifest strictly decodes and validates a locked manifest.
func DecodeRuntimeManifest(data []byte) (*RuntimeManifest, error) {
	var manifest RuntimeManifest
	if err := jsonx.Unmarshal(data, &manifest, jsonx.RejectUnknownMembers(true)); err != nil {
		return nil, fmt.Errorf("decode runtime manifest: %w", err)
	}
	if err := ValidateRuntimeManifest(manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

// CanonicalBytes returns the cross-language canonical runtime manifest JSON.
func (manifest RuntimeManifest) CanonicalBytes() ([]byte, error) {
	if err := ValidateRuntimeManifest(manifest); err != nil {
		return nil, err
	}
	return canonicalJSON(manifest)
}

// Digest returns the manifest's validated content identity.
func (manifest RuntimeManifest) Digest() string { return manifest.ContentHash }

// ValidateRuntimeManifest checks the complete locked first-slice identity.
func ValidateRuntimeManifest(manifest RuntimeManifest) error {
	if manifest.SchemaVersion != RuntimeManifestSchemaVersion {
		return fmt.Errorf("unsupported runtime manifest schema %d", manifest.SchemaVersion)
	}
	if manifest.Runtime != LockedRuntimeName || manifest.RuntimeABIVersion != LockedRuntimeABIVersion {
		return fmt.Errorf("unsupported runtime identity %q ABI %d", manifest.Runtime, manifest.RuntimeABIVersion)
	}
	if manifest.Target.Triple != "x86_64-unknown-linux-gnu" || manifest.Target.CPU != "generic" ||
		manifest.Target.Features == nil || len(manifest.Target.Features) != 0 || manifest.Target.ObjectFormat != "elf" {
		return fmt.Errorf("unsupported runtime target: %#v", manifest.Target)
	}
	if !isSupportedRuntimeProfile(manifest.Profile) || manifest.GC != "tracing" || manifest.Exceptions != "none" || manifest.Panic != "abort" {
		return fmt.Errorf("unsupported runtime profile: profile=%s gc=%s exceptions=%s panic=%s", manifest.Profile, manifest.GC, manifest.Exceptions, manifest.Panic)
	}
	targetManifestHash, ok := targetManifestHashForProfile(manifest.Profile)
	if !ok || manifest.SourceHash != LockedRuntimeSourceHash || manifest.ABISchemaHash != LockedABISchemaHash || manifest.TargetManifestHash != targetManifestHash {
		return fmt.Errorf("runtime source or schema identity is not locked")
	}
	if err := validateRuntimeArtifact(manifest.Artifacts.UmbrellaArchive, "libbingo_runtime.a"); err != nil {
		return fmt.Errorf("umbrella archive: %w", err)
	}
	if err := validateRuntimeArtifact(manifest.Artifacts.StartupObject, "bingo_startup_empty.o"); err != nil {
		return fmt.Errorf("startup object: %w", err)
	}
	if err := validateRuntimeArtifact(manifest.Artifacts.ApplicationStartupObject, "bingo_application_startup.o"); err != nil {
		return fmt.Errorf("application startup object: %w", err)
	}
	if err := validateRuntimeArtifact(manifest.Artifacts.HarnessObject, "bingo_add_harness.o"); err != nil {
		return fmt.Errorf("harness object: %w", err)
	}
	if manifest.Artifacts.ChooseHarnessObject != nil {
		if err := validateRuntimeArtifact(*manifest.Artifacts.ChooseHarnessObject, "bingo_choose_harness.o"); err != nil {
			return fmt.Errorf("choose harness object: %w", err)
		}
	}
	if manifest.Artifacts.ComputeHarnessObject != nil {
		if err := validateRuntimeArtifact(*manifest.Artifacts.ComputeHarnessObject, "bingo_compute_harness.o"); err != nil {
			return fmt.Errorf("compute harness object: %w", err)
		}
	}
	if manifest.Artifacts.ClassifyHarnessObject != nil {
		if err := validateRuntimeArtifact(*manifest.Artifacts.ClassifyHarnessObject, "bingo_classify_harness.o"); err != nil {
			return fmt.Errorf("classify harness object: %w", err)
		}
	}
	if manifest.Artifacts.CoalesceHarnessObject != nil {
		if err := validateRuntimeArtifact(*manifest.Artifacts.CoalesceHarnessObject, "bingo_coalesce_harness.o"); err != nil {
			return fmt.Errorf("coalesce harness object: %w", err)
		}
	}
	if manifest.Artifacts.CoalesceAssignHarnessObject != nil {
		if err := validateRuntimeArtifact(*manifest.Artifacts.CoalesceAssignHarnessObject, "bingo_coalesce_assign_harness.o"); err != nil {
			return fmt.Errorf("coalesce assignment harness object: %w", err)
		}
	}
	if manifest.Artifacts.StringLengthHarnessObject != nil {
		if err := validateRuntimeArtifact(*manifest.Artifacts.StringLengthHarnessObject, "bingo_string_length_harness.o"); err != nil {
			return fmt.Errorf("string length harness object: %w", err)
		}
	}
	if manifest.Artifacts.ObjectAliasHarnessObject != nil {
		if err := validateRuntimeArtifact(*manifest.Artifacts.ObjectAliasHarnessObject, "bingo_object_alias_harness.o"); err != nil {
			return fmt.Errorf("object alias harness object: %w", err)
		}
	}
	if manifest.Artifacts.PropertyNullishAssignHarnessObject != nil {
		if err := validateRuntimeArtifact(*manifest.Artifacts.PropertyNullishAssignHarnessObject, "bingo_property_nullish_assign_harness.o"); err != nil {
			return fmt.Errorf("property nullish assignment harness object: %w", err)
		}
	}
	if manifest.Artifacts.ClosureCounterHarnessObject != nil {
		if err := validateRuntimeArtifact(*manifest.Artifacts.ClosureCounterHarnessObject, "bingo_closure_counter_harness.o"); err != nil {
			return fmt.Errorf("closure counter harness object: %w", err)
		}
	}
	if manifest.Artifacts.ClassCounterHarnessObject != nil {
		if err := validateRuntimeArtifact(*manifest.Artifacts.ClassCounterHarnessObject, "bingo_class_counter_harness.o"); err != nil {
			return fmt.Errorf("class counter harness object: %w", err)
		}
	}
	if manifest.Artifacts.DerivedCounterHarnessObject != nil {
		if err := validateRuntimeArtifact(*manifest.Artifacts.DerivedCounterHarnessObject, "bingo_derived_counter_harness.o"); err != nil {
			return fmt.Errorf("derived counter harness object: %w", err)
		}
	}
	if manifest.Artifacts.ClassAccessHarnessObject != nil {
		if err := validateRuntimeArtifact(*manifest.Artifacts.ClassAccessHarnessObject, "bingo_class_access_harness.o"); err != nil {
			return fmt.Errorf("class access harness object: %w", err)
		}
	}
	if manifest.Artifacts.CheckedObjectCastHarnessObject != nil {
		if err := validateRuntimeArtifact(*manifest.Artifacts.CheckedObjectCastHarnessObject, "bingo_checked_object_cast_harness.o"); err != nil {
			return fmt.Errorf("checked object cast harness object: %w", err)
		}
	}
	wantedCapabilities, _ := runtimeCapabilitiesForProfile(manifest.Profile)
	if len(manifest.Capabilities) != len(wantedCapabilities) {
		return fmt.Errorf("runtime capability count is %d, want %d for profile %q", len(manifest.Capabilities), len(wantedCapabilities), manifest.Profile)
	}
	for index, capability := range manifest.Capabilities {
		want := wantedCapabilities[index]
		if capability.LogicalName != want.logicalName || capability.SymbolName != want.symbolName ||
			capability.ABIVersion != "1.0.0" || capability.Signature != want.signature ||
			!slices.Equal(capability.Effects, want.effects) ||
			capability.RequiredFeatures == nil || len(capability.RequiredFeatures) != 0 {
			return fmt.Errorf("unsupported first-slice runtime capability %d: %#v", index, capability)
		}
		signatureHash, err := canonicalHash(capability.Signature)
		if err != nil {
			return fmt.Errorf("hash runtime capability signature: %w", err)
		}
		if capability.SignatureHash != signatureHash {
			return fmt.Errorf("runtime capability signature hash mismatch: got %s want %s", capability.SignatureHash, signatureHash)
		}
		if capability.ImplementationHash != manifest.Artifacts.UmbrellaArchive.SHA256 {
			return fmt.Errorf("runtime capability implementation hash does not match umbrella archive")
		}
	}
	if !strings.HasPrefix(manifest.Toolchain.Rustc, "rustc 1.97.1 ") ||
		!strings.HasPrefix(manifest.Toolchain.Cargo, "cargo 1.97.1 ") ||
		!strings.Contains(manifest.Toolchain.Clang, "20.1.8") {
		return fmt.Errorf("runtime toolchain is not locked: %#v", manifest.Toolchain)
	}
	want, err := runtimeManifestContentHash(manifest)
	if err != nil {
		return fmt.Errorf("hash runtime manifest: %w", err)
	}
	if manifest.ContentHash != want {
		return fmt.Errorf("runtime manifest hash mismatch: got %s want %s", manifest.ContentHash, want)
	}
	lockedHash, published := lockedRuntimeManifestHash(manifest.Profile)
	if !published {
		return fmt.Errorf("runtime profile %q has no authoritative manifest identity", manifest.Profile)
	}
	if manifest.ContentHash != lockedHash {
		return fmt.Errorf("runtime manifest identity %s is not locked for profile %q", manifest.ContentHash, manifest.Profile)
	}
	return nil
}

func validateRuntimeArtifact(artifact RuntimeArtifact, expectedFile string) error {
	if artifact.File != expectedFile {
		return fmt.Errorf("file is %q, want %q", artifact.File, expectedFile)
	}
	if !isLowerDigest(artifact.SHA256) || artifact.Bytes == 0 {
		return fmt.Errorf("invalid artifact identity: %#v", artifact)
	}
	return nil
}

func runtimeManifestContentHash(manifest RuntimeManifest) (string, error) {
	return canonicalHash(struct {
		SchemaVersion      uint32              `json:"schemaVersion"`
		Runtime            string              `json:"runtime"`
		RuntimeABIVersion  uint32              `json:"runtimeABIVersion"`
		Target             RuntimeTarget       `json:"target"`
		Profile            string              `json:"profile"`
		GC                 string              `json:"gc"`
		Exceptions         string              `json:"exceptions"`
		Panic              string              `json:"panic"`
		Capabilities       []RuntimeCapability `json:"capabilities"`
		Artifacts          RuntimeArtifacts    `json:"artifacts"`
		ABISchemaHash      string              `json:"abiSchemaHash"`
		TargetManifestHash string              `json:"targetManifestHash"`
		SourceHash         string              `json:"sourceHash"`
		Toolchain          RuntimeToolchain    `json:"toolchain"`
	}{manifest.SchemaVersion, manifest.Runtime, manifest.RuntimeABIVersion, manifest.Target, manifest.Profile, manifest.GC, manifest.Exceptions, manifest.Panic, manifest.Capabilities, manifest.Artifacts, manifest.ABISchemaHash, manifest.TargetManifestHash, manifest.SourceHash, manifest.Toolchain})
}

func canonicalHash(value any) (string, error) {
	data, err := canonicalJSON(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func canonicalJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var generic any
	if err := decoder.Decode(&generic); err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(generic); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'}), nil
}

func isLowerDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			if char < 'a' || char > 'f' {
				return false
			}
		}
	}
	return true
}
