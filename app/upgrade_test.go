package app

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/pancpp/nanotail-portal/auth"
	"github.com/pancpp/nanotail-portal/database"
	"github.com/pancpp/nanotail-portal/factoryreset"
	"github.com/pancpp/nanotail-portal/maintenance"
	"github.com/pancpp/nanotail-portal/upgrade"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

const upgradeStatusFields = `currentVersion latestRelease { version notes publishedAt packageName size } updateAvailable checkedAt stagedPackage { version sha256 size verifiedAt notes } installationSupported installation { id version phase error startedAt completedAt } onlineCheckSupported phase`
const upgradeStatusQuery = `query UpgradeStatus { upgradeStatus { ` + upgradeStatusFields + ` } }`
const upgradeCheckMutation = `mutation CheckForUpdates { checkForUpdates { ` + upgradeStatusFields + ` } }`
const upgradeDownloadMutation = `mutation DownloadUpgrade($version: String!) { downloadUpgrade(version: $version) { ` + upgradeStatusFields + ` } }`
const upgradeInstallMutation = `mutation InstallUpgrade($version: String!, $sha256: String!) { installUpgrade(version: $version, sha256: $sha256) { accepted installation { id version phase } } }`

func initUpgradeTestAPI(t *testing.T, e *echo.Echo, services graphQLServices) {
	t.Helper()
	if err := initAPIsWithClient(e, newTailscaleClient(), services); err != nil {
		t.Fatal(err)
	}
}

func upgradeGraphQLBody(t *testing.T, query, operation string, variables any) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query, "operationName": operation, "variables": variables})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func upgradeGraphQLRequest(t *testing.T, e *echo.Echo, query, operation string, variables any, token string) *httptest.ResponseRecorder {
	t.Helper()
	return appRequest(e, http.MethodPost, "/api/v1/query", upgradeGraphQLBody(t, query, operation, variables), token)
}

type upgradeGraphQLResponse struct {
	Data   map[string]json.RawMessage `json:"data"`
	Errors gqlerror.List              `json:"errors"`
}

func decodeUpgradeResponse(t *testing.T, response *httptest.ResponseRecorder) upgradeGraphQLResponse {
	t.Helper()
	var result upgradeGraphQLResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid GraphQL response (HTTP %d): %s: %v", response.Code, response.Body.String(), err)
	}
	return result
}

