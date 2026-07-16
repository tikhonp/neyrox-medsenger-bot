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

	g.POST("/init", mah.Init, util.AgentTokenJSON(deps.Maigo, maigo.RequestRoleSystem))
	g.POST("/status", mah.Status, util.AgentTokenJSON(deps.Maigo, maigo.RequestRoleSystem))
	g.POST("/remove", mah.Remove, util.AgentTokenJSON(deps.Maigo, maigo.RequestRoleSystem))
	g.GET("/scenario-capabilities/v1", mah.ScenarioCapabilities, util.AgentTokenHeader(deps.Maigo, maigo.RequestRoleSystem))
	g.GET("/scenario-capabilities/v1/objects/:object_type", mah.ScenarioCapabilityObjects, util.AgentTokenHeader(deps.Maigo, maigo.RequestRoleSystem))
	g.GET("/scenario-capabilities/v1/objects/:object_type/:object_id", mah.ScenarioCapabilityObject, util.AgentTokenHeader(deps.Maigo, maigo.RequestRoleSystem))
}
