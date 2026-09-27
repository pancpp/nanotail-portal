package tailscale

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"

	"github.com/tailscale/hujson"
)

var (
	ErrPeerRelayCredentials    = errors.New("Unable to grant peer relay access. Save OAuth credentials from this device's tailnet with policy_file write permission, then reload peer relay settings and retry")
	ErrPeerRelayIdentity       = errors.New("Unable to identify this device's tailnet and Tailscale IP for the relay grant. Check Tailscale sign-in, reload peer relay settings, and retry")
	ErrPeerRelayPolicy         = errors.New("Unable to confirm the peer relay access policy. Check the Tailscale admin console and connectivity, then reload peer relay settings before retrying")
	ErrPeerRelayPolicyConflict = errors.New("The tailnet policy changed while saving peer relay access. Reload peer relay settings and retry")
)

const relayCapability = "tailscale.com/cap/relay"

type peerRelayPolicy struct {
	load func(context.Context) (OAuthCredentials, error)
	api  *oauthRoutes
}

// WithPeerRelayPolicy installs the policy writer before the client is shared.
// Tokens and cloud writes use the same mutation lock as local settings and
// credential changes. Merely reading relay settings never changes policy.
func (c *Client) WithPeerRelayPolicy(load func(context.Context) (OAuthCredentials, error), client *http.Client) *Client {
	c.relayPolicy = &peerRelayPolicy{load: load, api: newOAuthRoutes(client)}
	return c
}

type relayPolicyTarget struct{ tailnet, deviceID, ip string }

func peerRelayTarget(status Status) (relayPolicyTarget, error) {
	if !status.HaveNodeKey || status.Self == nil || !approvalDeviceID.MatchString(status.Self.ID) ||
		status.CurrentTailnet == nil || strings.TrimSpace(status.CurrentTailnet.Name) == "" || status.CurrentTailnet.Name == "-" ||
		(status.BackendState != "Running" && status.BackendState != "Stopped") {
		return relayPolicyTarget{}, ErrPeerRelayIdentity
	}
	ips := status.Self.IPs
	if len(ips) == 0 {
		ips = status.IPs
	}
	var selected netip.Addr
	for _, value := range ips {
		ip, err := netip.ParseAddr(value)
		if err != nil || !ip.IsGlobalUnicast() || ip.Is4In6() || ip.Zone() != "" {
			continue
		}
		if !selected.IsValid() || ip.Is4() {
			selected = ip
		}
		if ip.Is4() {
			break
		}
	}
	if !selected.IsValid() {
		return relayPolicyTarget{}, ErrPeerRelayIdentity
	}
	return relayPolicyTarget{status.CurrentTailnet.Name, status.Self.ID, selected.String()}, nil
}

func (c *Client) ensurePeerRelayPolicy(ctx context.Context) error {
	p := c.relayPolicy
	if p == nil || p.load == nil {
		return ErrPeerRelayCredentials
	}
	credential, err := p.load(ctx)
	if err != nil || credential.ClientID == "" || credential.ClientSecret == "" {
		p.api.forget()
		return ErrPeerRelayCredentials
	}
	status, err := c.Status(ctx)
	if err != nil {
		return ErrPeerRelayIdentity
	}
	target, err := peerRelayTarget(status)
	if err != nil {
		return err
	}
	// Detect sign-in or credential changes outside this process before writing
	// policy and again before enabling the local listener.
	current := func() bool {
		latestCredential, err := p.load(ctx)
		if err != nil || latestCredential != credential {
			return false
		}
		latestStatus, err := c.Status(ctx)
		if err != nil {
			return false
		}
		latest, err := peerRelayTarget(latestStatus)
		return err == nil && latest == target
	}
	if err := p.ensure(ctx, credential, target, current); err != nil {
		return err
	}
	if !current() {
		return ErrPeerRelayIdentity
	}
	return nil
}

func (p *peerRelayPolicy) request(ctx context.Context, method, path, token, etag string, body []byte) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, method, tailscaleAPI+path, bytes.NewReader(body))
	if err != nil {
		return nil, "", ErrPeerRelayPolicy
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/hujson")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/hujson")
		req.Header.Set("If-Match", etag)
	}
	response, err := p.api.http.Do(req)
	if err != nil {
		return nil, "", ErrPeerRelayPolicy
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusUnauthorized:
		p.api.forget()
		return nil, "", ErrPeerRelayCredentials
	case http.StatusForbidden, http.StatusNotFound:
		return nil, "", ErrPeerRelayCredentials
	case http.StatusPreconditionFailed, http.StatusConflict:
		return nil, "", ErrPeerRelayPolicyConflict
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", ErrPeerRelayPolicy
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, "", ErrPeerRelayPolicy
	}
	return data, response.Header.Get("ETag"), nil
}

