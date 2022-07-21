package redirectstatus_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	redirectstatus "github.com/turingvideo/traefik-plugin-redirect-status"
)

func TestRedirectStatus(t *testing.T) {
	cfg := redirectstatus.CreateConfig()
	cfg.Status = append(cfg.Status, "401-403")
	cfg.To = "http://localhost"

	ctx := context.Background()
	next := http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(http.StatusUnauthorized)
	})

	handler, err := redirectstatus.New(ctx, next, cfg, "redirectstatus-plugin")
	if err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.org", nil)
	if err != nil {
		t.Fatal(err)
	}

	handler.ServeHTTP(recorder, req)
	resp := recorder.Result()

	if resp.StatusCode != http.StatusFound {
		t.Errorf("invalid status code: %d", resp.StatusCode)
	}

	if resp.Header.Get("Location") != cfg.To {
		t.Errorf("invalid response header: %s=%s", "Location", resp.Header.Get("Location"))
	}
}
