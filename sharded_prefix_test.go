package replicate

import (
	"regexp"
	"strings"
	"testing"
)

var hexShard = regexp.MustCompile(`^[0-9a-f]{2}$`)

func TestShardedPrefix_Deterministic(t *testing.T) {
	for _, sym := range []string{"AAPL", "MSFT", "NVDA", "BTC", "ETH"} {
		a := ShardedPrefix(sym)
		b := ShardedPrefix(sym)
		if a != b {
			t.Errorf("%s: prefix not stable %q vs %q", sym, a, b)
		}
		if !hexShard.MatchString(a) {
			t.Errorf("%s: prefix %q not 2-hex", sym, a)
		}
	}
}

func TestShardedPrefix_Distribution(t *testing.T) {
	// Synthetic load: hit 4000 unique symbols, count shards. Each of
	// the 256 buckets should appear at least once on a uniformly-keyed
	// hash; we don't enforce uniform spread (the test would be flaky)
	// but we do enforce coverage of >200 buckets so we know the hash
	// is actually mixing.
	buckets := map[string]int{}
	for i := 0; i < 4000; i++ {
		buckets[ShardedPrefix(symbolize(i))]++
	}
	if len(buckets) < 200 {
		t.Errorf("expected >200 unique shard buckets, got %d", len(buckets))
	}
}

func TestShardedKey_Layout(t *testing.T) {
	key := ShardedKey("markets", "AAPL", "000000000000007b.ltx")
	parts := strings.Split(key, "/")
	if len(parts) != 4 {
		t.Fatalf("expected 4 segments, got %d (%v)", len(parts), parts)
	}
	if parts[0] != "markets" {
		t.Errorf("segment[0]: %q", parts[0])
	}
	if !hexShard.MatchString(parts[1]) {
		t.Errorf("segment[1] not 2-hex: %q", parts[1])
	}
	if parts[2] != "AAPL" {
		t.Errorf("segment[2]: %q", parts[2])
	}
	if parts[3] != "000000000000007b.ltx" {
		t.Errorf("segment[3]: %q", parts[3])
	}
}

func TestShardedKey_NoRoot(t *testing.T) {
	key := ShardedKey("", "AAPL", "file.ltx")
	if strings.HasPrefix(key, "/") {
		t.Errorf("unexpected leading slash: %q", key)
	}
	if !strings.HasSuffix(key, "/AAPL/file.ltx") {
		t.Errorf("layout: %q", key)
	}
}

func TestShardedKey_NoSuffix(t *testing.T) {
	key := ShardedKey("markets", "AAPL", "")
	if strings.HasSuffix(key, "/") {
		t.Errorf("trailing slash: %q", key)
	}
	if !strings.HasSuffix(key, "/AAPL") {
		t.Errorf("layout: %q", key)
	}
}

func symbolize(i int) string {
	// Generate distinct 5-char codes: covers ~11.8M for the 4000 used.
	var b [5]byte
	for k := 0; k < 5; k++ {
		b[k] = byte('A' + (i % 26))
		i /= 26
	}
	return string(b[:])
}
