package handles

import (
	"fmt"
	"os"
	stdpath "path"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/fs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/setting"
	"github.com/OpenListTeam/OpenList/v4/internal/task"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/OpenListTeam/tache"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

type ServerDownloadReq struct {
	SrcDir string   `json:"src_dir"`
	Names  []string `json:"names"`
}

func isActiveServerDownloadState(state tache.State) bool {
	return state != tache.StateSucceeded && state != tache.StateCanceled && state != tache.StateFailed
}

func serverDownloadTaskExists(dstLocalPath string) bool {
	for _, t := range fs.ServerDownloadTaskManager.GetAll() {
		if t.DstLocalPath == dstLocalPath && (isActiveServerDownloadState(t.GetState()) || t.Paused) {
			return true
		}
	}
	return false
}

func FsServerDownload(c *gin.Context) {
	var req ServerDownloadReq
	if err := c.ShouldBind(&req); err != nil {
		common.ErrorResp(c, err, 400)
		return
	}
	if len(req.Names) == 0 {
		common.ErrorStrResp(c, "Empty file names", 400)
		return
	}
	dstRoot := setting.GetStr(conf.ServerDownloadDir)
	if strings.TrimSpace(dstRoot) == "" {
		common.ErrorStrResp(c, "server download dir is not configured", 400)
		return
	}
	user := c.Request.Context().Value(conf.UserKey).(*model.User)
	if user.IsGuest() {
		common.ErrorResp(c, errs.PermissionDenied, 403)
		return
	}
	if !user.CanAddServerDownloadTasks() {
		common.ErrorResp(c, errs.PermissionDenied, 403)
		return
	}
	srcDir, err := user.JoinPath(req.SrcDir)
	if err != nil {
		common.ErrorResp(c, err, 403)
		return
	}
	srcMeta, err := op.GetNearestMeta(srcDir)
	if err != nil && !errors.Is(errors.Cause(err), errs.MetaNotFound) {
		common.ErrorResp(c, err, 500, true)
		return
	}
	if !common.CanRead(user, srcMeta, srcDir) {
		common.ErrorResp(c, errs.PermissionDenied, 403)
		return
	}

	if !strings.HasSuffix(srcDir, "/") {
		srcDir += "/"
	}
	srcPaths := make([]string, 0, len(req.Names))
	for _, name := range req.Names {
		srcPath := stdpath.Join(srcDir, name)
		if !strings.HasPrefix(srcPath+"/", srcDir) {
			continue
		}
		base := stdpath.Base(srcPath)
		if base == "." || base == "/" {
			common.ErrorStrResp(c, fmt.Sprintf("invalid file name [%s]", name), 400)
			return
		}
		obj, err := fs.Get(c.Request.Context(), srcPath, &fs.GetArgs{NoLog: true})
		if err != nil {
			common.ErrorResp(c, err, 500)
			return
		}
		if obj.IsDir() {
			common.ErrorStrResp(c, fmt.Sprintf("server download does not support folder [%s]", name), 400)
			return
		}
		dstLocalPath, err := fs.ServerDownloadLocalPath(dstRoot, srcPath)
		if err != nil {
			common.ErrorResp(c, err, 500)
			return
		}
		if _, err := os.Stat(dstLocalPath); err == nil {
			common.ErrorStrResp(c, fmt.Sprintf("server download target already exists [%s]", name), 409)
			return
		} else if !os.IsNotExist(err) {
			common.ErrorResp(c, err, 500)
			return
		}
		partialLocalPath := dstLocalPath + ".openlist.part"
		if partialInfo, err := os.Stat(partialLocalPath); err == nil {
			if partialInfo.Size() > obj.GetSize() {
				common.ErrorStrResp(c, fmt.Sprintf("server download partial is larger than source [%s]", name), 409)
				return
			}
		} else if !os.IsNotExist(err) {
			common.ErrorResp(c, err, 500)
			return
		}
		if serverDownloadTaskExists(dstLocalPath) {
			common.ErrorStrResp(c, fmt.Sprintf("server download task already exists [%s]", name), 409)
			return
		}
		srcPaths = append(srcPaths, srcPath)
	}
	if len(srcPaths) == 0 {
		common.ErrorStrResp(c, "No valid file names", 400)
		return
	}

	var addedTasks []task.TaskExtensionInfo
	for _, srcPath := range srcPaths {
		t, err := fs.ServerDownload(c.Request.Context(), srcPath, dstRoot)
		if t != nil {
			addedTasks = append(addedTasks, t)
		}
		if err != nil {
			common.ErrorResp(c, err, 500)
			return
		}
	}
	taskInfos := getTaskInfos(addedTasks)
	taskIDs := make([]string, 0, len(taskInfos))
	for _, taskInfo := range taskInfos {
		taskIDs = append(taskIDs, taskInfo.ID)
	}
	common.SuccessWithMsgResp(c, fmt.Sprintf("Successfully created %d server download task(s)", len(addedTasks)), gin.H{
		"task_ids": taskIDs,
		"tasks":    taskInfos,
	})
}
