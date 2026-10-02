package graph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/pancpp/nanotail-portal/app/graph/model"
	"github.com/pancpp/nanotail-portal/upgrade"
)

var (
	ErrUpgradeAdmin = errors.New("Only portal administrators can manage upgrades")

	errUpgradeVersion = errors.New("Select a valid release version")
	errUpgradeService = errors.New("Upgrade management is unavailable on this device")
)

// upgradeOperationError limits upgrade-specific presentation to these resolvers.
// Its cause remains available to server logging without exposing internal paths.
type upgradeOperationError struct{ cause error }

func (e *upgradeOperationError) Error() string { return e.cause.Error() }
func (e *upgradeOperationError) Unwrap() error { return e.cause }

func upgradeFailure(err error) error {
	if err == nil {
		return nil
	}
	return &upgradeOperationError{cause: err}
}

// UpgradeErrorDetails returns safe public details for recognized upgrade failures.
// Unrecognized errors use the application's normal internal-error presenter.
func UpgradeErrorDetails(err error) (message, code string, ok bool) {
	if errors.Is(err, ErrUpgradeAdmin) {
		return ErrUpgradeAdmin.Error(), "FORBIDDEN", true
	}
	var operation *upgradeOperationError
	if !errors.As(err, &operation) {
		return "", "", false
	}
	for _, failure := range []struct {
		cause   error
		message string
		code    string
	}{
		{errUpgradeVersion, "Select a valid release version", "UPGRADE_INVALID_VERSION"},
		{errUpgradeService, "Upgrade management is unavailable on this device", "UPGRADE_UNAVAILABLE"},
		{upgrade.ErrInvalidSelection, "Select the verified package version and SHA-256 digest", "UPGRADE_INVALID_SELECTION"},
		{upgrade.ErrNoStagedPackage, "Download and verify an upgrade package before installing", "UPGRADE_NOT_STAGED"},
		{upgrade.ErrInstallPending, "Another upgrade or factory reset operation is in progress", "UPGRADE_BUSY"},
		{upgrade.ErrMaintenanceBusy, "Another upgrade or factory reset operation is in progress", "UPGRADE_BUSY"},
		{upgrade.ErrInstallUnsupported, "Upgrade installation is not supported on this device", "UPGRADE_UNSUPPORTED"},
		{upgrade.ErrInstallFailed, "Unable to prepare upgrade installation. Check local service logs.", "UPGRADE_INSTALL_FAILED"},
		{upgrade.ErrUnavailable, "GitHub update checking is unavailable. Try again later.", "UPGRADE_UNAVAILABLE"},
		{upgrade.ErrGitHubRateLimited, "GitHub's request limit was reached. Try again later.", "UPGRADE_RATE_LIMITED"},
		{upgrade.ErrGitHubUnavailable, "Unable to contact GitHub Releases. Try again later.", "UPGRADE_GITHUB_UNAVAILABLE"},
		{upgrade.ErrNoCompatiblePackage, "The latest GitHub release has no compatible package for this device.", "UPGRADE_NO_COMPATIBLE_PACKAGE"},
		{upgrade.ErrBusy, "Another upgrade operation is in progress", "UPGRADE_BUSY"},
		{upgrade.ErrTooLarge, "The upgrade package must be 128 MiB or smaller", "UPGRADE_TOO_LARGE"},
		{upgrade.ErrInvalidPackage, "Package verification failed. The GitHub release package is not trusted or is incompatible with this device.", "UPGRADE_INVALID_PACKAGE"},
		{upgrade.ErrVersionMismatch, "The release version changed. Check for updates again.", "UPGRADE_VERSION_MISMATCH"},
		{context.Canceled, "The upgrade request timed out or was canceled", "UPGRADE_TIMEOUT"},
		{context.DeadlineExceeded, "The upgrade request timed out or was canceled", "UPGRADE_TIMEOUT"},
	} {
		if errors.Is(operation.cause, failure.cause) {
			return failure.message, failure.code, true
		}
	}
	return "", "", false
}

func (r *queryResolver) upgradeStatus(ctx context.Context) (*upgrade.Status, error) {
	if err := requireAdmin(ctx, ErrUpgradeAdmin); err != nil {
		return nil, err
	}
	if r.Upgrades == nil {
		return nil, upgradeFailure(errUpgradeService)
	}
	status := r.Upgrades.Status()
	return &status, nil
}

func (r *mutationResolver) checkForUpdates(ctx context.Context) (*upgrade.Status, error) {
	if err := requireAdmin(ctx, ErrUpgradeAdmin); err != nil {
		return nil, err
	}
	if r.Upgrades == nil {
		return nil, upgradeFailure(errUpgradeService)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	status, err := r.Upgrades.Check(ctx)
	if err != nil {
		return nil, upgradeFailure(err)
	}
	return &status, nil
}

func (r *mutationResolver) downloadUpgrade(ctx context.Context, version string) (*upgrade.Status, error) {
	if err := requireAdmin(ctx, ErrUpgradeAdmin); err != nil {
		return nil, err
	}
	if !upgrade.ValidVersion(version) {
		return nil, upgradeFailure(errUpgradeVersion)
	}
	if r.Upgrades == nil {
		return nil, upgradeFailure(errUpgradeService)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	status, err := r.Upgrades.Download(ctx, version)
	if err != nil {
		return nil, upgradeFailure(err)
	}
	return &status, nil
}

func (r *mutationResolver) installUpgrade(ctx context.Context, version, digest string) (*model.UpgradeInstallResult, error) {
	if err := requireAdmin(ctx, ErrUpgradeAdmin); err != nil {
		return nil, err
	}
	if !upgrade.ValidVersion(version) || len(digest) != sha256.Size*2 || digest != strings.ToLower(digest) {
		return nil, upgradeFailure(upgrade.ErrInvalidSelection)
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return nil, upgradeFailure(upgrade.ErrInvalidSelection)
	}
	request := queryContextValue(ctx)
	if r.Upgrades == nil || request == nil || request.AfterResponse == nil {
		return nil, upgradeFailure(errUpgradeService)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	installation, err := r.Upgrades.PrepareInstall(ctx, version, digest)
	if err != nil {
		return nil, upgradeFailure(err)
	}
	// Durable preparation must always hand off, even if the client disconnects.
	request.AfterResponse(r.Upgrades.ScheduleInstall)
	return &model.UpgradeInstallResult{Accepted: true, Installation: installation}, nil
}
