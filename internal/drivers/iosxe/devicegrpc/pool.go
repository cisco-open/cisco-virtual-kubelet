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

// Package devicegrpc provides a workload-classed gRPC connection pool
// per device. Consumers obtain a *grpc.ClientConn via Lease(key, class)
// and call the returned Release() exactly once when done.
//
// Conns are partitioned by WorkloadClass so unrelated streams cannot
// HOL-block each other on shared HTTP/2 flow control. A bulk transfer on
// ClassBulkTransfer therefore does not back-pressure unary work on
// ClassControl. Within a single class on a single device, callers share
// the same conn and refcount it. A Pool has one dial policy and may be
// shared only by consumers with identical TLS and per-RPC credential
// requirements. Current production gNOI wiring uses the control and
// bulk-transfer classes; gNMI dials independently.
//
// DialConfig carries transport security and optional per-RPC credentials
// for pool consumers.
package devicegrpc

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// WorkloadClass partitions per-device gRPC connections.
type WorkloadClass int

const (
	// ClassControl carries unary RPCs that complete in milliseconds:
	// gNMI Set, gNMI Get, gNOI System.Time, OS.Verify, Cert.* and so on.
	ClassControl WorkloadClass = iota

	// ClassTelemetry carries long-lived gNMI Subscribe streams. Held
	// for the lifetime of the device worker.
	ClassTelemetry

	// ClassBulkTransfer carries bidi streams that transit hundreds of
	// megabytes: gNOI OS.Install, File.Put, File.Get. Leases are taken
	// per-RPC and released as soon as the stream completes.
	ClassBulkTransfer
)

func (c WorkloadClass) String() string {
	switch c {
	case ClassControl:
		return "control"
	case ClassTelemetry:
		return "telemetry"
	case ClassBulkTransfer:
		return "bulk-transfer"
	default:
		return fmt.Sprintf("unknown(%d)", int(c))
	}
}

// DeviceKey identifies a device target.
type DeviceKey struct {
	Address string
	Port    int
}

// Target returns the "addr:port" form used by grpc.NewClient.
func (k DeviceKey) Target() string {
	return fmt.Sprintf("%s:%d", k.Address, k.Port)
}

// DialConfig is the per-pool TLS + auth material.
type DialConfig struct {
	// TLSConfig, when non-nil, is used as the gRPC dial credentials.
	// Nil dials with insecure plaintext credentials.
	TLSConfig *tls.Config

	// Username, when non-empty, drives AuthContext to preserve the legacy
	// HTTP Basic metadata contract. New secure IOS XE gNOI uses
	// RPCCredentials instead.
	Username string
	Password string

	// RPCCredentials, when non-nil, are attached to every RPC on the
	// connection. They may only be used when TLSConfig is also non-nil;
	// defaultDial rejects credentials on a plaintext connection.
	RPCCredentials credentials.PerRPCCredentials

	// Extra dial options. Tests pass grpc.WithContextDialer here to
	// wire a bufconn listener; production wiring leaves this empty.
	Extra []grpc.DialOption
}

// AuthContext returns a context decorator that attaches the legacy HTTP Basic
// metadata. Empty username yields a passthrough.
func (c DialConfig) AuthContext() func(context.Context) context.Context {
	if c.Username == "" {
		return func(ctx context.Context) context.Context { return ctx }
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(c.Username + ":" + c.Password))
	return func(ctx context.Context) context.Context {
		return metadata.AppendToOutgoingContext(ctx, "authorization", "Basic "+encoded)
	}
}

// Lease is a refcounted hold on a *grpc.ClientConn. Release must be
// invoked exactly once; subsequent calls are no-ops.
type Lease struct {
	// Conn is the shared *grpc.ClientConn for the (DeviceKey, WorkloadClass)
	// the lease was taken under.
	Conn *grpc.ClientConn

	release func()
	once    sync.Once
}

// Release decrements the refcount on the underlying conn. When the
// last lease is released, the pool closes the conn. Safe to call on
// a nil receiver and idempotent on a non-nil one.
func (l *Lease) Release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		if l.release != nil {
			l.release()
		}
	})
}