func requireUpgradeSuccess(t *testing.T, response *httptest.ResponseRecorder, field string) upgrade.Status {
	t.Helper()
	result := decodeUpgradeResponse(t, response)
	if response.Code != http.StatusOK || len(result.Errors) != 0 || len(result.Data[field]) == 0 || string(result.Data[field]) == "null" {
		t.Fatalf("upgrade GraphQL failure: HTTP %d: %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("upgrade response can be cached")
	}
	var status upgrade.Status
	if err := json.Unmarshal(result.Data[field], &status); err != nil {
		t.Fatal(err)
	}
	return status
}

func requireUpgradeError(t *testing.T, response *httptest.ResponseRecorder, code, message string) {
	t.Helper()
	result := decodeUpgradeResponse(t, response)
	if response.Code != http.StatusOK || len(result.Errors) != 1 || (code != "" && result.Errors[0].Extensions["code"] != code) || !strings.Contains(result.Errors[0].Message, message) {
		t.Fatalf("want GraphQL error %s (%s), got HTTP %d: %s", code, message, response.Code, response.Body.String())
	}
	if response.Flushed {
		t.Fatal("rejected operation flushed an installation acknowledgement")
	}
}

func TestUpgradeRequiresCurrentAdministrator(t *testing.T) {
	e, admin := setupLoginApp(t)
	regular := &database.User{Username: "regular", Passwd: admin.Passwd, Role: "user"}
	if _, err := database.DB().NewInsert().Model(regular).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	source := &upgradeAPISource{}
	service := upgrade.NewService(t.TempDir(), "v1.0.0", "linux", "arm64", nil, source)
	initUpgradeTestAPI(t, e, graphQLServices{upgrades: service})
	digest := strings.Repeat("ab", 32)
	operations := []struct {
		query, name string
		variables   any
	}{
		{upgradeStatusQuery, "UpgradeStatus", nil},
		{upgradeCheckMutation, "CheckForUpdates", nil},
		{upgradeDownloadMutation, "DownloadUpgrade", map[string]any{"version": "v2.0.0"}},
		{upgradeInstallMutation, "InstallUpgrade", map[string]any{"version": "v2.0.0", "sha256": digest}},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			for _, token := range []string{"", "Bearer invalid"} {
				response := upgradeGraphQLRequest(t, e, operation.query, operation.name, operation.variables, token)
				if response.Code != http.StatusUnauthorized {
					t.Fatalf("invalid JWT: HTTP %d: %s", response.Code, response.Body.String())
				}
			}
			requireUpgradeError(t, upgradeGraphQLRequest(t, e, operation.query, operation.name, operation.variables, testAuthorization(t, 99999)), "", auth.ErrUnauthorized.Error())
			requireUpgradeError(t, upgradeGraphQLRequest(t, e, operation.query, operation.name, operation.variables, testAuthorization(t, regular.PID)), "FORBIDDEN", "administrators")
		})
	}
	if source.checked != 0 || source.opened != 0 {
		t.Fatal("unauthorized user reached the release source")
	}
	token := testAuthorization(t, admin.PID)
	status := requireUpgradeSuccess(t, upgradeGraphQLRequest(t, e, upgradeStatusQuery, "UpgradeStatus", nil, token), "upgradeStatus")
	if status.InstallationSupported || !status.OnlineCheckSupported || status.UpdateAvailable || status.StagedPackage != nil || source.checked != 0 {
		t.Fatalf("incorrect initial availability: %+v", status)
	}
	requireUpgradeSuccess(t, upgradeGraphQLRequest(t, e, upgradeCheckMutation, "CheckForUpdates", nil, token), "checkForUpdates")
	if service.Status().CheckedAt == nil || service.Status().LatestRelease != nil || source.checked != 1 {
		t.Fatal("no-release check did not record its result")
	}
	requireUpgradeError(t, upgradeGraphQLRequest(t, e, upgradeDownloadMutation, "DownloadUpgrade", map[string]any{"version": "v2.0.0"}, token), "UPGRADE_VERSION_MISMATCH", "Check for updates")
	// The original token must lose administrator access as soon as the DB role changes.
	admin.Role = "user"
	if _, err := database.DB().NewUpdate().Model(admin).WherePK().Column("role").Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, operation := range operations {
		requireUpgradeError(t, upgradeGraphQLRequest(t, e, operation.query, operation.name, operation.variables, token), "FORBIDDEN", "administrators")
	}
	if source.checked != 1 || source.opened != 0 {
		t.Fatal("revoked administrator reached the release source")
	}
	if _, err := database.DB().NewDelete().Model(admin).WherePK().Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, operation := range operations {
		requireUpgradeError(t, upgradeGraphQLRequest(t, e, operation.query, operation.name, operation.variables, token), "", auth.ErrUnauthorized.Error())
	}
}

