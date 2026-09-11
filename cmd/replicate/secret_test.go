package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/hanzoai/replicate/cmd/replicate"
)

// stand answers as IAM and as KMS, and points the process's identity at a token
// file holding assertion. It returns the number of reads KMS served.
func stand(t *testing.T, assertion string, keys map[string]string) *int {
	t.Helper()

	reads := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/iam/oauth/token":
			if err := r.ParseForm(); err != nil {
				t.Errorf("token request is not a form: %v", err)
			}
			if got := r.Form.Get("assertion"); got != assertion {
				t.Errorf("assertion is %q, want the projected token %q", got, assertion)
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "bearer-1", "token_type": "Bearer", "expires_in": 3600})

		case strings.HasPrefix(r.URL.Path, "/v1/kms/secrets/"):
			if got := r.Header.Get("Authorization"); got != "Bearer bearer-1" {
				t.Errorf("read carries %q, want the minted bearer", got)
			}
			name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			value, ok := keys[name]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			reads++
			json.NewEncoder(w).Encode(map[string]string{"env": r.URL.Query().Get("env"), "name": name, "value": value})

		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte(assertion+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HANZO_SA_TOKEN", token)
	t.Setenv("HANZO_IAM_URL", srv.URL)
	t.Setenv("KMS_URL", srv.URL)
	return &reads
}

// A configuration that names a store gets its values from that store, with the
// process's own identity, and never from a variable on the pod.
func TestAConfigReadsItsCredentialsFromTheStore(t *testing.T) {
	reads := stand(t, "projected-token", map[string]string{"identity": "AGE-SECRET-KEY-1REAL"})

	config, err := main.ParseConfig(strings.NewReader(`
secrets:
- path: /chat-replicate-age
  keys: [identity]
addr: ${identity}
`), true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config.Addr != "AGE-SECRET-KEY-1REAL" {
		t.Errorf("addr is %q, want the value the store answered", config.Addr)
	}
	if *reads != 1 {
		t.Errorf("KMS served %d reads, want one per key", *reads)
	}
}

// The environment cannot stand in for the store. A variable of the same name is
// how a credential leaks back onto the pod, so the store answers first.
func TestTheStoreAnswersAheadOfTheEnvironment(t *testing.T) {
	stand(t, "projected-token", map[string]string{"identity": "from-the-store"})
	t.Setenv("identity", "from-the-environment")

	config, err := main.ParseConfig(strings.NewReader(`
secrets:
- path: /chat-replicate-age
  keys: [identity]
addr: ${identity}
`), true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config.Addr != "from-the-store" {
		t.Errorf("addr is %q, want the store's answer", config.Addr)
	}
}

// Names the store does not hold still come from the environment: only the keys
// a `secrets:` block claims move.
func TestEverythingElseStillComesFromTheEnvironment(t *testing.T) {
	stand(t, "projected-token", map[string]string{"identity": "from-the-store"})
	t.Setenv("REPLICATE_TEST_ADDR", ":9999")

	config, err := main.ParseConfig(strings.NewReader(`
secrets:
- path: /chat-replicate-age
  keys: [identity]
addr: ${REPLICATE_TEST_ADDR}
`), true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config.Addr != ":9999" {
		t.Errorf("addr is %q, want the environment's answer", config.Addr)
	}
}

// A key the store does not hold stops the process here, rather than starting a
// replica whose credential expanded to the empty string.
func TestAMissingKeyStopsTheProcess(t *testing.T) {
	stand(t, "projected-token", map[string]string{"identity": "from-the-store"})

	_, err := main.ParseConfig(strings.NewReader(`
secrets:
- path: /chat-replicate-age
  keys: [identity, recipients]
addr: ${identity}
`), true)
	if err == nil {
		t.Fatal("expected an error naming the key the store does not hold")
	}
	if !strings.Contains(err.Error(), "recipients") {
		t.Errorf("error is %q, want it to name recipients", err)
	}
}

// A configuration that names no store is untouched by any of this.
func TestNoStoreMeansNoIdentityIsNeeded(t *testing.T) {
	t.Setenv("HANZO_SA_TOKEN", filepath.Join(t.TempDir(), "absent"))
	t.Setenv("REPLICATE_TEST_ADDR", ":8080")

	config, err := main.ParseConfig(strings.NewReader("addr: ${REPLICATE_TEST_ADDR}\n"), true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if config.Addr != ":8080" {
		t.Errorf("addr is %q", config.Addr)
	}
}
