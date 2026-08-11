package ast2bingo

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/microsoft/typescript-go/internal/bingo"
)

// PrimitiveLoweringSchema names the source-to-HIR implementation contract.
// It is separate from the HIR wire major so implementation changes can
// invalidate caches without inventing a compatible compiler identity.
const PrimitiveLoweringSchema = "bingo-hir-lowering-v7"

// These values are intentionally empty in source. Fork builds inject them
// from ts2bin.lock.json with -ldflags -X, so the exact checkout is recorded
// without making source code self-referential.
var (
	injectedUpstreamCommit string
	injectedForkCommit     string
)

//go:embed compiler_identity.go pass_binding.go replay.go
var primitiveLoweringSources embed.FS

var (
	primitiveLoweringHashOnce sync.Once
	primitiveLoweringHash     string
)

// PrimitiveLoweringHash returns a stable, non-self-referential digest of the
// compiled first-slice lowering implementation and its HIR schema major.
func PrimitiveLoweringHash() string {
	primitiveLoweringHashOnce.Do(func() {
		digest := sha256.New()
		_, _ = digest.Write([]byte(PrimitiveLoweringSchema))
		_, _ = digest.Write([]byte{0})
		_, _ = digest.Write([]byte(fmt.Sprintf("hir-schema:%d", bingo.HIRSchemaVersion)))
		_, _ = digest.Write([]byte{0})
		for _, path := range []string{"compiler_identity.go", "pass_binding.go", "replay.go"} {
			content, err := primitiveLoweringSources.ReadFile(path)
			if err != nil {
				panic(fmt.Sprintf("read embedded lowering source %q: %v", path, err))
			}
			_, _ = digest.Write([]byte(path))
			_, _ = digest.Write([]byte{0})
			_, _ = digest.Write(content)
			_, _ = digest.Write([]byte{0})
		}
		primitiveLoweringHash = hex.EncodeToString(digest.Sum(nil))
	})
	return primitiveLoweringHash
}

// NewCompilerBuildIdentity combines fork provenance with the compiled
// lowering implementation. Callers that read ts2bin.lock.json use this
// boundary rather than copying identity fields into replay artifacts.
func NewCompilerBuildIdentity(upstreamCommit, forkCommit string) (bingo.CompilerBuildIdentity, error) {
	identity := bingo.CompilerBuildIdentity{
		UpstreamCommit: upstreamCommit,
		ForkCommit:     forkCommit,
		LoweringSchema: PrimitiveLoweringSchema,
		LoweringHash:   PrimitiveLoweringHash(),
	}
	if err := bingo.ValidateCompilerBuildIdentity(identity); err != nil {
		return bingo.CompilerBuildIdentity{}, err
	}
	return identity, nil
}

// InjectedCompilerBuildIdentity returns the identity supplied by the build.
// The variables are set with -ldflags -X using their fully-qualified names;
// an unconfigured development binary fails closed instead of minting HIR with
// guessed fork provenance.
func InjectedCompilerBuildIdentity() (bingo.CompilerBuildIdentity, error) {
	identity, err := NewCompilerBuildIdentity(
		injectedUpstreamCommit,
		injectedForkCommit,
	)
	if err != nil {
		return bingo.CompilerBuildIdentity{}, fmt.Errorf("compiler build identity was not injected: %w", err)
	}
	return identity, nil
}
