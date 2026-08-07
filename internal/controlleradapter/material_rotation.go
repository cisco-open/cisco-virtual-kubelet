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

package controlleradapter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"time"
)

const (
	// DefaultMaterialRotationPollInterval bounds how long a worker normally
	// takes to notice kubelet's atomic projected-volume symlink swap.
	DefaultMaterialRotationPollInterval = 5 * time.Second

	// DefaultMaxSessionLifetime is the longest an adapter may reuse a client or
	// authenticated/TLS session without rebuilding it from the mounted files.
	// Rotation events should cause an earlier rebuild; this limit is the
	// independent backstop if an edge notification is missed.
	DefaultMaxSessionLifetime = 15 * time.Minute
)

// MaterialRotationPolicy is the product-neutral credential and trust refresh
// contract supplied to every adapter.
//
// Changes is edge-triggered and coalescing. On receipt, an adapter must discard
// cached authentication and TLS state, reread all runtime material paths, and
// requeue affected work before its next external request. An adapter must also
// rebuild those sessions at least once per MaxSessionLifetime even if no event
// arrives. The channel carries no file content or Secret identity.
type MaterialRotationPolicy struct {
	Changes            <-chan struct{}
	MaxSessionLifetime time.Duration
}

// ProjectedMaterialWatcher observes Kubernetes AtomicWriter's ..data symlink
// for credential, CA, and intent-Secret volume roots. It never opens or hashes
// the projected values. The watcher is a controller-runtime Runnable and must
// be added to the worker manager so its lifecycle is tied to the process.
type ProjectedMaterialWatcher struct {
	roots        []string
	pollInterval time.Duration
	changes      chan struct{}
	revisions    map[string]projectedMaterialRevision
	started      atomic.Bool
}

type projectedMaterialRevision struct {
	present bool
	target  string
}

// NewProjectedMaterialWatcher validates and snapshots volume roots before an
// adapter is constructed. A change between construction and manager startup is
// therefore still reported by Start's immediate comparison.
func NewProjectedMaterialWatcher(pollInterval time.Duration, roots ...string) (*ProjectedMaterialWatcher, error) {
	if pollInterval <= 0 {
		return nil, fmt.Errorf("material rotation poll interval must be greater than zero")
	}
	unique := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		if root == "" {
			return nil, fmt.Errorf("material rotation root must not be empty")
		}
		cleaned := filepath.Clean(root)
		if !filepath.IsAbs(cleaned) || cleaned == string(filepath.Separator) {
			return nil, fmt.Errorf("material rotation root %q must be an absolute non-root path", root)
		}
		unique[cleaned] = struct{}{}
	}
	if len(unique) == 0 {
		return nil, fmt.Errorf("at least one material rotation root is required")
	}

	ordered := make([]string, 0, len(unique))
	for root := range unique {
		ordered = append(ordered, root)
	}
	sort.Strings(ordered)
	watcher := &ProjectedMaterialWatcher{
		roots:        ordered,
		pollInterval: pollInterval,
		changes:      make(chan struct{}, 1),
		revisions:    make(map[string]projectedMaterialRevision, len(ordered)),
	}
	if err := watcher.snapshot(false); err != nil {
		return nil, err
	}
	return watcher, nil
}

// Policy returns the adapter-facing refresh contract. Multiple adapter
// reconcilers should share one fan-out coordinator inside the adapter rather
// than compete as direct consumers of this single coalescing channel.
func (w *ProjectedMaterialWatcher) Policy(maxSessionLifetime time.Duration) (MaterialRotationPolicy, error) {
	if w == nil {
		return MaterialRotationPolicy{}, fmt.Errorf("nil projected material watcher")
	}
	if maxSessionLifetime <= 0 || maxSessionLifetime > 24*time.Hour {
		return MaterialRotationPolicy{}, fmt.Errorf("max session lifetime must be greater than zero and at most 24h")
	}
	return MaterialRotationPolicy{
		Changes:            w.changes,
		MaxSessionLifetime: maxSessionLifetime,
	}, nil
}

// Start polls until context cancellation. Filesystem inspection errors stop the
// worker manager instead of allowing a silently stale authentication session.
func (w *ProjectedMaterialWatcher) Start(ctx context.Context) error {
	if w == nil {
		return fmt.Errorf("nil projected material watcher")
	}
	if !w.started.CompareAndSwap(false, true) {
		return fmt.Errorf("projected material watcher may only be started once")
	}
	if err := w.snapshot(true); err != nil {
		return err
	}
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := w.snapshot(true); err != nil {
				return err
			}
		}
	}
}

func (w *ProjectedMaterialWatcher) snapshot(notify bool) error {
	changed := false
	for _, root := range w.roots {
		revision, err := readProjectedMaterialRevision(root)
		if err != nil {
			return fmt.Errorf("inspect projected material root %q: %w", root, err)
		}
		previous, known := w.revisions[root]
		if known && previous != revision {
			changed = true
		}
		w.revisions[root] = revision
	}
	if changed && notify {
		select {
		case w.changes <- struct{}{}:
		default:
		}
	}
	return nil
}

func readProjectedMaterialRevision(root string) (projectedMaterialRevision, error) {
	target, err := os.Readlink(filepath.Join(root, "..data"))
	if err == nil {
		return projectedMaterialRevision{present: true, target: target}, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return projectedMaterialRevision{}, nil
	}
	return projectedMaterialRevision{}, err
}
