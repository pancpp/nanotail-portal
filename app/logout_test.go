package app

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/pancpp/nanotail-portal/app/auth"
	"github.com/pancpp/nanotail-portal/app/graph"
	"github.com/pancpp/nanotail-portal/database"
	"github.com/pancpp/nanotail-portal/tailscale"
)

func TestLogoutTailscaleGraphQL(t *testing.T) {
	db := setupAuthDatabase(t, t.Context())
	admin, regular := &database.User{Username: "admin", Role: "admin"}, &database.User{Username: "user", Role: "user"}
	for _, user := range []*database.User{admin, regular} {
		if _, err := db.NewInsert().Model(user).Exec(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name    string
		pid     int64
		missing bool
		err     error
		want    string
		calls   int
	}{
		{name: "admin", pid: admin.PID, calls: 1},
		{name: "regular", pid: regular.PID, want: graph.ErrConnectionAdmin.Error()},
		{name: "missing identity", want: auth.ErrUnauthorized.Error()},
		{name: "deleted identity", pid: 99999, want: auth.ErrUnauthorized.Error()},
		{name: "no backend", pid: admin.PID, missing: true, want: tailscale.ErrConnectionUnavailable.Error()},
		{name: "unavailable", pid: admin.PID, err: tailscale.ErrConnectionUnavailable, want: tailscale.ErrConnectionUnavailable.Error(), calls: 1},
		{name: "unknown outcome", pid: admin.PID, err: tailscale.ErrLogoutApply, want: tailscale.ErrLogoutApply.Error(), calls: 1},
		{name: "masked diagnostic", pid: admin.PID, err: errors.New("private daemon output"), want: "Internal Server Error", calls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := &connectionMock{err: tc.err}
			var connector graph.TailscaleConnector = mock
			if tc.missing {
				connector = nil
			}
			result := connectionGraphQL(t, connector, tc.pid, `mutation { logoutTailscale }`, nil)
			if mock.logoutCalls != tc.calls || mock.calls != 0 {
				t.Fatalf("logout calls=%d want=%d; connection changes=%d", mock.logoutCalls, tc.calls, mock.calls)
			}
			if tc.want != "" {
				var errs []struct{ Message string }
				if err := json.Unmarshal(result["errors"], &errs); err != nil {
					t.Fatal(err)
				}
				if len(errs) != 1 || errs[0].Message != tc.want || string(result["data"]) != "null" {
					t.Fatalf("result=%s", result)
				}
			} else if len(result["errors"]) != 0 || string(result["data"]) != `{"logoutTailscale":true}` {
				t.Fatalf("result=%s", result)
			}
		})
	}
}
