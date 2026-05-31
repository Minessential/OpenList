package fs

import (
	"path/filepath"
	"testing"
)

func TestServerDownloadLocalPath(t *testing.T) {
	base := t.TempDir()

	got, err := ServerDownloadLocalPath(base, "/Drive/Movie/a.mkv")
	if err != nil {
		t.Fatalf("serverDownloadLocalPath returned error: %v", err)
	}

	want := filepath.Join(base, "Drive", "Movie", "a.mkv")
	if got != want {
		t.Fatalf("serverDownloadLocalPath() = %q, want %q", got, want)
	}
}

func TestServerDownloadLocalPathRejectsRoot(t *testing.T) {
	_, err := ServerDownloadLocalPath(t.TempDir(), "/")
	if err == nil {
		t.Fatal("serverDownloadLocalPath should reject root path")
	}
}

func TestServerDownloadLocalPathStaysWithinBase(t *testing.T) {
	base := t.TempDir()

	got, err := ServerDownloadLocalPath(base, "/../escape.txt")
	if err != nil {
		t.Fatalf("serverDownloadLocalPath returned error: %v", err)
	}

	want := filepath.Join(base, "escape.txt")
	if got != want {
		t.Fatalf("serverDownloadLocalPath() = %q, want %q", got, want)
	}
}
