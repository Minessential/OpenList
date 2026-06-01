package handles

import (
	"context"
	"math"
	"os"
	stdpath "path"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/task"

	"github.com/OpenListTeam/OpenList/v4/internal/fs"
	"github.com/OpenListTeam/OpenList/v4/internal/offline_download/tool"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/OpenListTeam/tache"
	"github.com/gin-gonic/gin"
)

type TaskInfo struct {
	ID               string      `json:"id"`
	Type             string      `json:"type,omitempty"`
	Name             string      `json:"name"`
	Creator          string      `json:"creator"`
	CreatorRole      int         `json:"creator_role"`
	State            tache.State `json:"state"`
	StateText        string      `json:"state_text"`
	Status           string      `json:"status"`
	Progress         float64     `json:"progress"`
	StartTime        *time.Time  `json:"start_time"`
	EndTime          *time.Time  `json:"end_time"`
	TotalBytes       int64       `json:"total_bytes"`
	DownloadedBytes  int64       `json:"downloaded_bytes,omitempty"`
	SrcPath          string      `json:"src_path,omitempty"`
	SrcStorageMp     string      `json:"src_storage_mp,omitempty"`
	DstLocalPath     string      `json:"dst_local_path,omitempty"`
	PartialLocalPath string      `json:"partial_local_path,omitempty"`
	Paused           bool        `json:"paused,omitempty"`
	Resumable        bool        `json:"resumable,omitempty"`
	ResumeOffset     int64       `json:"resume_offset,omitempty"`
	Error            string      `json:"error"`
	FailedReason     string      `json:"failed_reason,omitempty"`
}

func stateText(state tache.State) string {
	switch state {
	case tache.StatePending:
		return "pending"
	case tache.StateRunning:
		return "running"
	case tache.StateSucceeded:
		return "succeeded"
	case tache.StateCanceling:
		return "canceling"
	case tache.StateCanceled:
		return "canceled"
	case tache.StateErrored:
		return "errored"
	case tache.StateFailing:
		return "failing"
	case tache.StateFailed:
		return "failed"
	case tache.StateWaitingRetry:
		return "waiting_retry"
	case tache.StateBeforeRetry:
		return "before_retry"
	default:
		return "unknown"
	}
}

func getTaskInfo[T task.TaskExtensionInfo](task T) TaskInfo {
	errMsg := ""
	if task.GetErr() != nil {
		errMsg = task.GetErr().Error()
	}
	progress := task.GetProgress()
	// if progress is NaN, set it to 100
	if math.IsNaN(progress) {
		progress = 100
	}
	creatorName := ""
	creatorRole := -1
	if task.GetCreator() != nil {
		creatorName = task.GetCreator().Username
		creatorRole = task.GetCreator().Role
	}
	info := TaskInfo{
		ID:          task.GetID(),
		Name:        task.GetName(),
		Creator:     creatorName,
		CreatorRole: creatorRole,
		State:       task.GetState(),
		StateText:   stateText(task.GetState()),
		Status:      task.GetStatus(),
		Progress:    progress,
		StartTime:   task.GetStartTime(),
		EndTime:     task.GetEndTime(),
		TotalBytes:  task.GetTotalBytes(),
		Error:       errMsg,
	}
	if st, ok := any(task).(*fs.ServerDownloadTask); ok {
		info.Type = "server_download"
		info.DownloadedBytes = st.DownloadedBytes
		info.SrcPath = st.SrcPath
		if info.SrcPath == "" {
			info.SrcPath = stdpath.Join(st.SrcStorageMp, st.SrcActualPath)
		}
		info.SrcStorageMp = st.SrcStorageMp
		info.DstLocalPath = st.DstLocalPath
		info.Paused = st.Paused
		info.Resumable = st.IsResumable()
		info.PartialLocalPath = st.PartialLocalPath
		info.ResumeOffset = st.ResumeOffset
		info.FailedReason = st.FailedReason
		if st.Paused {
			info.StateText = "paused"
		}
	}
	return info
}

func getTaskInfos[T task.TaskExtensionInfo](tasks []T) []TaskInfo {
	return utils.MustSliceConvert(tasks, getTaskInfo[T])
}

