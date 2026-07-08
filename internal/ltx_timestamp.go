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
