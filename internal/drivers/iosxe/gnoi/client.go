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

package gnoi

import (
	"context"
	"errors"
	"time"

	"google.golang.org/grpc"

	certpb "github.com/openconfig/gnoi/cert"
	resetpb "github.com/openconfig/gnoi/factory_reset"
	filepb "github.com/openconfig/gnoi/file"
	ospb "github.com/openconfig/gnoi/os"
	syspb "github.com/openconfig/gnoi/system"
)

// Service identifies one of the gNOI sub-services. Used by the
// capability cache and surfaced through ErrServiceUnsupported.
type Service string

const (
	ServiceOS           Service = "os"
	ServiceSystem       Service = "system"
	ServiceFile         Service = "file"
	ServiceCert         Service = "cert"
	ServiceFactoryReset Service = "factory_reset"
)

// ErrServiceUnsupported wraps a codes.Unimplemented response from the
// device and names the gNOI service that failed. Reconcilers surface
// this verbatim in Status conditions so operators see a clean
// "X service not supported on this IOS-XE release" rather than an
// opaque gRPC stack trace.
type ErrServiceUnsupported struct {
	Service Service
	Cause   error
}

func (e *ErrServiceUnsupported) Error() string {
	return "gnoi: service " + string(e.Service) + " unsupported on device: " + e.Cause.Error()
}

func (e *ErrServiceUnsupported) Unwrap() error { return e.Cause }

// AuthContext decorates outgoing RPC contexts with the authentication
// metadata the device requires. For IOS-XE this is HTTP-Basic, the
// same shape used by gNMI; alternate-shape devices substitute their
// own at the call site.
type AuthContext func(context.Context) context.Context

// Provider exposes a per-device gNOI client to reconcilers.
type Provider interface {
	GNOIClient(ctx context.Context) (*Client, error)
}

// ResetProvider is implemented by providers that can drop their
// current gRPC leases and build a fresh client after a transient
// transport failure.
type ResetProvider interface {
	Provider
	ResetGNOIClient(ctx context.Context)
}

// BulkConnProvider lazily leases the bulk-transfer gRPC connection
// used by OS.Install and File.Put/Get. The returned release function
// must be called once the bulk RPC has completed.
type BulkConnProvider func(ctx context.Context) (*grpc.ClientConn, func(), error)

// Options carries optional inputs to New. Defaults are sensible for
// IOS-XE; tests substitute fake clients via the With* hooks.
type Options struct {
	// Auth is the per-RPC context decorator. nil means no auth
	// metadata is attached.
	Auth AuthContext

	// BulkConn, when non-nil, is used for bulk-transfer RPCs
	// (OS.Install, File.Put, File.Get) instead of the main control
	// conn. Set this from a separate devicegrpc.Pool lease under
	// WorkloadClass ClassBulkTransfer so a 500 MB image transfer
	// cannot HOL-block control RPCs.
	BulkConn *grpc.ClientConn

	// BulkConnProvider lazily leases a bulk-transfer conn per bulk RPC.
	// Prefer this in production so idle per-device VK pods do not hold
	// ClassBulkTransfer leases until an OS/file transfer actually runs.
	BulkConnProvider BulkConnProvider

	// Now is the clock used by the capability cache. nil means time.Now.
	Now func() time.Time
}

// Client is the per-device gNOI client. Construct one via New using a
// shared *grpc.ClientConn leased from devicegrpc.Pool under
// WorkloadClass ClassControl. The Client never closes the conn —
// ownership stays with the lease holder.
type Client struct {
	conn     *grpc.ClientConn
	bulkConn *grpc.ClientConn
	auth     AuthContext

	os     ospb.OSClient
	system syspb.SystemClient
	file   filepb.FileClient
	cert   certpb.CertificateManagementClient
	reset  resetpb.FactoryResetClient

	osBulk       ospb.OSClient
	fileBulk     filepb.FileClient
	bulkProvider BulkConnProvider

	cap *CapabilityCache
}

// New constructs a Client. conn is mandatory and is used for all
// non-bulk-transfer RPCs; opts.BulkConn, when non-nil, is used for
// OS.Install, File.Put, and File.Get exclusively.
func New(conn *grpc.ClientConn, opts Options) (*Client, error) {
	if conn == nil {
		return nil, errors.New("gnoi: New: nil control conn")
	}
	bulk := opts.BulkConn
	if bulk == nil {
		// Fallback: use the control conn for bulk transfers too.
		// Documented as a footgun in package doc — callers SHOULD
		// supply a ClassBulkTransfer lease in production.
		bulk = conn
	}
	c := &Client{
		conn:     conn,
		bulkConn: bulk,
		auth:     opts.Auth,

		os:     ospb.NewOSClient(conn),
		system: syspb.NewSystemClient(conn),
		file:   filepb.NewFileClient(conn),
		cert:   certpb.NewCertificateManagementClient(conn),
		reset:  resetpb.NewFactoryResetClient(conn),

		osBulk:       ospb.NewOSClient(bulk),
		fileBulk:     filepb.NewFileClient(bulk),
		bulkProvider: opts.BulkConnProvider,

		cap: NewCapabilityCache(opts.Now),
	}
	if c.auth == nil {
		c.auth = func(ctx context.Context) context.Context { return ctx }
	}
	return c, nil
}

// Capabilities returns the per-service capability cache. Callers can
// pre-probe by invoking the cache directly or rely on lazy probing
// on first method call.
func (c *Client) Capabilities() *CapabilityCache { return c.cap }

func (c *Client) bulkOSClient(ctx context.Context) (ospb.OSClient, func(), error) {
	if c.bulkProvider == nil {
		return c.osBulk, func() {}, nil
	}
	conn, release, err := c.bulkProvider(ctx)
	if err != nil {
		return nil, func() {}, err
	}
	if release == nil {
		release = func() {}
	}
	return ospb.NewOSClient(conn), release, nil
}

func (c *Client) bulkFileClient(ctx context.Context) (filepb.FileClient, func(), error) {
	if c.bulkProvider == nil {
		return c.fileBulk, func() {}, nil
	}
	conn, release, err := c.bulkProvider(ctx)
	if err != nil {
		return nil, func() {}, err
	}
	if release == nil {
		release = func() {}
	}
	return filepb.NewFileClient(conn), release, nil
}

// authCtx applies the configured AuthContext to ctx.
func (c *Client) authCtx(ctx context.Context) context.Context {
	if c.auth == nil {
		return ctx
	}
	return c.auth(ctx)
}
