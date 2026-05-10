// Copyright © 2026 Cisco Systems, Inc.
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

package main

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

// recordingHandler counts the slog records that were not dropped by the
// sampler. It satisfies slog.Handler and ignores formatting concerns; the
// only thing that matters here is whether Handle was reached.
type recordingHandler struct {
	calls atomic.Int64
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *recordingHandler) Handle(_ context.Context, _ slog.Record) error {
	h.calls.Add(1)
	return nil
}
func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

func TestSamplingSlogHandlerWarnAndErrorAlwaysPass(t *testing.T) {
	rec := &recordingHandler{}
	h := &samplingSlogHandler{next: rec, debug: false, bucket: newControllerInfoBucket(0)}
	for _, lvl := range []slog.Level{slog.LevelWarn, slog.LevelError} {
		r := slog.NewRecord(time.Now(), lvl, "msg", 0)
		if err := h.Handle(context.Background(), r); err != nil {
			t.Fatalf("Handle(%s) error: %v", lvl, err)
		}
	}
	if got := rec.calls.Load(); got != 2 {
		t.Fatalf("warn+error reached handler=%d, want 2", got)
	}
}

func TestSamplingSlogHandlerInfoRateLimit(t *testing.T) {
	rec := &recordingHandler{}
	h := &samplingSlogHandler{
		next:   rec,
		debug:  false,
		bucket: newControllerInfoBucket(3), // budget = 3 records
	}
	for i := 0; i < 10; i++ {
		r := slog.NewRecord(time.Now(), slog.LevelInfo, "msg", 0)
		if err := h.Handle(context.Background(), r); err != nil {
			t.Fatalf("Handle: %v", err)
		}
	}
	if got := rec.calls.Load(); got != 3 {
		t.Fatalf("INFO records that reached handler=%d, want 3 (budget)", got)
	}
}

func TestSamplingSlogHandlerDebugGate(t *testing.T) {
	for _, debug := range []bool{false, true} {
		rec := &recordingHandler{}
		h := &samplingSlogHandler{next: rec, debug: debug, bucket: newControllerInfoBucket(0)}
		r := slog.NewRecord(time.Now(), slog.LevelDebug, "msg", 0)
		if err := h.Handle(context.Background(), r); err != nil {
			t.Fatalf("Handle: %v", err)
		}
		want := int64(0)
		if debug {
			want = 1
		}
		if got := rec.calls.Load(); got != want {
			t.Fatalf("debug=%v reached=%d want=%d", debug, got, want)
		}
	}
}

func TestControllerInfoBucketDisabledWhenZero(t *testing.T) {
	b := newControllerInfoBucket(0)
	for i := 0; i < 1000; i++ {
		if !b.allow() {
			t.Fatalf("disabled bucket dropped a token at i=%d", i)
		}
	}
}

func TestControllerInfoBucketRefillsAfterOneSecond(t *testing.T) {
	b := newControllerInfoBucket(2)
	if !b.allow() || !b.allow() {
		t.Fatal("first two tokens should pass")
	}
	if b.allow() {
		t.Fatal("third call within window should drop")
	}
	// Force-age the lastRefill instead of sleeping a full second.
	b.mu.Lock()
	b.lastRefill = time.Now().Add(-2 * time.Second)
	b.mu.Unlock()
	if !b.allow() {
		t.Fatal("after refill, first token should pass again")
	}
}
