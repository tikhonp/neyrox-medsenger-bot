// Package config provides configuration structures and functions to load them.
package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	DB *Database

	Server *Server

	// Host is the public scheme+host of this agent, used to build absolute URLs in views.
	Host string

	// SentryDSN configures error reporting; empty disables it.
	SentryDSN string

	// NeyroxAPIHost is the base URL of the Neyrox API (e.g. https://adm.neyrox.com).
	NeyroxAPIHost string

	// FetchSleepDuration is the interval between worker polling cycles.
	FetchSleepDuration time.Duration
}

type Database struct {
	User string

	Password string

	Dbname string

	Host string

	Port int
}

type Server struct {
	// Port to listen on.
	Port uint16

	// Debug puts the server in debug mode (disables Sentry, enables verbose logs).
	Debug bool

	// MedsengerAgentKey is the Medsenger agent secret key.
	MedsengerAgentKey string
}

func mustAtoi(s string) int {
	v, err := strconv.Atoi(s)
	if err != nil {
		panic(err)
	}
	return v
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func LoadConfigFromEnv() *Config {
	fetchSleepMin, err := strconv.Atoi(getenvDefault("FETCH_SLEEP_DURATION_MIN", "15"))
	if err != nil {
		fetchSleepMin = 15
	}
	return &Config{
		Server: &Server{
			Port:              uint16(mustAtoi(os.Getenv("SERVER_PORT"))),
			MedsengerAgentKey: os.Getenv("NEYROX_KEY"),
			Debug:             os.Getenv("DEBUG") == "true",
		},
		DB: &Database{
			User:     os.Getenv("DB_LOGIN"),
			Password: os.Getenv("DB_PASSWORD"),
			Dbname:   os.Getenv("DB_DATABASE"),
			Host:     os.Getenv("DB_HOST"),
			Port:     mustAtoi(os.Getenv("DB_PORT")),
		},
		Host:               os.Getenv("HOST"),
		SentryDSN:          os.Getenv("SENTRY_DSN"),
		NeyroxAPIHost:      getenvDefault("NEYROX_API_HOST", "https://adm.neyrox.com"),
		FetchSleepDuration: time.Duration(fetchSleepMin) * time.Minute,
	}
}
