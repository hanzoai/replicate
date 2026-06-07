package file

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/hanzoai/replicate"
)

const (
	DefaultLeaseTTL  = 30 * time.Second
	DefaultLeaseFile = "lock.json"
	LeaserType       = "file"
)

var (
	_ replicate.Leaser = (*Leaser)(nil)

	ErrLeaseRequired        = errors.New("lease required")
	ErrLeaseETagRequired    = errors.New("lease etag required")
	ErrLeaseAlreadyReleased = errors.New("lease already released")
)

// Leaser implements replicate.Leaser using POSIX filesystem semantics:
// O_CREAT|O_EXCL for initial creation, content-hash ETag matching for
// updates, atomic rename for cutover. Mirrors the S3 leaser's CAS
// contract so callers can swap backends without code changes.
type Leaser struct {
	logger *slog.Logger

	Dir   string        // directory containing the lock file
	Path  string        // relative path under Dir; empty means DefaultLeaseFile
	TTL   time.Duration // lease TTL on Acquire/Renew
	Owner string        // string written to the lease body for forensics
}

// NewLeaser constructs a file-backed lease coordinator rooted at dir.
// Lease state is a single JSON file at dir/path/lock.json. Concurrent
// processes coordinate via filesystem CAS — O_EXCL for first writer,
// content-hash ETag for renew/release.
func NewLeaser(dir string) *Leaser {
	owner, _ := os.Hostname()
	if owner == "" {
		owner = fmt.Sprintf("pid-%d", os.Getpid())
	} else {
		owner = fmt.Sprintf("%s:%d", owner, os.Getpid())
	}
	return &Leaser{
		logger: slog.Default().WithGroup("file-leaser"),
		Dir:    dir,
		TTL:    DefaultLeaseTTL,
		Owner:  owner,
	}
}

func (l *Leaser) SetLogger(logger *slog.Logger) {
	l.logger = logger.WithGroup("file-leaser")
}

func (l *Leaser) Type() string { return LeaserType }

func (l *Leaser) lockPath() string {
	name := l.Path
	if name == "" {
		name = DefaultLeaseFile
	}
	return filepath.Join(l.Dir, name)
}

func (l *Leaser) AcquireLease(ctx context.Context) (*replicate.Lease, error) {
	existing, etag, err := l.readLease()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read existing lease: %w", err)
	}

	if existing != nil && !existing.IsExpired() {
		return nil, &replicate.LeaseExistsError{
			Owner:     existing.Owner,
			ExpiresAt: existing.ExpiresAt,
		}
	}

	var generation int64 = 1
	if existing != nil {
		generation = existing.Generation + 1
	}

	newLease := &replicate.Lease{
		Generation: generation,
		ExpiresAt:  time.Now().Add(l.TTL),
		Owner:      l.Owner,
	}

	newETag, err := l.writeLease(newLease, etag)
	if err != nil {
		var leaseErr *replicate.LeaseExistsError
		if errors.As(err, &leaseErr) {
			if current, _, readErr := l.readLease(); readErr == nil && current != nil {
				return nil, &replicate.LeaseExistsError{
					Owner:     current.Owner,
					ExpiresAt: current.ExpiresAt,
				}
			}
		}
		return nil, err
	}

	newLease.ETag = newETag
	l.logger.Debug("lease acquired",
		"generation", newLease.Generation,
		"owner", newLease.Owner,
		"expires_at", newLease.ExpiresAt,
		"etag", newLease.ETag)

	return newLease, nil
}

func (l *Leaser) RenewLease(ctx context.Context, lease *replicate.Lease) (*replicate.Lease, error) {
	if lease == nil {
		return nil, ErrLeaseRequired
	}
	if lease.ETag == "" {
		return nil, ErrLeaseETagRequired
	}

	newLease := &replicate.Lease{
		Generation: lease.Generation,
		ExpiresAt:  time.Now().Add(l.TTL),
		Owner:      l.Owner,
	}

	newETag, err := l.writeLease(newLease, lease.ETag)
	if err != nil {
		var leaseErr *replicate.LeaseExistsError
		if errors.As(err, &leaseErr) {
			return nil, replicate.ErrLeaseNotHeld
		}
		return nil, err
	}

	newLease.ETag = newETag
	return newLease, nil
}

func (l *Leaser) ReleaseLease(ctx context.Context, lease *replicate.Lease) error {
	if lease == nil {
		return ErrLeaseRequired
	}
	if lease.ETag == "" {
		return ErrLeaseETagRequired
	}

	current, etag, err := l.readLease()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrLeaseAlreadyReleased
		}
		return fmt.Errorf("read lease: %w", err)
	}
	if etag != lease.ETag {
		return replicate.ErrLeaseNotHeld
	}
	_ = current

	if err := os.Remove(l.lockPath()); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrLeaseAlreadyReleased
		}
		return fmt.Errorf("remove lease: %w", err)
	}
	return nil
}

// readLease loads the lock file, returning the parsed lease and its
// content-hash ETag. Returns os.ErrNotExist when no lock file is
// present.
func (l *Leaser) readLease() (*replicate.Lease, string, error) {
	data, err := os.ReadFile(l.lockPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", os.ErrNotExist
		}
		return nil, "", err
	}
	var lease replicate.Lease
	if err := json.Unmarshal(data, &lease); err != nil {
		return nil, "", fmt.Errorf("decode lease: %w", err)
	}
	etag := contentETag(data)
	lease.ETag = etag
	return &lease, etag, nil
}

// writeLease performs a content-hash CAS: when etag is "" the file
// must not exist (O_EXCL); otherwise the on-disk content must hash to
// etag, otherwise LeaseExistsError. Final write is atomic via temp +
// rename.
func (l *Leaser) writeLease(lease *replicate.Lease, etag string) (string, error) {
	data, err := json.Marshal(lease)
	if err != nil {
		return "", fmt.Errorf("encode lease: %w", err)
	}

	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir lease dir: %w", err)
	}

	lockPath := l.lockPath()

	if etag == "" {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				return "", &replicate.LeaseExistsError{}
			}
			return "", fmt.Errorf("create lease: %w", err)
		}
		if _, err := f.Write(data); err != nil {
			_ = f.Close()
			_ = os.Remove(lockPath)
			return "", fmt.Errorf("write lease: %w", err)
		}
		if err := f.Close(); err != nil {
			return "", fmt.Errorf("close lease: %w", err)
		}
		return contentETag(data), nil
	}

	current, err := os.ReadFile(lockPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", &replicate.LeaseExistsError{}
		}
		return "", fmt.Errorf("read lease: %w", err)
	}
	if contentETag(current) != etag {
		return "", &replicate.LeaseExistsError{}
	}

	tmp, err := os.CreateTemp(l.Dir, ".lock-*.json")
	if err != nil {
		return "", fmt.Errorf("create temp: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmpName, lockPath); err != nil {
		_ = os.Remove(tmpName)
		return "", fmt.Errorf("rename lease: %w", err)
	}
	return contentETag(data), nil
}

func contentETag(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
