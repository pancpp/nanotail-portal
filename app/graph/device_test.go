package graph

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/pancpp/nanotail-portal/app/graph/model"
	"github.com/pancpp/nanotail-portal/auth"
	"github.com/pancpp/nanotail-portal/device"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

type deviceStatusReaderFunc func(context.Context) (device.Status, error)

func (f deviceStatusReaderFunc) Status(ctx context.Context) (device.Status, error) { return f(ctx) }

func TestDeviceStatusMapping(t *testing.T) {
	for _, network := range []device.Status{
		{LANIP: "192.0.2.2/24", LANIPv6: "fd00::2/64", LANIPType: "static", LANIPv6Type: "auto", Gateway: "192.0.2.1", Gateway6: "fe80::1", DNS: []string{"192.0.2.53", "2001:db8::53"}},
		{LANIPType: "unknown", LANIPv6Type: "disabled", DNS: []string{}},
	} {
		input := network
		input.Hostname, input.EthAddr, input.Health = "nanotail", "02:00:00:00:00:01", "healthy"
		input.CPULoad, input.Memory, input.Uptime = 42, 65, 5000000000
		input.LastRestart = time.Unix(1700000000, 0).UTC()
		r := &Resolver{Device: deviceStatusReaderFunc(func(ctx context.Context) (device.Status, error) {
			if ctx != t.Context() {
				t.Fatal("request context was not propagated")
			}
			return input, nil
		})}
		got, err := r.Query().DeviceStatus(t.Context())
		want := &model.DeviceStatus{
			Hostname: "nanotail", LanIPType: network.LANIPType, LanIP: network.LANIP, Gateway: network.Gateway,
			DNS: network.DNS, LanIPv6Type: network.LANIPv6Type, LanIPv6: network.LANIPv6, Gateway6: network.Gateway6, EthAddr: "02:00:00:00:00:01",
			Cpuload: 42, Memory: 65, LastRestart: input.LastRestart, Uptime: 5000000000, Health: "healthy",
		}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, %v; want %+v", got, err, want)
		}
	}
}

func TestDeviceStatusGraphQL(t *testing.T) {
	const query = `{"query":"query { deviceStatus { hostname lanIPType lanIP gateway dns lanIPv6Type lanIPv6 gateway6 ethAddr cpuload memory lastRestart uptime health } }"}`
	for _, tt := range []struct {
		failed  bool
		network device.Status
	}{
		{network: device.Status{LANIPType: "DHCP", LANIP: "192.0.2.2/24", Gateway: "192.0.2.1", DNS: []string{"192.0.2.53", "2001:db8::53"}, LANIPv6Type: "auto", LANIPv6: "fd00::2/64", Gateway6: "fe80::1"}},
		{network: device.Status{LANIPType: "unknown", LANIPv6Type: "disabled", DNS: []string{}}},
		{failed: true},
	} {
		r := &Resolver{Device: deviceStatusReaderFunc(func(context.Context) (device.Status, error) {
			if tt.failed {
				return device.Status{}, errors.New("private OS diagnostic")
			}
			status := tt.network
			status.Hostname, status.EthAddr, status.Health = "nanotail", "02:00:00:00:00:01", "healthy"
			status.CPULoad, status.Uptime = 100, 5000000000
			status.LastRestart = time.Unix(1700000000, 0).UTC()
			return status, nil
		})}
		server := handler.New(NewExecutableSchema(Config{Resolvers: r}))
		server.AddTransport(transport.POST{})
		req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(query))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.ServeHTTP(response, req)
		if response.Code != http.StatusOK {
			t.Fatalf("status query failed: %d %s", response.Code, response.Body.String())
		}
		var envelope struct {
			Data   map[string]map[string]json.RawMessage `json:"data"`
			Errors gqlerror.List                         `json:"errors"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if tt.failed {
			if envelope.Data != nil || len(envelope.Errors) != 1 || envelope.Errors[0].Message != ErrDeviceStatus.Error() ||
				strings.Contains(response.Body.String(), "private OS diagnostic") {
				t.Fatalf("unsafe failure response: %s", response.Body.String())
			}
			continue
		}
		if len(envelope.Errors) != 0 {
			t.Fatalf("unexpected GraphQL errors: %v", envelope.Errors)
		}
		for field, want := range map[string]any{
			"lanIPType": tt.network.LANIPType, "lanIP": tt.network.LANIP, "gateway": tt.network.Gateway,
			"dns": tt.network.DNS, "lanIPv6Type": tt.network.LANIPv6Type, "lanIPv6": tt.network.LANIPv6, "gateway6": tt.network.Gateway6,
		} {
			wantJSON, _ := json.Marshal(want)
			if got := string(envelope.Data["deviceStatus"][field]); got != string(wantJSON) {
				t.Errorf("%s = %s, want %s", field, got, wantJSON)
			}
		}
		for field, want := range map[string]string{
			"hostname": `"nanotail"`,
			"ethAddr":  `"02:00:00:00:00:01"`, "cpuload": `100`,
			"memory": `0`, "lastRestart": `"2023-11-14T22:13:20Z"`, "uptime": `5000000000`, "health": `"healthy"`,
		} {
			if got := string(envelope.Data["deviceStatus"][field]); got != want {
				t.Errorf("%s = %s, want %s", field, got, want)
			}
		}
	}
}

func TestDeviceStatusMissingReader(t *testing.T) {
	status, err := (&Resolver{}).Query().DeviceStatus(t.Context())
	if status != nil || !errors.Is(err, ErrDeviceStatus) {
		t.Fatalf("missing reader: %+v %v", status, err)
	}
}

func TestSetDeviceIPRequiresAuthentication(t *testing.T) {
	for _, input := range []*model.DeviceIP{nil, {Type: "DHCP", DNS: []string{}}} {
		ok, err := (&Resolver{}).Mutation().SetDeviceIP(t.Context(), input)
		if ok || !errors.Is(err, auth.ErrUnauthorized) {
			t.Fatalf("unauthenticated network mutation returned %v, %v", ok, err)
		}
	}
}