func argsContains[T comparable](v T, slice ...T) bool {
	return utils.SliceContains(slice, v)
}

func isPausedServerDownloadTask(task any) bool {
	st, ok := task.(*fs.ServerDownloadTask)
	return ok && st.Paused
}

func getUserInfo(c *gin.Context) (bool, uint, bool) {
	if user, ok := c.Request.Context().Value(conf.UserKey).(*model.User); ok {
		return user.IsAdmin(), user.ID, true
	} else {
		return false, 0, false
	}
}

func getTargetedHandler[T task.TaskExtensionInfo](manager task.Manager[T], callback func(c *gin.Context, task T)) gin.HandlerFunc {
	return func(c *gin.Context) {
		isAdmin, uid, ok := getUserInfo(c)
		if !ok {
			// if there is no bug, here is unreachable
			common.ErrorStrResp(c, "user invalid", 401)
			return
		}
		t, ok := manager.GetByID(c.Query("tid"))
		if !ok {
			common.ErrorStrResp(c, "task not found", 404)
			return
		}
		if !isAdmin && uid != t.GetCreator().ID {
			// to avoid an attacker using error messages to guess valid TID, return a 404 rather than a 403
			common.ErrorStrResp(c, "task not found", 404)
			return
		}
		callback(c, t)
	}
}

func getBatchHandler[T task.TaskExtensionInfo](manager task.Manager[T], callback func(task T)) gin.HandlerFunc {
	return func(c *gin.Context) {
		isAdmin, uid, ok := getUserInfo(c)
		if !ok {
			common.ErrorStrResp(c, "user invalid", 401)
			return
		}
		var tids []string
		if err := c.ShouldBind(&tids); err != nil {
			common.ErrorStrResp(c, "invalid request format", 400)
			return
		}
		retErrs := make(map[string]string)
		for _, tid := range tids {
			t, ok := manager.GetByID(tid)
			if !ok || (!isAdmin && uid != t.GetCreator().ID) {
				retErrs[tid] = "task not found"
				continue
			}
			callback(t)
		}
		common.SuccessResp(c, retErrs)
	}
}

