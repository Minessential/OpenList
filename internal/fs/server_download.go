package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/setting"
	"github.com/OpenListTeam/OpenList/v4/internal/stream"
	"github.com/OpenListTeam/OpenList/v4/internal/task"
	"github.com/OpenListTeam/OpenList/v4/pkg/http_range"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/OpenListTeam/tache"
	"github.com/pkg/errors"
)

type ServerDownloadTask struct {
	task.TaskExtension
	SrcStorage       driver.Driver `json:"-"`
	SrcPath          string        `json:"src_path"`
	SrcStorageMp     string        `json:"src_storage_mp"`
	SrcActualPath    string        `json:"src_actual_path"`
	DstLocalPath     string        `json:"dst_local_path"`
	PartialLocalPath string        `json:"partial_local_path"`
	Paused           bool          `json:"paused"`
	ResumeOffset     int64         `json:"resume_offset"`
	SrcSize          int64         `json:"src_size"`
	SrcModTime       int64         `json:"src_mod_time"`
	SrcHash          string        `json:"src_hash"`
	Status           string        `json:"-"`
	DownloadedBytes  int64         `json:"downloaded_bytes"`
	FailedReason     string        `json:"failed_reason"`
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
		SrcStorage:       srcStorage,
		SrcPath:          srcObjPath,
		SrcStorageMp:     srcStorage.GetStorage().MountPath,
		SrcActualPath:    srcActualPath,
		DstLocalPath:     dstLocalPath,
		PartialLocalPath: serverDownloadPartialPath(dstLocalPath),
	}
	t.setCurrentMaxRetry()
	ServerDownloadTaskManager.Add(t)
	return t, nil
}

