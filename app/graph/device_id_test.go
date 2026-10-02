package graph

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
)

func TestDeviceIDGraphQL(t *testing.T) {
	for _, tt := range []struct {
		name          string
		query         string
		id            string
		missingReader bool
		want          string
		wantReads     int
	}{
		{
			name:  "cached device ID without Tailscale or device metrics",
			query: `query { deviceID }`, id: "00112233aabbccdd",
			want: `{"data":{"deviceID":"00112233aabbccdd"}}`, wantReads: 1,
		},
		{
			name: "empty ID", query: `query { deviceID }`,
			want: `{"data":{"deviceID":null}}`, wantReads: 1,
		},
		{
			name: "missing reader", query: `query { deviceID }`, missingReader: true,
			want: `{"data":{"deviceID":null}}`,
		},
		{
			name: "unselected field does not read credentials", query: `query { __typename }`,
			want: `{"data":{"__typename":"Query"}}`,
		},
		{
			name: "skipped field does not read credentials", query: `query { __typename deviceID @skip(if: true) }`,
			want: `{"data":{"__typename":"Query"}}`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reads := 0
			r := &Resolver{}
			if !tt.missingReader {
				r.DeviceID = func() string {
					reads++
					return tt.id
				}
			}
			server := handler.New(NewExecutableSchema(Config{Resolvers: r}))
			server.AddTransport(transport.POST{})
			req := httptest.NewRequest(http.MethodPost, "/query", strings.NewReader(`{"query":"`+tt.query+`"}`))
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			server.ServeHTTP(response, req)
			if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != tt.want {
				t.Fatalf("device ID response: %d %s; want %s", response.Code, response.Body.String(), tt.want)
			}
			if reads != tt.wantReads {
				t.Fatalf("credential reads = %d; want %d", reads, tt.wantReads)
			}
		})
	}
}
