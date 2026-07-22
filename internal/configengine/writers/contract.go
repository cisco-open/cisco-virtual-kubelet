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

// Package writers defines the platform-neutral compiler contract between a
// canonical NetAsCode family and the device-facing transport operation graph.
//
// Platform packages own their registries, release profiles, and concrete
// encoders. Keeping those concerns out of this package prevents one platform's
// schema or version policy from leaking into another platform.
package writers

import (
	"context"

	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
)

// SectionWriter compiles and applies one canonical NetAsCode family.
// Implementations are stateless; per-device state belongs to the transport or
// to an immutable writer instance returned by a platform registry.
//
// YANGPaths is retained as the historical method name for source
// compatibility. Paths are transport-native resource selectors: IOS XE uses
// YANG paths and NX-OS uses DME selectors.
type SectionWriter interface {
	Family() string
	YANGPaths() []string
	Fetch(context.Context, transport.Interface) (any, error)
	Diff(desired, observed any) ([]transport.Op, error)
	Apply(context.Context, transport.Interface, []transport.Op) error
}

// DiffContext carries immutable reconciliation provenance to writers whose
// canonical model has different defaulting semantics from a native CVK
// source. Most writers only implement SectionWriter.Diff and remain unaware
// of this context.
type DiffContext struct {
	Platform      string
	DeviceVersion string
	ModelVersion  string
}

// ContextualDiffer is an optional extension for writers that must distinguish
// a declared external-model contract from native CVK omission semantics.
type ContextualDiffer interface {
	DiffWithContext(ctx DiffContext, desired, observed any) ([]transport.Op, error)
}

// Diff invokes a context-aware compiler when available and otherwise keeps
// the legacy SectionWriter behavior.
func Diff(ctx DiffContext, w SectionWriter, desired, observed any) ([]transport.Op, error) {
	if contextual, ok := w.(ContextualDiffer); ok {
		return contextual.DiffWithContext(ctx, desired, observed)
	}
	return w.Diff(desired, observed)
}

// OperationScope separates observation selectors from mutation targets. This
// matters for protocols such as NX-API DME, where a writer may fetch through a
// synthetic family endpoint but mutate a concrete DME distinguished name.
type OperationScope struct {
	ReadPaths     []string
	WritePrefixes []string
}

// ScopedWriter is the preferred scope contract for new platform writers.
type ScopedWriter interface {
	OperationScope() OperationScope
}

// ScopeOf returns a defensive copy of a writer's declared scope. Legacy IOS
// XE writers use YANGPaths for reads and writes; new writers should implement
// ScopedWriter whenever those address spaces differ.
func ScopeOf(w SectionWriter) OperationScope {
	if w == nil {
		return OperationScope{}
	}
	if scoped, ok := w.(ScopedWriter); ok {
		scope := scoped.OperationScope()
		scope.ReadPaths = append([]string(nil), scope.ReadPaths...)
		scope.WritePrefixes = append([]string(nil), scope.WritePrefixes...)
		return scope
	}
	paths := append([]string(nil), w.YANGPaths()...)
	return OperationScope{
		ReadPaths:     paths,
		WritePrefixes: append([]string(nil), paths...),
	}
}

// PruneCapable is implemented by families that can safely delete entries
// which are observed on the device but absent from canonical intent.
type PruneCapable interface {
	PruneDiff(desired, observed any) ([]transport.Op, error)
}

// KeyExtractable reports stable object identities for ownership and scoped
// pruning. Identity semantics are family-specific and must not be inferred by
// generic list-merging heuristics.
type KeyExtractable interface {
	KeysOf(any) []string
}

// FamilySchema is the transport-independent portion of a family contract.
// Platform packages may keep additional wire-model metadata alongside it.
type FamilySchema struct {
	Family        string
	Shape         string // "singleton" | "keyed_list"
	ManagedLeaves []string
	InnerKey      string
	KeyField      string
}

// Lookup resolves a family compiler for one detected device software release.
// Platform registries must fail closed for unsupported releases.
type Lookup func(family, deviceRelease string) SectionWriter

// VersionValidator validates a platform-native device software version before
// any writer is allowed to mutate the device.
type VersionValidator func(version string) error

// VersionErrorClassifier lets platform-neutral startup code distinguish
// unsupported and malformed release failures without importing a platform's
// concrete error types.
type VersionErrorClassifier func(error) bool
