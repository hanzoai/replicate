# SQLite → S3 over PQ-ZAP

## Why

LTX replication is the durability tier: every SQLite WAL frame ends up
in S3, gets restored on failover, and is the long-term system of
record. Shipping it over plaintext HTTP/1.1 + AWS-v4 signing has two
problems for a post-quantum stack:

1. **Wire layer is classical.** TLS handshake, body framing, content
   addressing — all assume pre-PQ primitives. A network attacker with
   a future quantum computer recording the traffic today can decrypt
   it later ("harvest now, decrypt later").

2. **Latency tax for in-cluster S2S.** The most common Liquidity
   topology runs the replication target as a pod in the same cluster
   (hanzos3/s3 server, or a MinIO sidecar). Plaintext HTTP/1.1 +
   AWS-v4 signing per part adds ~3-5ms per request on hot loops.

The PQ-ZAP path solves both:

- ZAP wire format is post-quantum-attested (ML-KEM key exchange,
  ML-DSA peer identity, AES-256-GCM bulk). Traffic recorded today
  remains confidential against future quantum adversaries.
- Zero-copy capnp body framing eliminates the marshal/unmarshal hit.
  Replication throughput is bound by the underlying disk + network,
  not transport overhead.

## How

The `ReplicaClient.HTTPClient` field accepts any `*http.Client`. To
swap in ZAP-HTTP transport, wire a `luxfi/zap/clienthttp` client:

```go
import (
    "github.com/hanzoai/replicate/s3"
    "github.com/luxfi/zap/clienthttp"
)

zc, stop, err := clienthttp.NewClient("hanzo-s3",
    clienthttp.WithLocalTrust(),       // in-cluster: trust mDNS scope
    clienthttp.WithHTTPTimeout(24*time.Hour),
)
if err != nil { ... }
defer stop()

rc := s3.NewReplicaClient()
rc.Bucket   = "ltx-prod"
rc.Region   = "us-east-1"
rc.Endpoint = "http://hanzo-s3" // ignored under clienthttp — peers come from mDNS
rc.HTTPClient = zc
```

clienthttp:

- Resolves `hanzo-s3` peers via mDNS in the cluster-private network.
- Per-RoundTrip, the round-robin Picker selects a peer.
- The ZAP-HTTP Dialer returns a `zap-proto/http` Transport speaking
  to that peer.
- `WithLocalTrust()` says: don't attach an Authorization header —
  trust comes from the cluster CA + mDNS scope.

When `HTTPClient` is nil, the legacy plaintext transport runs
unchanged. Migrate one tier at a time (dev → testnet → mainnet).

## What "fully PQ-ZAP" requires

- [x] **Wire transport**: `HTTPClient` injection point — this commit.
- [ ] **Server side**: `hanzos3/s3` must accept ZAP-HTTP listener
      (existing as part of the Hanzo S3 server roadmap; check that
      repo's `zap_server` files).
- [ ] **Key wrap**: LTX file bodies SSE-C keys MUST be ML-KEM-wrapped
      before SetObject. Today: AES-256 with a classical-symmetric key.
      Tomorrow: extend `SSECustomerKey` plumbing to accept an ML-KEM
      ciphertext alongside the symmetric key, and verify on the
      server side before the bucket store.
- [ ] **Signing**: AWS SigV4 stays for compatibility with
      S3-compatible providers (MinIO, Exoscale, R2). For the
      hanzos3-native path, replace SigV4 with ML-DSA peer-signed
      requests — server skips signature check when the ZAP TLS peer
      cert chains to the cluster CA.
- [ ] **Leaser**: `leaser.go` publishes WAL leases via DynamoDB or
      hanzos3 conditional-PUT today. Move to ZAP CompareAndSwap on
      a cluster-local keyspace so lease coordination is also
      post-quantum.

This commit is step 1 — the transport injection point. Subsequent
commits land each remaining bullet.
