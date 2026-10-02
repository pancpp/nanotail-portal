package upgrade

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type integrationFixture struct {
	manager                *IntegrationManager
	previous, next         string
	oldService, newService []byte
	oldNginx, newNginx     []byte
	commands               []string
}

func newIntegrationFixture(t *testing.T) *integrationFixture {
	t.Helper()
	root := t.TempDir()
	f := &integrationFixture{
		previous: filepath.Join(root, "releases", "1.0.0"), next: filepath.Join(root, "releases", "2.0.0"),
		oldNginx: []byte("server { listen 80; }\n"), newNginx: []byte("server { listen 8080; }\n"),
	}
	f.oldService = []byte("[Unit]\nDescription=previous\n[Service]\nType=simple\nExecStart=" + filepath.Join(root, "current", APPLICATION) + " --data-dir " + filepath.Join(root, "data") + "\nRestart=always\n")
	f.newService = bytes.Replace(f.oldService, []byte("Description=previous"), []byte("Description=next"), 1)
	f.manager = &IntegrationManager{
		ServicePath: filepath.Join(root, "installed", "nanotail-portal.service"),
		NginxPath:   filepath.Join(root, "installed", "nanotail-portal.nginx"),
		Run: func(_ context.Context, name string, args ...string) error {
			f.commands = append(f.commands, name+" "+strings.Join(args, " "))
			return nil
		},
	}
	for path, data := range map[string][]byte{
		filepath.Join(f.previous, "integration", "nanotail-portal.service"): f.oldService,
		filepath.Join(f.previous, "integration", "nanotail-portal.nginx"):   f.oldNginx,
		filepath.Join(f.next, "integration", "nanotail-portal.service"):     f.newService,
		filepath.Join(f.next, "integration", "nanotail-portal.nginx"):       f.newNginx,
		f.manager.ServicePath: f.oldService, f.manager.NginxPath: f.oldNginx,
	} {
		writeIntegrationFixture(t, path, data)
	}
	return f
}

func writeIntegrationFixture(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func assertIntegrationFile(t *testing.T, path string, want []byte, mode os.FileMode) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, want) {
		t.Fatalf("integration bytes differ at %s: %q, %v", path, data, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != mode {
		t.Fatalf("integration mode at %s: %v, %v", path, info, err)
	}
}

func TestIntegrationUnchangedAndAbsentFilesDoNothing(t *testing.T) {
	for _, absent := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchanged", true: "absent"}[absent], func(t *testing.T) {
			f := newIntegrationFixture(t)
			for name, old := range map[string][]byte{"nanotail-portal.service": f.oldService, "nanotail-portal.nginx": f.oldNginx} {
				path := filepath.Join(f.next, "integration", name)
				if absent {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				} else {
					writeIntegrationFixture(t, path, old)
				}
			}
			before, err := os.Stat(f.manager.ServicePath)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := f.manager.Plan(f.previous, f.next)
			if err != nil || plan.ServiceChanged || plan.NginxChanged {
				t.Fatalf("unexpected plan: %+v, %v", plan, err)
			}
			if err := f.manager.Apply(t.Context(), plan); err != nil {
				t.Fatal(err)
			}
			if err := f.manager.Restore(t.Context(), plan); err != nil {
				t.Fatal(err)
			}
			if len(f.commands) != 0 {
				t.Fatalf("unchanged integrations ran commands: %v", f.commands)
			}
			after, err := os.Stat(f.manager.ServicePath)
			if err != nil || !os.SameFile(before, after) {
				t.Fatal("unchanged service integration was replaced")
			}
			assertIntegrationFile(t, f.manager.ServicePath, f.oldService, 0600)
			assertIntegrationFile(t, f.manager.NginxPath, f.oldNginx, 0600)
		})
	}
}

