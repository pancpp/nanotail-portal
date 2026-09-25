package app

import (
	"testing"

	"github.com/pancpp/nanotail-portal/database"
)

func TestLoadRoutingCredentials(t *testing.T) {
	setupTailscaleApp(t)
	empty, err := loadRoutingCredentials(t.Context())
	if err != nil || empty.ClientID != "" || empty.ClientSecret != "" {
		t.Fatalf("empty: %+v %v", empty, err)
	}
	saved := &database.TailscaleClient{PID: 1, ClientID: "client-id", ClientSecret: "client-secret", ApiToken: "old-token"}
	if _, err := database.DB().NewInsert().Model(saved).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	credential, err := loadRoutingCredentials(t.Context())
	if err != nil || credential.ClientID != saved.ClientID || credential.ClientSecret != saved.ClientSecret {
		t.Fatal("saved credentials were not loaded")
	}
	if _, err := database.DB().NewDelete().Model(saved).WherePK().Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	empty, err = loadRoutingCredentials(t.Context())
	if err != nil || empty.ClientID != "" || empty.ClientSecret != "" {
		t.Fatal("removed credentials were retained")
	}
	if _, err := database.DB().NewDropTable().Model(saved).Exec(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRoutingCredentials(t.Context()); err == nil {
		t.Fatal("database failure was treated as missing credentials")
	}
}
