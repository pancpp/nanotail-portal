package app

import (
	"context"
	"log"
	"runtime"

	"github.com/pancpp/nanotail-portal/conf"
	"github.com/pancpp/nanotail-portal/upgrade"
)

func newUpgradeService(ctx context.Context) (*upgrade.Service, error) {
	keys, err := upgrade.TrustedKeys()
	if err != nil {
		return nil, err
	}
	version, _, _, _ := conf.GetVersion()
	service := upgrade.NewService(upgrade.STAGING_DIR, version, runtime.GOOS, runtime.GOARCH, keys, upgrade.NewGitHubSource())
	if err := service.Load(ctx); err != nil {
		log.Printf("(upgrade) previous staged package is unavailable: %v", err)
	}
	return service, nil
}