func (t *ServerDownloadTask) setCurrentMaxRetry() {
	_, maxRetry := t.GetRetry()
	if maxRetry != 0 {
		return
	}
	t.SetRetry(0, setting.GetInt(conf.ServerDownloadTaskMaxRetry, conf.Conf.Tasks.Download.MaxRetry))
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

func serverDownloadPartialPath(dstLocalPath string) string {
	return dstLocalPath + ".openlist.part"
}

func (t *ServerDownloadTask) ensurePartialLocalPath() {
	if t.PartialLocalPath == "" && t.DstLocalPath != "" {
		t.PartialLocalPath = serverDownloadPartialPath(t.DstLocalPath)
	}
}

func (t *ServerDownloadTask) RefreshResumeOffset() int64 {
	t.ensurePartialLocalPath()
	if t.PartialLocalPath == "" {
		t.ResumeOffset = 0
		return t.ResumeOffset
	}
	info, err := os.Stat(t.PartialLocalPath)
	if err != nil {
		t.ResumeOffset = 0
		return t.ResumeOffset
	}
	t.ResumeOffset = info.Size()
	return t.ResumeOffset
}

func (t *ServerDownloadTask) IsResumable() bool {
	if t.GetState() == tache.StateSucceeded {
		return false
	}
	if _, err := os.Stat(t.DstLocalPath); err == nil {
		return false
	}
	return t.RefreshResumeOffset() > 0 || t.Paused || t.GetState() == tache.StateFailed || t.GetState() == tache.StateCanceled
}

func serverDownloadSrcHash(obj model.Obj) string {
	hash := obj.GetHash().String()
	if hash == "null" || hash == "{}" {
		return ""
	}
	return hash
}

func (t *ServerDownloadTask) validateAndStoreSource(obj model.Obj) error {
	size := obj.GetSize()
	modTime := int64(0)
	if !obj.ModTime().IsZero() {
		modTime = obj.ModTime().UnixNano()
	}
	hash := serverDownloadSrcHash(obj)

	if t.SrcSize > 0 && t.SrcSize != size {
		return fmt.Errorf("source file size changed: was %d, now %d", t.SrcSize, size)
	}
	if t.SrcModTime > 0 && modTime > 0 && t.SrcModTime != modTime {
		return fmt.Errorf("source file modified time changed")
	}
	if t.SrcHash != "" && hash != "" && t.SrcHash != hash {
		return fmt.Errorf("source file hash changed")
	}

	t.SrcSize = size
	if modTime > 0 {
		t.SrcModTime = modTime
	}
	if hash != "" {
		t.SrcHash = hash
	}
	t.Persist()
	return nil
}

func (t *ServerDownloadTask) setDownloadedBytes(downloaded int64) {
	total := t.GetTotalBytes()
	if downloaded < 0 {
		downloaded = 0
	}
	if total > 0 && downloaded > total {
		downloaded = total
	}
	t.DownloadedBytes = downloaded
	t.ResumeOffset = downloaded
	if total <= 0 {
		t.SetProgress(0)
		return
	}
	t.SetProgress(float64(downloaded) * 100 / float64(total))
}

func (t *ServerDownloadTask) normalizeRecoveredState() {
	t.ensurePartialLocalPath()
	if t.Paused {
		t.SetState(tache.StateCanceled)
		return
	}
	if t.GetState() == tache.StateCanceling {
		t.SetState(tache.StatePending)
	}
}

func (t *ServerDownloadTask) UnmarshalJSON(data []byte) error {
	type alias ServerDownloadTask
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*t = ServerDownloadTask(a)
	t.normalizeRecoveredState()
	return nil
}

func (t *ServerDownloadTask) GetName() string {
	return fmt.Sprintf("server download [%s](%s) to (%s)", t.SrcStorageMp, t.SrcActualPath, t.DstLocalPath)
}

func (t *ServerDownloadTask) GetStatus() string {
	return t.Status
}

func (t *ServerDownloadTask) Run() error {
	t.ensurePartialLocalPath()
	t.Paused = false
	if t.SrcStorage == nil {
		storage, _, err := op.GetStorageAndActualPath(t.SrcStorageMp)
		if err != nil {
			return errors.WithMessage(err, "failed get src storage")
		}
		t.SrcStorage = storage
	}

	t.ClearEndTime()
	t.SetStartTime(time.Now())
	t.FailedReason = ""
	defer func() { t.SetEndTime(time.Now()) }()

	t.Status = "checking target file"
	if _, err := os.Stat(t.DstLocalPath); err == nil {
		err := fmt.Errorf("target file already exists: %s", t.DstLocalPath)
		t.FailedReason = err.Error()
		return err
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
	if err := t.validateAndStoreSource(srcObj); err != nil {
		t.FailedReason = err.Error()
		return err
	}

	t.Status = "checking partial file"
	offset := t.RefreshResumeOffset()
	if offset > srcObj.GetSize() {
		err := fmt.Errorf("partial file is larger than source file: partial=%d source=%d", offset, srcObj.GetSize())
		t.FailedReason = err.Error()
		return err
	}
	t.setDownloadedBytes(offset)

	if err := os.MkdirAll(filepath.Dir(t.DstLocalPath), 0o700); err != nil {
		t.FailedReason = err.Error()
		return errors.WithMessage(err, "failed create target directory")
	}

	remaining := t.GetTotalBytes() - offset
	progressFn := func(percentage float64) {
		copied := int64(float64(remaining) * percentage / 100)
		t.setDownloadedBytes(offset + copied)
	}

	if remaining > 0 {
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

		file, err := os.OpenFile(t.PartialLocalPath, os.O_WRONLY|os.O_CREATE, 0o600)
		if err != nil {
			t.FailedReason = err.Error()
			return errors.WithMessage(err, "failed open partial file")
		}
		if _, err := file.Seek(offset, 0); err != nil {
			_ = file.Close()
			t.FailedReason = err.Error()
			return errors.WithMessage(err, "failed seek partial file")
		}

		t.Status = "downloading to server"
		reader, err := ss.RangeRead(http_range.Range{Start: offset, Length: remaining})
		if err != nil {
			_ = file.Close()
			t.FailedReason = err.Error()
			return errors.WithMessage(err, "failed create ranged source reader")
		}
		if err := utils.CopyWithCtx(t.Ctx(), file, reader, remaining, progressFn); err != nil {
			_ = file.Close()
			t.FailedReason = err.Error()
			return errors.WithMessage(err, "failed download file to server")
		}
		if err := file.Close(); err != nil {
			t.FailedReason = err.Error()
			return errors.WithMessage(err, "failed close partial file")
		}
	} else if t.GetTotalBytes() == 0 {
		file, err := os.OpenFile(t.PartialLocalPath, os.O_WRONLY|os.O_CREATE, 0o600)
		if err != nil {
			t.FailedReason = err.Error()
			return errors.WithMessage(err, "failed create empty partial file")
		}
		if err := file.Close(); err != nil {
			t.FailedReason = err.Error()
			return errors.WithMessage(err, "failed close empty partial file")
		}
	}
	partialInfo, err := os.Stat(t.PartialLocalPath)
	if err != nil {
		t.FailedReason = err.Error()
		return errors.WithMessage(err, "failed stat partial file")
	}
	if partialInfo.Size() != t.GetTotalBytes() {
		err := fmt.Errorf("partial file size mismatch: partial=%d source=%d", partialInfo.Size(), t.GetTotalBytes())
		t.FailedReason = err.Error()
		return err
	}
	if _, err := os.Stat(t.DstLocalPath); err == nil {
		t.FailedReason = "target file already exists"
		return fmt.Errorf("target file already exists: %s", t.DstLocalPath)
	} else if !os.IsNotExist(err) {
		t.FailedReason = err.Error()
		return errors.WithMessage(err, "failed check target file before rename")
	}
	if err := os.Rename(t.PartialLocalPath, t.DstLocalPath); err != nil {
		t.FailedReason = err.Error()
		return errors.WithMessage(err, "failed rename partial file")
	}
	t.Status = "downloaded to server"
	t.setDownloadedBytes(t.GetTotalBytes())
	return nil
}

var ServerDownloadTaskManager *tache.Manager[*ServerDownloadTask]

var _ task.TaskExtensionInfo = (*ServerDownloadTask)(nil)
