// Package client — canonical client interface for Hanzo Replicate.
//
//	import repl "github.com/hanzoai/replicate/client"
//	var r repl.Replicator = hanzoreplicate.New(cfg)

package client

import (
	"context"
	"time"
)

// Replicator is the cross-region data-sync surface: declare streams,
// inspect lag, pause + resume, replay from a checkpoint. The data plane
// (CDC tailing, transport, conflict resolution) runs out-of-band; this
// interface is the management API every operator + admin tool uses.
type Replicator interface {
	// Kind reports the backend identifier
	// (hanzo-replicate | debezium | kafka-mirror | warpstream | litestream).
	Kind() string

	// UpsertStream creates or replaces a replication stream.
	UpsertStream(ctx context.Context, s Stream) error

	// DeleteStream removes a stream and its replication state.
	DeleteStream(ctx context.Context, name string) error

	// ListStreams returns every stream in scope.
	ListStreams(ctx context.Context) ([]Stream, error)

	// GetStatus returns the runtime state of one stream.
	GetStatus(ctx context.Context, name string) (*StreamStatus, error)

	// Pause halts a stream without losing its checkpoint. Idempotent.
	Pause(ctx context.Context, name string) error

	// Resume restarts a paused stream from its last checkpoint.
	Resume(ctx context.Context, name string) error

	// Replay rewinds a stream to a prior checkpoint (or a wall-clock
	// timestamp) and re-emits from there. The destination is responsible
	// for idempotent application of the replayed events.
	Replay(ctx context.Context, name string, from Checkpoint) error
}

// Stream is one replication unit. Source + destination + filter.
type Stream struct {
	Name        string
	Source      Endpoint
	Destination Endpoint
	// Mode: cdc | snapshot | snapshot_then_cdc.
	Mode string
	// Filter restricts which rows/objects replicate. SQL-ish predicate
	// (postgres CDC) or JSONPath (document stores).
	Filter string
	// Conflict policy when source and destination diverge:
	// last_writer_wins | source_wins | dest_wins | manual_review.
	Conflict string
	// CheckpointInterval is the cadence at which durable progress
	// markers are written.
	CheckpointInterval time.Duration
}

// Endpoint identifies one end of a replication.
type Endpoint struct {
	// Kind: postgres | sqlite | base | s3 | nats | kafka.
	Kind string
	// Region is the deployment region (us-east | us-west | eu-fra).
	Region string
	// URI is the dial string (postgres://..., s3://bucket, nats://...).
	URI string
	// AuthRef points at a KMS secret holding the credentials.
	AuthRef string
}

// StreamStatus is the runtime state of one stream.
type StreamStatus struct {
	Name              string
	State             string // running | paused | failed | catching_up | snapshot
	LastEvent         time.Time
	LastCheckpoint    Checkpoint
	LagSeconds        float64
	LagEvents         int64
	BytesReplicated   int64
	EventsReplicated  int64
	ErrorsLastHour    int
	LastError         string
}

// Checkpoint identifies a position in a stream.
type Checkpoint struct {
	// LSN is the source-native ordering token (postgres WAL LSN,
	// kafka offset, s3 manifest version, ...).
	LSN string
	// Wall is the wall-clock timestamp at the source when this
	// checkpoint was written.
	Wall time.Time
}
