package util

import (
	"log"

	"github.com/getsentry/sentry-go"
)

func StartSentry(dsn string) error {
	log.Printf("Release version: %s", AppVersion)
	return sentry.Init(sentry.ClientOptions{
		Dsn:              dsn,
		Debug:            false,
		SendDefaultPII:   true,
		EnableTracing:    true,
		TracesSampleRate: 1.0,
		AttachStacktrace: true,
		SampleRate:       1.0,
		Release:          AppVersion,
	})
}
