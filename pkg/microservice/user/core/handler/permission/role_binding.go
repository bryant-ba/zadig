/*
Copyright 2023 The KodeRover Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package permission

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/koderover/zadig/v2/pkg/microservice/user/core/service/permission"
	"github.com/koderover/zadig/v2/pkg/microservice/user/core/service/user"
	"github.com/koderover/zadig/v2/pkg/setting"
	internalhandler "github.com/koderover/zadig/v2/pkg/shared/handler"
	e "github.com/koderover/zadig/v2/pkg/tool/errors"
	"github.com/koderover/zadig/v2/pkg/tool/log"
	"github.com/koderover/zadig/v2/pkg/types"
)

func ListRoleBindings(c *gin.Context) {
	ctx := internalhandler.NewContext(c)
	defer func() { internalhandler.JSONResponse(c, ctx) }()

	projectName := c.Query("namespace")
	if projectName == "" {
		ctx.Err = e.ErrInvalidParam.AddDesc("namespace is empty")
		return
	}

	uid := c.Query("uid")
	gid := c.Query("gid")
	if uid != "" && gid != "" {
		ctx.Err = e.ErrInvalidParam.AddDesc("cannot pass uid and gid together")
		return
	}

	ctx.Resp, ctx.Err = permission.ListRoleBindings(projectName, uid, gid, ctx.Logger)
}

type createRoleBindingReq struct {
	Identities []*types.Identity `json:"identities"`
	Role       string            `json:"role"`
}

func CreateRoleBinding(c *gin.Context) {
	ctx, err := internalhandler.NewContextWithAuthorization(c)
	defer func() { internalhandler.JSONResponse(c, ctx) }()

	if err != nil {
		ctx.Err = fmt.Errorf("authorization Info Generation failed: err %s", err)
		ctx.UnAuthorized = true
		return
	}

	data, err := c.GetRawData()
	if err != nil {
		log.Errorf("CreateRoleBinding c.GetRawData() err : %v", err)
		ctx.Err = e.ErrInvalidParam.AddErr(err)
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(data))

	projectName := c.Query("namespace")
	if projectName == "" {
		ctx.Err = e.ErrInvalidParam.AddDesc("namespace is empty")
		return
	}

	req := new(createRoleBindingReq)
	if err := c.ShouldBindJSON(&req); err != nil {
		ctx.Err = err
		return
	}

	detail := ""
	for _, arg := range req.Identities {
		if arg.IdentityType == "user" {
			userInfo, err := user.GetUser(arg.UID, ctx.Logger)
			if err != nil {
				ctx.Err = e.ErrInvalidParam.AddErr(err)
				return
			}
			username := ""
			if userInfo != nil {
				username = userInfo.Name
			}
			detail += "用户：" + username + "，"
		} else if arg.IdentityType == "group" {
			groupInfo, err := user.GetUserGroup(arg.GID, ctx.Logger)
			if err != nil {
				ctx.Err = e.ErrInvalidParam.AddErr(err)
				return
			}
			username := ""
			if groupInfo != nil {
				username = groupInfo.Name
			}
			detail += "用户组：" + username + "，"
		}
	}
	detail += "角色名称：" + req.Role + "\n"
	internalhandler.InsertDetailedOperationLog(c, ctx.UserName, projectName, setting.OperationSceneProject, "创建", "角色绑定", detail, string(data), ctx.Logger, "")

	if !ctx.Resources.IsSystemAdmin {
		if projectName == "*" {
			ctx.UnAuthorized = true
			return
		}

		if _, ok := ctx.Resources.ProjectAuthInfo[projectName]; !ok {
			ctx.UnAuthorized = true
			return
		}

		if !ctx.Resources.ProjectAuthInfo[projectName].IsProjectAdmin {
			ctx.UnAuthorized = true
			return
		}
	}

	ctx.Err = permission.CreateRoleBindings(req.Role, projectName, req.Identities, ctx.Logger)
}

type updateRoleBindingForUserReq struct {
	Roles []string `json:"roles"`
}

func UpdateRoleBindingForUser(c *gin.Context) {
	ctx, err := internalhandler.NewContextWithAuthorization(c)
	defer func() { internalhandler.JSONResponse(c, ctx) }()

	if err != nil {
		ctx.Err = fmt.Errorf("authorization Info Generation failed: err %s", err)
		ctx.UnAuthorized = true
		return
	}

	data, err := c.GetRawData()
	if err != nil {
		log.Errorf("CreateSystemRoleBinding c.GetRawData() err : %v", err)
		ctx.Err = e.ErrInvalidParam.AddErr(err)
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(data))

	projectName := c.Query("namespace")
	if projectName == "" {
		ctx.Err = e.ErrInvalidParam.AddDesc("namespace is empty")
		return
	}
	userID := c.Param("uid")
	if userID == "" {
		ctx.Err = e.ErrInvalidParam.AddDesc("uid is empty")
		return
	}
	args := new(updateRoleBindingForUserReq)
	if err := c.ShouldBindJSON(&args); err != nil {
		ctx.Err = err
		return
	}

	userInfo, err := user.GetUser(userID, ctx.Logger)
	if err != nil {
		ctx.Err = e.ErrInvalidParam.AddErr(err)
		return
	}

	username := ""
	if userInfo != nil {
		username = userInfo.Name
	}
	detail := "用户：" + username + "，角色名称："
	for _, arg := range args.Roles {
		detail += arg + "，"
	}
	detail = strings.Trim(detail, "，")

	internalhandler.InsertDetailedOperationLog(c, ctx.UserName, projectName, setting.OperationSceneProject, "更新", "角色绑定", detail, string(data), ctx.Logger, "")

	if !ctx.Resources.IsSystemAdmin {
		if projectName == "*" {
			ctx.UnAuthorized = true
			return
		}

		if _, ok := ctx.Resources.ProjectAuthInfo[projectName]; !ok {
			ctx.UnAuthorized = true
			return
		}

		if !ctx.Resources.ProjectAuthInfo[projectName].IsProjectAdmin {
			ctx.UnAuthorized = true
			return
		}
	}

	ctx.Err = permission.UpdateRoleBindingForUser(userID, projectName, args.Roles, ctx.Logger)
}

func DeleteRoleBindingForUser(c *gin.Context) {
	ctx, err := internalhandler.NewContextWithAuthorization(c)
	defer func() { internalhandler.JSONResponse(c, ctx) }()

	if err != nil {
		ctx.Err = fmt.Errorf("authorization Info Generation failed: err %s", err)
		ctx.UnAuthorized = true
		return
	}

	data, err := c.GetRawData()
	if err != nil {
		log.Errorf("CreateSystemRoleBinding c.GetRawData() err : %v", err)
		ctx.Err = e.ErrInvalidParam.AddErr(err)
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(data))

	projectName := c.Query("namespace")
	if projectName == "" {
		ctx.Err = e.ErrInvalidParam.AddDesc("namespace is empty")
		return
	}
	userID := c.Param("uid")
	if userID == "" {
		ctx.Err = e.ErrInvalidParam.AddDesc("uid is empty")
		return
	}

	userInfo, err := user.GetUser(userID, ctx.Logger)
	if err != nil {
		ctx.Err = e.ErrInvalidParam.AddErr(err)
		return
	}

	username := ""
	if userInfo != nil {
		username = userInfo.Name
	}
	detail := "用户：" + username

	internalhandler.InsertDetailedOperationLog(c, ctx.UserName, projectName, setting.OperationSceneProject, "删除", "角色绑定", detail, string(data), ctx.Logger, "")

	if !ctx.Resources.IsSystemAdmin {
		if projectName == "*" {
			ctx.UnAuthorized = true
			return
		}

		if _, ok := ctx.Resources.ProjectAuthInfo[projectName]; !ok {
			ctx.UnAuthorized = true
			return
		}

		if !ctx.Resources.ProjectAuthInfo[projectName].IsProjectAdmin {
			ctx.UnAuthorized = true
			return
		}
	}

	ctx.Err = permission.DeleteRoleBindingForUser(userID, projectName, ctx.Logger)
}

func UpdateRoleBindingForGroup(c *gin.Context) {
	ctx, err := internalhandler.NewContextWithAuthorization(c)
	defer func() { internalhandler.JSONResponse(c, ctx) }()

	if err != nil {
		ctx.Err = fmt.Errorf("authorization Info Generation failed: err %s", err)
		ctx.UnAuthorized = true
		return
	}

	data, err := c.GetRawData()
	if err != nil {
		log.Errorf("CreateSystemRoleBinding c.GetRawData() err : %v", err)
		ctx.Err = e.ErrInvalidParam.AddErr(err)
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(data))

	projectName := c.Query("namespace")
	if projectName == "" {
		ctx.Err = e.ErrInvalidParam.AddDesc("namespace is empty")
		return
	}
	groupID := c.Param("gid")
	if groupID == "" {
		ctx.Err = e.ErrInvalidParam.AddDesc("gid is empty")
		return
	}
	args := new(updateRoleBindingForUserReq)
	if err := c.ShouldBindJSON(&args); err != nil {
		ctx.Err = err
		return
	}

	groupInfo, err := user.GetUserGroup(groupID, ctx.Logger)
	if err != nil {
		ctx.Err = e.ErrInvalidParam.AddErr(err)
		return
	}

	groupName := ""
	if groupInfo != nil {
		groupName = groupInfo.Name
	}
	detail := "用户组：" + groupName + "，角色名称："
	for _, arg := range args.Roles {
		detail += arg + "，"
	}
	detail = strings.Trim(detail, "，")

	internalhandler.InsertDetailedOperationLog(c, ctx.UserName, projectName, setting.OperationSceneProject, "更新", "角色绑定", detail, string(data), ctx.Logger, "")

	if !ctx.Resources.IsSystemAdmin {
		if projectName == "*" {
			ctx.UnAuthorized = true
			return
		}

		if _, ok := ctx.Resources.ProjectAuthInfo[projectName]; !ok {
			ctx.UnAuthorized = true
			return
		}

		if !ctx.Resources.ProjectAuthInfo[projectName].IsProjectAdmin {
			ctx.UnAuthorized = true
			return
		}
	}

	ctx.Err = permission.UpdateRoleBindingForUserGroup(groupID, projectName, args.Roles, ctx.Logger)
}

func DeleteRoleBindingForGroup(c *gin.Context) {
	ctx, err := internalhandler.NewContextWithAuthorization(c)
	defer func() { internalhandler.JSONResponse(c, ctx) }()

	if err != nil {
		ctx.Err = fmt.Errorf("authorization Info Generation failed: err %s", err)
		ctx.UnAuthorized = true
		return
	}

	data, err := c.GetRawData()
	if err != nil {
		log.Errorf("CreateSystemRoleBinding c.GetRawData() err : %v", err)
		ctx.Err = e.ErrInvalidParam.AddErr(err)
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(data))

	projectName := c.Query("namespace")
	if projectName == "" {
		ctx.Err = e.ErrInvalidParam.AddDesc("namespace is empty")
		return
	}
	groupID := c.Param("gid")
	if groupID == "" {
		ctx.Err = e.ErrInvalidParam.AddDesc("gid is empty")
		return
	}

	groupInfo, err := user.GetUserGroup(groupID, ctx.Logger)
	if err != nil {
		ctx.Err = e.ErrInvalidParam.AddErr(err)
		return
	}

	groupName := ""
	if groupInfo != nil {
		groupName = groupInfo.Name
	}
	detail := "用户组：" + groupName

	internalhandler.InsertDetailedOperationLog(c, ctx.UserName, projectName, setting.OperationSceneProject, "删除", "角色绑定", detail, string(data), ctx.Logger, "")

	if !ctx.Resources.IsSystemAdmin {
		if projectName == "*" {
			ctx.UnAuthorized = true
			return
		}

		if _, ok := ctx.Resources.ProjectAuthInfo[projectName]; !ok {
			ctx.UnAuthorized = true
			return
		}

		if !ctx.Resources.ProjectAuthInfo[projectName].IsProjectAdmin {
			ctx.UnAuthorized = true
			return
		}
	}

	ctx.Err = permission.DeleteRoleBindingForUserGroup(groupID, projectName, ctx.Logger)
}
