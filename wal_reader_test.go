package replicate_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"testing"

	"github.com/hanzoai/replicate"
)

func TestWALReader(t *testing.T) {
	t.Run("OK", func(t *testing.T) {
		buf := make([]byte, 4096)
		b, err := os.ReadFile("testdata/wal-reader/ok/wal")
		if err != nil {
			t.Fatal(err)
		}

		// Initialize reader with header info.
		r, err := replicate.NewWALReader(bytes.NewReader(b), slog.Default())
		if err != nil {
			t.Fatal(err)
		} else if got, want := r.PageSize(), uint32(4096); got != want {
			t.Fatalf("PageSize()=%d, want %d", got, want)
		} else if got, want := r.Offset(), int64(0); got != want {
			t.Fatalf("Offset()=%d, want %d", got, want)
		}

		// Read first frame.
		if pgno, commit, err := r.ReadFrame(context.Background(), buf); err != nil {
			t.Fatal(err)
		} else if got, want := pgno, uint32(1); got != want {
			t.Fatalf("pgno=%d, want %d", got, want)
		} else if got, want := commit, uint32(0); got != want {
			t.Fatalf("commit=%d, want %d", got, want)
		} else if !bytes.Equal(buf, b[56:4152]) {
			t.Fatal("page data mismatch")
		} else if got, want := r.Offset(), int64(32); got != want {
			t.Fatalf("Offset()=%d, want %d", got, want)
		}

		// Read second frame. End of transaction.
		if pgno, commit, err := r.ReadFrame(context.Background(), buf); err != nil {
			t.Fatal(err)
		} else if got, want := pgno, uint32(2); got != want {
			t.Fatalf("pgno=%d, want %d", got, want)
		} else if got, want := commit, uint32(2); got != want {
			t.Fatalf("commit=%d, want %d", got, want)
		} else if !bytes.Equal(buf, b[4176:8272]) {
			t.Fatal("page data mismatch")
		} else if got, want := r.Offset(), int64(4152); got != want {
			t.Fatalf("Offset()=%d, want %d", got, want)
		}

		// Read third frame.
		if pgno, commit, err := r.ReadFrame(context.Background(), buf); err != nil {
			t.Fatal(err)
		} else if got, want := pgno, uint32(2); got != want {
			t.Fatalf("pgno=%d, want %d", got, want)
		} else if got, want := commit, uint32(2); got != want {
			t.Fatalf("commit=%d, want %d", got, want)
		} else if !bytes.Equal(buf, b[8296:12392]) {
			t.Fatal("page data mismatch")
		} else if got, want := r.Offset(), int64(8272); got != want {
			t.Fatalf("Offset()=%d, want %d", got, want)
		}

		if _, _, err := r.ReadFrame(context.Background(), buf); !errors.Is(err, io.EOF) {
			t.Fatalf("unexpected error: %s", err)
		}
	})

	t.Run("SaltMismatch", func(t *testing.T) {
		buf := make([]byte, 4096)
		b, err := os.ReadFile("testdata/wal-reader/salt-mismatch/wal")
		if err != nil {
			t.Fatal(err)
		}

		// Initialize reader with header info.
		r, err := replicate.NewWALReader(bytes.NewReader(b), slog.Default())
		if err != nil {
			t.Fatal(err)
		} else if got, want := r.PageSize(), uint32(4096); got != want {
			t.Fatalf("PageSize()=%d, want %d", got, want)
		} else if got, want := r.Offset(), int64(0); got != want {
			t.Fatalf("Offset()=%d, want %d", got, want)
		}

		// Read first frame.
		if pgno, commit, err := r.ReadFrame(context.Background(), buf); err != nil {
			t.Fatal(err)
		} else if got, want := pgno, uint32(1); got != want {
			t.Fatalf("pgno=%d, want %d", got, want)
		} else if got, want := commit, uint32(0); got != want {
			t.Fatalf("commit=%d, want %d", got, want)
		} else if !bytes.Equal(buf, b[56:4152]) {
			t.Fatal("page data mismatch")
		}

		// Read second frame. Salt has been altered so it doesn't match header.
		if _, _, err := r.ReadFrame(context.Background(), buf); !errors.Is(err, io.EOF) {
			t.Fatalf("unexpected error: %s", err)
		}
	})

	t.Run("FrameChecksumMismatch", func(t *testing.T) {
		buf := make([]byte, 4096)
		b, err := os.ReadFile("testdata/wal-reader/frame-checksum-mismatch/wal")
		if err != nil {
			t.Fatal(err)
		}

		// Initialize reader with header info.
		r, err := replicate.NewWALReader(bytes.NewReader(b), slog.Default())
		if err != nil {
			t.Fatal(err)
		} else if got, want := r.PageSize(), uint32(4096); got != want {
			t.Fatalf("PageSize()=%d, want %d", got, want)
		} else if got, want := r.Offset(), int64(0); got != want {
			t.Fatalf("Offset()=%d, want %d", got, want)
		}

		// Read first frame.
		if pgno, commit, err := r.ReadFrame(context.Background(), buf); err != nil {
			t.Fatal(err)
		} else if got, want := pgno, uint32(1); got != want {
			t.Fatalf("pgno=%d, want %d", got, want)
		} else if got, want := commit, uint32(0); got != want {
			t.Fatalf("commit=%d, want %d", got, want)
		} else if !bytes.Equal(buf, b[56:4152]) {
			t.Fatal("page data mismatch")
		}

		// Read second frame. Checksum has been altered so it doesn't match.
		if _, _, err := r.ReadFrame(context.Background(), buf); !errors.Is(err, io.EOF) {
			t.Fatalf("unexpected error: %s", err)
		}
	})

	t.Run("ZeroLength", func(t *testing.T) {
		_, err := replicate.NewWALReader(bytes.NewReader(nil), slog.Default())
		if !errors.Is(err, io.EOF) {
			t.Fatalf("unexpected error: %#v", err)
		}
	})

	t.Run("PartialHeader", func(t *testing.T) {
		_, err := replicate.NewWALReader(bytes.NewReader(make([]byte, 10)), slog.Default())
		if !errors.Is(err, io.EOF) {
			t.Fatalf("unexpected error: %#v", err)
		}
	})

	t.Run("BadMagic", func(t *testing.T) {
		_, err := replicate.NewWALReader(bytes.NewReader(make([]byte, 32)), slog.Default())
		if err == nil || err.Error() != `invalid wal header magic: 0` {
			t.Fatalf("unexpected error: %#v", err)
		}
	})

	t.Run("BadHeaderChecksum", func(t *testing.T) {
		data := []byte{
			0x37, 0x7f, 0x06, 0x83, 0x00, 0x00, 0x00, 0x00,
			0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
		_, err := replicate.NewWALReader(bytes.NewReader(data), slog.Default())
		if !errors.Is(err, io.EOF) {
			t.Fatalf("unexpected error: %#v", err)
		}
	})

	t.Run("BadHeaderVersion", func(t *testing.T) {
		data := []byte{
			0x37, 0x7f, 0x06, 0x83, 0x00, 0x00, 0x00, 0x01,
			0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
			0x15, 0x7b, 0x20, 0x92, 0xbb, 0xf8, 0x34, 0x1d}
		_, err := replicate.NewWALReader(bytes.NewReader(data), slog.Default())
		if err == nil || err.Error() != `unsupported wal version: 1` {
			t.Fatalf("unexpected error: %#v", err)
		}
	})

	t.Run("ErrBufferSize", func(t *testing.T) {
		b, err := os.ReadFile("testdata/wal-reader/ok/wal")
		if err != nil {
			t.Fatal(err)
		}

		// Initialize reader with header info.
		r, err := replicate.NewWALReader(bytes.NewReader(b), slog.Default())
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := r.ReadFrame(context.Background(), make([]byte, 512)); err == nil || err.Error() != `WALReader.ReadFrame(): buffer size (512) must match page size (4096)` {
			t.Fatalf("unexpected error: %#v", err)
		}
	})

	t.Run("ErrPartialFrameHeader", func(t *testing.T) {
		b, err := os.ReadFile("testdata/wal-reader/ok/wal")
		if err != nil {
			t.Fatal(err)
		}

		r, err := replicate.NewWALReader(bytes.NewReader(b[:40]), slog.Default())
		if err != nil {
			t.Fatal(err)
		} else if _, _, err := r.ReadFrame(context.Background(), make([]byte, 4096)); !errors.Is(err, io.EOF) {
			t.Fatalf("unexpected error: %#v", err)
		}
	})

	t.Run("ErrFrameHeaderOnly", func(t *testing.T) {
		b, err := os.ReadFile("testdata/wal-reader/ok/wal")
		if err != nil {
			t.Fatal(err)
		}

		r, err := replicate.NewWALReader(bytes.NewReader(b[:56]), slog.Default())
		if err != nil {
			t.Fatal(err)
		} else if _, _, err := r.ReadFrame(context.Background(), make([]byte, 4096)); !errors.Is(err, io.EOF) {
			t.Fatalf("unexpected error: %#v", err)
		}
	})

	t.Run("ErrPartialFrameData", func(t *testing.T) {
		b, err := os.ReadFile("testdata/wal-reader/ok/wal")
		if err != nil {
			t.Fatal(err)
		}

		r, err := replicate.NewWALReader(bytes.NewReader(b[:1000]), slog.Default())
		if err != nil {
			t.Fatal(err)
		} else if _, _, err := r.ReadFrame(context.Background(), make([]byte, 4096)); !errors.Is(err, io.EOF) {
			t.Fatalf("unexpected error: %#v", err)
		}
	})
}

// TestWALReader_PageMap_ConcurrentAppend verifies that a WAL scan is bounded by
// the size of the WAL when the reader was opened.
//
// SQLite appends frames to the WAL while replicate reads it. An unbounded
// reader chases the writer's tail: every read finds more bytes, so the scan
// never reaches EOF. In DB.checkpoint() that scan is the pre-checkpoint sync,
// so the checkpoint never executes, the WAL is never truncated, and it grows
// until the disk fills.
//
// growingWAL models the writer always winning that race: it appends a fresh
// committed frame on every read. The scan must still return exactly the frames
// that existed when the reader was opened.
func TestWALReader_PageMap_ConcurrentAppend(t *testing.T) {
	// Two transactions: pages 1 & 2 commit at size 2, then page 3 commits at 3.
	w := newGrowingWAL(4096, 200)
	w.appendTx(1, 0)
	w.appendTx(2, 2)
	w.appendTx(3, 3)
	initialSize := w.Size()

	r, err := replicate.NewWALReader(w, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	m, maxOffset, commit, err := r.PageMap(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if w.appended() == 0 {
		t.Fatal("wal did not grow during the scan; test is not exercising the race")
	}
	if got, want := commit, uint32(3); got != want {
		t.Fatalf("commit=%d, want %d (scan ran past the frames present when opened)", got, want)
	}
	if got, want := len(m), 3; got != want {
		t.Fatalf("len(m)=%d, want %d", got, want)
	}
	for pgno := uint32(1); pgno <= 3; pgno++ {
		if _, ok := m[pgno]; !ok {
			t.Fatalf("page %d missing from page map", pgno)
		}
	}
	if got, want := maxOffset, initialSize; got != want {
		t.Fatalf("maxOffset=%d, want %d", got, want)
	}
}

// growingWAL is an io.ReaderAt holding a valid SQLite WAL that appends one more
// committed frame on every read, up to maxAppend frames.
type growingWAL struct {
	mu               sync.Mutex
	buf              []byte
	pageSize         uint32
	salt1, salt2     uint32
	chksum1, chksum2 uint32
	nextPgno         uint32
	maxAppend, n     int
}

func newGrowingWAL(pageSize uint32, maxAppend int) *growingWAL {
	w := &growingWAL{
		pageSize:  pageSize,
		salt1:     0x1b9a294b,
		salt2:     0x37f91916,
		nextPgno:  4,
		maxAppend: maxAppend,
	}

	hdr := make([]byte, replicate.WALHeaderSize)
	binary.BigEndian.PutUint32(hdr[0:], 0x377f0683) // big-endian checksums
	binary.BigEndian.PutUint32(hdr[4:], 3007000)
	binary.BigEndian.PutUint32(hdr[8:], pageSize)
	binary.BigEndian.PutUint32(hdr[12:], 1)
	binary.BigEndian.PutUint32(hdr[16:], w.salt1)
	binary.BigEndian.PutUint32(hdr[20:], w.salt2)
	w.chksum1, w.chksum2 = replicate.WALChecksum(binary.BigEndian, 0, 0, hdr[:24])
	binary.BigEndian.PutUint32(hdr[24:], w.chksum1)
	binary.BigEndian.PutUint32(hdr[28:], w.chksum2)

	w.buf = hdr
	return w
}

// appendTx appends a single frame carrying pgno. A non-zero commit marks it as
// the last frame of a transaction against a database of that many pages.
func (w *growingWAL) appendTx(pgno, commit uint32) {
	data := make([]byte, w.pageSize)
	for i := range data {
		data[i] = byte(pgno)
	}

	hdr := make([]byte, replicate.WALFrameHeaderSize)
	binary.BigEndian.PutUint32(hdr[0:], pgno)
	binary.BigEndian.PutUint32(hdr[4:], commit)
	binary.BigEndian.PutUint32(hdr[8:], w.salt1)
	binary.BigEndian.PutUint32(hdr[12:], w.salt2)
	w.chksum1, w.chksum2 = replicate.WALChecksum(binary.BigEndian, w.chksum1, w.chksum2, hdr[:8])
	w.chksum1, w.chksum2 = replicate.WALChecksum(binary.BigEndian, w.chksum1, w.chksum2, data)
	binary.BigEndian.PutUint32(hdr[16:], w.chksum1)
	binary.BigEndian.PutUint32(hdr[20:], w.chksum2)

	w.buf = append(append(w.buf, hdr...), data...)
}

func (w *growingWAL) Size() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return int64(len(w.buf))
}

func (w *growingWAL) appended() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.n
}

func (w *growingWAL) ReadAt(p []byte, off int64) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// The writer always wins the race: every read finds more WAL than the last.
	if w.n < w.maxAppend {
		w.appendTx(w.nextPgno, w.nextPgno)
		w.nextPgno++
		w.n++
	}

	if off >= int64(len(w.buf)) {
		return 0, io.EOF
	}
	n := copy(p, w.buf[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func TestWALReader_FrameSaltsUntil(t *testing.T) {
	t.Run("OK", func(t *testing.T) {
		b, err := os.ReadFile("testdata/wal-reader/frame-salts/wal")
		if err != nil {
			t.Fatal(err)
		}

		r, err := replicate.NewWALReader(bytes.NewReader(b), slog.Default())
		if err != nil {
			t.Fatal(err)
		}

		m, err := r.FrameSaltsUntil(context.Background(), [2]uint32{0x00000000, 0x00000000})
		if err != nil {
			t.Fatal(err)
		}
		if got, want := len(m), 3; got != want {
			t.Fatalf("len(m)=%d, want %d", got, want)
		}
		if _, ok := m[[2]uint32{0x1b9a294b, 0x37f91916}]; !ok {
			t.Fatalf("salt 0 not found")
		}
		if _, ok := m[[2]uint32{0x1b9a294a, 0x031f195e}]; !ok {
			t.Fatalf("salt 1 not found")
		}
		if _, ok := m[[2]uint32{0x1b9a2949, 0x13b3dd67}]; !ok {
			t.Fatalf("salt 2 not found")
		}
	})
}
