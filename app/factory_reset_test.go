package app

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/pancpp/nanotail-portal/auth"
	"github.com/pancpp/nanotail-portal/database"
	"github.com/pancpp/nanotail-portal/factoryreset"
)

func TestFactoryResetAuthorizationAndConfirmation(t *testing.T) {
	e, admin := setupLoginApp(t)
	regular := &database.User{Username: "regular", Passwd: admin.Passwd, Role: "user"}
	if _, err := database.DB().NewInsert().Model(regular).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	adminToken, _ := auth.CreateJwtToken(admin.PID)
	regularToken, _ := auth.CreateJwtToken(regular.PID)
	deletedToken, _ := auth.CreateJwtToken(9999)
	preflightCalls := 0
	var preflightErr error
	reset := factoryreset.NewController(func() error { preflightCalls++; return preflightErr })
	initFactoryResetAPI(e, reset)
	valid := `{"confirmed":true,"confirmation":"RESET","password":"admin"}`
	for _, tc := range []struct {
		name, token, body string
		status            int
	}{
		{"missing JWT", "", valid, 401},
		{"invalid JWT", "invalid", valid, 401},
		{"non-administrator", regularToken, valid, 403},
		{"deleted account", deletedToken, valid, 401},
		{"no confirmation", adminToken, `{"password":"admin"}`, 400},
		{"only second confirmation", adminToken, `{"confirmation":"RESET","password":"admin"}`, 400},
		{"only first confirmation", adminToken, `{"confirmed":true,"password":"admin"}`, 400},
		{"wrong phrase", adminToken, `{"confirmed":true,"confirmation":"reset","password":"admin"}`, 400},
		{"wrong password", adminToken, `{"confirmed":true,"confirmation":"RESET","password":"wrong"}`, 403},
		{"empty body", adminToken, "", 400},
		{"trailing JSON", adminToken, valid + " {}", 400},
		{"client-supplied path", adminToken, strings.TrimSuffix(valid, "}") + `,"path":"/"}`, 400},
		{"oversized", adminToken, strings.Repeat(" ", 4096) + valid, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := appRequest(e, http.MethodPost, "/api/v1/factory-reset", tc.body, "Bearer "+tc.token)
			if w.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", w.Code, tc.status, w.Body.String())
			}
			if preflightCalls != 0 || reset.Pending() {
				t.Fatal("unauthorized or unconfirmed request reached reset")
			}
		})
	}
	preflightErr = errors.New("private filesystem details")
	w := appRequest(e, http.MethodPost, "/api/v1/factory-reset", valid, "Bearer "+adminToken)
	if w.Code != 409 || strings.Contains(w.Body.String(), "private filesystem") || reset.Pending() {
		t.Fatalf("unsafe preflight result: %d %s", w.Code, w.Body.String())
	}
	preflightErr = nil
	w = appRequest(e, http.MethodPost, "/api/v1/factory-reset", valid, "Bearer "+adminToken)
	if w.Code != 202 || !w.Flushed || w.Body.String() != "{\"accepted\":true}\n" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("acceptance not flushed: %d %s", w.Code, w.Body.String())
	}
	select {
	case <-reset.Requests():
	default:
		t.Fatal("reset not scheduled after acceptance")
	}
	w = appRequest(e, http.MethodPost, "/api/v1/factory-reset", valid, "Bearer "+adminToken)
	if w.Code != 409 {
		t.Fatalf("duplicate accepted: %d", w.Code)
	}
	select {
	case <-reset.Requests():
		t.Fatal("duplicate scheduled")
	default:
	}
}