func TestUpgradeRejectsInvalidGraphQLSelections(t *testing.T) {
	e, admin := setupLoginApp(t)
	source := &upgradeAPISource{}
	service := upgrade.NewService(t.TempDir(), "v1.0.0", "linux", "arm64", nil, source)
	initUpgradeTestAPI(t, e, graphQLServices{upgrades: service})
	token := testAuthorization(t, admin.PID)
	digest := strings.Repeat("ab", 32)
	for _, variables := range []any{nil, map[string]any{}, map[string]any{"version": nil}, map[string]any{"version": 123}, map[string]any{"version": []string{"v2.0.0"}}} {
		response := upgradeGraphQLRequest(t, e, upgradeDownloadMutation, "DownloadUpgrade", variables, token)
		result := decodeUpgradeResponse(t, response)
		if len(result.Errors) == 0 || response.Flushed {
			t.Fatalf("invalid download variables accepted: %v: %s", variables, response.Body.String())
		}
	}
	for _, version := range []string{"", "../../file", "v2", " v2.0.0", strings.Repeat("x", 129)} {
		requireUpgradeError(t, upgradeGraphQLRequest(t, e, upgradeDownloadMutation, "DownloadUpgrade", map[string]any{"version": version}, token), "UPGRADE_INVALID_VERSION", "")
	}
	for _, variables := range []any{nil, map[string]any{}, map[string]any{"version": "v2.0.0"}, map[string]any{"sha256": digest}, map[string]any{"version": "v2.0.0", "sha256": nil}, map[string]any{"version": "v2.0.0", "sha256": 123}} {
		response := upgradeGraphQLRequest(t, e, upgradeInstallMutation, "InstallUpgrade", variables, token)
		if len(decodeUpgradeResponse(t, response).Errors) == 0 || response.Flushed {
			t.Fatalf("invalid installation variables accepted: %v: %s", variables, response.Body.String())
		}
	}
	for _, variables := range []map[string]any{
		{"version": "../../file", "sha256": digest}, {"version": "v2.0.0", "sha256": "bad"},
		{"version": "v2.0.0", "sha256": strings.ToUpper(digest)}, {"version": "v2.0.0", "sha256": " " + digest},
	} {
		requireUpgradeError(t, upgradeGraphQLRequest(t, e, upgradeInstallMutation, "InstallUpgrade", variables, token), "UPGRADE_INVALID_SELECTION", "")
	}
	for _, query := range []string{
		`mutation { downloadUpgrade(version: "v2.0.0", url: "https://untrusted.invalid/package") { currentVersion } }`,
		`mutation { installUpgrade(version: "v2.0.0", sha256: "` + digest + `", path: "/tmp/package.tar.gz") { accepted } }`,
		`mutation { uploadUpgrade(package: "untrusted") { currentVersion } }`,
	} {
		response := upgradeGraphQLRequest(t, e, query, "", nil, token)
		if len(decodeUpgradeResponse(t, response).Errors) == 0 || response.Flushed {
			t.Fatalf("unsupported GraphQL arguments or field accepted: %s", response.Body.String())
		}
	}
	requireUpgradeError(t, upgradeGraphQLRequest(t, e, upgradeInstallMutation, "InstallUpgrade", map[string]any{"version": "v2.0.0", "sha256": digest}, token), "UPGRADE_UNSUPPORTED", "not supported")
	if source.checked != 0 || source.opened != 0 || service.Status().StagedPackage != nil {
		t.Fatal("invalid selection caused upgrade work")
	}
}

func TestUpgradeRESTEndpointsAreRemoved(t *testing.T) {
	e, admin := setupLoginApp(t)
	source := &upgradeAPISource{}
	service := upgrade.NewService(t.TempDir(), "v1.0.0", "linux", "arm64", nil, source)
	initUpgradeTestAPI(t, e, graphQLServices{upgrades: service})
	for _, route := range e.Router().Routes() {
		if strings.HasPrefix(route.Path, "/api/v1/upgrade") {
			t.Fatalf("legacy REST route remains registered: %s", route.Path)
		}
	}
	for _, path := range []string{"/api/v1/upgrade", "/api/v1/upgrade/check", "/api/v1/upgrade/download", "/api/v1/upgrade/upload", "/api/v1/upgrade/install"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			response := appRequest(e, method, path, `{"version":"v2.0.0"}`, testAuthorization(t, admin.PID))
			if response.Code == http.StatusOK && method == http.MethodGet {
				if !strings.Contains(response.Header().Get("Content-Type"), "text/html") {
					t.Fatalf("legacy route returned API data: %s %s", path, response.Body.String())
				}
			} else if response.Code != http.StatusNotFound && response.Code != http.StatusMethodNotAllowed {
				t.Fatalf("legacy route accepted %s %s: HTTP %d", method, path, response.Code)
			}
		}
	}
	if source.checked != 0 || source.opened != 0 || service.Status().CheckedAt != nil || service.Status().StagedPackage != nil {
		t.Fatal("legacy REST request caused an upgrade side effect")
	}
}

