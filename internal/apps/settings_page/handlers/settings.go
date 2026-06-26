package handlers

import (
	"errors"

	"github.com/labstack/echo/v5"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/apps/settings_page/views"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/db/models"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/util"
)

// render draws the settings page for the current contract, optionally with an error banner.
func (sph *SettingsPageHandler) render(c *echo.Context, errMsg string) error {
	contractID, err := util.GetContractID(c)
	if err != nil {
		return err
	}
	contract, err := sph.DB.Contracts().Get(contractID)
	if err != nil {
		return err
	}
	account, err := sph.DB.NeyroxAccounts().GetByContractID(contractID)
	if err != nil && !errors.Is(err, models.ErrNeyroxAccountNotFound) {
		return err
	}
	return util.TemplRender(c, views.Settings(contract, account, util.GetAgentToken(c), errMsg))
}

func (sph *SettingsPageHandler) SettingsGet(c *echo.Context) error {
	return sph.render(c, "")
}

type connectForm struct {
	Email    string `form:"email" validate:"required,email"`
	Password string `form:"password" validate:"required"`
}

func (sph *SettingsPageHandler) ConnectPost(c *echo.Context) error {
	var f connectForm
	if err := c.Bind(&f); err != nil {
		return err
	}
	if err := c.Validate(f); err != nil {
		return sph.render(c, "Введите корректный email и пароль.")
	}
	contractID, err := util.GetContractID(c)
	if err != nil {
		return err
	}
	if _, err := sph.DB.NeyroxAccounts().Connect(contractID, f.Email, f.Password); err != nil {
		return err
	}
	return sph.render(c, "")
}

func (sph *SettingsPageHandler) DisconnectPost(c *echo.Context) error {
	contractID, err := util.GetContractID(c)
	if err != nil {
		return err
	}
	if err := sph.DB.NeyroxAccounts().DeleteByContractID(contractID); err != nil {
		return err
	}
	return sph.render(c, "")
}
