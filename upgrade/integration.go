package upgrade

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const MAX_INTEGRATION_BYTES = 64 << 10

// IntegrationManager updates only the explicitly configured installed files.
// The caller serializes operations and persists the plan before Apply.
type IntegrationManager struct {
	ServicePath string
	NginxPath   string
	Run         func(ctx context.Context, name string, args ...string) error
}

// IntegrationPlan keeps references to retained release files, not configuration
// backups. Digests detect changes to either release after the plan was created.
type IntegrationPlan struct {
	PreviousRelease       string `json:"previousRelease"`
	NextRelease           string `json:"nextRelease"`
	ServiceChanged        bool   `json:"serviceChanged"`
	NginxChanged          bool   `json:"nginxChanged"`
	ServicePreviousSHA256 string `json:"servicePreviousSHA256,omitempty"`
	ServiceNextSHA256     string `json:"serviceNextSHA256,omitempty"`
	NginxPreviousSHA256   string `json:"nginxPreviousSHA256,omitempty"`
	NginxNextSHA256       string `json:"nginxNextSHA256,omitempty"`
}

func (m *IntegrationManager) Plan(previousRelease, nextRelease string) (*IntegrationPlan, error) {
	if !filepath.IsAbs(previousRelease) || !filepath.IsAbs(nextRelease) || filepath.Dir(previousRelease) != filepath.Dir(nextRelease) {
		return nil, errors.New("integration releases must be absolute sibling directories")
	}
	for _, directory := range []string{previousRelease, nextRelease} {
		info, err := os.Stat(directory)
		if err != nil {
			return nil, fmt.Errorf("inspect integration release: %w", err)
		}
		if !info.IsDir() {
			return nil, errors.New("integration release is not a directory")
		}
	}
	plan := &IntegrationPlan{PreviousRelease: previousRelease, NextRelease: nextRelease}
	for _, component := range m.components(plan) {
		candidate, exists, err := readIntegrationFile(nextRelease, component.relative)
		if err != nil {
			return nil, fmt.Errorf("read candidate %s integration: %w", component.name, err)
		}
		if !exists {
			continue
		}
		if component.name == "service" {
			if err := validatePortalService(candidate, integrationExecutable(previousRelease)); err != nil {
				return nil, err
			}
		}
		installed, exists, err := readInstalledIntegration(component.target)
		if err != nil {
			return nil, fmt.Errorf("read installed %s integration: %w", component.name, err)
		}
		if !exists {
			return nil, fmt.Errorf("installed %s integration is missing; cannot preserve its original state", component.name)
		}
		if bytes.Equal(installed, candidate) {
			continue
		}
		previous, exists, err := readIntegrationFile(previousRelease, component.relative)
		if err != nil {
			return nil, fmt.Errorf("read previous %s integration: %w", component.name, err)
		}
		if !exists || !bytes.Equal(previous, installed) {
			return nil, fmt.Errorf("previous release does not match the installed %s integration; cannot safely restore it", component.name)
		}
		*component.changed = true
		*component.previousDigest = integrationDigest(previous)
		*component.nextDigest = integrationDigest(candidate)
	}
	return plan, nil
}

func (m *IntegrationManager) Apply(ctx context.Context, plan *IntegrationPlan) error {
	if plan == nil {
		return errors.New("integration plan is missing")
	}
	for _, component := range m.components(plan) {
		if !*component.changed {
			continue
		}
		if err := m.applyComponent(ctx, plan, component, false); err != nil {
			return err
		}
	}
	return nil
}

