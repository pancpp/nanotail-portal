package conf

import (
	"log"

	"github.com/spf13/pflag"
)

const (
	DB_UNKNOWN = iota
	DB_INIT
	DB_MIGRATE
	DB_STATUS
	DB_CREATE
)

var (
	gDbCmd int = DB_UNKNOWN
	gDbArg string
)

func DbCmdArg() (int, string) {
	return gDbCmd, gDbArg
}

func initDBArgs() {
	if pflag.NArg() >= 2 {
		switch pflag.Arg(0) {
		case "db":
			switch pflag.Arg(1) {
			case "init":
				gDbCmd = DB_INIT
			case "migrate":
				gDbCmd = DB_MIGRATE
			case "status":
				gDbCmd = DB_STATUS
			case "create":
				gDbCmd = DB_CREATE
				if pflag.NArg() >= 3 {
					gDbArg = pflag.Arg(2)
				} else {
					log.Fatal("unknown db migration name")
				}
			default:
				log.Fatal("unknown db migration command")
			}
		}
	}
}
