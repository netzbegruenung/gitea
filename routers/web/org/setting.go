// Copyright 2014 The Gogs Authors. All rights reserved.
// Copyright 2019 The Gitea Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package org

import (
	"net/http"
	"net/url"
	"strings"

	"code.gitea.io/gitea/models"
	"code.gitea.io/gitea/models/db"
	repo_model "code.gitea.io/gitea/models/repo"
	user_model "code.gitea.io/gitea/models/user"
	"code.gitea.io/gitea/models/webhook"
	"code.gitea.io/gitea/modules/base"
	"code.gitea.io/gitea/modules/context"
	"code.gitea.io/gitea/modules/log"
	"code.gitea.io/gitea/modules/setting"
	"code.gitea.io/gitea/modules/web"
	user_setting "code.gitea.io/gitea/routers/web/user/setting"
	"code.gitea.io/gitea/services/forms"
	"code.gitea.io/gitea/services/org"
	user_service "code.gitea.io/gitea/services/user"
)

const (
	// tplSettingsOptions template path for render settings
	tplSettingsOptions base.TplName = "org/settings/options"
	// tplSettingsDelete template path for render delete repository
	tplSettingsDelete base.TplName = "org/settings/delete"
	// tplSettingsHooks template path for render hook settings
	tplSettingsHooks base.TplName = "org/settings/hooks"
	// tplSettingsLabels template path for render labels settings
	tplSettingsLabels base.TplName = "org/settings/labels"
)

// Settings render the main settings page
func Settings(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("org.settings")
	ctx.Data["PageIsOrgSettings"] = true
	ctx.Data["PageIsSettingsOptions"] = true
	ctx.Data["CurrentVisibility"] = ctx.Org.Organization.Visibility
	ctx.Data["RepoAdminChangeTeamAccess"] = ctx.Org.Organization.RepoAdminChangeTeamAccess
	ctx.HTML(http.StatusOK, tplSettingsOptions)
}

// SettingsPost response for settings change submitted
func SettingsPost(ctx *context.Context) {
	form := web.GetForm(ctx).(*forms.UpdateOrgSettingForm)
	ctx.Data["Title"] = ctx.Tr("org.settings")
	ctx.Data["PageIsOrgSettings"] = true
	ctx.Data["PageIsSettingsOptions"] = true
	ctx.Data["CurrentVisibility"] = ctx.Org.Organization.Visibility

	if ctx.HasError() {
		ctx.HTML(http.StatusOK, tplSettingsOptions)
		return
	}

	org := ctx.Org.Organization
	nameChanged := org.Name != form.Name

	// Check if organization name has been changed.
	if org.LowerName != strings.ToLower(form.Name) {
		isExist, err := user_model.IsUserExist(org.ID, form.Name)
		if err != nil {
			ctx.ServerError("IsUserExist", err)
			return
		} else if isExist {
			ctx.Data["OrgName"] = true
			ctx.RenderWithErr(ctx.Tr("form.username_been_taken"), tplSettingsOptions, &form)
			return
		} else if err = user_model.ChangeUserName(org.AsUser(), form.Name); err != nil {
			switch {
			case db.IsErrNameReserved(err):
				ctx.Data["OrgName"] = true
				ctx.RenderWithErr(ctx.Tr("repo.form.name_reserved", err.(db.ErrNameReserved).Name), tplSettingsOptions, &form)
			case db.IsErrNamePatternNotAllowed(err):
				ctx.Data["OrgName"] = true
				ctx.RenderWithErr(ctx.Tr("repo.form.name_pattern_not_allowed", err.(db.ErrNamePatternNotAllowed).Pattern), tplSettingsOptions, &form)
			default:
				ctx.ServerError("ChangeUserName", err)
			}
			return
		}
		// reset ctx.org.OrgLink with new name
		ctx.Org.OrgLink = setting.AppSubURL + "/org/" + url.PathEscape(form.Name)
		log.Trace("Organization name changed: %s -> %s", org.Name, form.Name)
		nameChanged = false
	}

	// In case it's just a case change.
	org.Name = form.Name
	org.LowerName = strings.ToLower(form.Name)

	if ctx.User.IsAdmin {
		org.MaxRepoCreation = form.MaxRepoCreation
	}

	org.FullName = form.FullName
	org.Description = form.Description
	org.Website = form.Website
	org.Location = form.Location
	org.RepoAdminChangeTeamAccess = form.RepoAdminChangeTeamAccess

	visibilityChanged := form.Visibility != org.Visibility
	org.Visibility = form.Visibility

	if err := user_model.UpdateUser(org.AsUser(), false); err != nil {
		ctx.ServerError("UpdateUser", err)
		return
	}

	// update forks visibility
	if visibilityChanged {
		repos, _, err := models.GetUserRepositories(&models.SearchRepoOptions{
			Actor: org.AsUser(), Private: true, ListOptions: db.ListOptions{Page: 1, PageSize: org.NumRepos},
		})
		if err != nil {
			ctx.ServerError("GetRepositories", err)
			return
		}
		for _, repo := range repos {
			repo.OwnerName = org.Name
			if err := models.UpdateRepository(repo, true); err != nil {
				ctx.ServerError("UpdateRepository", err)
				return
			}
		}
	} else if nameChanged {
		if err := repo_model.UpdateRepositoryOwnerNames(org.ID, org.Name); err != nil {
			ctx.ServerError("UpdateRepository", err)
			return
		}
	}

	log.Trace("Organization setting updated: %s", org.Name)
	ctx.Flash.Success(ctx.Tr("org.settings.update_setting_success"))
	ctx.Redirect(ctx.Org.OrgLink + "/settings")
}

