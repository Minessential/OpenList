package model

import "testing"

func TestCanAddServerDownloadTasks(t *testing.T) {
	const serverDownloadBit int32 = 1 << 16

	if CanAddServerDownloadTasks(0) {
		t.Fatal("expected false when permission bit is unset")
	}
	if !CanAddServerDownloadTasks(serverDownloadBit) {
		t.Fatal("expected true when permission bit is set")
	}

	u := &User{Permission: serverDownloadBit}
	if !u.CanAddServerDownloadTasks() {
		t.Fatal("expected method receiver to honor server download permission bit")
	}
}

func TestServerDownloadPermissionDoesNotReuseOfflineDownloadBit(t *testing.T) {
	const offlineDownloadBit int32 = 1 << 2

	if CanAddServerDownloadTasks(offlineDownloadBit) {
		t.Fatal("server download permission must not reuse offline download permission bit")
	}
}
