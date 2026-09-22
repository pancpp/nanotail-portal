package conf

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

var (
	gVersion     string
	gBuildTime   string
	gGitHash     string
	gBuildNumber string
)

var (
	gConf *viper.Viper
)

func init() {
	var showVersion bool
	pflag.BoolVarP(&showVersion, "version", "V", false, "Show version information")
	pflag.Parse()

	if showVersion {
		fmt.Println("Version     :", gVersion)
		fmt.Println("BuildTime   :", gBuildTime)
		fmt.Println("GitHash     :", gGitHash)
		fmt.Println("BuildNumber :", gBuildNumber)
		os.Exit(0)
	}
}

func init() {
	conf := viper.New()

	// cloud server settings
	conf.SetDefault("http_listen_addr", ":8080")
	conf.SetDefault("log_dir", "/srv/fairnet-portal/logs")
	conf.SetDefault("enable_console_log", true)

	// set config path
	conf.SetConfigFile("fairnet-portal.yml")

	gConf = conf
}

func Init(ctx context.Context) error {
	confFilePath := gConf.ConfigFileUsed()
	if _, err := os.Stat(confFilePath); errors.Is(err, os.ErrNotExist) {
		f, err := os.Create("centauri.yml")
		if err != nil {
			return err
		}
		f.Close()
	}

	if err := gConf.ReadInConfig(); err != nil {
		return err
	}

	return nil
}

func GetVersion() (version, buildTime, gitHash, buildNumber string) {
	version = gVersion
	buildTime = gBuildTime
	gitHash = gGitHash
	buildNumber = gBuildNumber
	return
}

func GetString(key string) string {
	return gConf.GetString(key)
}

func GetInt(key string) int {
	return gConf.GetInt(key)
}

func GetInt64(key string) int64 {
	return gConf.GetInt64(key)
}

func GetBool(key string) bool {
	return gConf.GetBool(key)
}

func GetStringSlice(key string) []string {
	return gConf.GetStringSlice(key)
}

func GetTime(key string) time.Time {
	return gConf.GetTime(key)
}
