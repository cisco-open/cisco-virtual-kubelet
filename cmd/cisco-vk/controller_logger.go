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
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/emit"
)

// controllerDebugLogging resolves the controller's effective log level from
// flag > LOG_LEVEL env > default "info" and reports whether DEBUG records
// should bypass the INFO rate limit.
func controllerDebugLogging() (bool, error) {
	lvl := strings.ToLower(strings.TrimSpace(logLevel))
	if lvl == "" {
		lvl = strings.ToLower(strings.TrimSpace(os.Getenv("LOG_LEVEL")))
	}
	switch lvl {
	case "", "info", "warn", "warning", "error":
		return false, nil
	case "debug":
		return true, nil
	default:
		return false, fmt.Errorf("invalid log level %q (want debug|info|warn|error)", lvl)
	}
}

// newControllerSlogHandler returns the base slog handler used by
// controller-runtime. When OTLP is unset the controller must still surface logs
// locally because an OTel bridge without a provider drops records.
func newControllerSlogHandler(loggerProvider otellog.LoggerProvider) (slog.Handler, bool) {
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		return slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level:     slog.LevelInfo,
			AddSource: false,
		}), true
	}
	opts := []otelslog.Option{}
	if loggerProvider != nil {
		opts = append(opts, otelslog.WithLoggerProvider(loggerProvider))
	}
	return otelslog.NewHandler("cisco-vk-controller", opts...), false
}

// newControllerRuntimeLogger returns a logr.Logger that writes through handler,
// sampled so a busy reconcile loop cannot flood Loki:
//   - WARN+ records always pass through.
//   - DEBUG records always pass through if the controller is in debug mode
//     (otherwise dropped silently — caller is opting in by setting the level).
//   - INFO records are rate-limited to infoBudgetPerSec; over-budget records
//     drop silently and bump cisco_vk_signal_budget_dropped_total
//     {signal=logs, reason=rate_limit_controller_info, device=""}.
func newControllerRuntimeLogger(handler slog.Handler, infoBudgetPerSec int, debug bool) logr.Logger {
	if handler == nil {
		handler, _ = newControllerSlogHandler(nil)
	}
	sampling := &samplingSlogHandler{
		next:   handler,
		debug:  debug,
		bucket: newControllerInfoBucket(infoBudgetPerSec),
	}
	return logr.FromSlogHandler(sampling)
}

// samplingSlogHandler wraps another slog.Handler with the WARN+ / DEBUG /
// INFO-rate-limit policy described on newControllerRuntimeLogger.
type samplingSlogHandler struct {
	next   slog.Handler
	debug  bool
	bucket *controllerInfoBucket
}

func (h *samplingSlogHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	if lvl >= slog.LevelWarn {
		return h.next.Enabled(ctx, lvl)
	}
	if lvl == slog.LevelInfo {
		return h.next.Enabled(ctx, lvl)
	}
	if h.debug {
		return h.next.Enabled(ctx, lvl)
	}
	return false
}

func (h *samplingSlogHandler) Handle(ctx context.Context, r slog.Record) error {
	switch {
	case r.Level >= slog.LevelWarn:
		return h.next.Handle(ctx, r)
	case r.Level == slog.LevelInfo:
		if h.bucket.allow() {
			return h.next.Handle(ctx, r)
		}
		emit.RecordBudgetDropped(ctx, "logs", "rate_limit_controller_info", "")
		return nil
	default:
		if h.debug {
			return h.next.Handle(ctx, r)
		}
		return nil
	}
}

func (h *samplingSlogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &samplingSlogHandler{
		next:   h.next.WithAttrs(attrs),
		debug:  h.debug,
		bucket: h.bucket,
	}
}

func (h *samplingSlogHandler) WithGroup(name string) slog.Handler {
	return &samplingSlogHandler{
		next:   h.next.WithGroup(name),
		debug:  h.debug,
		bucket: h.bucket,
	}
}

// controllerInfoBucket is a token bucket of capacity = infoBudgetPerSec,
// refilled to capacity once per second. A zero or negative budget disables
// rate limiting (every Allow returns true).
type controllerInfoBucket struct {
	mu         sync.Mutex
	capacity   int
	tokens     int
	lastRefill time.Time
	disabled   bool
}

func newControllerInfoBucket(perSec int) *controllerInfoBucket {
	if perSec <= 0 {
		return &controllerInfoBucket{disabled: true}
	}
	return &controllerInfoBucket{
		capacity:   perSec,
		tokens:     perSec,
		lastRefill: time.Now(),
	}
}

func (b *controllerInfoBucket) allow() bool {
	if b == nil || b.disabled {
		return true
	}
	now := time.Now()
	b.mu.Lock()
	defer b.mu.Unlock()
	if now.Sub(b.lastRefill) >= time.Second {
		b.tokens = b.capacity
		b.lastRefill = now
	}
	if b.tokens <= 0 {
		return false
	}
	b.tokens--
	return true
}