// SettingsAvatar response for change avatar on settings page
func SettingsAvatar(ctx *context.Context) {
	form := web.GetForm(ctx).(*forms.AvatarForm)
	form.Source = forms.AvatarLocal
	if err := user_setting.UpdateAvatarSetting(ctx, form, ctx.Org.Organization.AsUser()); err != nil {
		ctx.Flash.Error(err.Error())
	} else {
		ctx.Flash.Success(ctx.Tr("org.settings.update_avatar_success"))
	}

	ctx.Redirect(ctx.Org.OrgLink + "/settings")
}

// SettingsDeleteAvatar response for delete avatar on settings page
func SettingsDeleteAvatar(ctx *context.Context) {
	if err := user_service.DeleteAvatar(ctx.Org.Organization.AsUser()); err != nil {
		ctx.Flash.Error(err.Error())
	}

	ctx.Redirect(ctx.Org.OrgLink + "/settings")
}

// SettingsDelete response for deleting an organization
func SettingsDelete(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("org.settings")
	ctx.Data["PageIsOrgSettings"] = true
	ctx.Data["PageIsSettingsDelete"] = true

	if ctx.Req.Method == "POST" {
		if ctx.Org.Organization.Name != ctx.FormString("org_name") {
			ctx.Data["Err_OrgName"] = true
			ctx.RenderWithErr(ctx.Tr("form.enterred_invalid_org_name"), tplSettingsDelete, nil)
			return
		}

		if err := org.DeleteOrganization(ctx.Org.Organization); err != nil {
			if models.IsErrUserOwnRepos(err) {
				ctx.Flash.Error(ctx.Tr("form.org_still_own_repo"))
				ctx.Redirect(ctx.Org.OrgLink + "/settings/delete")
			} else {
				ctx.ServerError("DeleteOrganization", err)
			}
		} else {
			log.Trace("Organization deleted: %s", ctx.Org.Organization.Name)
			ctx.Redirect(setting.AppSubURL + "/")
		}
		return
	}

	ctx.HTML(http.StatusOK, tplSettingsDelete)
}

// Webhooks render webhook list page
func Webhooks(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("org.settings")
	ctx.Data["PageIsOrgSettings"] = true
	ctx.Data["PageIsSettingsHooks"] = true
	ctx.Data["BaseLink"] = ctx.Org.OrgLink + "/settings/hooks"
	ctx.Data["BaseLinkNew"] = ctx.Org.OrgLink + "/settings/hooks"
	ctx.Data["Description"] = ctx.Tr("org.settings.hooks_desc")

	ws, err := webhook.ListWebhooksByOpts(&webhook.ListWebhookOptions{OrgID: ctx.Org.Organization.ID})
	if err != nil {
		ctx.ServerError("GetWebhooksByOrgId", err)
		return
	}

	ctx.Data["Webhooks"] = ws
	ctx.HTML(http.StatusOK, tplSettingsHooks)
}

// DeleteWebhook response for delete webhook
func DeleteWebhook(ctx *context.Context) {
	if err := webhook.DeleteWebhookByOrgID(ctx.Org.Organization.ID, ctx.FormInt64("id")); err != nil {
		ctx.Flash.Error("DeleteWebhookByOrgID: " + err.Error())
	} else {
		ctx.Flash.Success(ctx.Tr("repo.settings.webhook_deletion_success"))
	}

	ctx.JSON(http.StatusOK, map[string]interface{}{
		"redirect": ctx.Org.OrgLink + "/settings/hooks",
	})
}

// Labels render organization labels page
func Labels(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("repo.labels")
	ctx.Data["PageIsOrgSettings"] = true
	ctx.Data["PageIsOrgSettingsLabels"] = true
	ctx.Data["RequireTribute"] = true
	ctx.Data["LabelTemplates"] = models.LabelTemplates
	ctx.HTML(http.StatusOK, tplSettingsLabels)
}
