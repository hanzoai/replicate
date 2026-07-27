package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The replicate command serves two addressable HTTP surfaces beside the control
// socket: the metrics listener and the MCP listener. Both are route tables, so
// both get route tests.

// TestMetricsApp_Routes pins the metrics surface: /metrics on any method (the
// handler itself decides), the pprof subtree beside it, 404 for anything else.
func TestMetricsApp_Routes(t *testing.T) {
	app := newMetricsApp()

	do := func(method, path string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, "http://localhost"+path, nil)
		require.NoError(t, err)
		resp, err := app.Fiber().Test(req)
		require.NoError(t, err)
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}

	// net/http.DefaultServeMux never filtered by method, so the route must not
	// either — every verb has to reach the metrics handler.
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost} {
		t.Run(method+" /metrics", func(t *testing.T) {
			require.Equal(t, http.StatusOK, do(method, "/metrics").StatusCode)
		})
	}

	// pprof rode this listener through net/http/pprof's DefaultServeMux init;
	// it is an explicit route now, including the wildcard that carries the
	// profile names ServeMux used to resolve from the subtree pattern.
	for _, path := range []string{
		"/debug/pprof/",
		"/debug/pprof/cmdline",
		"/debug/pprof/symbol",
		"/debug/pprof/heap?debug=1",
		"/debug/pprof/goroutine?debug=1",
	} {
		t.Run("GET "+path, func(t *testing.T) {
			require.Equal(t, http.StatusOK, do(http.MethodGet, path).StatusCode)
		})
	}

	for _, path := range []string{"/", "/nope", "/metricsx", "/debug"} {
		t.Run("404 "+path, func(t *testing.T) {
			require.Equal(t, http.StatusNotFound, do(http.MethodGet, path).StatusCode)
		})
	}
}

// TestMCPServer_Routes pins that the MCP transport owns the whole root subtree,
// the way the "/" ServeMux pattern did.
func TestMCPServer_Routes(t *testing.T) {
	s, err := NewMCP(t.Context(), "")
	require.NoError(t, err)

	srv := httptest.NewServer(s)
	defer srv.Close()

	const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":` +
		`{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`

	for _, path := range []string{"/", "/mcp", "/anything/at/all"} {
		t.Run("POST "+path, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, srv.URL+path, stringBody(initialize))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			require.Equal(t, http.StatusOK, resp.StatusCode)
			b, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Contains(t, string(b), "protocolVersion")
		})
	}
}

// TestMCPServer_SSEHeadersPrecedeFirstEvent is why the MCP transport is the one
// surface still on net/http.
//
// mcp-go opens its notification stream by writing the SSE headers and flushing
// an empty body; a client cannot consider the stream established until those
// headers land. net/http puts them on the wire at that Flush. fasthttp — zip's
// engine — only writes a streamed response once the stream writer produces
// bytes, so behind zip.AdaptNetHTTP these headers would be withheld until the
// first notification, which may never come.
//
// If this test still passes after moving the transport onto zip, the engine has
// gained flush-on-empty and the route can be ported.
func TestMCPServer_SSEHeadersPrecedeFirstEvent(t *testing.T) {
	s, err := NewMCP(t.Context(), "")
	require.NoError(t, err)

	srv := httptest.NewServer(s)
	defer srv.Close()

	// No notification will ever be sent on this stream, so headers can only
	// arrive from the handler's own flush.
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "text/event-stream")

	start := time.Now()
	resp, err := client.Do(req)
	require.NoError(t, err, "SSE response headers must arrive without a first event")
	defer resp.Body.Close()

	require.Less(t, time.Since(start), 2*time.Second)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
}

type stringReadCloser struct {
	s string
	i int
}

func stringBody(s string) io.Reader { return &stringReadCloser{s: s} }

func (r *stringReadCloser) Read(p []byte) (int, error) {
	if r.i >= len(r.s) {
		return 0, io.EOF
	}
	n := copy(p, r.s[r.i:])
	r.i += n
	return n, nil
}
