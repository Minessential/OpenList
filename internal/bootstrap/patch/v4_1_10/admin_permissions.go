package v4_1_10

import (
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
)

func GrantAdminServerDownloadPermission() {
	admin, err := op.GetAdmin()
	if err != nil {
		utils.Log.Errorf("Cannot grant server download permission to admin: %v", err)
		return
	}
	if admin.Permission&(1<<16) != 0 {
		return
	}
	admin.Permission |= 1 << 16
	if err := op.UpdateUser(admin); err != nil {
		utils.Log.Errorf("Cannot update admin server download permission: %v", err)
	}
}
