package frontendwire

import (
	"os"
	"testing"
)

const maxSnapshotFuzzInput = 256 << 10

func FuzzDecodeFrontendSnapshot(f *testing.F) {
	seed, err := os.ReadFile("../../testdata/ts2bin/choose/frontend-snapshot.json")
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	objectSeed, err := os.ReadFile("../../testdata/ts2bin/objectalias/frontend-snapshot.json")
	if err != nil {
		f.Fatal(err)
	}
	f.Add(objectSeed)
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxSnapshotFuzzInput {
			return
		}
		snapshot, err := DecodeFrontendSnapshot(data)
		if err != nil {
			return
		}
		canonical, err := snapshot.CanonicalBytes()
		if err != nil {
			t.Fatalf("accepted frontend snapshot is not canonical: %v", err)
		}
		if _, err := DecodeFrontendSnapshot(canonical); err != nil {
			t.Fatalf("canonical frontend snapshot does not round trip: %v", err)
		}
	})
}

func FuzzDecodeProgramSnapshot(f *testing.F) {
	seed, err := os.ReadFile("../../testdata/ts2bin/choose/frontend-snapshot.json")
	if err != nil {
		f.Fatal(err)
	}
	frontend, err := DecodeFrontendSnapshot(seed)
	if err != nil {
		f.Fatal(err)
	}
	programSeed, err := frontend.Program.CanonicalBytes()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(programSeed)
	objectSeed, err := os.ReadFile("../../testdata/ts2bin/objectalias/frontend-snapshot.json")
	if err != nil {
		f.Fatal(err)
	}
	objectFrontend, err := DecodeFrontendSnapshot(objectSeed)
	if err != nil {
		f.Fatal(err)
	}
	objectProgramSeed, err := objectFrontend.Program.CanonicalBytes()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(objectProgramSeed)
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxSnapshotFuzzInput {
			return
		}
		snapshot, err := DecodeProgramSnapshot(data)
		if err != nil {
			return
		}
		canonical, err := snapshot.CanonicalBytes()
		if err != nil {
			t.Fatalf("accepted program snapshot is not canonical: %v", err)
		}
		if _, err := DecodeProgramSnapshot(canonical); err != nil {
			t.Fatalf("canonical program snapshot does not round trip: %v", err)
		}
	})
}