func TestIntegrationAppliesAndRestoresOnlyChangedComponents(t *testing.T) {
	for _, flags := range []struct{ service, nginx bool }{{true, false}, {false, true}, {true, true}} {
		f := newIntegrationFixture(t)
		if !flags.service {
			writeIntegrationFixture(t, filepath.Join(f.next, "integration", "nanotail-portal.service"), f.oldService)
		}
		if !flags.nginx {
			writeIntegrationFixture(t, filepath.Join(f.next, "integration", "nanotail-portal.nginx"), f.oldNginx)
		}
		plan, err := f.manager.Plan(f.previous, f.next)
		if err != nil || plan.ServiceChanged != flags.service || plan.NginxChanged != flags.nginx {
			t.Fatalf("unexpected plan: %+v, %v", plan, err)
		}
		// A persisted plan must support recovery in a new process.
		encoded, err := json.Marshal(plan)
		if err != nil {
			t.Fatal(err)
		}
		var loaded IntegrationPlan
		if err := json.Unmarshal(encoded, &loaded); err != nil {
			t.Fatal(err)
		}
		if err := f.manager.Apply(t.Context(), &loaded); err != nil {
			t.Fatal(err)
		}
		var expected []string
		if flags.service {
			expected = append(expected, "systemd-analyze verify "+filepath.Join(f.next, "integration", "nanotail-portal.service"), "systemctl daemon-reload")
			assertIntegrationFile(t, f.manager.ServicePath, f.newService, 0644)
		}
		if flags.nginx {
			expected = append(expected, "nginx -t", "systemctl reload nginx")
			assertIntegrationFile(t, f.manager.NginxPath, f.newNginx, 0644)
		}
		if !reflect.DeepEqual(f.commands, expected) {
			t.Fatalf("wrong apply commands: %v, want %v", f.commands, expected)
		}
		f.commands = nil
		// Restoring needs only the retained previous release, even if the failed
		// candidate was removed before recovery resumed.
		if err := os.RemoveAll(f.next); err != nil {
			t.Fatal(err)
		}
		if err := f.manager.Restore(t.Context(), &loaded); err != nil {
			t.Fatal(err)
		}
		expected = nil
		serviceMode, nginxMode := os.FileMode(0600), os.FileMode(0600)
		if flags.service {
			expected = append(expected, "systemd-analyze verify "+filepath.Join(f.previous, "integration", "nanotail-portal.service"), "systemctl daemon-reload")
			serviceMode = 0644
		}
		if flags.nginx {
			expected = append(expected, "nginx -t", "systemctl reload nginx")
			nginxMode = 0644
		}
		if !reflect.DeepEqual(f.commands, expected) {
			t.Fatalf("wrong restore commands: %v, want %v", f.commands, expected)
		}
		assertIntegrationFile(t, f.manager.ServicePath, f.oldService, serviceMode)
		assertIntegrationFile(t, f.manager.NginxPath, f.oldNginx, nginxMode)
		files, err := os.ReadDir(filepath.Dir(f.manager.ServicePath))
		if err != nil || len(files) != 2 {
			t.Fatalf("temporary configuration files remain: %v, %v", files, err)
		}
	}
}