func TestUpgradeInstallErrorsDoNotExposeInternalDetails(t *testing.T) {
	e, admin := setupLoginApp(t)
	source := &upgradeAPISource{}
	service := upgrade.NewService(t.TempDir(), "v1.0.0", "linux", "arm64", nil, source)
	initUpgradeTestAPI(t, e, graphQLServices{upgrades: service})
	for _, test := range []struct {
		err  error
		code string
	}{
		{upgrade.ErrInstallUnsupported, "UPGRADE_UNSUPPORTED"}, {upgrade.ErrInvalidSelection, "UPGRADE_INVALID_SELECTION"},
		{upgrade.ErrNoStagedPackage, "UPGRADE_NOT_STAGED"}, {upgrade.ErrInstallPending, "UPGRADE_BUSY"},
		{upgrade.ErrMaintenanceBusy, "UPGRADE_BUSY"}, {upgrade.ErrInstallFailed, "UPGRADE_INSTALL_FAILED"},
	} {
		// Pass through a resolver so its upgrade-specific error wrapper is tested.
		source.err = errors.Join(test.err, errors.New("private installation path"))
		response := upgradeGraphQLRequest(t, e, upgradeCheckMutation, "CheckForUpdates", nil, testAuthorization(t, admin.PID))
		requireUpgradeError(t, response, test.code, "")
		if strings.Contains(response.Body.String(), "private installation path") {
			t.Fatalf("installation error mapping leaked details: %s", response.Body.String())
		}
	}
}

func TestMaintenanceBlocksNewWorkButAllowsAuthenticatedUpgradeStatus(t *testing.T) {
	e, admin := setupLoginApp(t)
	var gate maintenance.Gate
	reset := factoryreset.NewController(func() error { return nil }, &gate)
	source := &upgradeAPISource{}
	service := upgrade.NewService(t.TempDir(), "v1.0.0", "linux", "arm64", nil, source)
	installer := upgrade.NewInstaller(upgrade.InstallerOptions{StateDir: t.TempDir(), Gate: &gate, ValidateHost: func(context.Context) error { return nil }}, nil, "linux", "arm64")
	service.SetInstaller(installer)
	initUpgradeTestAPI(t, e, graphQLServices{upgrades: service, reset: reset, installer: installer})
	e.Use(maintenanceMiddleware(reset, installer))
	token := testAuthorization(t, admin.PID)
	for _, operation := range []string{"reset", "install"} {
		if operation == "reset" {
			if err := reset.Accept(); err != nil {
				t.Fatal(err)
			}
		} else if !gate.TryAcquire(operation) {
			t.Fatal("cannot acquire install gate")
		}
		for _, test := range []struct {
			query, name, field string
			variables          any
		}{
			{upgradeStatusQuery, "UpgradeStatus", "upgradeStatus", nil},
			{`query Progress { progress: upgradeStatus { currentVersion } }`, "Progress", "progress", nil},
			{`query Progress { ...Status } fragment Status on Query { progress: upgradeStatus { currentVersion } }`, "Progress", "progress", nil},
			{`query Progress { ... on Query { progress: upgradeStatus { currentVersion } } }`, "Progress", "progress", nil},
			{`query Progress($skip: Boolean!) { upgradeStatus { currentVersion } portalVersion @skip(if: $skip) }`, "Progress", "upgradeStatus", map[string]any{"skip": true}},
			{`query Progress($include: Boolean!) { upgradeStatus { currentVersion } portalVersion @include(if: $include) }`, "Progress", "upgradeStatus", map[string]any{"include": false}},
			{`query UpgradeStatus { portalVersion } query Progress { upgradeStatus { currentVersion } }`, "Progress", "upgradeStatus", nil},
			{`query Progress { upgradeStatus { currentVersion } } mutation Other { checkForUpdates { currentVersion } }`, "Progress", "upgradeStatus", nil},
		} {
			requireUpgradeSuccess(t, upgradeGraphQLRequest(t, e, test.query, test.name, test.variables, token), test.field)
		}
		for _, test := range []struct {
			query, name string
			variables   any
		}{
			{`query UpgradeStatus { portalVersion }`, "UpgradeStatus", nil},
			{`query Progress { upgradeStatus { currentVersion } portalVersion }`, "Progress", nil},
			{`query Progress { ...Mixed } fragment Mixed on Query { upgradeStatus { currentVersion } portalVersion }`, "Progress", nil},
			{`query Progress($skip: Boolean!) { upgradeStatus { currentVersion } portalVersion @skip(if: $skip) }`, "Progress", map[string]any{"skip": false}},
			{`query Progress($include: Boolean!) { upgradeStatus { currentVersion } portalVersion @include(if: $include) }`, "Progress", map[string]any{"include": true}},
			{`query Progress { upgradeStatus @skip(if: true) { currentVersion } }`, "Progress", nil},
			{upgradeCheckMutation, "CheckForUpdates", nil},
			{upgradeDownloadMutation, "DownloadUpgrade", map[string]any{"version": "v2.0.0"}},
			{upgradeInstallMutation, "InstallUpgrade", map[string]any{"version": "v2.0.0", "sha256": strings.Repeat("ab", 32)}},
			{`query Progress { upgradeStatus { currentVersion } } mutation UpgradeStatus { checkForUpdates { currentVersion } }`, "UpgradeStatus", nil},
			{`query Progress { upgradeStatus { currentVersion } } query Other { portalVersion }`, "Other", nil},
		} {
			requireUpgradeError(t, upgradeGraphQLRequest(t, e, test.query, test.name, test.variables, token), "MAINTENANCE", "in progress")
		}
		response := upgradeGraphQLRequest(t, e, upgradeStatusQuery, "UpgradeStatus", nil, "")
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("maintenance bypassed JWT: HTTP %d", response.Code)
		}
		requireUpgradeError(t, upgradeGraphQLRequest(t, e, upgradeStatusQuery, "UpgradeStatus", nil, testAuthorization(t, 99999)), "", auth.ErrUnauthorized.Error())
		for _, path := range []string{"/", "/api/v1/upgrade", "/api/v1/upgrade/check"} {
			response = appRequest(e, http.MethodGet, path, "", token)
			if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") != "5" {
				t.Fatalf("%s admitted non-GraphQL request %s: HTTP %d", operation, path, response.Code)
			}
		}
		if source.checked != 0 || source.opened != 0 {
			t.Fatal("maintenance admitted a release-source operation")
		}
		if operation == "reset" {
			reset.RetryAllowed()
		} else {
			gate.Release(operation)
		}
	}
}

