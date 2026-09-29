package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Exercise the actual executable/re-exec boundary. Everything, including the
// fake Tailscale binary and SQLite database, lives in a disposable installation.
func TestFactoryResetProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess integration test")
	}
	binary := filepath.Join(t.TempDir(), "nanotail-portal")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	for _, failLogout := range []bool{false, true} {
		t.Run(fmt.Sprintf("logout_failure_%v", failLogout), func(t *testing.T) {
			var defaultListener net.Listener
			if !failLogout {
				var err error
				defaultListener, err = net.Listen("tcp", "127.0.0.1:7080")
				if err != nil {
					t.Skip("default reset listener is already in use")
				}
				t.Cleanup(func() { _ = defaultListener.Close() })
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := listener.Addr().String()
			listener.Close()
			dir := t.TempDir()
			fake := filepath.Join(dir, "fake-tailscale")
			const script = `#!/bin/sh
case "$1" in
  logout)
    printf 'logout\n' >> "$NANOTAIL_TEST_CALLS"
    if [ "$NANOTAIL_TEST_LOGOUT_FAIL" = "true" ]; then exit 1; fi
    ;;
  status) printf '{"BackendState":"NeedsLogin","HaveNodeKey":false}\n' ;;
  *) exit 1 ;;
esac
`
			if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			configuration := fmt.Sprintf("http_listen_addr: %q\ntailscale_binary: %q\nenable_console_log: false\n", address, fake)
			if err := os.WriteFile(filepath.Join(dir, "nanotail-portal.yml"), []byte(configuration), 0600); err != nil {
				t.Fatal(err)
			}
			output, err := os.Create(filepath.Join(dir, "test-output"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { output.Close() })
			startProcess := func() (*exec.Cmd, chan error) {
				t.Helper()
				cmd := exec.Command(binary)
				cmd.Dir = dir
				cmd.Env = append(os.Environ(), "NANOTAIL_TEST_CALLS="+filepath.Join(dir, "commands"), fmt.Sprintf("NANOTAIL_TEST_LOGOUT_FAIL=%v", failLogout))
				// This survives factory reset/re-exec and prevents integration
				// tests from touching physical LEDs or the host's real VPN.
				cmd.Env = append(cmd.Env, "NANOTAIL_VPN_TRAFFIC_LED=false", "NANOTAIL_TAILSCALE_BINARY="+fake)
				cmd.Stdout, cmd.Stderr = output, output
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- cmd.Wait() }()
				return cmd, done
			}
			cmd, done := startProcess()
			t.Cleanup(func() {
				_ = cmd.Process.Signal(syscall.SIGTERM)
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					_ = cmd.Process.Kill()
					<-done
				}
				if t.Failed() {
					data, _ := os.ReadFile(filepath.Join(dir, "test-output"))
					t.Logf("subprocess output:\n%s", data)
				}
			})
			client := &http.Client{Timeout: time.Second}
			request := func(base, path, token, body string) (int, []byte) {
				req, err := http.NewRequest(http.MethodPost, "http://"+base+path, strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Content-Type", "application/json")
				if token != "" {
					req.Header.Set("Authorization", "Bearer "+token)
				}
				res, err := client.Do(req)
				if err != nil {
					return 0, nil
				}
				defer res.Body.Close()
				data, _ := io.ReadAll(res.Body)
				return res.StatusCode, data
			}
			waitLogin := func(base, password string) string {
				t.Helper()
				body, _ := json.Marshal(map[string]string{"username": "admin", "password": password})
				for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
					status, data := request(base, "/api/login", "", string(body))
					var result struct{ Token string }
					if status == 200 && json.Unmarshal(data, &result) == nil && result.Token != "" {
						return result.Token
					}
					time.Sleep(25 * time.Millisecond)
				}
				t.Fatalf("portal did not accept expected credentials on %s", base)
				return ""
			}
			checkResetDiagnostics := func(stages ...string) {
				t.Helper()
				data, err := os.ReadFile(filepath.Join(dir, "logs", "nanotail-portal.log"))
				if err != nil {
					t.Fatal(err)
				}
				remaining := string(data)
				for _, stage := range stages {
					index := strings.Index(remaining, "[factory-reset] "+stage)
					if index < 0 {
						t.Fatalf("missing ordered reset diagnostic %q in the normal log file: %s", stage, data)
					}
					remaining = remaining[index+len("[factory-reset] ")+len(stage):]
				}
			}
			token := waitLogin(address, "admin")
			oldKey, err := os.ReadFile(filepath.Join(dir, "nanotail-portal.key"))
			if err != nil || len(oldKey) == 0 {
				t.Fatal("startup did not persist a signing key")
			}
			// A normal process restart must keep existing browser sessions valid.
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					done <- err
					t.Fatal(err)
				}
				done <- nil // Keep cleanup nonblocking if the next launch fails.
			case <-time.After(5 * time.Second):
				t.Fatal("normal shutdown timed out")
			}
			cmd, done = startProcess()
			waitLogin(address, "admin")
			storedKey, err := os.ReadFile(filepath.Join(dir, "nanotail-portal.key"))
			if err != nil || !bytes.Equal(storedKey, oldKey) {
				t.Fatal("normal restart replaced the signing key")
			}
			// This deliberately uses the JWT from before the normal restart.
			status, data := request(address, "/api/v1/query", token, `{"query":"mutation { changePassword(passwords: { oldpassword: \"admin\", newpassword: \"changed-password\" }) }"}`)
			if status != 200 || !bytes.Contains(data, []byte(`"changePassword":true`)) {
				t.Fatalf("password setup failed: %d %s", status, data)
			}
			status, data = request(address, "/api/v1/query", token, `{"query":"mutation { setTailscaleCredential(credential: { clientId: \"test-client\", clientSecret: \"test-secret\" }) }"}`)
			if status != 200 || !bytes.Contains(data, []byte(`"setTailscaleCredential":true`)) {
				t.Fatalf("credential setup failed: %d %s", status, data)
			}
			if err := os.WriteFile(filepath.Join(dir, "logs", "old.log"), []byte("old logs"), 0600); err != nil {
				t.Fatal(err)
			}
			if defaultListener != nil {
				defaultListener.Close()
			}
			status, data = request(address, "/api/v1/factory-reset", token, `{"confirmed":true,"confirmation":"RESET","password":"changed-password"}`)
			if status != 202 || !bytes.Contains(data, []byte(`"accepted":true`)) {
				t.Fatalf("reset not acknowledged: %d %s", status, data)
			}
			// Wait for the fake logout before checking the restarted server.
			for deadline := time.Now().Add(10 * time.Second); ; {
				if data, _ := os.ReadFile(filepath.Join(dir, "commands")); strings.Contains(string(data), "logout") {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("Tailscale logout was not called")
				}
				time.Sleep(25 * time.Millisecond)
			}
			if failLogout {
				waitLogin(address, "changed-password")
				storedKey, err := os.ReadFile(filepath.Join(dir, "nanotail-portal.key"))
				if err != nil || !bytes.Equal(storedKey, oldKey) {
					t.Fatal("failed reset changed the signing key")
				}
				status, _ := request(address, "/api/v1/query", token, `{"query":"query { __typename }"}`)
				if status != 200 {
					t.Fatal("failed reset revoked existing JWTs")
				}
				data, _ := os.ReadFile(filepath.Join(dir, "nanotail-portal.yml"))
				if string(data) != configuration {
					t.Fatal("failed logout cleared config")
				}
				if _, err := os.Stat(filepath.Join(dir, "logs", "old.log")); err != nil {
					t.Fatal("failed logout cleared logs")
				}
				checkResetDiagnostics("starting reset", "portal services stopped", "aborted before clearing any files", "portal ready")
				return
			}
			newToken := waitLogin("127.0.0.1:7080", "admin")
			newKey, err := os.ReadFile(filepath.Join(dir, "nanotail-portal.key"))
			if err != nil || len(newKey) == 0 || bytes.Equal(oldKey, newKey) {
				t.Fatal("successful reset did not replace the signing key")
			}
			for _, path := range []string{"/api/v1/query", "/api/v1/factory-reset"} {
				status, _ := request("127.0.0.1:7080", path, token, `{"query":"query { __typename }"}`)
				if status != 401 {
					t.Fatalf("old JWT was accepted after reset by %s: status=%d", path, status)
				}
			}
			if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
				t.Fatal("restart did not preserve process PID")
			}
			data, _ = os.ReadFile(filepath.Join(dir, "nanotail-portal.yml"))
			if len(data) != 0 {
				t.Fatal("config was not cleared")
			}
			if _, err := os.Stat(filepath.Join(dir, "logs", "old.log")); !os.IsNotExist(err) {
				t.Fatal("old log survived reset")
			}
			if _, err := os.Stat(filepath.Join(dir, ".nanotail-reset-pending")); !os.IsNotExist(err) {
				t.Fatal("reset marker survived successful startup")
			}
			// Verify that the saved OAuth client was erased.
			status, data = request("127.0.0.1:7080", "/api/v1/query", newToken, `{"query":"query { tailscaleClient { clientId } }"}`)
			if status != 200 || !bytes.Contains(data, []byte(`"tailscaleClient":null`)) {
				t.Fatalf("credentials survived reset: %d %s", status, data)
			}
			status, _ = request("127.0.0.1:7080", "/api/login", "", `{"username":"admin","password":"changed-password"}`)
			if status != 401 {
				t.Fatal("old administrator password survived reset")
			}
			checkResetDiagnostics("data cleanup complete", "portal ready")
		})
	}
}
