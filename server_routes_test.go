package replicate_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hanzoai/replicate"
)

// The control socket's route table is its API. These tests pin every path and
// method the server has ever answered, so a routing change cannot slip through
// as "the handler tests still pass".

// routeTestServer starts a control server backed by an empty store.
func routeTestServer(t *testing.T) (*replicate.Server, *http.Client) {
	t.Helper()

	store := replicate.NewStore(nil, replicate.CompactionLevels{{Level: 0}})
	store.CompactionMonitorEnabled = false
	require.NoError(t, store.Open(t.Context()))
	t.Cleanup(func() { store.Close(t.Context()) })

	server := replicate.NewServer(store)
	server.SocketPath = testSocketPath(t)
	server.Version = "v0.0.0-routes"
	require.NoError(t, server.Start())
	t.Cleanup(func() { server.Close() })

	return server, newSocketClient(t, server.SocketPath)
}

func routeDo(t *testing.T, client *http.Client, method, path, body string) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, "http://localhost"+path, rdr)
	require.NoError(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// TestServer_Routes walks the whole route table: every registered path answers
// on its own method with the documented status, and the JSON endpoints answer
// with a bare "application/json" content type and a newline-terminated body.
func TestServer_Routes(t *testing.T) {
	_, client := routeTestServer(t)

	// The eight control endpoints. Each is exercised with a request that
	// reaches the handler, so a wrong status means the route moved, not that
	// the request was malformed.
	for _, tt := range []struct {
		method string
		path   string
		body   string
		status int
	}{
		{http.MethodGet, "/info", "", http.StatusOK},
		{http.MethodGet, "/list", "", http.StatusOK},
		{http.MethodGet, "/txid?path=/nope/db", "", http.StatusNotFound},
		{http.MethodGet, "/txid", "", http.StatusBadRequest},
		{http.MethodPost, "/start", `{}`, http.StatusBadRequest},
		{http.MethodPost, "/stop", `{}`, http.StatusBadRequest},
		{http.MethodPost, "/sync", `{}`, http.StatusBadRequest},
		{http.MethodPost, "/register", `{}`, http.StatusBadRequest},
		{http.MethodPost, "/unregister", `{}`, http.StatusBadRequest},
	} {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			resp := routeDo(t, client, tt.method, tt.path, tt.body)
			require.Equal(t, tt.status, resp.StatusCode)
			require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

			b, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.True(t, strings.HasSuffix(string(b), "\n"), "body must stay newline-terminated: %q", b)
		})
	}
}

// TestServer_RouteMethods pins the method half of each pattern: the verb a
// route was not registered for is rejected, never silently answered.
func TestServer_RouteMethods(t *testing.T) {
	_, client := routeTestServer(t)

	for _, tt := range []struct{ method, path string }{
		{http.MethodGet, "/start"},
		{http.MethodGet, "/stop"},
		{http.MethodGet, "/sync"},
		{http.MethodGet, "/register"},
		{http.MethodGet, "/unregister"},
		{http.MethodPost, "/info"},
		{http.MethodPost, "/list"},
		{http.MethodPost, "/txid"},
		{http.MethodDelete, "/list"},
		{http.MethodPut, "/start"},
	} {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			resp := routeDo(t, client, tt.method, tt.path, "")
			require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
		})
	}
}

// TestServer_RouteNotFound pins that unregistered paths 404 rather than
// falling through to a neighbouring route.
func TestServer_RouteNotFound(t *testing.T) {
	_, client := routeTestServer(t)

	for _, path := range []string{"/", "/nope", "/info/extra", "/debug", "/metrics"} {
		t.Run(path, func(t *testing.T) {
			resp := routeDo(t, client, http.MethodGet, path, "")
			require.Equal(t, http.StatusNotFound, resp.StatusCode)
		})
	}
}

// TestServer_RoutesPprof pins the pprof subtree, including the wildcard that
// carries the profile names net/http.ServeMux used to resolve for us.
func TestServer_RoutesPprof(t *testing.T) {
	_, client := routeTestServer(t)

	for _, tt := range []struct {
		path        string
		contentType string
		contains    string
	}{
		{"/debug/pprof/", "text/html; charset=utf-8", "/debug/pprof/"},
		{"/debug/pprof/cmdline", "text/plain; charset=utf-8", ""},
		{"/debug/pprof/symbol", "text/plain; charset=utf-8", "num_symbols"},
		{"/debug/pprof/heap?debug=1", "text/plain; charset=utf-8", "heap profile"},
		{"/debug/pprof/goroutine?debug=1", "text/plain; charset=utf-8", "goroutine profile"},
		{"/debug/pprof/allocs?debug=1", "text/plain; charset=utf-8", "heap profile"},
	} {
		t.Run(tt.path, func(t *testing.T) {
			resp := routeDo(t, client, http.MethodGet, tt.path, "")
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Equal(t, tt.contentType, resp.Header.Get("Content-Type"))

			b, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Contains(t, string(b), tt.contains)
		})
	}

	// pprof answers GET only on the control socket.
	resp := routeDo(t, client, http.MethodPost, "/debug/pprof/heap", "")
	require.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}
