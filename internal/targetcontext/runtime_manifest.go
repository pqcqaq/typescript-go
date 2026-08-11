// Package targetcontext resolves target-independent build requests against
// observed LLVM and locked runtime manifests.
package targetcontext

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/microsoft/typescript-go/internal/bingo"
	jsonx "github.com/microsoft/typescript-go/internal/json"
)

const (
	RuntimeManifestSchemaVersion uint32 = 1
	LockedRuntimeName                   = "core-es2020"
	LockedRuntimeABIVersion      uint32 = 1
	LockedRuntimeSourceHash             = "6094e7460d450b6cab129f4620c51206b1a05ea783b034623b2987ba00e49ad2"
	LockedABISchemaHash                 = "5af9f32c464859fa43b890026438855012ddf4e9ed3f6f89ed5d020ecf6c6a25"
	LockedTargetManifestHash            = "163f4e5e3a0a5f25c160748ac0c08c92a1a256361d05cd4c85151826697dff6b"
	LockedRuntimeManifestHash           = "5959a76b5407b501bdc6333b728e49252498f6a82f19b827d800a9e83124bccf"
)

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
	StartupObject               RuntimeArtifact  `json:"startupObject"`
	ApplicationStartupObject    RuntimeArtifact  `json:"applicationStartupObject"`
	HarnessObject               RuntimeArtifact  `json:"harnessObject"`
	ComputeHarnessObject        *RuntimeArtifact `json:"computeHarnessObject,omitempty"`
	ChooseHarnessObject         *RuntimeArtifact `json:"chooseHarnessObject,omitempty"`
	ClassifyHarnessObject       *RuntimeArtifact `json:"classifyHarnessObject,omitempty"`
	CoalesceHarnessObject       *RuntimeArtifact `json:"coalesceHarnessObject,omitempty"`
	CoalesceAssignHarnessObject *RuntimeArtifact `json:"coalesceAssignHarnessObject,omitempty"`
	StringLengthHarnessObject   *RuntimeArtifact `json:"stringLengthHarnessObject,omitempty"`
	UmbrellaArchive             RuntimeArtifact  `json:"umbrellaArchive"`
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
	if manifest.Profile != "static" || manifest.GC != "tracing" || manifest.Exceptions != "none" || manifest.Panic != "abort" {
		return fmt.Errorf("unsupported runtime profile: profile=%s gc=%s exceptions=%s panic=%s", manifest.Profile, manifest.GC, manifest.Exceptions, manifest.Panic)
	}
	if manifest.SourceHash != LockedRuntimeSourceHash || manifest.ABISchemaHash != LockedABISchemaHash || manifest.TargetManifestHash != LockedTargetManifestHash {
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
	if len(manifest.Capabilities) != 1 {
		return fmt.Errorf("runtime capability count is %d, want 1", len(manifest.Capabilities))
	}
	capability := manifest.Capabilities[0]
	if capability.LogicalName != "rt.abi.version" || capability.SymbolName != "bingo_rt_abi_version_v1" ||
		capability.ABIVersion != "1.0.0" || capability.Signature != "() -> u32" ||
		capability.Effects == nil || len(capability.Effects) != 0 ||
		capability.RequiredFeatures == nil || len(capability.RequiredFeatures) != 0 {
		return fmt.Errorf("unsupported first-slice runtime capability: %#v", capability)
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
	if manifest.ContentHash != LockedRuntimeManifestHash {
		return fmt.Errorf("runtime manifest identity %s is not locked", manifest.ContentHash)
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