// DialFunc is the constructor seam tests use to substitute a bufconn
// dialer. Production wiring uses the default at defaultDial.
type DialFunc func(ctx context.Context, target string, cfg DialConfig) (*grpc.ClientConn, error)

func defaultDial(_ context.Context, target string, cfg DialConfig) (*grpc.ClientConn, error) {
	if cfg.RPCCredentials != nil && cfg.TLSConfig == nil {
		return nil, errors.New("devicegrpc: per-RPC credentials require TLS")
	}
	var creds credentials.TransportCredentials
	if cfg.TLSConfig != nil {
		creds = credentials.NewTLS(cfg.TLSConfig)
	} else {
		creds = insecure.NewCredentials()
	}
	opts := make([]grpc.DialOption, 0, 2+len(cfg.Extra))
	opts = append(opts, grpc.WithTransportCredentials(creds))
	if cfg.RPCCredentials != nil {
		opts = append(opts, grpc.WithPerRPCCredentials(cfg.RPCCredentials))
	}
	opts = append(opts, cfg.Extra...)
	return grpc.NewClient(target, opts...)
}

// Pool hands out workload-classed gRPC connections to per-device
// consumers.
type Pool interface {
	// Lease returns a Lease holding a *grpc.ClientConn for the given
	// device and workload class. Concurrent callers for the same
	// (key, class) share the same conn and increment its refcount.
	Lease(ctx context.Context, key DeviceKey, class WorkloadClass) (*Lease, error)

	// Close releases all pool-owned conns. Outstanding leases become
	// stale immediately and their Release() calls are no-ops; the
	// returned error names the count of leases still outstanding so
	// callers can spot lifecycle bugs in tests.
	Close() error
}

// New constructs a Pool. Pass dial=nil to use the default
// grpc.NewClient-based dialer; tests substitute a bufconn dialer.
func New(cfg DialConfig, dial DialFunc) Pool {
	if dial == nil {
		dial = defaultDial
	}
	return &pool{
		cfg:     cfg,
		dial:    dial,
		entries: make(map[entryKey]*entry),
	}
}

type entryKey struct {
	DeviceKey
	Class WorkloadClass
}

type entry struct {
	conn  *grpc.ClientConn
	refs  int
	class WorkloadClass
}

type pool struct {
	cfg  DialConfig
	dial DialFunc

	mu      sync.Mutex
	entries map[entryKey]*entry
	closed  bool
}

func (p *pool) Lease(ctx context.Context, key DeviceKey, class WorkloadClass) (*Lease, error) {
	if key.Address == "" {
		return nil, errors.New("devicegrpc: DeviceKey.Address is empty")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, errors.New("devicegrpc: pool is closed")
	}
	k := entryKey{DeviceKey: key, Class: class}
	e, ok := p.entries[k]
	if !ok {
		conn, err := p.dial(ctx, key.Target(), p.cfg)
		if err != nil {
			return nil, fmt.Errorf("devicegrpc: dial %s (%s): %w", key.Target(), class, err)
		}
		e = &entry{conn: conn, class: class}
		p.entries[k] = e
	}
	e.refs++
	recordLease(class)
	return &Lease{
		Conn:    e.conn,
		release: func() { p.release(k) },
	}, nil
}

func (p *pool) release(k entryKey) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.entries[k]
	if !ok {
		return
	}
	e.refs--
	recordRelease(e.class)
	if e.refs <= 0 {
		_ = e.conn.Close()
		delete(p.entries, k)
	}
}

func (p *pool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	var outstanding int
	for k, e := range p.entries {
		if e.refs > 0 {
			outstanding += e.refs
			recordCloseLeaks(e.class, e.refs)
		}
		_ = e.conn.Close()
		delete(p.entries, k)
	}
	if outstanding > 0 {
		return fmt.Errorf("devicegrpc: pool closed with %d outstanding leases", outstanding)
	}
	return nil
}
