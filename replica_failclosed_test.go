package replicate_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/hanzoai/ltx"
	"github.com/luxfi/age"

	"github.com/hanzoai/replicate"
	"github.com/hanzoai/replicate/mock"
)

// ageIntro is the fixed header every age v1 ciphertext begins with. A plaintext
// Litestream LTX object begins with the "LTX1" magic instead; asserting on both
// proves the destination holds ciphertext, not clear-text WAL.
const ageIntro = "age-encryption.org/v1"

// TestWriteLTXFileFailsClosedWithoutRecipient is the core regression test for the
// fleet-wide plaintext exposure: with RequireEncryption set but no age recipient
// configured, WriteLTXFile MUST return ErrEncryptionRequired and MUST NOT hand
// any bytes to the underlying client. Previously it silently wrote plaintext.
func TestWriteLTXFileFailsClosedWithoutRecipient(t *testing.T) {
	var writeCalled bool
	client := &mock.ReplicaClient{
		WriteLTXFileFunc: func(_ context.Context, _ int, _, _ ltx.TXID, r io.Reader) (*ltx.FileInfo, error) {
			writeCalled = true
			_, _ = io.Copy(io.Discard, r)
			return &ltx.FileInfo{MinTXID: 1, MaxTXID: 1}, nil
		},
	}

	r := replicate.NewReplica(nil)
	r.Client = client
	r.RequireEncryption = true
	// No AgeRecipients — the misconfiguration under test.

	_, err := r.WriteLTXFile(context.Background(), 0, 1, 1, strings.NewReader("money ledger row: debit 100.00"))
	if !errors.Is(err, replicate.ErrEncryptionRequired) {
		t.Fatalf("expected ErrEncryptionRequired, got %v", err)
	}
	if writeCalled {
		t.Fatal("client.WriteLTXFile was called — plaintext would have reached the destination despite fail-closed policy")
	}
}

// TestStartFailsClosedWithoutRecipient verifies a monitored replica refuses to
// begin the write loop when encryption is required but unconfigured, so a
// misconfigured sidecar crash-loops instead of streaming plaintext WAL.
func TestStartFailsClosedWithoutRecipient(t *testing.T) {
	r := replicate.NewReplica(nil)
	r.Client = &mock.ReplicaClient{}
	r.MonitorEnabled = true
	r.RequireEncryption = true
	// No AgeRecipients.

	if err := r.Start(context.Background()); !errors.Is(err, replicate.ErrEncryptionRequired) {
		t.Fatalf("expected ErrEncryptionRequired from Start, got %v", err)
	}
}

// TestCompactFailsClosedWithoutRecipient verifies the compaction write path also
// fails closed: it must not decrypt source files and re-emit a plaintext
// compacted result. The guard runs before any source listing, so the client is
// never touched.
func TestCompactFailsClosedWithoutRecipient(t *testing.T) {
	var touched bool
	client := &mock.ReplicaClient{
		LTXFilesFunc: func(_ context.Context, _ int, _ ltx.TXID, _ bool) (ltx.FileIterator, error) {
			touched = true
			return nil, nil
		},
		WriteLTXFileFunc: func(_ context.Context, _ int, _, _ ltx.TXID, r io.Reader) (*ltx.FileInfo, error) {
			touched = true
			_, _ = io.Copy(io.Discard, r)
			return &ltx.FileInfo{}, nil
		},
	}

	c := replicate.NewCompactor(client, nil)
	c.RequireEncryption = true
	// No AgeRecipients.

	if _, err := c.Compact(context.Background(), 1); !errors.Is(err, replicate.ErrEncryptionRequired) {
		t.Fatalf("expected ErrEncryptionRequired from Compact, got %v", err)
	}
	if touched {
		t.Fatal("compactor read/wrote via the client despite fail-closed policy")
	}
}

// TestWriteLTXFileEncryptsWhenRequiredWithRecipient verifies the happy path:
// with RequireEncryption AND a recipient, the write succeeds, the stored object
// is age ciphertext (age header, not LTX1), and it round-trips back to the
// original plaintext through the decrypt path.
func TestWriteLTXFileEncryptsWhenRequiredWithRecipient(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}

	var stored bytes.Buffer
	client := &mock.ReplicaClient{
		WriteLTXFileFunc: func(_ context.Context, _ int, _, _ ltx.TXID, r io.Reader) (*ltx.FileInfo, error) {
			if _, err := io.Copy(&stored, r); err != nil {
				return nil, err
			}
			return &ltx.FileInfo{MinTXID: 1, MaxTXID: 1}, nil
		},
		OpenLTXFileFunc: func(_ context.Context, _ int, _, _ ltx.TXID, _, _ int64) (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(stored.Bytes())), nil
		},
	}

	r := replicate.NewReplica(nil)
	r.Client = client
	r.RequireEncryption = true
	r.AgeRecipients = []age.Recipient{identity.Recipient()}
	r.AgeIdentities = []age.Identity{identity}

	plaintext := []byte("money ledger row: debit 100.00")
	if _, err := r.WriteLTXFile(context.Background(), 0, 1, 1, bytes.NewReader(plaintext)); err != nil {
		t.Fatalf("WriteLTXFile: %v", err)
	}

	assertAgeCiphertext(t, stored.Bytes(), plaintext)

	// Round-trip: decrypt path returns the original bytes.
	rc, err := r.OpenLTXFile(context.Background(), 0, 1, 1, 0, 0)
	if err != nil {
		t.Fatalf("OpenLTXFile: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("round-trip mismatch:\n  want: %q\n  got:  %q", plaintext, got)
	}
}

// assertAgeCiphertext asserts that b is age ciphertext and NOT a plaintext
// LTX/SQLite object that contains the source data.
func assertAgeCiphertext(t *testing.T, b, plaintext []byte) {
	t.Helper()
	if !bytes.HasPrefix(b, []byte(ageIntro)) {
		t.Fatalf("stored object is not age ciphertext: header=%q", firstBytes(b, 24))
	}
	if bytes.HasPrefix(b, []byte(ltx.Magic)) {
		t.Fatalf("stored object begins with LTX plaintext magic %q", ltx.Magic)
	}
	if bytes.HasPrefix(b, []byte("SQLite format 3\x00")) {
		t.Fatal("stored object is a raw SQLite database")
	}
	if bytes.Contains(b, plaintext) {
		t.Fatal("plaintext found inside stored object — encryption did not protect the data")
	}
}

func firstBytes(b []byte, n int) []byte {
	if len(b) < n {
		return b
	}
	return b[:n]
}