func taskRoute[T task.TaskExtensionInfo](g *gin.RouterGroup, manager task.Manager[T]) {
	g.GET("/undone", func(c *gin.Context) {
		isAdmin, uid, ok := getUserInfo(c)
		if !ok {
			// if there is no bug, here is unreachable
			common.ErrorStrResp(c, "user invalid", 401)
			return
		}
		common.SuccessResp(c, getTaskInfos(manager.GetByCondition(func(task T) bool {
			// avoid directly passing the user object into the function to reduce closure size
			return (isAdmin || uid == task.GetCreator().ID) &&
				argsContains(task.GetState(), tache.StatePending, tache.StateRunning, tache.StateCanceling,
					tache.StateErrored, tache.StateFailing, tache.StateWaitingRetry, tache.StateBeforeRetry) ||
				((isAdmin || uid == task.GetCreator().ID) && isPausedServerDownloadTask(any(task)))
		})))
	})
	g.GET("/done", func(c *gin.Context) {
		isAdmin, uid, ok := getUserInfo(c)
		if !ok {
			// if there is no bug, here is unreachable
			common.ErrorStrResp(c, "user invalid", 401)
			return
		}
		common.SuccessResp(c, getTaskInfos(manager.GetByCondition(func(task T) bool {
			return (isAdmin || uid == task.GetCreator().ID) &&
				argsContains(task.GetState(), tache.StateCanceled, tache.StateFailed, tache.StateSucceeded) &&
				!isPausedServerDownloadTask(any(task))
		})))
	})
	g.POST("/info", getTargetedHandler(manager, func(c *gin.Context, task T) {
		common.SuccessResp(c, getTaskInfo(task))
	}))
	g.POST("/cancel", getTargetedHandler(manager, func(c *gin.Context, task T) {
		manager.Cancel(task.GetID())
		common.SuccessResp(c)
	}))
	g.POST("/delete", getTargetedHandler(manager, func(c *gin.Context, task T) {
		cleanupServerDownloadBeforeRemove(any(task), false)
		manager.Remove(task.GetID())
		common.SuccessResp(c)
	}))
	g.POST("/retry", getTargetedHandler(manager, func(c *gin.Context, task T) {
		manager.Retry(task.GetID())
		common.SuccessResp(c)
	}))
	g.POST("/cancel_some", getBatchHandler(manager, func(task T) {
		manager.Cancel(task.GetID())
	}))
	g.POST("/delete_some", getBatchHandler(manager, func(task T) {
		cleanupServerDownloadBeforeRemove(any(task), false)
		manager.Remove(task.GetID())
	}))
	g.POST("/retry_some", getBatchHandler(manager, func(task T) {
		manager.Retry(task.GetID())
	}))
	g.POST("/clear_done", func(c *gin.Context) {
		isAdmin, uid, ok := getUserInfo(c)
		if !ok {
			// if there is no bug, here is unreachable
			common.ErrorStrResp(c, "user invalid", 401)
			return
		}
		manager.RemoveByCondition(func(task T) bool {
			matched := (isAdmin || uid == task.GetCreator().ID) &&
				argsContains(task.GetState(), tache.StateCanceled, tache.StateFailed, tache.StateSucceeded) &&
				!isPausedServerDownloadTask(any(task))
			if matched {
				cleanupServerDownloadBeforeRemove(any(task), false)
			}
			return matched
		})
		common.SuccessResp(c)
	})
	g.POST("/clear_succeeded", func(c *gin.Context) {
		isAdmin, uid, ok := getUserInfo(c)
		if !ok {
			// if there is no bug, here is unreachable
			common.ErrorStrResp(c, "user invalid", 401)
			return
		}
		manager.RemoveByCondition(func(task T) bool {
			matched := (isAdmin || uid == task.GetCreator().ID) && task.GetState() == tache.StateSucceeded
			if matched {
				cleanupServerDownloadBeforeRemove(any(task), false)
			}
			return matched
		})
		common.SuccessResp(c)
	})
	g.POST("/retry_failed", func(c *gin.Context) {
		isAdmin, uid, ok := getUserInfo(c)
		if !ok {
			// if there is no bug, here is unreachable
			common.ErrorStrResp(c, "user invalid", 401)
			return
		}
		tasks := manager.GetByCondition(func(task T) bool {
			return (isAdmin || uid == task.GetCreator().ID) && task.GetState() == tache.StateFailed
		})
		for _, t := range tasks {
			manager.Retry(t.GetID())
		}
		common.SuccessResp(c)
	})
}

type serverDownloadDeleteReq struct {
	IDs         []string `json:"ids"`
	DeleteFiles bool     `json:"delete_files"`
}

