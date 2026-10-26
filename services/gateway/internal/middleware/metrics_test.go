package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	pkgmetrics "github.com/fsociety/todo/pkg/metrics"
	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus"
)

func labelValues(t *testing.T, label string) map[string]bool {
	t.Helper()
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, mf := range mfs {
		if mf.GetName() != "http_requests_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == label {
					got[l.GetValue()] = true
				}
			}
		}
	}
	return got
}

func TestHTTPMetricsLabelsUseRoutePattern(t *testing.T) {
	r := chi.NewRouter()
	r.Use(pkgmetrics.HTTPMiddleware(RoutePattern))
	r.Get("/api/v1/tasks/{taskID}", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusNotFound) // a handler 404 keeps its route label
	})
	sub := chi.NewRouter()
	sub.Get("/{sessionID}", func(w http.ResponseWriter, _ *http.Request) {})
	r.Mount("/api/v1/sessions", sub)

	for _, p := range []string{
		"/api/v1/tasks/a", "/api/v1/tasks/b", "/api/v1/sessions/x", "/api/v1/sessions/y",
		"/random404", "/other/404", "/api/v1/nope", "/api/v1/sessions/x/y/z",
	} {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, p, nil))
	}
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("BREW", "/api/v1/tasks/a", nil))

	got := labelValues(t, "path")
	for p := range got {
		t.Logf("path label: %q", p)
	}
	want := map[string]bool{
		"/api/v1/tasks/{taskID}":       true,
		"/api/v1/sessions/{sessionID}": true,
		pkgmetrics.Unmatched:           true,
	}
	for p := range got {
		if !want[p] {
			t.Errorf("unexpected path label %q", p)
		}
	}
	for p := range want {
		if !got[p] {
			t.Errorf("missing path label %q", p)
		}
	}

	if methods := labelValues(t, "method"); methods["BREW"] || !methods["OTHER"] {
		t.Errorf("method labels = %v, want unknown methods bucketed as OTHER", methods)
	}
}
