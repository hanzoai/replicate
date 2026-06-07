// Package peer implements peer-to-peer LTX frame streaming over luxfi/zap.
//
// Use case: an N-pod ATS where every market has one owner pod and one
// standby pod. Owner's WAL frames must reach the standby's local disk
// before the trader sees an ACK (W=2 quorum). S3 PUT in that path is a
// 50ms ceiling; ZAP same-AZ RTT is ~200µs. This package replaces S3 in
// the hot path with a peer LTX channel — S3 keeps its job as the cold
// tier (backup, compaction archive, new-pod bootstrap).
//
// Wire protocol (4 opcodes in the caller-supplied namespace):
//
//	0xA020 MsgPeerWALFrame      request:  [8B mkt][4B txid][4B len][N bytes]
//	0xA021 MsgPeerWALFrameAck   response: [1B code][8B mkt][4B txid]
//	0xA022 MsgPeerSnapshotReq   request:  [8B mkt][8B from_txid]
//	0xA023 MsgPeerSnapshotResp  response: [1B code][8B mkt][8B txid][N bytes]
//
// No JSON, no protobuf. Fixed-width little-endian. The 8-byte market_id
// is a Blake3-keyed digest of the symbol — caller resolves symbol→id at
// session setup; the peer wire never carries the variable-length symbol.
//
// The ReplicaClient implements replicate.ReplicaClient so the existing
// replicate.DB / replicate.Replica plumbing routes a peer replica
// identically to an S3 one. Only the hot path differs: W=2 quorum
// returns after the peer ACK; the S3 replica continues uploading
// asynchronously through its own monitor loop.
package peer

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hanzoai/ltx"
	"github.com/luxfi/zap"

	"github.com/hanzoai/replicate"
)

// Opcode constants — the canonical 0xA020..0xA023 quadruple for the
// peer LTX wire. They live in the 0xA0 namespace and MUST be registered
// against a dispatcher that owns the namespace byte (e.g. ATS's
// atsrpc.Dispatcher). The peer package is namespace-agnostic — it
// hands the (req, resp) pairs to whatever Dispatcher implementation the
// caller injects.
const (
	OpWALFrame      uint16 = 0xA020
	OpWALFrameAck   uint16 = 0xA021
	OpSnapshotReq   uint16 = 0xA022
	OpSnapshotResp  uint16 = 0xA023
)

// Error codes carried in the first byte of every response payload.
const (
	ErrCodeOK             uint8 = 0x00
	ErrCodeBadFrame       uint8 = 0x01
	ErrCodeUnknownMarket  uint8 = 0x02
	ErrCodeServerInternal uint8 = 0x03
	ErrCodeNotStandby     uint8 = 0x04
)

// ReplicaClientType is the type string returned by ReplicaClient.Type().
// Identifies peer replicas in the replicate.DB Replicas slice.
const ReplicaClientType = "peer"

// MarketID is the 8-byte handle that identifies a per-symbol stream on
// the wire. It is derived once per ReplicaClient and stays constant for
// the life of the journal. The peer derives the same MarketID from the
// same symbol — no symbol bytes travel.
type MarketID [8]byte

// Compile-time check that ReplicaClient satisfies the interface.
var _ replicate.ReplicaClient = (*ReplicaClient)(nil)

// Dispatcher is the minimal contract the peer Server registers against.
// ATS's atsrpc.Dispatcher satisfies it. Decoupling here keeps the peer
// package out of any opcode-namespace-owner package's import graph.
type Dispatcher interface {
	Register(reqOp, respOp uint16, h Handler) error
}

// Handler mirrors the per-opcode signature each Dispatcher accepts.
// It MUST return a well-formed response payload — the first byte is
// the error_code. Returning (nil, err) is a programming error.
type Handler func(ctx context.Context, peer string, payload []byte) (response []byte, _ error)

// Node abstracts the subset of luxfi/zap.Node the peer client needs.
// Production wiring passes a *zap.Node directly; tests can pass a
// fake. Keeping this interface narrow avoids dragging the full Node
// API surface into every call site.
type Node interface {
	Call(ctx context.Context, peerID string, msg *zap.Message) (*zap.Message, error)
}

