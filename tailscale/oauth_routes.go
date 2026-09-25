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
	"time"
)

// OAuthCredentials are sent only to Tailscale's token endpoint, never returned
// to the browser or written to logs.
type OAuthCredentials struct{ ClientID, ClientSecret string }

var (
	errApprovalCredentials = errors.New("OAuth authentication failed. Check the saved client ID and secret and grant devices:routes write permission")
	errApprovalPermission  = errors.New("OAuth route approval was denied. Grant devices:routes write permission and use credentials from this device's tailnet")
	errApprovalUnavailable = errors.New("Unable to verify Tailscale route approval. Check internet access; the portal will check again automatically")
	errApprovalPending     = errors.New("Waiting for Tailscale to receive this device's current route advertisements")
	errApprovalReadback    = errors.New("Tailscale has not confirmed all route approvals. The portal will read the current approvals before trying again")
)

const tailscaleAPI = "https://api.tailscale.com/api/v2"

// oauthRoutes is used only while Client.mutations is held.
type oauthRoutes struct {
	http       *http.Client
	credential OAuthCredentials
	token      string
	expires    time.Time
}

func newOAuthRoutes(client *http.Client) *oauthRoutes {
	if client == nil {
		client = &http.Client{}
	}
	secured := *client
	secured.Timeout = 15 * time.Second
	// A redirect must never carry an OAuth secret or bearer token elsewhere.
	secured.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &oauthRoutes{http: &secured}
}

func (o *oauthRoutes) forget() {
	o.credential = OAuthCredentials{}
	o.token = ""
	o.expires = time.Time{}
}

func (o *oauthRoutes) request(ctx context.Context, method, path, contentType string, body []byte, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, tailscaleAPI+path, bytes.NewReader(body))
	if err != nil {
		return nil, errApprovalUnavailable
	}
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := o.http.Do(req)
	if err != nil {
		return nil, errApprovalUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Never expose upstream response bodies, URLs, or HTTP/transport errors.
		if response.StatusCode == http.StatusUnauthorized {
			o.forget()
			return nil, errApprovalCredentials
		}
		if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusNotFound {
			return nil, errApprovalPermission
		}
		if path == "/oauth/token" && response.StatusCode == http.StatusBadRequest {
			return nil, errApprovalCredentials
		}
		return nil, errApprovalUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, errApprovalUnavailable
	}
	return data, nil
}

func (o *oauthRoutes) accessToken(ctx context.Context, credential OAuthCredentials) (string, error) {
	if credential != o.credential {
		o.forget()
	}
	if o.token != "" && time.Now().Add(time.Minute).Before(o.expires) {
		return o.token, nil
	}
	form := url.Values{
		"grant_type": {"client_credentials"}, "client_id": {credential.ClientID},
		"client_secret": {credential.ClientSecret}, "scope": {"devices:routes"},
	}
	data, err := o.request(ctx, http.MethodPost, "/oauth/token", "application/x-www-form-urlencoded", []byte(form.Encode()), "")
	if err != nil {
		return "", err
	}
	var result struct {
		Token   string `json:"access_token"`
		Type    string `json:"token_type"`
		Expires int    `json:"expires_in"`
	}
	if json.Unmarshal(data, &result) != nil || result.Token == "" || len(result.Token) > 8192 ||
		strings.ContainsAny(result.Token, " \t\r\n") || !strings.EqualFold(result.Type, "Bearer") || result.Expires <= 0 || result.Expires > 86400 {
		return "", errApprovalCredentials
	}
	o.credential, o.token, o.expires = credential, result.Token, time.Now().Add(time.Duration(result.Expires)*time.Second)
	return result.Token, nil
}

type remoteRoutes struct {
	Advertised []string `json:"advertisedRoutes"`
	Enabled    []string `json:"enabledRoutes"`
}

func (o *oauthRoutes) read(ctx context.Context, path, token string) (remoteRoutes, error) {
	data, err := o.request(ctx, http.MethodGet, path, "", nil, token)
	if err != nil {
		return remoteRoutes{}, err
	}
	var result remoteRoutes
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields["advertisedRoutes"] == nil || fields["enabledRoutes"] == nil || json.Unmarshal(data, &result) != nil {
		return remoteRoutes{}, errApprovalUnavailable
	}
	// Accept explicit null as an empty list, but never infer an absent field.
	result.Advertised, result.Enabled = nonNil(result.Advertised), nonNil(result.Enabled)
	for _, routes := range [][]string{result.Advertised, result.Enabled} {
		if len(routes) > 4096 {
			return remoteRoutes{}, errApprovalUnavailable
		}
		for i, route := range routes {
			p, err := netip.ParsePrefix(route)
			if err != nil || p != p.Masked() || p.Addr().Is4In6() {
				return remoteRoutes{}, errApprovalUnavailable
			}
			routes[i] = p.String()
		}
	}
	return result, nil
}

func containsRoutes(have, want []string) bool {
	for _, route := range want {
		if !slices.Contains(have, route) {
			return false
		}
	}
	return true
}

// approve is additive: preserve existing approvals, including pre-approved routes.
// Always read before a write, and read back even after a successful POST.
func (o *oauthRoutes) approve(ctx context.Context, credential OAuthCredentials, deviceID string, desired []string, stillCurrent func() bool) error {
	token, err := o.accessToken(ctx, credential)
	if err != nil {
		return err
	}
	path := "/device/" + url.PathEscape(deviceID) + "/routes"
	current, err := o.read(ctx, path, token)
	if err != nil {
		return err
	}
	if !containsRoutes(current.Advertised, desired) {
		return errApprovalPending
	}
	if containsRoutes(current.Enabled, desired) {
		return nil
	}
	if !stillCurrent() {
		return errApprovalPending
	}
	routes := append(slices.Clone(current.Enabled), desired...)
	slices.Sort(routes)
	routes = slices.Compact(routes)
	body, _ := json.Marshal(map[string][]string{"routes": routes})
	if _, err := o.request(ctx, http.MethodPost, path, "application/json", body, token); err != nil {
		return err
	}
	after, err := o.read(ctx, path, token)
	if err != nil {
		return err
	}
	if !containsRoutes(after.Enabled, desired) || !containsRoutes(after.Advertised, desired) {
		return errApprovalReadback
	}
	return nil
}
