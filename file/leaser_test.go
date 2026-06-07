package file

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hanzoai/replicate"
)

func newLeaser(t *testing.T, owner string) *Leaser {
	t.Helper()
	l := NewLeaser(t.TempDir())
	l.Owner = owner
	l.TTL = 200 * time.Millisecond
	return l
}

func TestLeaser_AcquireFresh(t *testing.T) {
	ctx := context.Background()
	l := newLeaser(t, "node-a")
	lease, err := l.AcquireLease(ctx)
	if err != nil {
		t.Fatalf("AcquireLease: %v", err)
	}
	if lease.Generation != 1 {
		t.Errorf("generation: got %d want 1", lease.Generation)
	}
	if lease.Owner != "node-a" {
		t.Errorf("owner: got %q want node-a", lease.Owner)
	}
	if lease.ETag == "" {
		t.Errorf("etag empty")
	}
	if lease.IsExpired() {
		t.Errorf("fresh lease should not be expired")
	}
}

func TestLeaser_AcquireContended(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	a := NewLeaser(dir)
	a.Owner = "node-a"
	a.TTL = 5 * time.Second

	b := NewLeaser(dir)
	b.Owner = "node-b"
	b.TTL = 5 * time.Second

	if _, err := a.AcquireLease(ctx); err != nil {
		t.Fatalf("a.AcquireLease: %v", err)
	}
	_, err := b.AcquireLease(ctx)
	if err == nil {
		t.Fatalf("b.AcquireLease succeeded with active lease")
	}
	var leaseExists *replicate.LeaseExistsError
	if !errors.As(err, &leaseExists) {
		t.Fatalf("expected LeaseExistsError, got %T: %v", err, err)
	}
	if leaseExists.Owner != "node-a" {
		t.Errorf("contention owner: got %q want node-a", leaseExists.Owner)
	}
}

func TestLeaser_AcquireAfterExpiry(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a := NewLeaser(dir)
	a.Owner = "node-a"
	a.TTL = 50 * time.Millisecond

	got, err := a.AcquireLease(ctx)
	if err != nil {
		t.Fatalf("a.AcquireLease: %v", err)
	}
	gen := got.Generation
	time.Sleep(80 * time.Millisecond)

	b := NewLeaser(dir)
	b.Owner = "node-b"
	b.TTL = 5 * time.Second
	got2, err := b.AcquireLease(ctx)
	if err != nil {
		t.Fatalf("b.AcquireLease after expiry: %v", err)
	}
	if got2.Generation != gen+1 {
		t.Errorf("generation: got %d want %d", got2.Generation, gen+1)
	}
	if got2.Owner != "node-b" {
		t.Errorf("owner: got %q want node-b", got2.Owner)
	}
}

func TestLeaser_RenewBumpsExpiry(t *testing.T) {
	ctx := context.Background()
	l := newLeaser(t, "node-a")
	lease, err := l.AcquireLease(ctx)
	if err != nil {
		t.Fatalf("AcquireLease: %v", err)
	}
	firstExpiry := lease.ExpiresAt
	firstETag := lease.ETag
	gen := lease.Generation
	time.Sleep(30 * time.Millisecond)

	renewed, err := l.RenewLease(ctx, lease)
	if err != nil {
		t.Fatalf("RenewLease: %v", err)
	}
	if !renewed.ExpiresAt.After(firstExpiry) {
		t.Errorf("renew did not bump expiry: %v vs %v", renewed.ExpiresAt, firstExpiry)
	}
	if renewed.Generation != gen {
		t.Errorf("renew bumped generation: got %d want %d", renewed.Generation, gen)
	}
	if renewed.ETag == firstETag {
		t.Errorf("renew did not bump etag")
	}
}

func TestLeaser_RenewLosesRaceToStealer(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	a := NewLeaser(dir)
	a.Owner = "node-a"
	a.TTL = 30 * time.Millisecond

	lease, err := a.AcquireLease(ctx)
	if err != nil {
		t.Fatalf("a.AcquireLease: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	b := NewLeaser(dir)
	b.Owner = "node-b"
	b.TTL = 5 * time.Second
	if _, err := b.AcquireLease(ctx); err != nil {
		t.Fatalf("b.AcquireLease: %v", err)
	}

	if _, err := a.RenewLease(ctx, lease); !errors.Is(err, replicate.ErrLeaseNotHeld) {
		t.Fatalf("expected ErrLeaseNotHeld, got %v", err)
	}
}

func TestLeaser_Release(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	a := NewLeaser(dir)
	a.Owner = "node-a"
	a.TTL = 5 * time.Second
	lease, err := a.AcquireLease(ctx)
	if err != nil {
		t.Fatalf("AcquireLease: %v", err)
	}
	if err := a.ReleaseLease(ctx, lease); err != nil {
		t.Fatalf("ReleaseLease: %v", err)
	}

	b := NewLeaser(dir)
	b.Owner = "node-b"
	b.TTL = 5 * time.Second
	got, err := b.AcquireLease(ctx)
	if err != nil {
		t.Fatalf("b.AcquireLease after release: %v", err)
	}
	if got.Owner != "node-b" {
		t.Errorf("post-release owner: got %q want node-b", got.Owner)
	}
}

func TestLeaser_ReleaseAfterSteal(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	a := NewLeaser(dir)
	a.Owner = "node-a"
	a.TTL = 30 * time.Millisecond
	lease, err := a.AcquireLease(ctx)
	if err != nil {
		t.Fatalf("a.AcquireLease: %v", err)
	}
	time.Sleep(50 * time.Millisecond)

	b := NewLeaser(dir)
	b.Owner = "node-b"
	b.TTL = 5 * time.Second
	if _, err := b.AcquireLease(ctx); err != nil {
		t.Fatalf("b.AcquireLease: %v", err)
	}

	if err := a.ReleaseLease(ctx, lease); !errors.Is(err, replicate.ErrLeaseNotHeld) {
		t.Fatalf("expected ErrLeaseNotHeld on stale release, got %v", err)
	}
}

func TestLeaser_ConcurrentAcquireSingleWinner(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	const N = 32
	var wg sync.WaitGroup
	var winners atomic.Int64
	results := make([]error, N)

	wg.Add(N)
	start := make(chan struct{})
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			l := NewLeaser(dir)
			l.Owner = "node-X"
			l.TTL = 5 * time.Second
			<-start
			_, err := l.AcquireLease(ctx)
			results[i] = err
			if err == nil {
				winners.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := winners.Load(); got != 1 {
		t.Fatalf("expected exactly 1 winner, got %d", got)
	}
}
