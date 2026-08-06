package tsfrontend

import "github.com/microsoft/typescript-go/internal/frontendwire"

// validatedProgramSnapshot is the package-private handoff for consumers that
// require every lowering invariant to have been checked.
type validatedProgramSnapshot struct {
	snapshot ProgramSnapshot
}

func newValidatedProgramSnapshot(snapshot ProgramSnapshot) (validatedProgramSnapshot, error) {
	encoded, err := snapshot.CanonicalBytes()
	if err != nil {
		return validatedProgramSnapshot{}, err
	}
	decoded, err := frontendwire.DecodeProgramSnapshot(encoded)
	if err != nil {
		return validatedProgramSnapshot{}, err
	}
	return validatedProgramSnapshot{snapshot: *decoded}, nil
}

// ValidateProgramSnapshot preserves the capture package API while delegating
// serialized snapshot validation to the checker-free wire contract.
func ValidateProgramSnapshot(snapshot ProgramSnapshot) error {
	return frontendwire.ValidateProgramSnapshot(snapshot)
}

func isDigest(value string) bool {
	return frontendwire.IsCanonicalDigest(value)
}
