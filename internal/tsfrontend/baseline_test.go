package tsfrontend

import (
	"bytes"
	"testing"
)

func TestCanonicalBuildInfoJSONIsStableAndComplete(t *testing.T) {
	t.Parallel()
	a, err := CanonicalBuildInfoJSON()
	if err != nil {
		t.Fatal(err)
	}
	b, err := CanonicalBuildInfoJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("build info changed between calls:\n%s\n%s", a, b)
	}
	info := CurrentBuildInfo()
	if info.TypeScriptGoCommit != TypeScriptGoCommit || info.StandardLibraryHash != StandardLibraryHash {
		t.Fatalf("incomplete build info: %#v", info)
	}
	if info.TypeScriptVersion == "" || info.GoVersion == "" || info.LLVMMajor != 20 ||
		info.LLVMVersion != LockedLLVMVersion || info.LLDVersion != LockedLLDVersion {
		t.Fatalf("invalid build info: %#v", info)
	}
}
