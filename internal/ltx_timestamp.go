package internal

import (
	"bytes"
	"fmt"
	"io"
	"time"

	"github.com/hanzoai/ltx"
)

// ageStreamIntro is the fixed first line of every age v1 ciphertext. When a
// replica client receives an age-encrypted LTX object it cannot read the LTX
// header (the bytes are ciphertext), so timestamp extraction falls back to the
// write time instead of failing.
const ageStreamIntro = "age-encryption.org/v1"

// ExtractLTXTimestamp returns the timestamp to record for an object being
// written to a replica, along with a reader that replays the full stream (the
// header bytes consumed for inspection are preserved, so callers upload the
// complete object).
//
// For a plaintext LTX object the timestamp is read from the LTX header. For an
// age-encrypted object the header is unreadable, so the current time is used —
// this is what makes client-side age encryption work with every storage
// backend, all of which otherwise fail trying to parse an LTX header out of
// ciphertext ("extract timestamp from LTX header: invalid LTX file"). A stream
// that is neither a valid LTX nor age ciphertext is a genuine corrupt-object
// error and is surfaced as before.
func ExtractLTXTimestamp(rd io.Reader) (time.Time, io.Reader, error) {
	// Sniff exactly enough bytes to recognise an age ciphertext intro, then
	// replay them so no data is lost regardless of which branch we take.
	sniff := make([]byte, len(ageStreamIntro))
	n, _ := io.ReadFull(rd, sniff)
	sniff = sniff[:n]
	full := io.MultiReader(bytes.NewReader(sniff), rd)

	if string(sniff) == ageStreamIntro {
		return time.Now().UTC(), full, nil
	}

	hdr, reader, err := ltx.PeekHeader(full)
	if err != nil {
		return time.Time{}, reader, fmt.Errorf("extract timestamp from LTX header: %w", err)
	}
	return time.UnixMilli(hdr.Timestamp).UTC(), reader, nil
}

// DecryptIfSealed returns a reader of plaintext LTX bytes from rd, decrypting only
// when rd actually IS an age ciphertext.
//
// Why this is not simply age.Decrypt: having identities configured is not the same
// fact as the object being sealed. A bucket can hold both — ours does, because
// snapshots were written before age was wired and sealed ones landed after — and
// decrypting unconditionally turns every older object into:
//
//	age decrypt: failed to read header: parsing age header: unexpected intro "LTX1\x00…"
//
// That took chat, hanzo-app, dataroom and studio down: the restore init container
// cannot rehydrate an empty PVC, so no pod with this sidecar can cold-start. There
// was no config that read both, because removing the identities makes the WRITER
// fail closed instead.
//
// The write path already made exactly this distinction — ExtractLTXTimestamp above
// sniffs the same intro so that mixed buckets upload correctly. This is the read
// half of that, so the two directions agree.
//
// It does NOT weaken encryption: with zero identities the caller never had a key
// and this returns the stream untouched, exactly as before. A sealed object still
// requires a valid identity, and a corrupt one still fails — only the plaintext
// case, which previously could not be read at all, now round-trips.
func DecryptIfSealed(rd io.Reader, decrypt func(io.Reader) (io.Reader, error)) (io.Reader, error) {
	// Sniff exactly the intro's width and replay it, so neither branch loses bytes.
	sniff := make([]byte, len(ageStreamIntro))
	n, _ := io.ReadFull(rd, sniff)
	sniff = sniff[:n]
	full := io.MultiReader(bytes.NewReader(sniff), rd)

	if string(sniff) != ageStreamIntro {
		return full, nil // plaintext LTX (or a corrupt object, which LTX itself will reject)
	}
	return decrypt(full)
}
