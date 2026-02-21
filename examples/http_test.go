package examples

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gfanton/tsar"
)

func TestHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(apiHandler))
	defer srv.Close()

	tsar.Run(t, tsar.Params{
		Dir: "testdata/http",
		Setup: func(env *tsar.Env) error {
			env.Setenv("SERVER", srv.URL)
			return nil
		},
	})
}

// TestHTTPFlaky demonstrates repeat -all with a server that fails intermittently.
// The test expects some failures and verifies the stats summary.
func TestHTTPFlaky(t *testing.T) {
	var reqCount atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := reqCount.Add(1)
		if n%5 == 0 { // fail every 5th request
			w.WriteHeader(503)
			fmt.Fprint(w, "overloaded")
			return
		}
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	tsar.Run(t, tsar.Params{
		Dir: "testdata/http_flaky",
		Setup: func(env *tsar.Env) error {
			env.Setenv("SERVER", srv.URL)
			return nil
		},
	})
}

func apiHandler(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == "GET" && r.URL.Path == "/health":
		fmt.Fprint(w, "ok")
	case r.Method == "GET" && r.URL.Path == "/api/info":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Version", "1.0.0")
		fmt.Fprint(w, `{"status":"healthy","version":"1.0.0"}`)
	case r.Method == "POST" && r.URL.Path == "/api/echo":
		ct := r.Header.Get("Content-Type")
		if ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		body, _ := io.ReadAll(r.Body)
		w.Write(body)
	default:
		w.WriteHeader(404)
		fmt.Fprint(w, "not found")
	}
}
