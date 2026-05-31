package handles

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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
		SrcPath:         "/movies/a.mkv",
		SrcStorageMp:    "/115",
		DstLocalPath:    "C:\\Downloads\\OpenList\\movies\\a.mkv",
		Status:          "downloading to server",
		DownloadedBytes: 512,
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
	target := filepath.Join(t.TempDir(), "partial.bin")
	if err := os.WriteFile(target, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	serverTask := &fs.ServerDownloadTask{DstLocalPath: target}

	if err := deleteServerDownloadLocalFile(serverTask); err != nil {
		t.Fatalf("deleteServerDownloadLocalFile returned error: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("expected file to be removed, got err=%v", err)
	}
	if err := deleteServerDownloadLocalFile(serverTask); err != nil {
		t.Fatalf("delete missing local file should be ignored: %v", err)
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
