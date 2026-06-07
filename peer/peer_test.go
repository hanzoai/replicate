package peer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hanzoai/ltx"
	"github.com/luxfi/zap"
)

// fakeDispatcher implements the Dispatcher contract over a single zap.Node
// so the peer Server can register without depending on atsrpc.
type fakeDispatcher struct {
	mu       sync.Mutex
	routes   map[uint16]routeEntry // reqOp → entry
	respOps  map[uint16]uint16     // respOp → reqOp
	node     *zap.Node
	attached bool
}

type routeEntry struct {
	respOp  uint16
	handler Handler
}

func newFakeDispatcher(node *zap.Node) *fakeDispatcher {
	return &fakeDispatcher{
		routes:  map[uint16]routeEntry{},
		respOps: map[uint16]uint16{},
		node:    node,
	}
}

func (f *fakeDispatcher) Register(reqOp, respOp uint16, h Handler) error {
	if h == nil {
		return errors.New("nil handler")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if reqOp == respOp {
		return errors.New("reqOp == respOp")
	}
	if _, ok := f.routes[reqOp]; ok {
		return fmt.Errorf("reqOp 0x%04x already registered", reqOp)
	}
	if existing, ok := f.respOps[respOp]; ok {
		return fmt.Errorf("respOp 0x%04x already in use by reqOp 0x%04x", respOp, existing)
	}
	f.routes[reqOp] = routeEntry{respOp: respOp, handler: h}
	f.respOps[respOp] = reqOp
	return nil
}

// attach binds the dispatcher's route method to the upper byte of every
// request opcode (the 0xA0 namespace). Idempotent.
func (f *fakeDispatcher) attach() {
	f.mu.Lock()
	if f.attached {
		f.mu.Unlock()
		return
	}
	f.attached = true
	f.mu.Unlock()
	f.node.Handle(uint16(0xA0), f.dispatch)
}

func (f *fakeDispatcher) dispatch(ctx context.Context, peerID string, msg *zap.Message) (*zap.Message, error) {
	op := msg.Flags()
	f.mu.Lock()
	r, ok := f.routes[op]
	f.mu.Unlock()
	root := msg.Root()
	var payload []byte
	if !root.IsNull() {
		payload = root.Bytes(0)
	}
	if !ok {
		return wrapMessage(op, []byte{ErrCodeServerInternal}), nil
	}
	resp, err := r.handler(ctx, peerID, payload)
	if err != nil || resp == nil {
		return wrapMessage(r.respOp, []byte{ErrCodeServerInternal}), nil
	}
	return wrapMessage(r.respOp, resp), nil
}

// recordingSink remembers every Apply call so the test can assert that
// frames arrived in order with the expected bytes.
type recordingSink struct {
	mu       sync.Mutex
	frames   []frameRecord
	failNext atomic.Bool
}

type frameRecord struct {
	id   MarketID
	txid ltx.TXID
	body []byte
}

func (s *recordingSink) Apply(ctx context.Context, id MarketID, txid ltx.TXID, r io.Reader) error {
	if s.failNext.CompareAndSwap(true, false) {
		return errors.New("synthetic apply failure")
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.frames = append(s.frames, frameRecord{id: id, txid: txid, body: body})
	s.mu.Unlock()
	return nil
}

func (s *recordingSink) Snapshot(ctx context.Context, id MarketID, fromTXID ltx.TXID) (io.ReadCloser, ltx.TXID, error) {
	return io.NopCloser(bytes.NewReader([]byte("snapshot-body"))), 42, nil
}

func (s *recordingSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.frames)
}

func (s *recordingSink) frameAt(i int) frameRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.frames[i]
}

