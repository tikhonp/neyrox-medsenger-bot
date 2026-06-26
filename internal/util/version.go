package util

// AppVersion is the application version. It is injected at build time via
// ldflags:
//
//	-X github.com/tikhonp/medsenger-neyrox-bot/internal/util.AppVersion=${APP_VERSION}
//
// and defaults to "dev" for local builds.
var AppVersion = "dev"