func removeLocalFileWithRetry(path string) error {
	if path == "" {
		return nil
	}
	var lastErr error
	for i := 0; i < 20; i++ {
		err := os.Remove(path)
		if err == nil || os.IsNotExist(err) {
			return nil
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	return lastErr
}

func deleteServerDownloadLocalFiles(t *fs.ServerDownloadTask, deleteFinal bool) error {
	t.RefreshResumeOffset()
	if err := removeLocalFileWithRetry(t.PartialLocalPath); err != nil {
		return err
	}
	if deleteFinal {
		return removeLocalFileWithRetry(t.DstLocalPath)
	}
	return nil
}

func deleteServerDownloadLocalFile(t *fs.ServerDownloadTask) error {
	return deleteServerDownloadLocalFiles(t, true)
}

func isServerDownloadTaskDone(t *fs.ServerDownloadTask) bool {
	return argsContains(t.GetState(), tache.StateSucceeded, tache.StateCanceled, tache.StateFailed)
}

func cancelServerDownloadTaskAndWait(tid string, t *fs.ServerDownloadTask) {
	if isServerDownloadTaskDone(t) {
		return
	}
	fs.ServerDownloadTaskManager.Cancel(tid)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if isServerDownloadTaskDone(t) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func shouldDeleteServerDownloadFile(t *fs.ServerDownloadTask, deleteFiles bool) bool {
	if deleteFiles {
		return true
	}
	return t.GetState() != tache.StateSucceeded
}

func cleanupServerDownloadBeforeRemove(task any, deleteFiles bool) {
	st, ok := task.(*fs.ServerDownloadTask)
	if !ok {
		return
	}
	cancelServerDownloadTaskAndWait(st.GetID(), st)
	_ = deleteServerDownloadLocalFiles(st, shouldDeleteServerDownloadFile(st, deleteFiles))
}

func pauseServerDownloadTask(c *gin.Context) {
	getTargetedHandler(fs.ServerDownloadTaskManager, func(c *gin.Context, t *fs.ServerDownloadTask) {
		if t.GetState() == tache.StateSucceeded {
			common.ErrorStrResp(c, "succeeded task cannot be paused", 409)
			return
		}
		t.Paused = true
		t.Status = "paused"
		t.FailedReason = ""
		t.Persist()
		fs.ServerDownloadTaskManager.Cancel(t.GetID())
		common.SuccessResp(c, getTaskInfo(t))
	})(c)
}

func resumeServerDownloadTask(c *gin.Context) {
	getTargetedHandler(fs.ServerDownloadTaskManager, func(c *gin.Context, t *fs.ServerDownloadTask) {
		if t.GetState() == tache.StateSucceeded {
			common.ErrorStrResp(c, "succeeded task cannot be resumed", 409)
			return
		}
		if isActiveServerDownloadState(t.GetState()) && !t.Paused {
			common.ErrorStrResp(c, "task is already active", 409)
			return
		}
		if t.Paused && !isServerDownloadTaskDone(t) {
			cancelServerDownloadTaskAndWait(t.GetID(), t)
			if !isServerDownloadTaskDone(t) {
				common.ErrorStrResp(c, "task is still canceling", 409)
				return
			}
		}
		if !t.Paused && !t.IsResumable() {
			common.ErrorStrResp(c, "task is not resumable", 409)
			return
		}
		t.Paused = false
		t.FailedReason = ""
		t.Status = "queued for resume"
		ctx, cancel := context.WithCancel(context.Background())
		t.SetCtx(ctx)
		t.SetCancelFunc(cancel)
		t.SetErr(nil)
		t.Persist()
		fs.ServerDownloadTaskManager.Retry(t.GetID())
		common.SuccessResp(c, getTaskInfo(t))
	})(c)
}

func deleteServerDownloadTasks(c *gin.Context) {
	isAdmin, uid, ok := getUserInfo(c)
	if !ok {
		common.ErrorStrResp(c, "user invalid", 401)
		return
	}
	var req serverDownloadDeleteReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorStrResp(c, "invalid request format", 400)
		return
	}
	retErrs := make(map[string]string)
	for _, tid := range req.IDs {
		t, ok := fs.ServerDownloadTaskManager.GetByID(tid)
		if !ok || (!isAdmin && uid != t.GetCreator().ID) {
			retErrs[tid] = "task not found"
			continue
		}
		cancelServerDownloadTaskAndWait(tid, t)
		if err := deleteServerDownloadLocalFiles(t, shouldDeleteServerDownloadFile(t, req.DeleteFiles)); err != nil {
			retErrs[tid] = err.Error()
			continue
		}
		fs.ServerDownloadTaskManager.Remove(tid)
	}
	common.SuccessResp(c, retErrs)
}

func SetupTaskRoute(g *gin.RouterGroup) {
	taskRoute(g.Group("/upload"), fs.UploadTaskManager)
	taskRoute(g.Group("/copy"), fs.CopyTaskManager)
	taskRoute(g.Group("/move"), fs.MoveTaskManager)
	taskRoute(g.Group("/offline_download"), tool.DownloadTaskManager)
	taskRoute(g.Group("/server_download"), fs.ServerDownloadTaskManager)
	g.POST("/server_download/pause", pauseServerDownloadTask)
	g.POST("/server_download/resume", resumeServerDownloadTask)
	g.POST("/server_download/delete_with_files", deleteServerDownloadTasks)
	taskRoute(g.Group("/offline_download_transfer"), tool.TransferTaskManager)
	taskRoute(g.Group("/decompress"), fs.ArchiveDownloadTaskManager)
	taskRoute(g.Group("/decompress_upload"), fs.ArchiveContentUploadTaskManager)
}