// FrameSink is the standby-side receiver for LTX frames pushed by the
// owner. The Server invokes Apply once per accepted frame; Apply MUST
// fsync to local storage before returning nil — that's the standby
// half of the W=2 contract. Apply receives an io.Reader over the raw
// LTX bytes; readers are expected to consume fully.
type FrameSink interface {
	// Apply persists one LTX frame for market id at the given TXID.
	// MUST return only after the bytes are durable on local storage.
	// Returning an error fails the ACK; the owner's Sync will fail and
	// the caller (Journal.Append) sees the error.
	Apply(ctx context.Context, id MarketID, txid ltx.TXID, r io.Reader) error

	// Snapshot returns an io.ReadCloser over the latest LTX snapshot
	// at-or-after fromTXID for the requesting peer. Implementations
	// may return io.EOF immediately if the requesting peer is already
	// caught up. The returned reader is closed by the caller.
	Snapshot(ctx context.Context, id MarketID, fromTXID ltx.TXID) (io.ReadCloser, ltx.TXID, error)
}

// Option configures a ReplicaClient.
type Option func(*ReplicaClient)

// WithTimeout sets the per-call deadline on every Push/Pull. Defaults
// to 2s. The brief budgets ~1ms p99 same-AZ; 2s is the kill-switch.
func WithTimeout(d time.Duration) Option {
	return func(c *ReplicaClient) {
		if d > 0 {
			c.timeout = d
		}
	}
}

// WithLogger overrides the default no-op logger.
func WithLogger(l *slog.Logger) Option {
	return func(c *ReplicaClient) {
		if l != nil {
			c.logger = l
		}
	}
}

// ReplicaClient is the client half of the peer wire. One per (market,
// owner→standby) pair. It plugs into replicate.DB as a sibling of the
// S3 ReplicaClient — the DB's quorum policy chooses which one drives
// the ACK path.
//
// On the wire, every WriteLTXFile call sends one MsgPeerWALFrame and
// blocks on the MsgPeerWALFrameAck. The standby fsyncs and ACKs in
// ~200µs same-AZ. There is no batching at this layer; the WAL frame
// is already the natural batch unit for the matcher.
type ReplicaClient struct {
	node     Node
	peerID   string
	marketID MarketID
	timeout  time.Duration
	logger   *slog.Logger

	closed atomic.Bool
}

// NewReplicaClient constructs a peer client for one (market, standby)
// pair. node is the local luxfi/zap.Node already wired into the cluster
// mesh; peerID is the standby's pod ID; marketID is the canonical
// 8-byte handle.
func NewReplicaClient(node Node, peerID string, marketID MarketID, opts ...Option) *ReplicaClient {
	c := &ReplicaClient{
		node:     node,
		peerID:   peerID,
		marketID: marketID,
		timeout:  2 * time.Second,
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Type returns "peer" — the discriminator used by the replicate.DB
// Replicas slice + the journal to distinguish this client from S3.
func (c *ReplicaClient) Type() string { return ReplicaClientType }

// Init is a no-op for the peer client. The ZAP node is started by the
// process owner; the peer client just rides it.
func (c *ReplicaClient) Init(ctx context.Context) error { return nil }

// SetLogger sets the logger used for diagnostics.
func (c *ReplicaClient) SetLogger(logger *slog.Logger) {
	if logger == nil {
		return
	}
	c.logger = logger.With("replica", ReplicaClientType)
}

// PeerID reports the standby peer this client targets.
func (c *ReplicaClient) PeerID() string { return c.peerID }

// MarketID reports the 8-byte market handle this client streams.
func (c *ReplicaClient) MarketID() MarketID { return c.marketID }

// Close marks the client unusable; subsequent WriteLTXFile calls return
// io.ErrClosedPipe.
func (c *ReplicaClient) Close() error {
	c.closed.Store(true)
	return nil
}

// ltxFramePool holds reusable byte slices for LTX frame bodies + the
// (header+body) payload buffer that travels into encodeWALFrameReq.
// LTX frames are typically 4-64KB at L0; a single pooled cap of 64KB
// covers the steady-state hot path with one allocation per pool miss.
// Get returns a slice with len=0; callers must append into it.
//
// The 64KB ceiling reflects the L0 frame size cap baked into the SQLite
// WAL → LTX pipeline (4KB page × ~16 pages per checkpointed WAL frame).
// Larger frames (compaction outputs at L1+) skip the pool — the caller
// just falls back to io.ReadAll's default allocation.
const ltxPoolCap = 64 * 1024

var ltxFramePool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, ltxPoolCap)
		return &b
	},
}

