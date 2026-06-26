// Package medsengeragent provides the Medsenger agent webhook routes (/medsenger).
package medsengeragent

import (
	"github.com/labstack/echo/v5"
	"github.com/tikhonp/maigo"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/apps/medsengeragent/handlers"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/util"
)

func ConfigureMedsengerAgentGroup(g *echo.Group, deps util.Dependencies) {
	mah := handlers.MedsengerAgentHandler(deps)

	g.Use(util.AgentTokenJSON(deps.Maigo, maigo.RequestRoleSystem))

	g.POST("/init", mah.Init)
	g.POST("/status", mah.Status)
	g.POST("/remove", mah.Remove)
}
