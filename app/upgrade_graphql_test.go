package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pancpp/nanotail-portal/maintenance"
	"github.com/pancpp/nanotail-portal/upgrade"
)

func TestUpgradeMaintenanceChecksPersistedOperations(t *testing.T) {
	e, admin := setupLoginApp(t)
	var gate maintenance.Gate
	source := &upgradeAPISource{}
	service := upgrade.NewService(t.TempDir(), "v1.0.0", "linux", "arm64", nil, source)
	installer := upgrade.NewInstaller(upgrade.InstallerOptions{
		StateDir: t.TempDir(), Gate: &gate,
		ValidateHost: func(context.Context) error { return nil },
	}, nil, "linux", "arm64")
	service.SetInstaller(installer)
	if err := initAPIsWithClient(e, newTailscaleClient(), graphQLServices{upgrades: service, installer: installer}); err != nil {
		t.Fatal(err)
	}
	e.Use(maintenanceMiddleware(nil, installer))
	token := testAuthorization(t, admin.PID)
	request := func(document string, cached bool) *httptest.ResponseRecorder {
		t.Helper()
		digest := sha256.Sum256([]byte(document))
		payload := map[string]any{"extensions": map[string]any{"persistedQuery": map[string]any{
			"version": 1, "sha256Hash": hex.EncodeToString(digest[:]),
		}}}
		if !cached {
			payload["query"] = document
		}
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		return appRequest(e, http.MethodPost, "/api/v1/query", string(body), token)
	}
	statusQuery := `query CachedStatus { progress: upgradeStatus { currentVersion } }`
	checkMutation := `mutation CachedCheck { checkForUpdates { currentVersion } }`
	requireUpgradeSuccess(t, request(statusQuery, false), "progress")
	requireUpgradeSuccess(t, request(checkMutation, false), "checkForUpdates")
	if !gate.TryAcquire("upgrade") {
		t.Fatal("cannot acquire maintenance gate")
	}
	defer gate.Release("upgrade")
	requireUpgradeSuccess(t, request(statusQuery, true), "progress")
	requireUpgradeError(t, request(checkMutation, true), "MAINTENANCE", "in progress")
	if source.checked != 1 || source.opened != 0 {
		t.Fatal("cached mutation bypassed maintenance")
	}
}