// readPooled reads r into a pooled buffer and returns the buffer + the
// release function. The caller MUST invoke release exactly once,
// AFTER the last reference to the returned slice is gone. Returning
// nil + nil + err on read failure keeps the caller's release/defer
// pattern symmetric — if the function returns an error, no release is
// owed.
//
// Frames exceeding ltxPoolCap fall back to a fresh allocation outside
// the pool so we don't grow the pool's working set unboundedly on
// compaction outliers. Buffers grown above ltxPoolCap during read are
// DROPPED on release — never returned to the pool — so a single
// outlier frame cannot permanently inflate the pool's working set
// (F-05).
func readPooled(r io.Reader) ([]byte, func(), error) {
	bufPtr := ltxFramePool.Get().(*[]byte)
	buf := (*bufPtr)[:0]
	for {
		if cap(buf) == 0 {
			buf = make([]byte, 0, 4096)
		}
		// Read into the unused tail of buf; grow when full.
		if len(buf) == cap(buf) {
			if cap(buf) >= ltxPoolCap {
				// Outlier — drop the pool buffer entirely, switch to
				// caller-owned growth. We do NOT Put the pool buffer
				// back; the next Get returns a fresh ltxPoolCap-bounded
				// allocation. This prevents pool poisoning where one
				// large frame grows the cached buffer beyond ltxPoolCap
				// and every subsequent reuse pays the inflated cost.
				rest, err := io.ReadAll(r)
				if err != nil {
					return nil, nil, err
				}
				full := append([]byte(nil), buf...)
				full = append(full, rest...)
				return full, func() {}, nil
			}
			newCap := cap(buf) * 2
			if newCap == 0 {
				newCap = 4096
			}
			grown := make([]byte, len(buf), newCap)
			copy(grown, buf)
			buf = grown
		}
		n, err := r.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if err == io.EOF {
			break
		}
		if err != nil {
			putPooled(bufPtr)
			return nil, nil, err
		}
	}
	*bufPtr = buf
	release := func() {
		putPooled(bufPtr)
	}
	return buf, release, nil
}

// putPooled returns the buffer to the pool, but only if its capacity
// is at or below ltxPoolCap. Oversize buffers are dropped on the
// floor so the pool's working-set cap stays bounded. Without this
// guard, one >64KB frame would permanently inflate the pool — the
// next Get returns a cap-inflated buffer, subsequent reads hit the
// outlier branch again, allocations stay quadratic forever (F-05).
func putPooled(bufPtr *[]byte) {
	if bufPtr == nil {
		return
	}
	if cap(*bufPtr) > ltxPoolCap {
		// Drop oversize — do NOT return to pool.
		return
	}
	*bufPtr = (*bufPtr)[:0]
	ltxFramePool.Put(bufPtr)
}

// payloadBufPool holds reusable buffers for the (header+body) payload
// passed into encodeWALFrameReq. Same sizing rationale as ltxFramePool —
// L0 frames stay under 64KB + walFrameHeaderLen=16 bytes.
var payloadBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, ltxPoolCap+walFrameHeaderLen)
		return &b
	},
}