// startNode brings up a zap.Node on a random localhost port and waits
// for it to be listening. Returns the node + its listen port so a peer
// node can ConnectDirect to "127.0.0.1:<port>".
func startNode(t *testing.T, nodeID string) (*zap.Node, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	n := zap.NewNode(zap.NodeConfig{
		NodeID:      nodeID,
		ServiceType: "_peer_test._tcp",
		Port:        port,
		NoDiscovery: true,
	})
	if err := n.Start(); err != nil {
		t.Fatalf("start node %s: %v", nodeID, err)
	}
	t.Cleanup(func() { n.Stop() })

	// Give the listener a moment to accept.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 50*time.Millisecond)
		if err == nil {
			_ = c.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return n, port
}

// connectNodes wires `from` directly to `to` so Calls flow over the
// real socket (no mDNS).
func connectNodes(t *testing.T, from *zap.Node, toPort int) {
	t.Helper()
	if err := from.ConnectDirect(fmt.Sprintf("127.0.0.1:%d", toPort)); err != nil {
		t.Fatalf("connect direct: %v", err)
	}
	// Wait for the connection to register on both sides.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(from.Peers()) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("connect direct: no peers visible after 1s")
}

func TestReplicaClient_WriteAck(t *testing.T) {
	ctx := context.Background()
	owner, _ := startNode(t, "owner")
	standby, standbyPort := startNode(t, "standby")

	standbyDisp := newFakeDispatcher(standby)
	standbyDisp.attach()

	sink := &recordingSink{}
	server := NewServer(sink)
	if err := server.Register(standbyDisp); err != nil {
		t.Fatalf("server register: %v", err)
	}

	connectNodes(t, owner, standbyPort)

	mkt := MarketID{1, 2, 3, 4, 5, 6, 7, 8}
	client := NewReplicaClient(owner, "standby", mkt, WithTimeout(2*time.Second))
	body := []byte("ltx-frame-payload-001")
	info, err := client.WriteLTXFile(ctx, 0, ltx.TXID(101), ltx.TXID(101), bytes.NewReader(body))
	if err != nil {
		t.Fatalf("WriteLTXFile: %v", err)
	}
	if info.MaxTXID != 101 {
		t.Errorf("max txid: got %d want %d", info.MaxTXID, 101)
	}
	if info.Size != int64(len(body)) {
		t.Errorf("size: got %d want %d", info.Size, len(body))
	}
	if got := sink.count(); got != 1 {
		t.Fatalf("sink frames: got %d want 1", got)
	}
	rec := sink.frameAt(0)
	if rec.id != mkt {
		t.Errorf("market id: got %x want %x", rec.id, mkt)
	}
	if rec.txid != 101 {
		t.Errorf("txid: got %d want 101", rec.txid)
	}
	if !bytes.Equal(rec.body, body) {
		t.Errorf("body mismatch")
	}
}

func TestReplicaClient_ApplyFailureSurfacesAsError(t *testing.T) {
	ctx := context.Background()
	owner, _ := startNode(t, "owner-apply-fail")
	standby, standbyPort := startNode(t, "standby-apply-fail")

	standbyDisp := newFakeDispatcher(standby)
	standbyDisp.attach()

	sink := &recordingSink{}
	sink.failNext.Store(true)
	server := NewServer(sink)
	if err := server.Register(standbyDisp); err != nil {
		t.Fatalf("server register: %v", err)
	}

	connectNodes(t, owner, standbyPort)

	mkt := MarketID{9, 9, 9, 9, 9, 9, 9, 9}
	client := NewReplicaClient(owner, "standby-apply-fail", mkt)
	_, err := client.WriteLTXFile(ctx, 0, ltx.TXID(7), ltx.TXID(7), bytes.NewReader([]byte("x")))
	if err == nil {
		t.Fatal("expected error on Apply failure, got nil")
	}
	var ackErr *AckError
	if !errors.As(err, &ackErr) {
		t.Fatalf("expected *AckError, got %T: %v", err, err)
	}
	if ackErr.Code != ErrCodeServerInternal {
		t.Errorf("ack code: got 0x%02x want 0x%02x", ackErr.Code, ErrCodeServerInternal)
	}
}

func TestReplicaClient_PeerCloseRejects(t *testing.T) {
	ctx := context.Background()
	owner, _ := startNode(t, "owner-close")
	standby, standbyPort := startNode(t, "standby-close")

	standbyDisp := newFakeDispatcher(standby)
	standbyDisp.attach()

	sink := &recordingSink{}
	server := NewServer(sink)
	if err := server.Register(standbyDisp); err != nil {
		t.Fatalf("server register: %v", err)
	}
	_ = server.Close()

	connectNodes(t, owner, standbyPort)

	mkt := MarketID{2, 0, 2, 6, 0, 6, 0, 6}
	client := NewReplicaClient(owner, "standby-close", mkt)
	_, err := client.WriteLTXFile(ctx, 0, ltx.TXID(1), ltx.TXID(1), bytes.NewReader([]byte("p")))
	if err == nil {
		t.Fatal("expected error after server.Close, got nil")
	}
	var ackErr *AckError
	if !errors.As(err, &ackErr) {
		t.Fatalf("expected *AckError, got %T: %v", err, err)
	}
	if ackErr.Code != ErrCodeNotStandby {
		t.Errorf("ack code: got 0x%02x want 0x%02x", ackErr.Code, ErrCodeNotStandby)
	}
}

func TestReplicaClient_SnapshotResp(t *testing.T) {
	ctx := context.Background()
	owner, _ := startNode(t, "owner-snap")
	standby, standbyPort := startNode(t, "standby-snap")

	standbyDisp := newFakeDispatcher(standby)
	standbyDisp.attach()

	sink := &recordingSink{}
	server := NewServer(sink)
	if err := server.Register(standbyDisp); err != nil {
		t.Fatalf("server register: %v", err)
	}

	connectNodes(t, owner, standbyPort)

	mkt := MarketID{7, 7, 7, 7, 7, 7, 7, 7}
	req := encodeSnapshotReq(mkt, 0)
	resp, err := owner.Call(ctx, "standby-snap", req)
	if err != nil {
		t.Fatalf("snapshot call: %v", err)
	}
	code, gotMkt, gotTXID, body, err := DecodeSnapshotResp(resp)
	if err != nil {
		t.Fatalf("decode snapshot resp: %v", err)
	}
	if code != ErrCodeOK {
		t.Fatalf("code: 0x%02x", code)
	}
	if gotMkt != mkt {
		t.Errorf("market mismatch")
	}
	if gotTXID != 42 {
		t.Errorf("txid: got %d want 42", gotTXID)
	}
	if !bytes.Equal(body, []byte("snapshot-body")) {
		t.Errorf("body: %q", body)
	}
}

func TestRegister_RejectsDuplicate(t *testing.T) {
	owner, _ := startNode(t, "register-test")
	disp := newFakeDispatcher(owner)
	disp.attach()

	sink := &recordingSink{}
	server := NewServer(sink)
	if err := server.Register(disp); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := server.Register(disp); err == nil {
		t.Fatal("expected duplicate register error, got nil")
	}
}

func TestWireRoundTrip(t *testing.T) {
	mkt := MarketID{0xa, 0xb, 0xc, 0xd, 0xe, 0xf, 0x10, 0x11}
	body := []byte("payload-octets")
	req := encodeWALFrameReq(mkt, 0x1234, body)
	// Round-trip through the parsed message → raw payload route the
	// dispatcher takes on the wire.
	root := req.Root()
	if root.IsNull() {
		t.Fatal("null root")
	}
	rawPayload := root.Bytes(0)
	gotMkt, gotTXID, gotBody, err := decodeWALFrameReq(rawPayload)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if gotMkt != mkt {
		t.Errorf("market: got %x want %x", gotMkt, mkt)
	}
	if gotTXID != 0x1234 {
		t.Errorf("txid: got 0x%x", gotTXID)
	}
	if !bytes.Equal(gotBody, body) {
		t.Errorf("body mismatch")
	}
}

// panickingSink implements FrameSink and panics on the Nth Apply call.
// Used to verify the server's panic recovery (F-22).
type panickingSink struct {
	mu        sync.Mutex
	calls     int
	panicAt   int
	postPanic []frameRecord
	panicMsg  string
}

func (s *panickingSink) Apply(ctx context.Context, id MarketID, txid ltx.TXID, r io.Reader) error {
	s.mu.Lock()
	s.calls++
	n := s.calls
	s.mu.Unlock()
	if s.panicAt > 0 && n == s.panicAt {
		// Drain the reader so the inbound buffer drains predictably.
		_, _ = io.ReadAll(r)
		panic(s.panicMsg)
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.postPanic = append(s.postPanic, frameRecord{id: id, txid: txid, body: body})
	s.mu.Unlock()
	return nil
}

func (s *panickingSink) Snapshot(ctx context.Context, id MarketID, fromTXID ltx.TXID) (io.ReadCloser, ltx.TXID, error) {
	return io.NopCloser(bytes.NewReader(nil)), 0, nil
}

// TestPeerServer_PanicInWALFrameHandlerDoesNotKillServer asserts that
// a panic inside the FrameSink does not propagate up the dispatch
// goroutine. The server must catch the panic, ACK with
// ErrCodeServerInternal, and remain available for subsequent frames.
//
// Closes F-22 (CRITICAL): a malformed payload that panicked inside
// LTXTailWriter.Apply previously DoS'd the entire standby's peer
// receive surface.
func TestPeerServer_PanicInWALFrameHandlerDoesNotKillServer(t *testing.T) {
	ctx := context.Background()
	owner, _ := startNode(t, "owner-panic")
	standby, standbyPort := startNode(t, "standby-panic")

	standbyDisp := newFakeDispatcher(standby)
	standbyDisp.attach()

	sink := &panickingSink{panicAt: 3, panicMsg: "synthetic apply panic"}
	server := NewServer(sink)
	if err := server.Register(standbyDisp); err != nil {
		t.Fatalf("server register: %v", err)
	}

	connectNodes(t, owner, standbyPort)

	mkt := MarketID{0xde, 0xad, 0xbe, 0xef, 0xca, 0xfe, 0xba, 0xbe}
	client := NewReplicaClient(owner, "standby-panic", mkt, WithTimeout(2*time.Second))

	// Send 10 frames; the 3rd panics. The remaining 7 must succeed.
	const N = 10
	results := make([]error, N)
	for i := 0; i < N; i++ {
		body := []byte(fmt.Sprintf("frame-%d", i))
		_, err := client.WriteLTXFile(ctx, 0, ltx.TXID(i+1), ltx.TXID(i+1), bytes.NewReader(body))
		results[i] = err
	}

	// Frame index 2 (3rd call) must surface as an error code; the
	// rest must succeed.
	for i, err := range results {
		if i == 2 {
			if err == nil {
				t.Fatalf("frame %d: expected panic-induced error, got nil", i)
			}
			var ackErr *AckError
			if !errors.As(err, &ackErr) {
				t.Fatalf("frame %d: expected *AckError, got %T: %v", i, err, err)
			}
			if ackErr.Code != ErrCodeServerInternal {
				t.Fatalf("frame %d: ack code 0x%02x want 0x%02x", i, ackErr.Code, ErrCodeServerInternal)
			}
			continue
		}
		if err != nil {
			t.Errorf("frame %d after panic: %v", i, err)
		}
	}

	sink.mu.Lock()
	totalCalls := sink.calls
	survivors := len(sink.postPanic)
	sink.mu.Unlock()
	if totalCalls != N {
		t.Errorf("sink calls: got %d want %d (server must still receive every frame)", totalCalls, N)
	}
	if survivors != N-1 {
		t.Errorf("survived frames: got %d want %d", survivors, N-1)
	}
}

// TestPeerServer_PanicInSnapshotHandlerDoesNotKillServer mirrors the
// WAL-frame panic test for the snapshot handler.
func TestPeerServer_PanicInSnapshotHandlerDoesNotKillServer(t *testing.T) {
	ctx := context.Background()
	owner, _ := startNode(t, "owner-snap-panic")
	standby, standbyPort := startNode(t, "standby-snap-panic")

	standbyDisp := newFakeDispatcher(standby)
	standbyDisp.attach()

	sink := &snapshotPanickingSink{}
	server := NewServer(sink)
	if err := server.Register(standbyDisp); err != nil {
		t.Fatalf("server register: %v", err)
	}

	connectNodes(t, owner, standbyPort)

	mkt := MarketID{1, 2, 3, 4, 5, 6, 7, 8}
	req := encodeSnapshotReq(mkt, 0)
	resp, err := owner.Call(ctx, "standby-snap-panic", req)
	if err != nil {
		t.Fatalf("snapshot call: %v", err)
	}
	code, _, _, _, err := DecodeSnapshotResp(resp)
	if err != nil {
		t.Fatalf("decode snapshot resp: %v", err)
	}
	if code != ErrCodeServerInternal {
		t.Fatalf("expected ErrCodeServerInternal, got 0x%02x", code)
	}

	// Server must still answer subsequent calls.
	sink.disablePanic.Store(true)
	resp2, err := owner.Call(ctx, "standby-snap-panic", req)
	if err != nil {
		t.Fatalf("snapshot call after panic: %v", err)
	}
	code2, _, _, _, err := DecodeSnapshotResp(resp2)
	if err != nil {
		t.Fatalf("decode resp after panic: %v", err)
	}
	if code2 != ErrCodeOK {
		t.Fatalf("post-panic code: 0x%02x want 0x00", code2)
	}
}

type snapshotPanickingSink struct {
	disablePanic atomic.Bool
}

func (s *snapshotPanickingSink) Apply(ctx context.Context, id MarketID, txid ltx.TXID, r io.Reader) error {
	return nil
}

func (s *snapshotPanickingSink) Snapshot(ctx context.Context, id MarketID, fromTXID ltx.TXID) (io.ReadCloser, ltx.TXID, error) {
	if !s.disablePanic.Load() {
		panic("synthetic snapshot panic")
	}
	return io.NopCloser(bytes.NewReader([]byte("ok"))), 1, nil
}

// TestPeerFramePool_DropsOversizeBuffers verifies that buffers grown
// beyond ltxPoolCap during read are dropped on release, not pooled.
// Without this, a single >64KB frame would permanently inflate the
// pool's cached buffer cap, forcing every subsequent reuse through
// the outlier branch (F-05).
func TestPeerFramePool_DropsOversizeBuffers(t *testing.T) {
	// Saturate the pool with a known reference buffer.
	original := make([]byte, 0, ltxPoolCap)
	ltxFramePool.Put(&original)

	// Read a frame larger than ltxPoolCap. The reader enters the
	// outlier branch (cap >= ltxPoolCap, len == cap), drops the
	// pool buffer, and returns a caller-owned slice. Release is a
	// no-op for the caller-owned slice.
	bigBody := bytes.Repeat([]byte{0xab}, ltxPoolCap*2)
	buf, release, err := readPooled(bytes.NewReader(bigBody))
	if err != nil {
		t.Fatalf("readPooled: %v", err)
	}
	if !bytes.Equal(buf, bigBody) {
		t.Fatalf("buf mismatch — outlier path corrupted body")
	}
	release()

	// Hammer the pool with many oversize frames to force any
	// poisoned buffers out into circulation. Every release must
	// drop oversize.
	for i := 0; i < 32; i++ {
		body := bytes.Repeat([]byte{byte(i)}, ltxPoolCap+1024)
		got, rel, err := readPooled(bytes.NewReader(body))
		if err != nil {
			t.Fatalf("readPooled iter %d: %v", i, err)
		}
		if len(got) != len(body) {
			t.Fatalf("iter %d: len mismatch got=%d want=%d", i, len(got), len(body))
		}
		rel()
	}

	// Now request many normal-sized frames. None should expose a
	// buffer with cap > ltxPoolCap. We drain the pool via Get to
	// inspect cached buffers directly.
	var samples []int
	for i := 0; i < 64; i++ {
		ptr := ltxFramePool.Get().(*[]byte)
		samples = append(samples, cap(*ptr))
		// Don't put back during sampling; we're inspecting.
	}
	for _, c := range samples {
		if c > ltxPoolCap {
			t.Errorf("pool contained oversize buffer cap=%d (>ltxPoolCap=%d) — F-05 regression",
				c, ltxPoolCap)
		}
	}
}

// TestPutPooled_DropsOversize is a focused unit test on the
// putPooled helper. It verifies that an oversize buffer is dropped
// without panicking and a normal-sized buffer is returned to the
// pool with len reset.
func TestPutPooled_DropsOversize(t *testing.T) {
	// Oversize buffer — must be dropped, not pooled.
	oversize := make([]byte, ltxPoolCap*3+100)
	ptr := &oversize
	putPooled(ptr)
	// We cannot directly assert "not in pool" without a custom pool;
	// instead assert: a normal-sized buffer that we Put + Get must
	// come back with len=0 and cap≤ltxPoolCap.

	normal := make([]byte, 100, ltxPoolCap)
	nPtr := &normal
	putPooled(nPtr)
	if len(*nPtr) != 0 {
		t.Errorf("putPooled did not reset len: got %d want 0", len(*nPtr))
	}
	if cap(*nPtr) != ltxPoolCap {
		t.Errorf("putPooled mutated cap: got %d want %d", cap(*nPtr), ltxPoolCap)
	}

	// putPooled(nil) must not panic.
	putPooled(nil)
}
