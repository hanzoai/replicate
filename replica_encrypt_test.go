package replicate_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/luxfi/age"
	"github.com/hanzoai/ltx"

	"github.com/hanzoai/replicate"
	"github.com/hanzoai/replicate/mock"
)

// TestReplicaEncryptDecryptRoundTrip writes data through the Replica's encrypt
// path (WriteLTXFile), then reads it back through the decrypt path
// (OpenLTXFile) and verifies the bytes match.
func TestReplicaEncryptDecryptRoundTrip(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	recipient := identity.Recipient()

	plaintext := []byte("test WAL segment data — roundtrip verification")

	// Capture encrypted bytes written by Replica.WriteLTXFile.
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
	r.AgeRecipients = []age.Recipient{recipient}
	r.AgeIdentities = []age.Identity{identity}

	// Write through encrypt path.
	if _, err := r.WriteLTXFile(context.Background(), 0, 1, 1, bytes.NewReader(plaintext)); err != nil {
		t.Fatalf("WriteLTXFile: %v", err)
	}

	// Read through decrypt path.
	rc, err := r.OpenLTXFile(context.Background(), 0, 1, 1, 0, 0)
	if err != nil {
		t.Fatalf("OpenLTXFile: %v", err)
	}
	defer rc.Close()

	decrypted, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Fatalf("roundtrip mismatch:\n  want: %q\n  got:  %q", plaintext, decrypted)
	}
}

// TestReplicaEncryptedOutputDiffers verifies that the encrypted output stored
// by WriteLTXFile is not equal to the original plaintext.
func TestReplicaEncryptedOutputDiffers(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	recipient := identity.Recipient()

	plaintext := []byte("plaintext that must not appear in ciphertext")

	var stored bytes.Buffer
	client := &mock.ReplicaClient{
		WriteLTXFileFunc: func(_ context.Context, _ int, _, _ ltx.TXID, r io.Reader) (*ltx.FileInfo, error) {
			if _, err := io.Copy(&stored, r); err != nil {
				return nil, err
			}
			return &ltx.FileInfo{MinTXID: 1, MaxTXID: 1}, nil
		},
	}

	r := replicate.NewReplica(nil)
	r.Client = client
	r.AgeRecipients = []age.Recipient{recipient}

	if _, err := r.WriteLTXFile(context.Background(), 0, 1, 1, bytes.NewReader(plaintext)); err != nil {
		t.Fatalf("WriteLTXFile: %v", err)
	}

	if bytes.Equal(plaintext, stored.Bytes()) {
		t.Fatal("encrypted output equals plaintext — encryption did not transform the data")
	}

	// Also verify plaintext does not appear as a substring of the ciphertext.
	if bytes.Contains(stored.Bytes(), plaintext) {
		t.Fatal("plaintext found as substring of ciphertext")
	}
}

// TestReplicaDecryptWithWrongKey verifies that decrypting with a different
// identity than the one used for encryption fails.
func TestReplicaDecryptWithWrongKey(t *testing.T) {
	encIdentity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	wrongIdentity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}

	plaintext := []byte("secret data encrypted for a specific recipient")

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

	// Write with the correct recipient.
	rw := replicate.NewReplica(nil)
	rw.Client = client
	rw.AgeRecipients = []age.Recipient{encIdentity.Recipient()}

	if _, err := rw.WriteLTXFile(context.Background(), 0, 1, 1, bytes.NewReader(plaintext)); err != nil {
		t.Fatalf("WriteLTXFile: %v", err)
	}

	// Read with the wrong identity.
	rr := replicate.NewReplica(nil)
	rr.Client = client
	rr.AgeIdentities = []age.Identity{wrongIdentity}

	rc, err := rr.OpenLTXFile(context.Background(), 0, 1, 1, 0, 0)
	if err == nil {
		// If OpenLTXFile didn't fail, reading should fail.
		_, readErr := io.ReadAll(rc)
		rc.Close()
		if readErr == nil {
			t.Fatal("expected error decrypting with wrong key, got none")
		}
	}
	// err != nil is the expected case — age.Decrypt returns an error.
}

// TestReplicaNoEncryption verifies that without age keys configured, data
// passes through WriteLTXFile and OpenLTXFile unmodified.
func TestReplicaNoEncryption(t *testing.T) {
	plaintext := []byte("unencrypted WAL data should pass through verbatim")

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
	// No AgeRecipients, no AgeIdentities — encryption disabled.

	if r.EncryptionEnabled() {
		t.Fatal("EncryptionEnabled should be false with no recipients")
	}
	if r.DecryptionEnabled() {
		t.Fatal("DecryptionEnabled should be false with no identities")
	}

	// Write.
	if _, err := r.WriteLTXFile(context.Background(), 0, 1, 1, bytes.NewReader(plaintext)); err != nil {
		t.Fatalf("WriteLTXFile: %v", err)
	}

	// Stored data should be identical to plaintext (no encryption applied).
	if !bytes.Equal(plaintext, stored.Bytes()) {
		t.Fatalf("stored data differs from plaintext when encryption is disabled:\n  want: %q\n  got:  %q", plaintext, stored.Bytes())
	}

	// Read.
	rc, err := r.OpenLTXFile(context.Background(), 0, 1, 1, 0, 0)
	if err != nil {
		t.Fatalf("OpenLTXFile: %v", err)
	}
	defer rc.Close()

	result, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}

	if !bytes.Equal(plaintext, result) {
		t.Fatalf("passthrough mismatch:\n  want: %q\n  got:  %q", plaintext, result)
	}
}
