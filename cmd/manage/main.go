// Command manage is a small CLI for operational tasks.
//
//	manage -c print-db-string   # print the postgres DSN (used by the Docker entrypoint to run goose)
package main

import (
	"flag"
	"fmt"

	"github.com/tikhonp/medsenger-neyrox-bot/internal/db"
	"github.com/tikhonp/medsenger-neyrox-bot/internal/util/config"
)

func main() {
	var command string
	const usage = "command to run. Available commands: print-db-string"
	flag.StringVar(&command, "command", "", usage)
	flag.StringVar(&command, "c", "", usage+" (shorthand)")
	flag.Parse()

	cfg := config.LoadConfigFromEnv()

	switch command {
	case "print-db-string":
		fmt.Print(db.DataSourceName(cfg.DB))
	default:
		fmt.Println("Invalid arguments")
	}
}