func TestIntegrationNginxValidationFailureCanBeRestoredAndRetried(t *testing.T) {
	f := newIntegrationFixture(t)
	plan, err := f.manager.Plan(f.previous, f.next)
	if err != nil {
		t.Fatal(err)
	}
	invalid := errors.New("invalid nginx configuration")
	reloadFailed := errors.New("nginx reload failed")
	failReload := false
	f.manager.Run = func(_ context.Context, name string, args ...string) error {
		f.commands = append(f.commands, name+" "+strings.Join(args, " "))
		current, err := os.ReadFile(f.manager.NginxPath)
		if err != nil {
			return err
		}
		if name == "nginx" && bytes.Equal(current, f.newNginx) {
			return invalid
		}
		if failReload && name == "systemctl" && reflect.DeepEqual(args, []string{"reload", "nginx"}) {
			return reloadFailed
		}
		return nil
	}
	if err := f.manager.Apply(t.Context(), plan); !errors.Is(err, invalid) {
		t.Fatalf("invalid nginx was not rejected: %v", err)
	}
	assertIntegrationFile(t, f.manager.NginxPath, f.newNginx, 0644)
	failReload = true
	if err := f.manager.Restore(t.Context(), plan); !errors.Is(err, reloadFailed) {
		t.Fatalf("restore did not expose reload failure: %v", err)
	}
	assertIntegrationFile(t, f.manager.NginxPath, f.oldNginx, 0644)
	f.commands = nil
	failReload = false
	if err := f.manager.Restore(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if len(f.commands) != 4 || f.commands[3] != "systemctl reload nginx" {
		t.Fatalf("retry skipped reload of already-restored configuration: %v", f.commands)
	}
	assertIntegrationFile(t, f.manager.ServicePath, f.oldService, 0644)
}

func TestIntegrationPlanRequiresOriginalFilesAndRejectsSymlinks(t *testing.T) {
	for _, mutation := range []string{"missing previous", "different previous", "missing installed", "symlink installed", "symlink candidate"} {
		t.Run(mutation, func(t *testing.T) {
			f := newIntegrationFixture(t)
			previous := filepath.Join(f.previous, "integration", "nanotail-portal.service")
			candidate := filepath.Join(f.next, "integration", "nanotail-portal.service")
			switch mutation {
			case "missing previous":
				if err := os.Remove(previous); err != nil {
					t.Fatal(err)
				}
			case "different previous":
				writeIntegrationFixture(t, previous, []byte("different"))
			case "missing installed":
				if err := os.Remove(f.manager.ServicePath); err != nil {
					t.Fatal(err)
				}
			case "symlink installed":
				if err := os.Remove(f.manager.ServicePath); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(previous, f.manager.ServicePath); err != nil {
					t.Fatal(err)
				}
			case "symlink candidate":
				if err := os.Remove(candidate); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(previous, candidate); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.manager.Plan(f.previous, f.next); err == nil {
				t.Fatal("unsafe integration plan accepted")
			}
			if len(f.commands) != 0 {
				t.Fatalf("planning changed system state: %v", f.commands)
			}
		})
	}
}

func TestIntegrationDetectsChangesAfterPlanning(t *testing.T) {
	for _, changed := range []string{"installed", "previous", "candidate"} {
		t.Run(changed, func(t *testing.T) {
			f := newIntegrationFixture(t)
			plan, err := f.manager.Plan(f.previous, f.next)
			if err != nil {
				t.Fatal(err)
			}
			path := f.manager.ServicePath
			if changed == "previous" {
				path = filepath.Join(f.previous, "integration", "nanotail-portal.service")
			}
			if changed == "candidate" {
				path = filepath.Join(f.next, "integration", "nanotail-portal.service")
			}
			writeIntegrationFixture(t, path, []byte("modified outside upgrade"))
			if err := f.manager.Apply(t.Context(), plan); err == nil {
				t.Fatal("changed integration accepted")
			}
			if len(f.commands) != 0 {
				t.Fatalf("changed integration ran commands: %v", f.commands)
			}
		})
	}
}

func TestIntegrationServiceContract(t *testing.T) {
	f := newIntegrationFixture(t)
	executable := integrationExecutable(f.previous)
	for _, test := range []struct {
		name, text string
		valid      bool
	}{
		{"simple", string(f.newService), true},
		{"exec", strings.ReplaceAll(string(f.newService), "Type=simple", "Type=exec"), true},
		{"default type", strings.ReplaceAll(string(f.newService), "Type=simple\n", ""), true},
		{"quoted path", strings.ReplaceAll(string(f.newService), executable, `"`+executable+`"`), true},
		{"arguments continuation", strings.ReplaceAll(string(f.newService), " --data-dir ", " \\\n --data-dir "), true},
		{"extra directives", string(f.newService) + "ExecStartPre=/bin/true\n", true},
		{"notify", strings.ReplaceAll(string(f.newService), "Type=simple", "Type=notify"), false},
		{"wrong executable", strings.ReplaceAll(string(f.newService), executable, "/bin/true"), false},
		{"wrong restart", strings.ReplaceAll(string(f.newService), "Restart=always", "Restart=on-failure"), false},
		{"multiple starts", string(f.newService) + "ExecStart=/bin/true\n", false},
		{"remain active", string(f.newService) + "RemainAfterExit=yes\n", false},
		{"prevent clean restart", string(f.newService) + "RestartPreventExitStatus=0\n", false},
		{"disguised restart", strings.ReplaceAll(string(f.newService), "Restart=always", "Restart=on-failure\nEnvironment=TEST=value \\\nRestart=always"), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validatePortalService([]byte(test.text), executable)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v, got %v", test.valid, err)
			}
		})
	}
}

func TestIntegrationServiceValidationAndCancellationDoNotWrite(t *testing.T) {
	f := newIntegrationFixture(t)
	plan, err := f.manager.Plan(f.previous, f.next)
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("systemd verification failed")
	f.manager.Run = func(context.Context, string, ...string) error { return want }
	if err := f.manager.Apply(t.Context(), plan); !errors.Is(err, want) {
		t.Fatalf("verification failure: %v", err)
	}
	assertIntegrationFile(t, f.manager.ServicePath, f.oldService, 0600)
	assertIntegrationFile(t, f.manager.NginxPath, f.oldNginx, 0600)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := f.manager.Apply(ctx, plan); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled apply: %v", err)
	}
}