// WriteLTXFile pushes one LTX frame to the peer and blocks until the
// peer ACKs (fsync confirmed). minTXID and maxTXID identify the frame;
// for L0 frames they are always equal. Returns the ltx.FileInfo the
// peer reports so the caller can record metadata; the Size field is
// the byte count actually transferred.
//
// This is the hot path. Errors here fail the trader's PlaceOrder; do
// not return nil unless the peer has confirmed durability.
//
// Memory: the LTX body buffer is pooled via ltxFramePool. Under
// sustained NYSE-class load (5K+ orders/sec/market), this drops
// per-Push allocations from O(frame_size_bytes) to O(1) on the
// steady-state path.
func (c *ReplicaClient) WriteLTXFile(ctx context.Context, level int, minTXID, maxTXID ltx.TXID, r io.Reader) (*ltx.FileInfo, error) {
	if c.closed.Load() {
		return nil, io.ErrClosedPipe
	}
	if c.node == nil {
		return nil, errors.New("peer: nil node")
	}
	body, release, err := readPooled(r)
	if err != nil {
		return nil, fmt.Errorf("peer: read ltx: %w", err)
	}
	defer release()

	reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	req := encodeWALFrameReq(c.marketID, uint32(maxTXID), body)
	resp, err := c.node.Call(reqCtx, c.peerID, req)
	if err != nil {
		return nil, fmt.Errorf("peer: ack call: %w", err)
	}
	code, ackID, ackTXID, decodeErr := decodeWALFrameAck(resp)
	if decodeErr != nil {
		return nil, fmt.Errorf("peer: decode ack: %w", decodeErr)
	}
	if code != ErrCodeOK {
		return nil, &AckError{Code: code, TXID: ltx.TXID(ackTXID)}
	}
	if ackID != c.marketID {
		return nil, fmt.Errorf("peer: ack market mismatch")
	}
	if ackTXID != uint32(maxTXID) {
		return nil, fmt.Errorf("peer: ack txid mismatch want=%d got=%d", maxTXID, ackTXID)
	}
	return &ltx.FileInfo{
		Level:     level,
		MinTXID:   minTXID,
		MaxTXID:   maxTXID,
		Size:      int64(len(body)),
		CreatedAt: time.Now().UTC(),
	}, nil
}

// LTXFiles returns an empty iterator. The peer is the live replication
// channel, not the source of truth for backfill — that's S3. Callers
// listing LTX files for restore go to the S3 replica.
func (c *ReplicaClient) LTXFiles(ctx context.Context, level int, seek ltx.TXID, useMetadata bool) (ltx.FileIterator, error) {
	return ltx.NewFileInfoSliceIterator(nil), nil
}

// OpenLTXFile returns io.ErrUnexpectedEOF. Same rationale as LTXFiles —
// peer is not a content store. Snapshot-style cold-start of a new
// standby uses MsgPeerSnapshotReq via the Server, not OpenLTXFile.
func (c *ReplicaClient) OpenLTXFile(ctx context.Context, level int, minTXID, maxTXID ltx.TXID, offset, size int64) (io.ReadCloser, error) {
	return nil, io.ErrUnexpectedEOF
}

// DeleteLTXFiles is a no-op — frames are ephemeral on the peer wire.
func (c *ReplicaClient) DeleteLTXFiles(ctx context.Context, a []*ltx.FileInfo) error {
	return nil
}

// DeleteAll is a no-op for the same reason as DeleteLTXFiles.
func (c *ReplicaClient) DeleteAll(ctx context.Context) error { return nil }

// AckError carries the wire-level error code so callers can switch on
// the failure mode (bad frame, unknown market, standby refusal).
type AckError struct {
	Code uint8
	TXID ltx.TXID
}

func (e *AckError) Error() string {
	switch e.Code {
	case ErrCodeBadFrame:
		return "peer: bad frame"
	case ErrCodeUnknownMarket:
		return "peer: unknown market"
	case ErrCodeServerInternal:
		return "peer: server internal"
	case ErrCodeNotStandby:
		return "peer: not standby"
	}
	return fmt.Sprintf("peer: ack code 0x%02x", e.Code)
}

