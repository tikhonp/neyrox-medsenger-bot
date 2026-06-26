// Package router builds the echo HTTP server and registers the app routes.
package router

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	sentryecho "github.com/getsentry/sentry-go/echo"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	mainpage "github.com/tikhonp/medsenger-neyrox-bot/internal/apps/main_page"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/apps/medsengeragent"
	settingspage "github.com/tikhonp/medsenger-neyrox-bot/internal/apps/settings_page"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/util"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/util/config"
)

func New(cfg *config.Config) *echo.Echo {
	e := echo.New()

	e.Validator = util.NewDefaultValidator()

	e.Pre(middleware.RemoveTrailingSlash())

	if !cfg.Server.Debug {
		e.Use(sentryecho.New(sentryecho.Options{
			Repanic:         true,
			WaitForDelivery: false,
			Timeout:         5 * time.Second,
		}))
	}
	e.Use(middleware.RequestLoggerWithConfig(util.GetRequestLoggerConfig(!cfg.Server.Debug)))
	e.Use(middleware.Recover())
	e.Use(middleware.CORS())

	return e
}

func RegisterRoutes(e *echo.Echo, deps util.Dependencies) {
	mainpage.ConfigureMainPageGroup(e.Group(""), deps)
	medsengeragent.ConfigureMedsengerAgentGroup(e.Group("/medsenger"), deps)
	settingspage.ConfigureSettingsPageGroup(e.Group("/medsenger/settings"), deps)
}

func Start(e *echo.Echo, cfg *config.Config) error {
	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	// Mirror echo's own Start: shut down gracefully on interrupt/SIGTERM.
	// HideBanner keeps the quiet startup the v4 scaffold had (e.HideBanner = true).
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return echo.StartConfig{Address: addr, HideBanner: true}.Start(ctx, e)
}