type installAcknowledgementRecorder struct {
	*httptest.ResponseRecorder
	t         *testing.T
	installer *upgrade.Installer
	writeErr  error
	state     string
	flushes   int
}

func (r *installAcknowledgementRecorder) Write(data []byte) (int, error) {
	if r.writeErr != nil {
		return 0, r.writeErr
	}
	return r.ResponseRecorder.Write(data)
}

func (r *installAcknowledgementRecorder) Flush() {
	r.t.Helper()
	r.flushes++
	if _, err := os.Stat(filepath.Join(r.state, "pending.json")); err != nil {
		r.t.Errorf("flushed before durable transaction existed: %v", err)
	}
	if r.Code != http.StatusOK {
		r.t.Errorf("flushed status %d before durable acceptance", r.Code)
	}
	select {
	case <-r.installer.Requests():
		r.t.Error("installation scheduled before acknowledgement flush")
	default:
	}
	r.ResponseRecorder.Flush()
}

func TestUpgradeInstallAcknowledgesBeforeSchedulingEvenIfClientDisconnects(t *testing.T) {
	for _, disconnected := range []bool{false, true} {
		t.Run(map[bool]string{false: "acknowledged", true: "client disconnected"}[disconnected], func(t *testing.T) {
			e, admin := setupLoginApp(t)
			root := t.TempDir()
			// The numbered TempDir child can inherit group-write permission even
			// when testing's enclosing temporary directory is private.
			if err := os.Chmod(root, 0700); err != nil {
				t.Fatal(err)
			}
			previous := filepath.Join(root, "releases", "v1.0.0")
			unitDir := filepath.Join(root, "systemd")
			for _, path := range []string{previous, unitDir} {
				if err := os.MkdirAll(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink("releases/v1.0.0", filepath.Join(root, "current")); err != nil {
				t.Fatal(err)
			}
			// A minimal ELF header passes architecture validation; no test process
			// executes the candidate, and every system command is replaced below.
			payload := make([]byte, 64)
			copy(payload, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
			binary.LittleEndian.PutUint16(payload[16:18], 2)
			binary.LittleEndian.PutUint16(payload[18:20], 183)
			binary.LittleEndian.PutUint32(payload[20:24], 1)
			binary.LittleEndian.PutUint16(payload[52:54], 64)
			executable := filepath.Join(previous, "nanotail-portal")
			if err := os.WriteFile(executable, payload, 0755); err != nil {
				t.Fatal(err)
			}
			public, private, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			var archive bytes.Buffer
			if err := upgrade.WritePackage(&archive, upgrade.Manifest{Version: "v2.0.0", OS: "linux", Arch: "arm64"}, map[string]string{"payload/nanotail-portal": executable}, private); err != nil {
				t.Fatal(err)
			}
			state := filepath.Join(root, "state")
			installer := upgrade.NewInstaller(upgrade.InstallerOptions{
				RootDir: root, StateDir: state, Executable: executable, BootID: "test-boot",
				ServicePath: filepath.Join(unitDir, "nanotail-portal.service"), NginxPath: filepath.Join(root, "nanotail-portal.nginx"),
				ValidateHost: func(context.Context) error { return nil },
				Run:          func(context.Context, string, ...string) ([]byte, error) { return nil, nil },
			}, []ed25519.PublicKey{public}, "linux", "arm64")
			source := &upgradeAPISource{}
			source.setPackage("v2.0.0", archive.Bytes())
			service := upgrade.NewService(filepath.Join(root, "staging"), "v1.0.0", "linux", "arm64", []ed25519.PublicKey{public}, source)
			service.SetInstaller(installer)
			if _, err := service.Check(t.Context()); err != nil {
				t.Fatal(err)
			}
			status, err := service.Download(t.Context(), "v2.0.0")
			if err != nil {
				t.Fatal(err)
			}
			initUpgradeTestAPI(t, e, graphQLServices{upgrades: service, installer: installer})
			e.Use(maintenanceMiddleware(nil, installer))
			query := upgradeInstallMutation
			if disconnected {
				query = `mutation InstallUpgrade($version: String!, $sha256: String!) { ...Install checkForUpdates @skip(if: true) { currentVersion } } fragment Install on Mutation { committed: installUpgrade(version: $version, sha256: $sha256) { accepted installation { id version phase } } }`
			}
			body := upgradeGraphQLBody(t, query, "InstallUpgrade", map[string]any{"version": "v2.0.0", "sha256": status.StagedPackage.SHA256})
			request := httptest.NewRequest(http.MethodPost, "/api/v1/query", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", testAuthorization(t, admin.PID))
			recorder := &installAcknowledgementRecorder{ResponseRecorder: httptest.NewRecorder(), t: t, installer: installer, state: state}
			if disconnected {
				recorder.writeErr = errors.New("client connection closed")
			}
			e.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK || !recorder.Flushed || recorder.flushes != 1 {
				t.Fatalf("acceptance: %d %s", recorder.Code, recorder.Body.String())
			}
			if !disconnected && (!strings.Contains(recorder.Body.String(), `"accepted":true`) || !strings.Contains(recorder.Body.String(), `"phase":"prepared"`)) {
				t.Fatalf("acceptance response omitted durable operation: %s", recorder.Body.String())
			}
			select {
			case <-installer.Requests():
			default:
				t.Fatal("accepted installation was not scheduled")
			}
			if _, err := os.Stat(filepath.Join(state, "pending.json")); err != nil {
				t.Fatal("accepted installation was not durably recorded")
			}
			if current, err := filepath.EvalSymlinks(filepath.Join(root, "current")); err != nil || current != previous {
				t.Fatal("HTTP handler activated the release")
			}
			response := appRequest(e, http.MethodPost, "/api/v1/query", body, testAuthorization(t, admin.PID))
			requireUpgradeError(t, response, "MAINTENANCE", "in progress")
			select {
			case <-installer.Requests():
				t.Fatal("duplicate install scheduled a second operation")
			default:
			}
		})
	}
}

func upgradeAPIFixture(t *testing.T, key ed25519.PrivateKey, version string) []byte {
	t.Helper()
	binary := []byte("signed application payload")
	digest := sha256.Sum256(binary)
	manifest, err := json.Marshal(map[string]any{
		"format_version": 1, "application": "nanotail-portal", "version": version,
		"os": "linux", "arch": "arm64", "release_notes": "Signed test release",
		"files": map[string]any{"payload/nanotail-portal": map[string]any{"size": len(binary), "sha256": hex.EncodeToString(digest[:])}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	tw := tar.NewWriter(gz)
	for _, entry := range []struct {
		name string
		data []byte
	}{
		{"manifest.json", manifest},
		{"manifest.sig", ed25519.Sign(key, manifest)},
		{"payload/nanotail-portal", binary},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: "nanotail-portal/" + entry.name, Size: int64(len(entry.data)), Mode: 0644, Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

// Requests select a release version; only the configured source supplies bytes.
type upgradeAPISource struct {
	release *upgrade.Release
	data    []byte
	err     error
	checked int
	opened  int
}

func (s *upgradeAPISource) Latest(context.Context, string, string) (*upgrade.Release, error) {
	s.checked++
	return s.release, s.err
}

func (s *upgradeAPISource) Open(context.Context, upgrade.Release) (io.ReadCloser, error) {
	s.opened++
	return io.NopCloser(bytes.NewReader(s.data)), s.err
}

func (s *upgradeAPISource) setPackage(version string, data []byte) {
	s.release = &upgrade.Release{Version: version, Notes: "Test release", PublishedAt: "2026-10-01T00:00:00Z", PackageName: "nanotail-portal-" + version + "-linux-arm64.tar.gz", Size: int64(len(data))}
	s.data = data
}

func TestUpgradeDownloadVerifiesAndStagesWithoutInstalling(t *testing.T) {
	e, admin := setupLoginApp(t)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	source := &upgradeAPISource{}
	original := upgradeAPIFixture(t, private, "v2.0.0")
	source.setPackage("v2.0.0", original)
	directory := t.TempDir()
	service := upgrade.NewService(directory, "v1.0.0", "linux", "arm64", []ed25519.PublicKey{public}, source)
	initUpgradeTestAPI(t, e, graphQLServices{upgrades: service})
	token := testAuthorization(t, admin.PID)
	status := requireUpgradeSuccess(t, upgradeGraphQLRequest(t, e, upgradeCheckMutation, "CheckForUpdates", nil, token), "checkForUpdates")
	if !status.UpdateAvailable || status.LatestRelease == nil || status.LatestRelease.Version != "v2.0.0" {
		t.Fatalf("check result: %+v", status)
	}
	status = requireUpgradeSuccess(t, upgradeGraphQLRequest(t, e, upgradeDownloadMutation, "DownloadUpgrade", map[string]any{"version": "v2.0.0"}, token), "downloadUpgrade")
	digest := sha256.Sum256(original)
	if status.CurrentVersion != "v1.0.0" || status.InstallationSupported || status.StagedPackage == nil || status.StagedPackage.Version != "v2.0.0" || status.StagedPackage.SHA256 != hex.EncodeToString(digest[:]) || status.StagedPackage.Notes != "Signed test release" {
		t.Fatalf("download must only stage authenticated metadata: %+v", status)
	}
	if source.checked != 1 || source.opened != 1 {
		t.Fatalf("release source requests: check=%d download=%d", source.checked, source.opened)
	}
	reloaded := requireUpgradeSuccess(t, upgradeGraphQLRequest(t, e, upgradeStatusQuery, "UpgradeStatus", nil, token), "upgradeStatus")
	if reloaded.StagedPackage == nil || reloaded.StagedPackage.SHA256 != status.StagedPackage.SHA256 || source.checked != 1 || source.opened != 1 {
		t.Fatal("reading status changed or lost the verified package")
	}
	_, wrongKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{[]byte("unsigned arbitrary content"), upgradeAPIFixture(t, wrongKey, "v3.0.0")} {
		source.setPackage("v3.0.0", data)
		requireUpgradeSuccess(t, upgradeGraphQLRequest(t, e, upgradeCheckMutation, "CheckForUpdates", nil, token), "checkForUpdates")
		requireUpgradeError(t, upgradeGraphQLRequest(t, e, upgradeDownloadMutation, "DownloadUpgrade", map[string]any{"version": "v3.0.0"}, token), "UPGRADE_INVALID_PACKAGE", "verification failed")
		if service.Status().StagedPackage.Version != "v2.0.0" {
			t.Fatal("rejected download replaced verified metadata")
		}
		retained, err := os.ReadFile(filepath.Join(directory, upgrade.STAGED_PACKAGE_NAME))
		if err != nil || !bytes.Equal(retained, original) {
			t.Fatalf("rejected download replaced verified package bytes: %v", err)
		}
	}
}

func TestUpgradeReportsGitHubFailures(t *testing.T) {
	e, admin := setupLoginApp(t)
	source := &upgradeAPISource{}
	service := upgrade.NewService(t.TempDir(), "v1.0.0", "linux", "arm64", nil, source)
	initUpgradeTestAPI(t, e, graphQLServices{upgrades: service})
	for _, test := range []struct {
		err           error
		code, message string
	}{
		{upgrade.ErrGitHubUnavailable, "UPGRADE_GITHUB_UNAVAILABLE", "Unable to contact GitHub Releases"},
		{upgrade.ErrGitHubRateLimited, "UPGRADE_RATE_LIMITED", "request limit"},
		{upgrade.ErrNoCompatiblePackage, "UPGRADE_NO_COMPATIBLE_PACKAGE", "no compatible package"},
		{context.DeadlineExceeded, "UPGRADE_TIMEOUT", "timed out"},
		{errors.New("unknown private upstream failure"), "", "Internal Server Error"},
	} {
		source.err = errors.Join(test.err, errors.New("internal details must not be exposed"))
		response := upgradeGraphQLRequest(t, e, upgradeCheckMutation, "CheckForUpdates", nil, testAuthorization(t, admin.PID))
		requireUpgradeError(t, response, test.code, test.message)
		if strings.Contains(response.Body.String(), "internal details") || strings.Contains(response.Body.String(), "private") {
			t.Fatalf("source failure leaked: %s", response.Body.String())
		}
	}
}

func TestUpgradeInstallMustBeSoleCollectedMutation(t *testing.T) {
	e, admin := setupLoginApp(t)
	source := &upgradeAPISource{}
	service := upgrade.NewService(t.TempDir(), "v1.0.0", "linux", "arm64", nil, source)
	installer := upgrade.NewInstaller(upgrade.InstallerOptions{StateDir: t.TempDir(), ValidateHost: func(context.Context) error { return nil }}, nil, "linux", "arm64")
	service.SetInstaller(installer)
	initUpgradeTestAPI(t, e, graphQLServices{upgrades: service, installer: installer})
	token := testAuthorization(t, admin.PID)
	install := `installUpgrade(version:"v2.0.0", sha256:"` + strings.Repeat("ab", 32) + `") { accepted }`
	for _, query := range []string{
		`mutation Install { checkForUpdates { currentVersion } ` + install + ` }`,
		`mutation Install { ` + install + ` checkForUpdates { currentVersion } }`,
		`mutation Install { first:` + install + ` second:` + install + ` }`,
		`mutation Install { ...First ...Second } fragment First on Mutation { first:` + install + ` } fragment Second on Mutation { second:` + install + ` }`,
		`mutation Install { ... on Mutation { checkForUpdates { currentVersion } } ` + install + ` }`,
		`mutation Install { checkForUpdates @include(if:true) { currentVersion } ` + install + ` }`,
	} {
		requireUpgradeError(t, upgradeGraphQLRequest(t, e, query, "Install", nil, token), "UPGRADE_INSTALL_OPERATION", "separate mutation")
		if source.checked != 0 || source.opened != 0 || installer.Pending() {
			t.Fatal("mixed install performed work before rejection")
		}
		select {
		case <-installer.Requests():
			t.Fatal("rejected mixed install scheduled a restart")
		default:
		}
	}
	// Fragments/aliases and skipped fields are legal when exactly one install
	// remains; it reaches ordinary selection validation without scheduling work.
	for _, query := range []string{
		`mutation Install { accepted:` + install + ` checkForUpdates @skip(if:true) { currentVersion } }`,
		`mutation Install { ...Only } fragment Only on Mutation { accepted:` + install + ` }`,
		`mutation Install { ` + install + ` } mutation Other { checkForUpdates { currentVersion } }`,
	} {
		requireUpgradeError(t, upgradeGraphQLRequest(t, e, query, "Install", nil, token), "UPGRADE_NOT_STAGED", "Download and verify")
		select {
		case <-installer.Requests():
			t.Fatal("unsatisfied install selection scheduled a restart")
		default:
		}
	}
	query := `mutation Install { ` + install + ` } mutation Check { checkForUpdates { ` + upgradeStatusFields + ` } }`
	requireUpgradeSuccess(t, upgradeGraphQLRequest(t, e, query, "Check", nil, token), "checkForUpdates")
	if source.checked != 1 {
		t.Fatal("unselected install operation blocked the selected check")
	}
}
