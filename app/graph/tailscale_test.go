package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/pancpp/nanotail-portal/app/graph/model"
	"github.com/pancpp/nanotail-portal/tailscale"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

type statusRunner []byte

func (r statusRunner) Run(context.Context, string, ...string) ([]byte, error) { return r, nil }

func queryStatus(t *testing.T, data []byte) (*model.TailscaleStatus, map[string]json.RawMessage) {
	t.Helper()
	client := tailscale.NewClient("tailscale", "", time.Second, statusRunner(data))
	server := handler.New(NewExecutableSchema(Config{Resolvers: &Resolver{Tailscale: client}}))
	server.AddTransport(transport.POST{})
	const query = `query {
	  tailscaleStatus {
	    version tun backendState haveNodeKey authURL tailscaleIPs health magicDnsSuffix
	    currentTailnet { name magicDNSSuffix magicDNSEnabled }
	    certDomains extraRecords { name type value }
	    clientVersion { runningLatest latestVersion urgentSecurityUpdate notify notifyURL notifyText }
	    self { ...PeerFields }
	    peers { ...PeerFields }
	  }
	}
	fragment PeerFields on TailscalePeer {
	  id nodeID publicKey hostName dnsName os userID tailscaleIPs allowedIPs tags addrs
	  curAddr relay peerRelay rxBytes txBytes created lastWrite lastSeen lastHandshake
	  online exitNode exitNodeOption active peerAPIURL taildropTarget noFileSharingReason
	  capMap inNetworkMap inMagicSock inEngine keyExpiry
	}`
	body, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/query", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, req)
	var envelope struct {
		Data struct {
			Status json.RawMessage `json:"tailscaleStatus"`
		} `json:"data"`
		Errors gqlerror.List `json:"errors"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || len(envelope.Errors) != 0 {
		t.Fatalf("status query failed: %d %s", response.Code, response.Body.String())
	}
	var status *model.TailscaleStatus
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Data.Status, &status); err != nil || status == nil {
		t.Fatalf("missing status: %s (%v)", envelope.Data.Status, err)
	}
	if err := json.Unmarshal(envelope.Data.Status, &fields); err != nil {
		t.Fatal(err)
	}
	return status, fields
}

func TestTailscaleStatusGraphQLMapping(t *testing.T) {
	data, err := os.ReadFile("../../tailscale/testdata/status.json")
	if err != nil {
		t.Fatal(err)
	}
	status, _ := queryStatus(t, data)
	if status.Version != "1.102.4" || !status.Tun || status.BackendState != "Running" || !status.HaveNodeKey ||
		status.AuthURL != "" || !reflect.DeepEqual(status.TailscaleIPs, []string{"100.64.0.1"}) ||
		status.Health == nil || len(status.Health) != 0 || status.MagicDNSSuffix != "example.ts.net" {
		t.Fatalf("incorrect status: %+v", status)
	}
	if !reflect.DeepEqual(status.CurrentTailnet, &model.Tailnet{
		Name: "example.test", MagicDNSSuffix: "example.ts.net", MagicDNSEnabled: true,
	}) || !reflect.DeepEqual(status.CertDomains, []string{"nanotail.example.ts.net"}) ||
		!reflect.DeepEqual(status.ExtraRecords, []*model.TailscaleDNSRecord{{Name: "service.example.test", Type: "A", Value: "100.64.0.2"}}) ||
		!reflect.DeepEqual(status.ClientVersion, &model.TailscaleClientVersion{
			LatestVersion: "1.102.5", UrgentSecurityUpdate: true, Notify: true,
			NotifyURL: "https://tailscale.com/download", NotifyText: "Update available",
		}) {
		t.Fatalf("incorrect metadata: %+v", status)
	}
	if status.Self == nil || status.Self.ID != "self" || status.Self.HostName != "nanotail" || !status.Self.Online ||
		status.Self.KeyExpiry != nil || status.Self.LastWrite != nil || status.Self.LastSeen != nil || status.Self.LastHandshake != nil ||
		status.Self.TailscaleIPs == nil || status.Self.AllowedIPs == nil || status.Self.Tags == nil || status.Self.PeerAPIURL == nil {
		t.Fatalf("incorrect self or null/empty fields: %+v", status.Self)
	}
	if len(status.Peers) != 2 || status.Peers[1].ID != "peer-z" {
		t.Fatalf("incorrect peer list: %+v", status.Peers)
	}
	lastWrite := time.Date(2026, 9, 24, 1, 0, 0, 0, time.UTC)
	lastHandshake := time.Date(2026, 9, 24, 0, 59, 0, 0, time.UTC)
	keyExpiry := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	wantPeer := &model.TailscalePeer{
		ID: "peer-a", NodeID: 7000000000, PublicKey: "nodekey:a", HostName: "desktop",
		DNSName: "desktop.example.ts.net.", Os: "linux", UserID: 8000000000,
		TailscaleIPs: []string{"100.64.0.2"}, AllowedIPs: []string{"100.64.0.2/32", "192.0.2.0/24"},
		Tags: []string{"tag:test"}, Addrs: []string{"203.0.113.10:41641"}, CurAddr: "203.0.113.10:41641",
		Relay: "sea", PeerRelay: "203.0.113.11:40000", RxBytes: 5000000000, TxBytes: 6000000000,
		Created: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), LastWrite: &lastWrite,
		LastHandshake: &lastHandshake, KeyExpiry: &keyExpiry,
		Online: true, ExitNode: true, ExitNodeOption: true, Active: true,
		PeerAPIURL: []string{"http://100.64.0.2:8080"}, TaildropTarget: 1, NoFileSharingReason: "test reason",
		CapMap:       map[string]any{"example/flag": nil, "example/settings": []any{map[string]any{"enabled": true, "limit": float64(2)}}},
		InNetworkMap: true, InMagicSock: true, InEngine: true,
	}
	if !reflect.DeepEqual(status.Peers[0], wantPeer) {
		t.Fatalf("incorrect peer mapping:\n got %+v\nwant %+v", status.Peers[0], wantPeer)
	}
}

func TestTailscaleSelfKeyExpiry(t *testing.T) {
	for _, expiry := range []string{"", "0001-01-01T00:00:00Z", "2027-01-01T00:00:00Z", "2020-01-01T00:00:00Z"} {
		t.Run(expiry, func(t *testing.T) {
			self := map[string]any{"ID": "self", "Online": true, "Created": "2019-01-01T00:00:00Z"}
			if expiry != "" {
				self["KeyExpiry"] = expiry
			}
			body, err := json.Marshal(map[string]any{"BackendState": "Running", "HaveNodeKey": true, "Self": self})
			if err != nil {
				t.Fatal(err)
			}
			status, fields := queryStatus(t, body)
			if !status.HaveNodeKey || status.Self == nil {
				t.Fatalf("node key metadata lost: %+v", status)
			}
			var rawSelf map[string]json.RawMessage
			if err := json.Unmarshal(fields["self"], &rawSelf); err != nil {
				t.Fatal(err)
			}
			if expiry == "" || expiry == "0001-01-01T00:00:00Z" {
				if status.Self.KeyExpiry != nil || string(rawSelf["keyExpiry"]) != "null" {
					t.Fatalf("absent expiry became a date: %s", fields["self"])
				}
			} else {
				want, err := time.Parse(time.RFC3339, expiry)
				if err != nil {
					t.Fatal(err)
				}
				if status.Self.KeyExpiry == nil || !status.Self.KeyExpiry.Equal(want) {
					t.Fatalf("expiry lost: %+v", status.Self.KeyExpiry)
				}
			}
		})
	}
}

func TestTailscaleStatusGraphQLOptionalFields(t *testing.T) {
	for _, data := range []string{
		`{"BackendState":"NeedsLogin","AuthURL":"https://login.tailscale.com/a/test"}`,
		`{"BackendState":"NeedsLogin","AuthURL":"https://login.tailscale.com/a/test","Self":null,"CurrentTailnet":null,"CertDomains":null,"ExtraRecords":null,"ClientVersion":null,"Peer":null}`,
	} {
		status, fields := queryStatus(t, []byte(data))
		if status.BackendState != "NeedsLogin" || status.AuthURL != "" {
			t.Fatalf("incorrect login state: %+v", status)
		}
		for _, field := range []string{"currentTailnet", "certDomains", "extraRecords", "clientVersion", "self"} {
			if string(fields[field]) != "null" {
				t.Errorf("%s = %s, want null", field, fields[field])
			}
		}
		for _, field := range []string{"tailscaleIPs", "health", "peers"} {
			if string(fields[field]) != "[]" {
				t.Errorf("%s = %s, want []", field, fields[field])
			}
		}
	}
	_, fields := queryStatus(t, []byte(`{"BackendState":"Running","CertDomains":[],"ExtraRecords":[],"ClientVersion":{"RunningLatest":true}}`))
	for _, field := range []string{"certDomains", "extraRecords"} {
		if string(fields[field]) != "[]" {
			t.Errorf("%s = %s, want []", field, fields[field])
		}
	}
}
