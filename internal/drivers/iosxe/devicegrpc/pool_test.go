// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package devicegrpc

import (
	"context"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// bufconnDial returns a DialFunc that ignores the target string and dials
// the supplied bufconn.Listener. The returned counter increments each
// time the pool actually dials — separate from the number of Lease calls.
func bufconnDial(t *testing.T, lis *bufconn.Listener) (DialFunc, *atomic.Int64) {
	t.Helper()
	var dials atomic.Int64
	return func(_ context.Context, _ string, _ DialConfig) (*grpc.ClientConn, error) {
		dials.Add(1)
		return grpc.NewClient(
			"passthrough:///bufconn",
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
		)
	}, &dials
}

func newBufconnServer(t *testing.T) *bufconn.Listener {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer()
	t.Cleanup(srv.Stop)
	go func() {
		// Errors during shutdown are expected; we silence them so the
		// test goroutine doesn't outlive t.
		_ = srv.Serve(lis)
	}()
	return lis
}

func TestLeaseSameClassSharesConn(t *testing.T) {
	lis := newBufconnServer(t)
	dial, dials := bufconnDial(t, lis)
	p := New(DialConfig{}, dial)
	t.Cleanup(func() { _ = p.Close() })

	ctx := context.Background()
	key := DeviceKey{Address: "10.0.0.1", Port: 50052}

	l1, err := p.Lease(ctx, key, ClassControl)
	if err != nil {
		t.Fatalf("first lease: %v", err)
	}
	l2, err := p.Lease(ctx, key, ClassControl)
	if err != nil {
		t.Fatalf("second lease: %v", err)
	}
	if l1.Conn != l2.Conn {
		t.Fatalf("expected shared conn for two ClassControl leases, got distinct conns")
	}
	if got := dials.Load(); got != 1 {
		t.Fatalf("expected exactly 1 dial for two same-class leases, got %d", got)
	}
	l1.Release()
	l2.Release()
}

func TestLeaseDifferentClassesAreIsolated(t *testing.T) {
	lis := newBufconnServer(t)
	dial, dials := bufconnDial(t, lis)
	p := New(DialConfig{}, dial)
	t.Cleanup(func() { _ = p.Close() })

	ctx := context.Background()
	key := DeviceKey{Address: "10.0.0.1", Port: 50052}

	control, err := p.Lease(ctx, key, ClassControl)
	if err != nil {
		t.Fatalf("control lease: %v", err)
	}
	telem, err := p.Lease(ctx, key, ClassTelemetry)
	if err != nil {
		t.Fatalf("telemetry lease: %v", err)
	}
	bulk, err := p.Lease(ctx, key, ClassBulkTransfer)
	if err != nil {
		t.Fatalf("bulk lease: %v", err)
	}
	if control.Conn == telem.Conn || control.Conn == bulk.Conn || telem.Conn == bulk.Conn {
		t.Fatalf("expected three distinct conns across classes; control=%p telemetry=%p bulk=%p",
			control.Conn, telem.Conn, bulk.Conn)
	}
	if got := dials.Load(); got != 3 {
		t.Fatalf("expected 3 dials (one per class), got %d", got)
	}
	control.Release()
	telem.Release()
	bulk.Release()
}

func TestReleaseIsIdempotent(t *testing.T) {
	lis := newBufconnServer(t)
	dial, dials := bufconnDial(t, lis)
	p := New(DialConfig{}, dial)
	t.Cleanup(func() { _ = p.Close() })

	ctx := context.Background()
	key := DeviceKey{Address: "10.0.0.1", Port: 50052}

	l, err := p.Lease(ctx, key, ClassControl)
	if err != nil {
		t.Fatalf("lease: %v", err)
	}
	// Multiple Releases on the same Lease must not double-decrement.
	l.Release()
	l.Release()
	l.Release()

	// After full release the next lease should dial fresh.
	l2, err := p.Lease(ctx, key, ClassControl)
	if err != nil {
		t.Fatalf("re-lease after full release: %v", err)
	}
	defer l2.Release()
	if got := dials.Load(); got != 2 {
		t.Fatalf("expected 2 dials after release+re-lease, got %d", got)
	}
}

func TestReleaseRefcountDoesNotPrematurelyClose(t *testing.T) {
	lis := newBufconnServer(t)
	dial, _ := bufconnDial(t, lis)
	p := New(DialConfig{}, dial)
	t.Cleanup(func() { _ = p.Close() })

	ctx := context.Background()
	key := DeviceKey{Address: "10.0.0.1", Port: 50052}

	l1, err := p.Lease(ctx, key, ClassControl)
	if err != nil {
		t.Fatalf("first lease: %v", err)
	}
	l2, err := p.Lease(ctx, key, ClassControl)
	if err != nil {
		t.Fatalf("second lease: %v", err)
	}
	defer l2.Release()

	conn := l1.Conn
	l1.Release()

	// l2 still holds the conn; it must remain usable. We can't directly
	// observe "open" but Conn.GetState() should not be Shutdown.
	if state := conn.GetState().String(); state == "SHUTDOWN" {
		t.Fatalf("conn was closed while a second lease was still held; state=%s", state)
	}
}

func TestLeaseAfterCloseFails(t *testing.T) {
	lis := newBufconnServer(t)
	dial, _ := bufconnDial(t, lis)
	p := New(DialConfig{}, dial)

	if err := p.Close(); err != nil {
		t.Fatalf("initial close: %v", err)
	}

	_, err := p.Lease(context.Background(), DeviceKey{Address: "10.0.0.1", Port: 50052}, ClassControl)
	if err == nil || !strings.Contains(err.Error(), "pool is closed") {
		t.Fatalf("expected pool-is-closed error, got %v", err)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	lis := newBufconnServer(t)
	dial, _ := bufconnDial(t, lis)
	p := New(DialConfig{}, dial)

	if err := p.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestCloseWithOutstandingLeasesReportsCount(t *testing.T) {
	lis := newBufconnServer(t)
	dial, _ := bufconnDial(t, lis)
	p := New(DialConfig{}, dial)

	ctx := context.Background()
	key := DeviceKey{Address: "10.0.0.1", Port: 50052}
	l1, err := p.Lease(ctx, key, ClassControl)
	if err != nil {
		t.Fatalf("lease 1: %v", err)
	}
	l2, err := p.Lease(ctx, key, ClassTelemetry)
	if err != nil {
		t.Fatalf("lease 2: %v", err)
	}

	err = p.Close()
	if err == nil {
		t.Fatalf("expected close error reporting outstanding leases")
	}
	if !strings.Contains(err.Error(), "2 outstanding") {
		t.Fatalf("expected error to name outstanding count, got %v", err)
	}

	// Stale leases must Release without panic and as no-op (pool already
	// closed their conns).
	l1.Release()
	l2.Release()
}

func TestEmptyAddressRejected(t *testing.T) {
	p := New(DialConfig{}, func(context.Context, string, DialConfig) (*grpc.ClientConn, error) {
		t.Fatal("dial should not be called for empty address")
		return nil, nil
	})
	t.Cleanup(func() { _ = p.Close() })

	_, err := p.Lease(context.Background(), DeviceKey{Address: "", Port: 50052}, ClassControl)
	if err == nil || !strings.Contains(err.Error(), "Address is empty") {
		t.Fatalf("expected empty-address error, got %v", err)
	}
}

func TestConcurrentLeasesDoNotDoubleDial(t *testing.T) {
	lis := newBufconnServer(t)
	dial, dials := bufconnDial(t, lis)
	p := New(DialConfig{}, dial)
	t.Cleanup(func() { _ = p.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	key := DeviceKey{Address: "10.0.0.1", Port: 50052}

	const N = 32
	var wg sync.WaitGroup
	leases := make([]*Lease, N)
	for i := range leases {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l, err := p.Lease(ctx, key, ClassControl)
			if err != nil {
				t.Errorf("lease %d: %v", i, err)
				return
			}
			leases[i] = l
		}(i)
	}
	wg.Wait()

	if got := dials.Load(); got != 1 {
		t.Fatalf("expected exactly 1 dial across %d concurrent same-class leases, got %d", N, got)
	}
	for _, l := range leases {
		if l != nil {
			l.Release()
		}
	}
}
