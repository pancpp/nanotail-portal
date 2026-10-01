package access

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	LOGIN_API              = "/login"
	UPDATE_IP_API          = "/update-ip"
	SERVER_REQUEST_TIMEOUT = 10 * time.Second
	MAX_RESPONSE_BYTES     = 1 << 20
)

type IPReporter struct {
	deviceID   string
	deviceSig  string
	apiPrefix  string
	httpClient *http.Client
}

func NewIPReporter(apiPrefix, deviceID, deviceSig string) (*IPReporter, error) {
	u, err := url.Parse(apiPrefix)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" ||
		u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(apiPrefix, "#") {
		return nil, ErrInvalidAPIURL
	}

	return &IPReporter{
		deviceID:  deviceID,
		deviceSig: deviceSig,
		apiPrefix: strings.TrimRight(apiPrefix, "/"),
		httpClient: &http.Client{
			Timeout: SERVER_REQUEST_TIMEOUT,
			// Login signatures are reusable credentials. Never forward them (or
			// bearer tokens) to a redirect target, including on the same host.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

func (r *IPReporter) Report(ctx context.Context, hostname, ipv4, ipv6 string) error {
	token, err := r.login(ctx)
	if err != nil {
		log.Println("Failed to login:", err)
		return err
	}

	return r.reportIP(ctx, token, hostname, ipv4, ipv6)
}

func (r *IPReporter) login(ctx context.Context) (string, error) {
	type ReqMsg struct {
		DeviceID  string `json:"device_id"`
		DeviceSig string `json:"device_sig"`
	}
	type ResMsg struct {
		Token string `json:"token"`
	}

	reqMsg := ReqMsg{DeviceID: r.deviceID, DeviceSig: r.deviceSig}
	var resMsg ResMsg
	if err := r.post(ctx, LOGIN_API, "", reqMsg, &resMsg); err != nil {
		return "", err
	}

	return resMsg.Token, nil
}

func (r *IPReporter) reportIP(ctx context.Context, token, hostname, ipv4, ipv6 string) error {
	type ReqMsg struct {
		Hostname string `json:"hostname"`
		IPv4     string `json:"ipv4"`
		IPv6     string `json:"ipv6"`
	}
	type ResMsg struct {
		Status string `json:"status"`
	}

	reqMsg := ReqMsg{Hostname: hostname, IPv4: ipv4, IPv6: ipv6}
	var resMsg ResMsg
	if err := r.post(ctx, UPDATE_IP_API, token, reqMsg, &resMsg); err != nil {
		return err
	}
	if resMsg.Status == "" {
		return ErrInvalidAPIResponse
	}
	if resMsg.Status != "success" {
		return ErrReportRejected
	}
	return nil
}

func (r *IPReporter) post(ctx context.Context, path, token string, reqMsg, resMsg any) error {
	reqBody, err := json.Marshal(reqMsg)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.apiPrefix+path, bytes.NewReader(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	res, err := r.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		// Do not log the response body: it may contain credentials.
		log.Printf("%v: %s returned HTTP %d", ErrAPIHTTPStatus, path, res.StatusCode)
		return ErrAPIHTTPStatus
	}
	resBody, err := io.ReadAll(io.LimitReader(res.Body, MAX_RESPONSE_BYTES+1))
	if err != nil {
		return err
	}
	if len(resBody) > MAX_RESPONSE_BYTES {
		return ErrAPIResponseTooLarge
	}
	if err := json.Unmarshal(resBody, resMsg); err != nil {
		return ErrInvalidAPIResponse
	}
	return nil
}
