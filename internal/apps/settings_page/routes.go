// Package settingspage provides the patient-facing settings page (/medsenger/settings)
// where the patient connects their Neyrox account.
package settingspage

import (
	"github.com/labstack/echo/v5"
	"github.com/tikhonp/maigo"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/apps/settings_page/handlers"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/util"
)

func ConfigureSettingsPageGroup(g *echo.Group, deps util.Dependencies) {
	sph := handlers.SettingsPageHandler(deps)

	g.GET("", sph.SettingsGet, util.AgentTokenGetParam(deps.Maigo, maigo.RequestRoleDoctor, maigo.RequestRolePatient))
	g.POST("/connect", sph.ConnectPost, util.AgentTokenForm(deps.Maigo, maigo.RequestRoleDoctor, maigo.RequestRolePatient))
	g.POST("/disconnect", sph.DisconnectPost, util.AgentTokenForm(deps.Maigo, maigo.RequestRoleDoctor, maigo.RequestRolePatient))
}
