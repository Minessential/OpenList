package v4_1_10

import (
	"strings"

	"github.com/OpenListTeam/OpenList/v4/internal/bootstrap/data"
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

func FillDefaultServerDownloadDir() {
	item, err := op.GetSettingItemByKey(conf.ServerDownloadDir)
	if err != nil {
		utils.Log.Errorf("Cannot load %s: %v", conf.ServerDownloadDir, err)
		return
	}
	if strings.TrimSpace(item.Value) != "" {
		return
	}
	item.Value = data.DefaultServerDownloadDirForPatch()
	if item.Value == "" {
		return
	}
	if err := op.SaveSettingItem(item); err != nil {
		utils.Log.Errorf("Cannot backfill %s: %v", conf.ServerDownloadDir, err)
	}
}
