package conf

import (
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
	gViper *viper.Viper
)

func init() {
	var (
		showVersion bool
		configFile  string
	)

	pflag.BoolVarP(&showVersion, "version", "V", false, "Show version information")
	pflag.StringVar(&dataDirectory, "data-dir", DEFAULT_DATA_DIR, "Persistent data directory (overrides NANOTAIL_DATA_DIR)")
	pflag.StringVarP(&configFile, "config", "c", "nanotail-portal.yml", "Configuration file, relative to the data directory unless absolute")
	pflag.Parse()
	if showVersion {
		fmt.Println("###############################################")
		fmt.Println("Version     :", gVersion)
		fmt.Println("BuildTime   :", gBuildTime)
		fmt.Println("GitHash     :", gGitHash)
		fmt.Println("BuildNumber :", gBuildNumber)
		fmt.Println("###############################################")
		os.Exit(0)
	}

	// viper
	v := viper.New()

	// server settings
	v.SetDefault("http_listen_addr", "127.0.0.1:7080")
	v.SetDefault("log_dir", "logs")
	v.SetDefault("enable_console_log", false)
	v.SetDefault("database", "nanotail-portal.sqlite3")
	v.SetDefault("tailscale_binary", "/usr/bin/tailscale")
	v.SetDefault("tailscale_socket", "")
	v.SetDefault("tailscale_timeout", "15s")
	v.SetDefault("vpn_traffic_led", true)
	v.SetDefault("access_enable", true)
	v.SetDefault("access_api_prefix", "https://tailscale.fairkid.ca/api/device/v1")
	v.SetDefault("access_eth_name", "eth0")

	// set config path
	v.SetConfigFile(configFile)
	gViper = v
}

func Init() error {
	if err := validateArguments(); err != nil {
		return err
	}
	p := ConfigFile()
	gViper.SetConfigFile(p)
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := gViper.ReadInConfig(); err != nil {
		return err
	}
	return nil
}

func GetVersion() (version, buildTime, gitHash, buildNumber string) {
	return gVersion, gBuildTime, gGitHash, gBuildNumber
}

func ConfigFile() string {
	return DataPath(gViper.ConfigFileUsed())
}

func GetString(key string) string {
	return gViper.GetString(key)
}

func GetInt(key string) int {
	return gViper.GetInt(key)
}

func GetInt64(key string) int64 {
	return gViper.GetInt64(key)
}

func GetBool(key string) bool {
	return gViper.GetBool(key)
}

func GetStringSlice(key string) []string {
	return gViper.GetStringSlice(key)
}

func GetTime(key string) time.Time {
	return gViper.GetTime(key)
}
