package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestInstanceServiceURL(t *testing.T) {
	cases := []struct {
		name      string
		webURL    string
		extraArgs string
		want      string
		wantOK    bool
	}{
		{"no args → default 3080", "", "", "http://127.0.0.1:3080", true},
		{"--port 3081", "", "--no-open --port 3081", "http://127.0.0.1:3081", true},
		{"--port=3090", "", "--port=3090", "http://127.0.0.1:3090", true},
		{"--port 0 auto → unknown", "", "--port 0", "", false},
		{"runtime URL wins", "http://127.0.0.1:49123", "--port 0", "http://127.0.0.1:49123", true},
	}
	for _, c := range cases {
		inst := &Instance{WebUrl: c.webURL, ExtraArgs: c.extraArgs}
		got, ok := instanceServiceURL(inst)
		if got != c.want || ok != c.wantOK {
			t.Errorf("%s: instanceServiceURL(%q, %q) = (%q, %v), want (%q, %v)",
				c.name, c.webURL, c.extraArgs, got, ok, c.want, c.wantOK)
		}
	}
}

func TestProbeService(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if !probeService(srv.URL) {
		t.Errorf("probeService(%s) = false, want true (live server)", srv.URL)
	}
	// A URL that is never dialable must report false without hanging long.
	// (Testing "server closed" is flaky on Windows: a just-closed listener can
	// leave a short window where connect still succeeds, so we only assert the
	// definitively-unbound case.)
	if probeService("http://127.0.0.1:1") {
		t.Errorf("probeService(http://127.0.0.1:1) = true, want false")
	}
}
