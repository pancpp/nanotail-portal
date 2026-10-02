package graph

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/pancpp/nanotail-portal/auth"
	"github.com/pancpp/nanotail-portal/upgrade"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func TestUpgradeErrorDetailsHideInternalDiagnostics(t *testing.T) {
	private := errors.New("private signing detail at /var/lib/private/package.tar.gz")
	for _, test := range []struct {
		name  string
		cause error
		code  string
	}{
		{"verification", upgrade.ErrInvalidPackage, "UPGRADE_INVALID_PACKAGE"},
		{"installation", upgrade.ErrInstallFailed, "UPGRADE_INSTALL_FAILED"},
		{"deadline", context.DeadlineExceeded, "UPGRADE_TIMEOUT"},
		{"cancellation", context.Canceled, "UPGRADE_TIMEOUT"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cause := errors.Join(test.cause, private)
			err := &gqlerror.Error{Err: upgradeFailure(cause), Message: cause.Error()}
			message, code, ok := UpgradeErrorDetails(err)
			if !ok || code != test.code || message == "" || strings.Contains(message, "private") {
				t.Fatalf("unsafe or missing error details: %q, %q, %t", message, code, ok)
			}
			if !errors.Is(err, private) {
				t.Fatal("internal cause was lost for server logging")
			}
		})
	}
	message, code, ok := UpgradeErrorDetails(fmt.Errorf("%w: %v", ErrUpgradeAdmin, private))
	if !ok || code != "FORBIDDEN" || message != ErrUpgradeAdmin.Error() {
		t.Fatalf("administrator error details: %q, %q, %t", message, code, ok)
	}
}

func TestUpgradeErrorDetailsLeaveUnrelatedAndUnknownErrorsToPresenter(t *testing.T) {
	for _, err := range []error{
		nil,
		context.Canceled,
		fmt.Errorf("unrelated operation: %w", context.DeadlineExceeded),
		upgrade.ErrInvalidPackage,
		upgradeFailure(errors.New("unexpected internal error")),
		auth.ErrUnauthorized,
	} {
		message, code, ok := UpgradeErrorDetails(err)
		if ok || message != "" || code != "" {
			t.Errorf("error %v was incorrectly classified: %q, %q, %t", err, message, code, ok)
		}
	}
}

func TestUpgradeResolversRequireIdentityBeforeServiceOrArgumentChecks(t *testing.T) {
	r := &Resolver{}
	operations := map[string]func() error{
		"status":   func() error { _, err := r.Query().UpgradeStatus(t.Context()); return err },
		"check":    func() error { _, err := r.Mutation().CheckForUpdates(t.Context()); return err },
		"download": func() error { _, err := r.Mutation().DownloadUpgrade(t.Context(), "invalid"); return err },
		"install":  func() error { _, err := r.Mutation().InstallUpgrade(t.Context(), "invalid", "invalid"); return err },
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			if err := operation(); !errors.Is(err, auth.ErrUnauthorized) {
				t.Fatalf("missing identity returned %v", err)
			}
		})
	}
}
