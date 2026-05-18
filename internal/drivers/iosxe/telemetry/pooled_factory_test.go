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

package telemetry

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/devicegrpc"
)

func TestPooledSubscribeClientFactoryLeasesClassTelemetry(t *testing.T) {
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	t.Cleanup(srv.Stop)
	go func() { _ = srv.Serve(lis) }()

	var dials atomic.Int64
	dial := func(_ context.Context, _ string, _ devicegrpc.DialConfig) (*grpc.ClientConn, error) {
		dials.Add(1)
		return grpc.NewClient(
			"passthrough:///bufconn",
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
				return lis.DialContext(ctx)
			}),
		)
	}
	pool := devicegrpc.New(devicegrpc.DialConfig{}, dial)
	t.Cleanup(func() { _ = pool.Close() })

	key := devicegrpc.DeviceKey{Address: "10.0.0.1", Port: 50052}
	f := NewPooledSubscribeClientFactory(pool, key, "admin", "pw")

	c1, err := f.NewClient(context.Background())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c1.Release == nil {
		t.Fatal("expected SubscribeClient.Release to be wired to lease.Release")
	}
	c2, err := f.NewClient(context.Background())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c1.Conn != c2.Conn {
		t.Fatalf("expected pooled factory to share conn between two ClassTelemetry clients")
	}
	if got := dials.Load(); got != 1 {
		t.Fatalf("expected exactly 1 dial across two factory calls, got %d", got)
	}
	c1.Release()
	c2.Release()
}

func TestPooledSubscribeClientFactoryNilPoolRejected(t *testing.T) {
	f := &PooledSubscribeClientFactory{}
	_, err := f.NewClient(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Pool") {
		t.Fatalf("expected non-nil Pool error, got %v", err)
	}
}
