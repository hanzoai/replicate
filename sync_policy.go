package replicate

// SyncPolicy controls how Sync() coordinates across multiple replicas.
//
// The current DB struct has a single Replica field that the monitor
// loop drives asynchronously. The W=2 hot-path use case in liquidity
// ATS needs a stronger guarantee: ACK only after a designated peer
// replica has confirmed durability. SyncPolicy expresses the contract
// the caller needs; the actual peer push happens at the journal layer
// (which knows the peer's identity and the symbol→stream binding) so
// the replicate package stays storage-agnostic.
type SyncPolicy uint8

const (
	// SyncAll waits for every configured replica. Default for single-
	// replica deployments (the historical behavior).
	SyncAll SyncPolicy = iota

	// SyncQuorumW2 returns after the local fsync + one peer ACK. The
	// remaining replicas (typically S3) continue uploading
	// asynchronously through the monitor loop. This is the contract
	// the ATS hot path relies on: ~1ms p99 instead of S3's 50ms.
	SyncQuorumW2

	// SyncLocalOnly returns after local fsync; all replicas lag. Used
	// for tests + dev clusters where durability is best-effort.
	SyncLocalOnly
)

// String returns a human-readable policy name for diagnostics.
func (p SyncPolicy) String() string {
	switch p {
	case SyncAll:
		return "all"
	case SyncQuorumW2:
		return "quorum_w2"
	case SyncLocalOnly:
		return "local_only"
	}
	return "unknown"
}
