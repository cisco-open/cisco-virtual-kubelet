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

// Package devicecoordination defines the lease namespace shared by
// independently reconciled, device-mutating workflows.
package devicecoordination

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// MutationGuard protects one complete device write lifecycle. Its completion
// callback receives the outcome so ambiguous failures retain their fence.
type MutationGuard = func(context.Context) (context.Context, func(error), error)

// ErrMutationIncomplete is the conservative deferred outcome until a guarded
// function returns normally. A panic must not release uncertain device work.
var ErrMutationIncomplete = errors.New("device mutation did not return normally")

type mutationGuardKey struct{}

// WithMutationGuard makes a guard available during driver construction, before
// optional device bootstrap writes (such as IOx sign-verification settings).
func WithMutationGuard(ctx context.Context, guard MutationGuard) context.Context {
	return context.WithValue(ctx, mutationGuardKey{}, guard)
}

func MutationGuardFromContext(ctx context.Context) MutationGuard {
	guard, _ := ctx.Value(mutationGuardKey{}).(MutationGuard)
	return guard
}

// MutationLeaseFamily serializes disruptive mutations against one device.
// It is intentionally platform-neutral so future IOS XR and NX-OS workflows
// participate in the same safety boundary without importing IOS XE code.
const MutationLeaseFamily = "device-disruptive-mutation"

// RetainLeaseAnnotation asks FamilyLeaser.Release to clear ownership rather
// than delete the object. Managed mode uses a pre-created, admission-protected
// Lease so its identity and per-device authorization boundary cannot vanish
// between operations.
const RetainLeaseAnnotation = "topology.cisco.vk/retain-lease"

// DeviceKey returns a short, label-safe key for a namespaced device. The
// namespace is part of the digest so equal device names in different tenant
// namespaces never share a lease accidentally.
func DeviceKey(namespace, name string) string {
	sum := sha256.Sum256([]byte(namespace + "\x00" + name))
	return "device-" + hex.EncodeToString(sum[:8])
}

// HolderIdentity returns a bounded identity for a single immutable operation.
// Kubernetes object UIDs are preferred because a deleted and recreated object
// with the same name must not inherit the old object's lease. Tests and local
// callers without a UID receive a stable digest of namespace/name instead.
func HolderIdentity(workflow, namespace, name, uid string) string {
	if uid != "" {
		return workflow + "/" + uid
	}
	sum := sha256.Sum256([]byte(namespace + "\x00" + name))
	return workflow + "/" + hex.EncodeToString(sum[:16])
}
