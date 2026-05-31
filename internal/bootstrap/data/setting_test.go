package data

import (
	"path/filepath"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
)

func TestDefaultServerDownloadDir(t *testing.T) {
	got := defaultServerDownloadDir()
	if got == "" {
		t.Fatal("expected non-empty default server download dir")
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("expected absolute path, got %q", got)
	}
	if filepath.Base(got) != "OpenList" {
		t.Fatalf("expected OpenList leaf directory, got %q", got)
	}
	if filepath.Base(filepath.Dir(got)) != "Downloads" {
		t.Fatalf("expected Downloads parent directory, got %q", filepath.Base(filepath.Dir(got)))
	}
}

func TestInitialSettingsUsesDefaultServerDownloadDir(t *testing.T) {
	oldConf := conf.Conf
	conf.Conf = conf.DefaultConfig(t.TempDir())
	defer func() {
		conf.Conf = oldConf
	}()

	settings := InitialSettings()
	for _, item := range settings {
		if item.Key == conf.ServerDownloadDir {
			if item.Value == "" {
				t.Fatal("expected server_download_dir default value to be populated")
			}
			return
		}
	}
	t.Fatal("server_download_dir setting not found")
}
