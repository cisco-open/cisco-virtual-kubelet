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
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProjectedMaterialWatcherSignalsAtomicRotation(t *testing.T) {
	root := t.TempDir()
	replaceDataSymlink(t, root, "..2026_08_07_00_00_00")
	watcher, err := NewProjectedMaterialWatcher(5*time.Millisecond, root)
	if err != nil {
		t.Fatalf("construct watcher: %v", err)
	}
	policy, err := watcher.Policy(DefaultMaxSessionLifetime)
	if err != nil {
		t.Fatalf("build policy: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- watcher.Start(ctx) }()
	replaceDataSymlink(t, root, "..2026_08_07_00_00_01")
	select {
	case <-policy.Changes:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for projected-volume rotation signal")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("watcher stopped with error: %v", err)
	}
}

func TestProjectedMaterialWatcherSignalsMissingToPresentAndCoalesces(t *testing.T) {
	root := filepath.Join(t.TempDir(), "optional-volume")
	watcher, err := NewProjectedMaterialWatcher(5*time.Millisecond, root)
	if err != nil {
		t.Fatalf("construct watcher: %v", err)
	}
	policy, err := watcher.Policy(time.Minute)
	if err != nil {
		t.Fatalf("build policy: %v", err)
	}
	if err := os.Mkdir(root, 0o750); err != nil {
		t.Fatalf("create volume root: %v", err)
	}

	replaceDataSymlink(t, root, "..2026_08_07_00_00_00")
	if err := watcher.snapshot(true); err != nil {
		t.Fatalf("scan missing-to-present rotation: %v", err)
	}
	// Leave that signal buffered, then scan repeated rotations. A slow adapter
	// gets one wake-up without unbounded event or Secret metadata accumulation.
	replaceDataSymlink(t, root, "..2026_08_07_00_00_01")
	if err := watcher.snapshot(true); err != nil {
		t.Fatalf("scan first buffered rotation: %v", err)
	}
	replaceDataSymlink(t, root, "..2026_08_07_00_00_02")
	if err := watcher.snapshot(true); err != nil {
		t.Fatalf("scan second buffered rotation: %v", err)
	}
	awaitRotation(t, policy.Changes)
	select {
	case <-policy.Changes:
		t.Fatal("rotation notifications were not coalesced")
	default:
	}
}

func TestProjectedMaterialWatcherCanOnlyStartOnce(t *testing.T) {
	watcher, err := NewProjectedMaterialWatcher(time.Second, t.TempDir())
	if err != nil {
		t.Fatalf("construct watcher: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := watcher.Start(ctx); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := watcher.Start(context.Background()); err == nil {
		t.Fatal("second Start was accepted")
	}
}

func TestProjectedMaterialWatcherRejectsUnsafeContract(t *testing.T) {
	if _, err := NewProjectedMaterialWatcher(0, "/safe"); err == nil {
		t.Fatal("zero poll interval accepted")
	}
	if _, err := NewProjectedMaterialWatcher(time.Second, "relative"); err == nil {
		t.Fatal("relative volume root accepted")
	}
	if _, err := NewProjectedMaterialWatcher(time.Second, string(filepath.Separator)); err == nil {
		t.Fatal("filesystem root accepted")
	}
	watcher, err := NewProjectedMaterialWatcher(time.Second, "/safe", "/safe")
	if err != nil {
		t.Fatalf("deduplicate roots: %v", err)
	}
	if _, err := watcher.Policy(0); err == nil {
		t.Fatal("zero max session lifetime accepted")
	}
	if _, err := watcher.Policy(24*time.Hour + time.Second); err == nil {
		t.Fatal("unbounded max session lifetime accepted")
	}
}

func awaitRotation(t *testing.T, changes <-chan struct{}) {
	t.Helper()
	select {
	case <-changes:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for projected-volume rotation signal")
	}
}

func replaceDataSymlink(t *testing.T, root, target string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatalf("create projected-volume root: %v", err)
	}
	temporary := filepath.Join(root, "..data-new")
	if err := os.Remove(temporary); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove stale temporary symlink: %v", err)
	}
	if err := os.Symlink(target, temporary); err != nil {
		t.Fatalf("create temporary data symlink: %v", err)
	}
	if err := os.Rename(temporary, filepath.Join(root, "..data")); err != nil {
		t.Fatalf("publish data symlink: %v", err)
	}
}