// Server is the standby side. It registers handlers for OpWALFrame and
// OpSnapshotReq against a Dispatcher; on receive it forwards into the
// caller's FrameSink. Construct one Server per pod and Register once.
type Server struct {
	sink    FrameSink
	logger  *slog.Logger

	mu      sync.RWMutex
	closed  bool
}

// NewServer constructs the standby-side receiver. sink is the local
// store the server fsyncs frames into.
func NewServer(sink FrameSink, opts ...ServerOption) *Server {
	s := &Server{
		sink:   sink,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// ServerOption configures a Server.
type ServerOption func(*Server)

// WithServerLogger overrides the default no-op logger.
func WithServerLogger(l *slog.Logger) ServerOption {
	return func(s *Server) {
		if l != nil {
			s.logger = l
		}
	}
}

// Register installs OpWALFrame and OpSnapshotReq handlers on the
// dispatcher. Returns an error on collision — last-write-wins is
// rejected by the dispatcher contract.
func (s *Server) Register(d Dispatcher) error {
	if d == nil {
		return errors.New("peer.Server.Register: nil dispatcher")
	}
	if err := d.Register(OpWALFrame, OpWALFrameAck, s.onWALFrame); err != nil {
		return fmt.Errorf("register WAL frame: %w", err)
	}
	if err := d.Register(OpSnapshotReq, OpSnapshotResp, s.onSnapshotReq); err != nil {
		return fmt.Errorf("register snapshot: %w", err)
	}
	return nil
}

// Close marks the server unwilling to apply new frames. Subsequent
// inbound frames receive ErrCodeNotStandby.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// onWALFrame is the OpWALFrame handler. Decodes the wire payload,
// dispatches to the FrameSink, and emits an ACK. The ACK is the W=2
// proof — never return nil before Apply has fsynced.
//
// A malformed frame body or a bug in the FrameSink's Apply path must
// NEVER crash the server's dispatch goroutine. Any panic that escapes
// Apply is recovered here, logged with full stack, and surfaced as
// ErrCodeServerInternal — the owner sees an error and retries, the
// standby keeps serving (F-22).
func (s *Server) onWALFrame(ctx context.Context, peerID string, payload []byte) (resp []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("peer: wal frame handler panic",
				"peer", peerID,
				"panic", fmt.Sprintf("%v", r),
				"stack", string(debug.Stack()))
			resp = encodeAck(ErrCodeServerInternal, MarketID{}, 0)
			err = nil
		}
	}()
	s.mu.RLock()
	closed := s.closed
	sink := s.sink
	s.mu.RUnlock()
	if closed || sink == nil {
		return encodeAck(ErrCodeNotStandby, MarketID{}, 0), nil
	}
	mkt, txid, body, err := decodeWALFrameReq(payload)
	if err != nil {
		return encodeAck(ErrCodeBadFrame, MarketID{}, 0), nil
	}
	if err := sink.Apply(ctx, mkt, ltx.TXID(txid), bytesReader(body)); err != nil {
		s.logger.Warn("peer: apply failed", "peer", peerID, "txid", txid, "err", err.Error())
		return encodeAck(ErrCodeServerInternal, mkt, txid), nil
	}
	return encodeAck(ErrCodeOK, mkt, txid), nil
}