// Restore retries commands for every changed component even when an earlier
// attempt already restored its file but failed before reloading the service.
func (m *IntegrationManager) Restore(ctx context.Context, plan *IntegrationPlan) error {
	if plan == nil {
		return errors.New("integration plan is missing")
	}
	var failures []error
	for _, component := range m.components(plan) {
		if !*component.changed {
			continue
		}
		if err := m.applyComponent(ctx, plan, component, true); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

type integrationComponent struct {
	name, relative, target     string
	changed                    *bool
	previousDigest, nextDigest *string
}

func (m *IntegrationManager) components(plan *IntegrationPlan) []integrationComponent {
	return []integrationComponent{
		{"service", "integration/nanotail-portal.service", m.ServicePath, &plan.ServiceChanged, &plan.ServicePreviousSHA256, &plan.ServiceNextSHA256},
		{"nginx", "integration/nanotail-portal.nginx", m.NginxPath, &plan.NginxChanged, &plan.NginxPreviousSHA256, &plan.NginxNextSHA256},
	}
}

func (m *IntegrationManager) applyComponent(ctx context.Context, plan *IntegrationPlan, component integrationComponent, restore bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	previous, previousExists, err := readIntegrationFile(plan.PreviousRelease, component.relative)
	if err != nil {
		return fmt.Errorf("read previous %s integration: %w", component.name, err)
	}
	if !previousExists || integrationDigest(previous) != *component.previousDigest {
		return fmt.Errorf("retained %s integration changed after planning", component.name)
	}
	installed, exists, err := readInstalledIntegration(component.target)
	if err != nil {
		return fmt.Errorf("read installed %s integration: %w", component.name, err)
	}
	if !exists || (integrationDigest(installed) != *component.previousDigest && integrationDigest(installed) != *component.nextDigest) {
		return fmt.Errorf("installed %s integration changed outside this upgrade", component.name)
	}
	data, release := previous, plan.PreviousRelease
	if !restore {
		next, nextExists, err := readIntegrationFile(plan.NextRelease, component.relative)
		if err != nil {
			return fmt.Errorf("read next %s integration: %w", component.name, err)
		}
		if !nextExists || integrationDigest(next) != *component.nextDigest {
			return fmt.Errorf("retained %s integration changed after planning", component.name)
		}
		data, release = next, plan.NextRelease
	}
	if component.name == "service" {
		if !restore {
			if err := validatePortalService(data, integrationExecutable(plan.PreviousRelease)); err != nil {
				return err
			}
		}
		if err := m.run(ctx, "systemd-analyze", "verify", filepath.Join(release, component.relative)); err != nil {
			return fmt.Errorf("validate service integration: %w", err)
		}
	}
	if !bytes.Equal(installed, data) {
		if err := writeInstalledIntegration(ctx, component.target, data); err != nil {
			return fmt.Errorf("write %s integration: %w", component.name, err)
		}
	}
	if component.name == "service" {
		if err := m.run(ctx, "systemctl", "daemon-reload"); err != nil {
			return fmt.Errorf("reload service definitions: %w", err)
		}
		return nil
	}
	if err := m.run(ctx, "nginx", "-t"); err != nil {
		return fmt.Errorf("validate nginx integration: %w", err)
	}
	if err := m.run(ctx, "systemctl", "reload", "nginx"); err != nil {
		return fmt.Errorf("reload nginx integration: %w", err)
	}
	return nil
}

func (m *IntegrationManager) run(ctx context.Context, name string, args ...string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.Run != nil {
		return m.Run(ctx, name, args...)
	}
	return exec.CommandContext(ctx, name, args...).Run()
}

func integrationExecutable(previousRelease string) string {
	return filepath.Join(filepath.Dir(filepath.Dir(previousRelease)), "current", APPLICATION)
}

// Signed units may contain other systemd directives. Check just the activation
// contract here; systemd-analyze validates the complete unit before it is used.
func validatePortalService(data []byte, executable string) error {
	section, serviceType, restart := "", "simple", ""
	remaining, pending := false, ""
	var prevent []string
	var starts []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), MAX_INTEGRATION_BYTES+1)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		line = pending + line
		pending = ""
		if strings.HasSuffix(line, "\\") {
			pending = strings.TrimSuffix(line, "\\") + " "
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = line
			continue
		}
		if section != "[Service]" {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		switch name {
		case "Type":
			serviceType = value
			if value == "" {
				serviceType = "simple"
			}
		case "Restart":
			restart = value
		case "RemainAfterExit":
			remaining = value != "" && value != "no" && value != "false" && value != "off" && value != "0"
		case "RestartPreventExitStatus":
			if value == "" {
				prevent = nil
			} else {
				prevent = append(prevent, strings.Fields(value)...)
			}
		case "ExecStart":
			if value == "" {
				starts = nil
			} else {
				starts = append(starts, value)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("parse service integration: %w", err)
	}
	if pending != "" {
		return errors.New("service integration has an unfinished continuation line")
	}
	if serviceType != "simple" && serviceType != "exec" {
		return errors.New("service integration must use Type=simple or Type=exec")
	}
	if restart != "always" || len(starts) != 1 {
		return errors.New("service integration must use Restart=always and one ExecStart")
	}
	if remaining {
		return errors.New("service integration must stop when the portal exits")
	}
	for _, status := range prevent {
		if status == "0" || status == "SUCCESS" {
			return errors.New("service integration must restart after a successful exit")
		}
	}
	command := starts[0]
	if command[0] == '"' || command[0] == '\'' {
		end := strings.IndexByte(command[1:], command[0])
		if end < 0 || (end+2 < len(command) && command[end+2] != ' ' && command[end+2] != '\t') {
			return errors.New("service integration has an invalid ExecStart executable")
		}
		command = command[1 : end+1]
	} else {
		command = strings.Fields(command)[0]
	}
	if command != executable {
		return errors.New("service integration must start the portal through the current release symlink")
	}
	return nil
}

func integrationDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func readInstalledIntegration(path string) ([]byte, bool, error) {
	if !filepath.IsAbs(path) {
		return nil, false, errors.New("installed integration path must be absolute")
	}
	return readIntegrationFile(filepath.Dir(path), filepath.Base(path))
}

func readIntegrationFile(directory, name string) ([]byte, bool, error) {
	root, err := os.OpenRoot(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer root.Close()
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() || info.Size() > MAX_INTEGRATION_BYTES {
		return nil, false, errors.New("integration must be a regular file of at most 64 KiB")
	}
	data, err := io.ReadAll(io.LimitReader(file, MAX_INTEGRATION_BYTES+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > MAX_INTEGRATION_BYTES {
		return nil, false, errors.New("integration exceeds 64 KiB")
	}
	return data, true, nil
}

func writeInstalledIntegration(ctx context.Context, path string, data []byte) error {
	if !filepath.IsAbs(path) {
		return errors.New("installed integration path must be absolute")
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer root.Close()
	name := filepath.Base(path)
	info, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("installed integration must be a regular file")
	}
	temporary := ".nanotail-integration-" + rand.Text()
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(temporary)
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Chmod(0644); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := root.Rename(temporary, name); err != nil {
		return err
	}
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
