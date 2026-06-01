package handles

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/fs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/task"
	"github.com/OpenListTeam/tache"
)

func TestGetTaskInfoIncludesServerDownloadFields(t *testing.T) {
	start := time.Now()
	serverTask := &fs.ServerDownloadTask{
		TaskExtension: task.TaskExtension{
			Creator: &model.User{Username: "alice", Role: model.GENERAL},
		},
		SrcPath:          "/movies/a.mkv",
		SrcStorageMp:     "/115",
		DstLocalPath:     "C:\\Downloads\\OpenList\\movies\\a.mkv",
		PartialLocalPath: "C:\\Downloads\\OpenList\\movies\\a.mkv.openlist.part",
		Status:           "downloading to server",
		DownloadedBytes:  512,
	}
	serverTask.SetStartTime(start)
	serverTask.SetTotalBytes(1024)
	serverTask.SetProgress(50)
	serverTask.SetState(tache.StateRunning)

	info := getTaskInfo(serverTask)
	if info.Type != "server_download" {
		t.Fatalf("expected type server_download, got %q", info.Type)
	}
	if info.StateText == "" {
		t.Fatal("expected state_text to be populated")
	}
	if info.DownloadedBytes != 512 {
		t.Fatalf("expected downloaded bytes 512, got %d", info.DownloadedBytes)
	}
	if info.SrcPath != "/movies/a.mkv" {
		t.Fatalf("unexpected src path %q", info.SrcPath)
	}
	if info.DstLocalPath != "C:\\Downloads\\OpenList\\movies\\a.mkv" {
		t.Fatalf("unexpected dst path %q", info.DstLocalPath)
	}
	if info.PartialLocalPath != "C:\\Downloads\\OpenList\\movies\\a.mkv.openlist.part" {
		t.Fatalf("unexpected partial path %q", info.PartialLocalPath)
	}
}

func TestGetTaskInfoBackfillsServerDownloadSrcPath(t *testing.T) {
	serverTask := &fs.ServerDownloadTask{
		SrcStorageMp:  "/movies",
		SrcActualPath: "/anime/a.mkv",
	}

	info := getTaskInfo(serverTask)
	if info.SrcPath != "/movies/anime/a.mkv" {
		t.Fatalf("expected backfilled src path, got %q", info.SrcPath)
	}
}

func TestShouldDeleteServerDownloadFile(t *testing.T) {
	serverTask := &fs.ServerDownloadTask{}
	serverTask.SetState(tache.StateRunning)
	if !shouldDeleteServerDownloadFile(serverTask, false) {
		t.Fatal("running task should delete unfinished local file")
	}

	serverTask.SetState(tache.StateSucceeded)
	if shouldDeleteServerDownloadFile(serverTask, false) {
		t.Fatal("succeeded task should keep local file unless requested")
	}
	if !shouldDeleteServerDownloadFile(serverTask, true) {
		t.Fatal("delete_files should delete succeeded task file")
	}
}

func TestDeleteServerDownloadLocalFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.bin")
	partial := target + ".openlist.part"
	if err := os.WriteFile(target, []byte("done"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partial, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	serverTask := &fs.ServerDownloadTask{DstLocalPath: target}

	if err := deleteServerDownloadLocalFile(serverTask); err != nil {
		t.Fatalf("deleteServerDownloadLocalFile returned error: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("expected file to be removed, got err=%v", err)
	}
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Fatalf("expected partial file to be removed, got err=%v", err)
	}
	if err := deleteServerDownloadLocalFile(serverTask); err != nil {
		t.Fatalf("delete missing local file should be ignored: %v", err)
	}
}

func TestDeleteServerDownloadLocalFilesKeepsSucceededFinalByDefault(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.bin")
	partial := target + ".openlist.part"
	if err := os.WriteFile(target, []byte("done"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partial, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	serverTask := &fs.ServerDownloadTask{DstLocalPath: target}
	serverTask.SetState(tache.StateSucceeded)

	if err := deleteServerDownloadLocalFiles(serverTask, shouldDeleteServerDownloadFile(serverTask, false)); err != nil {
		t.Fatalf("deleteServerDownloadLocalFiles returned error: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("expected final file to remain, got err=%v", err)
	}
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Fatalf("expected partial file to be removed, got err=%v", err)
	}
}

func TestGetTaskInfoMarksPausedServerDownload(t *testing.T) {
	serverTask := &fs.ServerDownloadTask{
		DstLocalPath: "/tmp/a.bin",
		Paused:       true,
	}
	serverTask.SetState(tache.StateCanceled)

	info := getTaskInfo(serverTask)
	if !info.Paused {
		t.Fatal("expected paused=true")
	}
	if info.StateText != "paused" {
		t.Fatalf("expected state_text paused, got %q", info.StateText)
	}
	if !info.Resumable {
		t.Fatal("expected paused task to be resumable")
	}
}

func TestIsActiveServerDownloadState(t *testing.T) {
	if !isActiveServerDownloadState(tache.StateRunning) {
		t.Fatal("running task should be active")
	}
	if isActiveServerDownloadState(tache.StateSucceeded) {
		t.Fatal("succeeded task should not be active")
	}
	if isActiveServerDownloadState(tache.StateCanceled) {
		t.Fatal("canceled task should not be active")
	}
	if isActiveServerDownloadState(tache.StateFailed) {
		t.Fatal("failed task should not be active")
	}
}

func TestPausedServerDownloadTaskCountsAsExisting(t *testing.T) {
	oldManager := fs.ServerDownloadTaskManager
	oldConf := conf.Conf
	defer func() { fs.ServerDownloadTaskManager = oldManager }()
	defer func() { conf.Conf = oldConf }()
	conf.Conf = conf.DefaultConfig(t.TempDir())
	fs.ServerDownloadTaskManager = tache.NewManager[*fs.ServerDownloadTask](tache.WithWorks(0))

	target := filepath.Join(t.TempDir(), "target.bin")
	serverTask := &fs.ServerDownloadTask{DstLocalPath: target, Paused: true}
	serverTask.SetState(tache.StateCanceled)
	fs.ServerDownloadTaskManager.Add(serverTask)

	if !serverDownloadTaskExists(target) {
		t.Fatal("paused server download task should block duplicate target")
	}
}