// onSnapshotReq is the OpSnapshotReq handler. Streams the requested
// snapshot back as a single response payload. Snapshots are bounded by
// the standby's available LTX history — a brand-new pod with no
// snapshot returns ErrCodeUnknownMarket; the caller falls back to S3.
//
// Same panic-safety contract as onWALFrame (F-22).
func (s *Server) onSnapshotReq(ctx context.Context, peerID string, payload []byte) (resp []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("peer: snapshot handler panic",
				"peer", peerID,
				"panic", fmt.Sprintf("%v", r),
				"stack", string(debug.Stack()))
			resp = encodeSnapshotResp(ErrCodeServerInternal, MarketID{}, 0, nil)
			err = nil
		}
	}()
	s.mu.RLock()
	closed := s.closed
	sink := s.sink
	s.mu.RUnlock()
	if closed || sink == nil {
		return encodeSnapshotResp(ErrCodeNotStandby, MarketID{}, 0, nil), nil
	}
	mkt, fromTXID, err := decodeSnapshotReq(payload)
	if err != nil {
		return encodeSnapshotResp(ErrCodeBadFrame, MarketID{}, 0, nil), nil
	}
	rc, txid, err := sink.Snapshot(ctx, mkt, ltx.TXID(fromTXID))
	if err != nil {
		if errors.Is(err, io.EOF) {
			return encodeSnapshotResp(ErrCodeOK, mkt, fromTXID, nil), nil
		}
		s.logger.Warn("peer: snapshot failed", "peer", peerID, "err", err.Error())
		return encodeSnapshotResp(ErrCodeUnknownMarket, mkt, 0, nil), nil
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		return encodeSnapshotResp(ErrCodeServerInternal, mkt, 0, nil), nil
	}
	return encodeSnapshotResp(ErrCodeOK, mkt, uint64(txid), body), nil
}

// MarketIDFromBytes packs 8 raw bytes into a MarketID. Caller is
// responsible for ensuring the source bytes were derived from a stable
// keyed hash of the symbol (e.g. blake3 keyed by domain string).
func MarketIDFromBytes(b []byte) MarketID {
	var id MarketID
	copy(id[:], b)
	return id
}

// ---------------------------------------------------------------------
// Wire encoding — fixed bytes, little-endian where applicable. The
// ZAP frame wraps these as a single Bytes field at root[0]; ATS's
// atsrpc.wrapResponse already does that on the response side.
// ---------------------------------------------------------------------

const (
	walFrameHeaderLen   = 8 + 4 + 4 // market_id + txid + frame_len
	ackLen              = 1 + 8 + 4 // code + market_id + txid_acked
	snapshotReqLen      = 8 + 8     // market_id + from_txid
	snapshotRespHdrLen  = 1 + 8 + 8 // code + market_id + txid
)

// encodeWALFrameReq builds the OpWALFrame ZAP message body.
// Layout: [8B mkt][4B txid][4B len][N body]. Returns a *zap.Message
// already finalized with the request opcode flag.
func encodeWALFrameReq(mkt MarketID, txid uint32, body []byte) *zap.Message {
	payload := make([]byte, walFrameHeaderLen+len(body))
	copy(payload[0:8], mkt[:])
	binary.LittleEndian.PutUint32(payload[8:12], txid)
	binary.LittleEndian.PutUint32(payload[12:16], uint32(len(body)))
	copy(payload[16:], body)
	return wrapMessage(OpWALFrame, payload)
}

// decodeWALFrameReq pulls the (market, txid, body) tuple out of the
// payload bytes. Returns an error on under-length or self-inconsistent
// length-field. The body slice aliases payload — do not retain past
// the handler's return.
func decodeWALFrameReq(payload []byte) (MarketID, uint32, []byte, error) {
	if len(payload) < walFrameHeaderLen {
		return MarketID{}, 0, nil, errors.New("short wal frame")
	}
	var mkt MarketID
	copy(mkt[:], payload[0:8])
	txid := binary.LittleEndian.Uint32(payload[8:12])
	frameLen := binary.LittleEndian.Uint32(payload[12:16])
	if int(frameLen) != len(payload)-walFrameHeaderLen {
		return MarketID{}, 0, nil, errors.New("frame_len mismatch")
	}
	body := payload[walFrameHeaderLen:]
	return mkt, txid, body, nil
}

// encodeAck builds the OpWALFrameAck response payload.
// Layout: [1B code][8B mkt][4B txid].
func encodeAck(code uint8, mkt MarketID, txid uint32) []byte {
	out := make([]byte, ackLen)
	out[0] = code
	copy(out[1:9], mkt[:])
	binary.LittleEndian.PutUint32(out[9:13], txid)
	return out
}

