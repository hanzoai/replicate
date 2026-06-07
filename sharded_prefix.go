package replicate

import (
	"encoding/hex"
	"strings"

	"github.com/zeebo/blake3"
)

// ShardedPrefix returns a 2-hex-char shard segment derived from a
// keyed blake3 of the input. Use it to bucket per-symbol or per-
// tenant S3 prefixes across the 256-shard space so a single S3
// partition doesn't bottleneck on AWS's ~3500 PUT/s/prefix limit.
//
// Example, sharding 12K markets at 100 PUT/s each:
//
//	prefix := replicate.ShardedPrefix("AAPL")
//	// prefix => "a3"
//	key := fmt.Sprintf("markets/%s/AAPL/%016x.ltx", prefix, txid)
//
// 256 shards × 3500 PUT/s/shard ≈ 900K PUT/s aggregate — well above
// the cold-tier write rate even at 12K markets full-throttle.
//
// The blake3 key is a constant string so the same input always lands
// on the same shard across processes + restarts. Changing the key
// resharded the entire keyspace; treat it as a one-way migration.
func ShardedPrefix(symbol string) string {
	h := blake3.NewDeriveKey("github.com/hanzoai/replicate.ShardedPrefix v1")
	_, _ = h.WriteString(symbol)
	var sum [1]byte
	d := h.Digest()
	_, _ = d.Read(sum[:])
	return hex.EncodeToString(sum[:])
}

// ShardedKey is a convenience builder: returns
// "<rootSegment>/<shard>/<symbol>/<suffix>". Use it to keep the call-
// site layout consistent across producers.
func ShardedKey(rootSegment, symbol, suffix string) string {
	var b strings.Builder
	b.Grow(len(rootSegment) + 1 + 2 + 1 + len(symbol) + 1 + len(suffix))
	if rootSegment != "" {
		b.WriteString(rootSegment)
		b.WriteByte('/')
	}
	b.WriteString(ShardedPrefix(symbol))
	b.WriteByte('/')
	b.WriteString(symbol)
	if suffix != "" {
		b.WriteByte('/')
		b.WriteString(suffix)
	}
	return b.String()
}