func (p *peerRelayPolicy) ensure(ctx context.Context, credential OAuthCredentials, target relayPolicyTarget, current func() bool) error {
	token, err := p.api.accessTokenForScope(ctx, credential, "policy_file")
	if err != nil {
		if errors.Is(err, errApprovalCredentials) || errors.Is(err, errApprovalPermission) {
			return ErrPeerRelayCredentials
		}
		return ErrPeerRelayPolicy
	}
	// Use the daemon's actual tailnet, never the token's default tailnet ("-").
	path := "/tailnet/" + url.PathEscape(target.tailnet) + "/acl"
	data, etag, err := p.request(ctx, http.MethodGet, path, token, "", nil)
	if err != nil {
		return err
	}
	updated, changed, err := addPeerRelayGrant(data, target.ip)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	// Refuse an unconditional overwrite if the server did not return a version.
	if etag == "" || etag == "*" {
		return ErrPeerRelayPolicy
	}
	if !current() {
		return ErrPeerRelayIdentity
	}
	if _, _, err := p.request(ctx, http.MethodPost, path, token, etag, updated); err != nil {
		return err
	}
	after, _, err := p.request(ctx, http.MethodGet, path, token, "", nil)
	if err != nil {
		return err
	}
	if _, missing, err := addPeerRelayGrant(after, target.ip); err != nil || missing {
		return ErrPeerRelayPolicy
	}
	return nil
}

// addPeerRelayGrant patches the syntax tree so comments, unknown policy fields,
// and existing rules survive. An existing unrestricted grant makes this a no-op.
func addPeerRelayGrant(data []byte, ip string) ([]byte, bool, error) {
	policy, err := hujson.Parse(data)
	if err != nil {
		return nil, false, ErrPeerRelayPolicy
	}
	if _, ok := policy.Value.(*hujson.Object); !ok {
		return nil, false, ErrPeerRelayPolicy
	}
	// Duplicate JSON keys are ambiguous; never silently pick one to overwrite.
	for value := range policy.All() {
		if object, ok := value.Value.(*hujson.Object); ok {
			seen := map[string]bool{}
			for _, member := range object.Members {
				name := member.Name.Value.(hujson.Literal).String()
				if seen[name] {
					return nil, false, ErrPeerRelayPolicy
				}
				seen[name] = true
			}
		}
	}
	standard := policy.Clone()
	standard.Standardize()
	var fields map[string]json.RawMessage
	if json.Unmarshal(standard.Pack(), &fields) != nil {
		return nil, false, ErrPeerRelayPolicy
	}
	var grants []map[string]json.RawMessage
	if raw, exists := fields["grants"]; exists {
		if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &grants) != nil {
			return nil, false, ErrPeerRelayPolicy
		}
	}
	for _, grant := range grants {
		var src, dst []string
		var app map[string]json.RawMessage
		var capabilities []json.RawMessage
		// Extra fields can constrain access (for example srcPosture); do not
		// mistake a conditional grant for the unrestricted grant requested here.
		if len(grant) != 3 || json.Unmarshal(grant["src"], &src) != nil || json.Unmarshal(grant["dst"], &dst) != nil || json.Unmarshal(grant["app"], &app) != nil {
			continue
		}
		raw := bytes.TrimSpace(app[relayCapability])
		if slices.Contains(src, "*") && slices.Contains(dst, ip) && len(raw) > 0 && raw[0] == '[' && json.Unmarshal(raw, &capabilities) == nil && len(capabilities) == 0 {
			return data, false, nil
		}
	}
	grant := map[string]any{"src": []string{"*"}, "dst": []string{ip}, "app": map[string]any{relayCapability: []any{}}}
	path, value := "/grants/-", any(grant)
	if _, exists := fields["grants"]; !exists {
		path, value = "/grants", []any{grant}
	}
	patch, _ := json.Marshal([]any{map[string]any{"op": "add", "path": path, "value": value}})
	if policy.Patch(patch) != nil {
		return nil, false, ErrPeerRelayPolicy
	}
	return policy.Pack(), true, nil
}
