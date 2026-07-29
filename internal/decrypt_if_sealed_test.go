package internal

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// The outage this exists to prevent: a bucket holding BOTH plaintext and sealed
// objects. Restore had identities configured, so it decrypted unconditionally and
// died on every older object with
//
//	age decrypt: failed to read header: parsing age header: unexpected intro "LTX1…"
//
// which meant no pod with the replicate sidecar could cold-start from an empty PVC.
func TestDecryptIfSealedPassesPlaintextThrough(t *testing.T) {
	// A real LTX object begins "LTX1" — exactly what the failure message quoted.
	plain := "LTX1\x00\x00\x00\x02 …the rest of a perfectly good LTX file"
	called := false
	rd, err := DecryptIfSealed(strings.NewReader(plain), func(io.Reader) (io.Reader, error) {
		called = true
		return nil, errors.New("decrypt must not be attempted on a plaintext object")
	})
	if err != nil {
		t.Fatalf("plaintext object refused: %v", err)
	}
	if called {
		t.Fatal("decrypt was attempted on a plaintext object — this is the outage")
	}
	got, _ := io.ReadAll(rd)
	// Byte-exact: the sniffed intro must be replayed, or the LTX header is corrupt.
	if string(got) != plain {
		t.Fatalf("stream not replayed intact:\n got %q\nwant %q", got, plain)
	}
}

func TestDecryptIfSealedDecryptsASealedObject(t *testing.T) {
	sealed := ageStreamIntro + "\n-> X25519 abc\n--- mac\n<ciphertext>"
	var handed []byte
	rd, err := DecryptIfSealed(strings.NewReader(sealed), func(x io.Reader) (io.Reader, error) {
		handed, _ = io.ReadAll(x)
		return strings.NewReader("PLAINTEXT-OUT"), nil
	})
	if err != nil {
		t.Fatalf("sealed object refused: %v", err)
	}
	// The decrypter must receive the WHOLE stream, intro included — age parses it.
	if string(handed) != sealed {
		t.Fatalf("decrypter got a truncated stream:\n got %q\nwant %q", handed, sealed)
	}
	if got, _ := io.ReadAll(rd); string(got) != "PLAINTEXT-OUT" {
		t.Fatalf("decrypted output = %q", got)
	}
}

// A sealed object with no usable key must still FAIL. Tolerating plaintext must not
// become tolerating an undecryptable object.
func TestDecryptIfSealedStillFailsOnABadKey(t *testing.T) {
	sealed := ageStreamIntro + "\n-> X25519 abc\n--- mac\n<ciphertext>"
	_, err := DecryptIfSealed(strings.NewReader(sealed), func(io.Reader) (io.Reader, error) {
		return nil, errors.New("no identity matched")
	})
	if err == nil {
		t.Fatal("a sealed object with no matching identity was accepted")
	}
}

// A stream SHORTER than the intro must not be mistaken for a sealed one.
func TestDecryptIfSealedHandlesAShortStream(t *testing.T) {
	for _, short := range []string{"", "LTX1", ageStreamIntro[:5]} {
		called := false
		rd, err := DecryptIfSealed(bytes.NewReader([]byte(short)), func(io.Reader) (io.Reader, error) {
			called = true
			return nil, errors.New("nope")
		})
		if err != nil {
			t.Fatalf("short stream %q refused: %v", short, err)
		}
		if called {
			t.Fatalf("short stream %q was treated as sealed", short)
		}
		if got, _ := io.ReadAll(rd); string(got) != short {
			t.Fatalf("short stream %q not replayed, got %q", short, got)
		}
	}
}
