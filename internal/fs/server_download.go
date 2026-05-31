package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/stream"
	"github.com/OpenListTeam/OpenList/v4/internal/task"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/OpenListTeam/tache"
	"github.com/pkg/errors"
)

type ServerDownloadTask struct {
	task.TaskExtension
	SrcStorage      driver.Driver `json:"-"`
	SrcPath         string        `json:"src_path"`
	SrcStorageMp    string        `json:"src_storage_mp"`
	SrcActualPath   string        `json:"src_actual_path"`
	DstLocalPath    string        `json:"dst_local_path"`
	Status          string        `json:"-"`
	DownloadedBytes int64         `json:"downloaded_bytes"`
	FailedReason    string        `json:"failed_reason"`
}

func ServerDownload(ctx context.Context, srcObjPath, dstRoot string) (task.TaskExtensionInfo, error) {
	srcStorage, srcActualPath, err := op.GetStorageAndActualPath(srcObjPath)
	if err != nil {
		return nil, errors.WithMessage(err, "failed get src storage")
	}
	dstLocalPath, err := serverDownloadLocalPath(dstRoot, srcObjPath)
	if err != nil {
		return nil, err
	}
	taskCreator, _ := ctx.Value(conf.UserKey).(*model.User)
	t := &ServerDownloadTask{
		TaskExtension: task.TaskExtension{
			Creator: taskCreator,
			ApiUrl:  common.GetApiUrl(ctx),
		},
		SrcStorage:    srcStorage,
		SrcPath:       srcObjPath,
		SrcStorageMp:  srcStorage.GetStorage().MountPath,
		SrcActualPath: srcActualPath,
		DstLocalPath:  dstLocalPath,
	}
	ServerDownloadTaskManager.Add(t)
	return t, nil
}

func ServerDownloadLocalPath(dstRoot, srcObjPath string) (string, error) {
	return serverDownloadLocalPath(dstRoot, srcObjPath)
}

func serverDownloadLocalPath(dstRoot, srcObjPath string) (string, error) {
	if strings.TrimSpace(dstRoot) == "" {
		return "", fmt.Errorf("server download dir is not configured")
	}
	cleanSrc := utils.FixAndCleanPath(srcObjPath)
	if cleanSrc == "/" {
		return "", fmt.Errorf("server download does not support root path")
	}
	rootAbs, err := filepath.Abs(filepath.Clean(dstRoot))
	if err != nil {
		return "", errors.WithMessage(err, "failed to resolve server download dir")
	}
	relSrc := strings.TrimPrefix(cleanSrc, "/")
	targetAbs := filepath.Join(rootAbs, filepath.FromSlash(relSrc))
	relTarget, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return "", errors.WithMessage(err, "failed to validate server download path")
	}
	if relTarget == ".." || strings.HasPrefix(relTarget, ".."+string(filepath.Separator)) || filepath.IsAbs(relTarget) {
		return "", fmt.Errorf("illegal server download path: %s", srcObjPath)
	}
	return targetAbs, nil
}

func (t *ServerDownloadTask) GetName() string {
	return fmt.Sprintf("server download [%s](%s) to (%s)", t.SrcStorageMp, t.SrcActualPath, t.DstLocalPath)
}

func (t *ServerDownloadTask) GetStatus() string {
	return t.Status
}

func (t *ServerDownloadTask) Run() error {
	if t.SrcStorage == nil {
		storage, _, err := op.GetStorageAndActualPath(t.SrcStorageMp)
		if err != nil {
			return errors.WithMessage(err, "failed get src storage")
		}
		t.SrcStorage = storage
	}

	t.ClearEndTime()
	t.SetStartTime(time.Now())
	t.DownloadedBytes = 0
	t.FailedReason = ""
	defer func() { t.SetEndTime(time.Now()) }()

	t.Status = "checking target file"
	if _, err := os.Stat(t.DstLocalPath); err == nil {
		t.Status = "target file exists, skipped"
		t.DownloadedBytes = t.GetTotalBytes()
		t.SetProgress(100)
		return nil
	} else if !os.IsNotExist(err) {
		t.FailedReason = err.Error()
		return errors.WithMessage(err, "failed check target file")
	}

	t.Status = "getting src object"
	srcObj, err := op.Get(t.Ctx(), t.SrcStorage, t.SrcActualPath)
	if err != nil {
		t.FailedReason = err.Error()
		return errors.WithMessagef(err, "failed get src [%s] file", t.SrcActualPath)
	}
	if srcObj.IsDir() {
		t.FailedReason = "server download does not support folders"
		return fmt.Errorf("server download does not support folders")
	}
	t.SetTotalBytes(srcObj.GetSize())

	t.Status = "getting src object link"
	link, srcObj, err := op.Link(t.Ctx(), t.SrcStorage, t.SrcActualPath, model.LinkArgs{})
	if err != nil {
		t.FailedReason = err.Error()
		return errors.WithMessagef(err, "failed get [%s] link", t.SrcActualPath)
	}
	defer link.Close()

	ss, err := stream.NewSeekableStream(&stream.FileStream{
		Obj: srcObj,
		Ctx: t.Ctx(),
	}, link)
	if err != nil {
		t.FailedReason = err.Error()
		return errors.WithMessagef(err, "failed get [%s] stream", t.SrcActualPath)
	}
	defer ss.Close()

	if err := os.MkdirAll(filepath.Dir(t.DstLocalPath), 0o700); err != nil {
		t.FailedReason = err.Error()
		return errors.WithMessage(err, "failed create target directory")
	}
	file, err := os.OpenFile(t.DstLocalPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			t.Status = "target file exists, skipped"
			t.DownloadedBytes = t.GetTotalBytes()
			t.SetProgress(100)
			return nil
		}
		t.FailedReason = err.Error()
		return errors.WithMessage(err, "failed create target file")
	}
	defer file.Close()

	progressFn := func(percentage float64) {
		t.SetProgress(percentage)
		t.DownloadedBytes = int64(float64(t.GetTotalBytes()) * percentage / 100)
	}

	t.Status = "downloading to server"
	if err := utils.CopyWithCtx(t.Ctx(), file, ss, ss.GetSize(), progressFn); err != nil {
		t.FailedReason = err.Error()
		_ = os.Remove(t.DstLocalPath)
		return errors.WithMessage(err, "failed download file to server")
	}
	t.Status = "downloaded to server"
	t.DownloadedBytes = t.GetTotalBytes()
	t.SetProgress(100)
	return nil
}

var ServerDownloadTaskManager *tache.Manager[*ServerDownloadTask]

var _ task.TaskExtensionInfo = (*ServerDownloadTask)(nil)
