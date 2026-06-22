package fs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/tache"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
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

func TestServerDownloadPartialPath(t *testing.T) {
	target := filepath.Join(t.TempDir(), "movie.mkv")
	if got := serverDownloadPartialPath(target); got != target+".openlist.part" {
		t.Fatalf("serverDownloadPartialPath() = %q", got)
	}
}

func TestServerDownloadRefreshResumeOffset(t *testing.T) {
	target := filepath.Join(t.TempDir(), "movie.mkv")
	partial := serverDownloadPartialPath(target)
	if err := os.WriteFile(partial, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}

	serverTask := &ServerDownloadTask{DstLocalPath: target}
	if got := serverTask.RefreshResumeOffset(); got != 7 {
		t.Fatalf("expected resume offset 7, got %d", got)
	}
	if serverTask.PartialLocalPath != partial {
		t.Fatalf("expected partial path %q, got %q", partial, serverTask.PartialLocalPath)
	}
}

func TestServerDownloadTaskUsesServerDownloadMaxRetrySetting(t *testing.T) {
	oldConf := conf.Conf
	conf.Conf = conf.DefaultConfig(t.TempDir())
	defer func() {
		conf.Conf = oldConf
	}()

	dB, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	db.Init(dB)
	defer func() {
		db.Close()
		op.SettingCacheUpdate()
	}()
	if err := op.SaveSettingItem(&model.SettingItem{
		Key:   conf.ServerDownloadTaskMaxRetry,
		Value: "3",
		Type:  conf.TypeNumber,
	}); err != nil {
		t.Fatal(err)
	}

	serverTask := &ServerDownloadTask{}
	serverTask.setCurrentMaxRetry()
	_, maxRetry := serverTask.GetRetry()
	if maxRetry != 3 {
		t.Fatalf("expected max retry 3, got %d", maxRetry)
	}
}

func TestServerDownloadValidateAndStoreSourceRejectsSizeChange(t *testing.T) {
	modTime := time.Unix(100, 0)
	serverTask := &ServerDownloadTask{}
	srcObj := &model.Object{Name: "a.bin", Size: 10, Modified: modTime}
	if err := serverTask.validateAndStoreSource(srcObj); err != nil {
		t.Fatalf("validateAndStoreSource returned error: %v", err)
	}

	changed := &model.Object{Name: "a.bin", Size: 11, Modified: modTime}
	if err := serverTask.validateAndStoreSource(changed); err == nil {
		t.Fatal("expected size change to be rejected")
	}
}

func TestServerDownloadValidateAndStoreSourceRejectsModTimeChange(t *testing.T) {
	serverTask := &ServerDownloadTask{}
	srcObj := &model.Object{Name: "a.bin", Size: 10, Modified: time.Unix(100, 0)}
	if err := serverTask.validateAndStoreSource(srcObj); err != nil {
		t.Fatalf("validateAndStoreSource returned error: %v", err)
	}

	changed := &model.Object{Name: "a.bin", Size: 10, Modified: time.Unix(101, 0)}
	if err := serverTask.validateAndStoreSource(changed); err == nil {
		t.Fatal("expected modified time change to be rejected")
	}
}

func TestServerDownloadUnmarshalKeepsPausedTaskPaused(t *testing.T) {
	payload := []byte(`{"id":"t1","state":1,"paused":true,"dst_local_path":"/tmp/a.bin"}`)
	var serverTask ServerDownloadTask
	if err := json.Unmarshal(payload, &serverTask); err != nil {
		t.Fatal(err)
	}
	if !serverTask.Paused {
		t.Fatal("expected paused flag to survive unmarshal")
	}
	if serverTask.GetState() != tache.StateCanceled {
		t.Fatalf("expected paused recovered task to be canceled, got %v", serverTask.GetState())
	}
	if serverTask.PartialLocalPath != "/tmp/a.bin.openlist.part" {
		t.Fatalf("unexpected partial path %q", serverTask.PartialLocalPath)
	}
}

func TestServerDownloadUnmarshalRestoresCancelingTaskToPending(t *testing.T) {
	payload := []byte(`{"id":"t1","state":3,"paused":false,"dst_local_path":"/tmp/a.bin"}`)
	var serverTask ServerDownloadTask
	if err := json.Unmarshal(payload, &serverTask); err != nil {
		t.Fatal(err)
	}
	if serverTask.GetState() != tache.StatePending {
		t.Fatalf("expected canceling task to recover as pending, got %v", serverTask.GetState())
	}
}

func TestServerDownloadIsResumableUsesPartial(t *testing.T) {
	target := filepath.Join(t.TempDir(), "movie.mkv")
	if err := os.WriteFile(serverDownloadPartialPath(target), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	serverTask := &ServerDownloadTask{
		DstLocalPath: target,
	}
	serverTask.SetState(tache.StateCanceled)
	if !serverTask.IsResumable() {
		t.Fatal("expected task with partial to be resumable")
	}
	if serverTask.ResumeOffset != 7 {
		t.Fatalf("expected resume offset 7, got %d", serverTask.ResumeOffset)
	}
}
