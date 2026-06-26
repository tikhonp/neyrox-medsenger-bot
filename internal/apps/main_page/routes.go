// Package mainpage provides the root route.
package mainpage

import (
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/util"
)

func ConfigureMainPageGroup(g *echo.Group, deps util.Dependencies) {
	g.GET("/", func(c *echo.Context) error {
		return c.String(http.StatusOK, "Купил мужик шляпу, а она ему как раз!")
	})
}
