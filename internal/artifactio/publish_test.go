package artifactio

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublishNewFilePublishesCompletedBytesAndCleansStagingFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "artifact")
	want := []byte("verified artifact")

	if err := PublishNewFile(path, want, 0o640); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("artifact bytes = %q, want %q", got, want)
	}
	assertNoStagingFiles(t, directory)
}

func TestPublishNewFileDoesNotReplaceConcurrentOwner(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "artifact")
	if err := os.WriteFile(path, []byte("existing owner"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := PublishNewFile(path, []byte("replacement"), 0o640); err == nil {
		t.Fatal("PublishNewFile accepted an existing destination")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "existing owner" {
		t.Fatalf("existing artifact was replaced with %q", got)
	}
	assertNoStagingFiles(t, directory)
}

func assertNoStagingFiles(t *testing.T, directory string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(directory, ".ts2bin-output-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("staging files remain: %v", matches)
	}
}
