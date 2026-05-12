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

// Package gnoi is the per-device gRPC Network Operations Interface
// client. It wraps the upstream openconfig/gnoi service stubs (OS,
// System, File, Certificate, FactoryReset) with ergonomic Go methods,
// per-service capability discovery, and HTTP-Basic auth metadata so
// the same credentials IOS-XE's gnxi-server accepts on the gNMI port
// are reused here.
//
// Connection ownership lives outside the package: callers Lease a
// *grpc.ClientConn from a devicegrpc.Pool and pass it to New. The
// client never closes the conn — callers retain the lease and Release
// it when they tear down the device worker. Unary RPCs and short
// streams use the same ClassControl-leased conn the gNMI config
// transport uses; bulk-transfer RPCs (OS.Install, File.Put / File.Get)
// MUST take a separate ClassBulkTransfer lease and pass the resulting
// conn via WithBulkConn(...), so a 500 MB image transfer cannot HOL-
// block the control conn.
//
// Capability discovery: gNMI Capabilities does not enumerate gNOI
// services, so service availability is probed lazily on first use
// with a cheap read-only RPC (OS.Verify, System.Time, File.Stat,
// Cert.GetCertificates) and cached for 24 h. Unimplemented services
// are surfaced as ErrServiceUnsupported by every method, so callers
// can fail fast with a clear reason rather than emitting opaque
// codes.Unimplemented stack traces.
package gnoi