// decodeWALFrameAck pulls (code, market, txid) out of the ACK payload.
// The wire message comes back wrapped as a single Bytes field at root[0]
// — we read it via msg.Root().Bytes(0).
func decodeWALFrameAck(msg *zap.Message) (uint8, MarketID, uint32, error) {
	if msg == nil {
		return 0, MarketID{}, 0, errors.New("nil ack")
	}
	root := msg.Root()
	if root.IsNull() {
		return 0, MarketID{}, 0, errors.New("null ack root")
	}
	payload := root.Bytes(0)
	if len(payload) < ackLen {
		return 0, MarketID{}, 0, errors.New("short ack")
	}
	code := payload[0]
	var mkt MarketID
	copy(mkt[:], payload[1:9])
	txid := binary.LittleEndian.Uint32(payload[9:13])
	return code, mkt, txid, nil
}

// encodeSnapshotReq builds the OpSnapshotReq message body.
func encodeSnapshotReq(mkt MarketID, fromTXID uint64) *zap.Message {
	payload := make([]byte, snapshotReqLen)
	copy(payload[0:8], mkt[:])
	binary.LittleEndian.PutUint64(payload[8:16], fromTXID)
	return wrapMessage(OpSnapshotReq, payload)
}

// decodeSnapshotReq pulls (market, fromTXID) from the request payload.
func decodeSnapshotReq(payload []byte) (MarketID, uint64, error) {
	if len(payload) < snapshotReqLen {
		return MarketID{}, 0, errors.New("short snapshot req")
	}
	var mkt MarketID
	copy(mkt[:], payload[0:8])
	from := binary.LittleEndian.Uint64(payload[8:16])
	return mkt, from, nil
}

// encodeSnapshotResp builds the OpSnapshotResp payload.
// Layout: [1B code][8B mkt][8B txid][N bytes].
func encodeSnapshotResp(code uint8, mkt MarketID, txid uint64, body []byte) []byte {
	out := make([]byte, snapshotRespHdrLen+len(body))
	out[0] = code
	copy(out[1:9], mkt[:])
	binary.LittleEndian.PutUint64(out[9:17], txid)
	copy(out[snapshotRespHdrLen:], body)
	return out
}

// DecodeSnapshotResp extracts (code, market, txid, body) from a
// SnapshotResp ZAP message — exported so cold-start callers can use it
// directly when bootstrapping new replicas.
func DecodeSnapshotResp(msg *zap.Message) (uint8, MarketID, uint64, []byte, error) {
	if msg == nil {
		return 0, MarketID{}, 0, nil, errors.New("nil resp")
	}
	root := msg.Root()
	if root.IsNull() {
		return 0, MarketID{}, 0, nil, errors.New("null root")
	}
	payload := root.Bytes(0)
	if len(payload) < snapshotRespHdrLen {
		return 0, MarketID{}, 0, nil, errors.New("short snapshot resp")
	}
	code := payload[0]
	var mkt MarketID
	copy(mkt[:], payload[1:9])
	txid := binary.LittleEndian.Uint64(payload[9:17])
	body := payload[snapshotRespHdrLen:]
	return code, mkt, txid, body, nil
}

// wrapMessage builds a single-Bytes-field ZAP message carrying payload
// at root[0]. Matches the wire shape the atsrpc dispatcher emits on
// the response side so the encoder is symmetric.
func wrapMessage(op uint16, payload []byte) *zap.Message {
	b := zap.NewBuilder(zap.HeaderSize + 32 + len(payload))
	obj := b.StartObject(8)
	obj.SetBytes(0, payload)
	obj.FinishAsRoot()
	msg, _ := zap.Parse(b.FinishWithFlags(op))
	return msg
}

// bytesReader returns an io.Reader over b without allocating a new
// io.NopCloser. Mirrors strings.NewReader for byte slices in the hot
// path; we don't need Close() semantics for in-memory bytes.
func bytesReader(b []byte) io.Reader {
	return &sliceReader{b: b}
}

type sliceReader struct {
	b   []byte
	pos int
}

func (r *sliceReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.pos:])
	r.pos += n
	return n, nil
}
