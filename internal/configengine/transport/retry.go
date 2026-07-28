// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package transport

import (
	"context"
	"errors"
	"math/rand"
	"net"
	"strings"
	"time"
)

// RetryPolicy controls the truncated exponential backoff applied to
// idempotent transport calls (Fetch, runningVerify re-Fetch, NETCONF
// hello). Apply / Mutate calls are NOT retried automatically because
// non-transactional transports (RESTCONF) cannot guarantee
// idempotency on partial application — those failures bubble up to
// the controller-runtime requeue path instead.
//
// Defaults (when RetryPolicy is zero-valued): 3 attempts, 200ms
// initial, 2× growth, 2s cap, ±20% jitter. Operators tune via
// FactoryOptions.RetryPolicy.
//
// Wave 10 release-readiness fix (2026-04-28).
type RetryPolicy struct {
	// MaxAttempts including the initial try. Zero or negative ⇒ 3.
	MaxAttempts int
	// Initial wait before retry #1. Zero ⇒ 200ms.
	Initial time.Duration
	// Growth multiplier per attempt. Zero or <1 ⇒ 2.
	Growth float64
	// Cap is the upper bound on per-retry wait. Zero ⇒ 2s.
	Cap time.Duration
	// JitterFraction in [0, 1). Zero ⇒ 0.2 (±20%).
	JitterFraction float64
}

func (p RetryPolicy) normalised() RetryPolicy {
	out := p
	if out.MaxAttempts <= 0 {
		out.MaxAttempts = 3
	}
	if out.Initial <= 0 {
		out.Initial = 200 * time.Millisecond
	}
	if out.Growth < 1 {
		out.Growth = 2
	}
	if out.Cap <= 0 {
		out.Cap = 2 * time.Second
	}
	if out.JitterFraction <= 0 || out.JitterFraction >= 1 {
		out.JitterFraction = 0.2
	}
	return out
}

// IsTransient reports whether err is a transport-level transient failure that
// retrying might help. Typed protocol errors can opt in via Retryable() bool
// (for example HTTP 429/5xx); otherwise the fallback covers TCP-level
// connection reset/refused/timeout failures. Permanent application errors
// (rpc-error, unknown-element, access-denied) are not retried.
//
// The matcher is conservative: anything not in the known-transient
// set returns false, so unrecognised errors fall through to the
// caller without retry. Better to under-retry than to mask a real
// failure under transient-style backoff.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	// Typed protocol errors take precedence over string heuristics. In
	// particular, DME and REST errors can explicitly distinguish retryable
	// 429/5xx responses from permanent 4xx validation failures while still
	// being wrapped with safe request context.
	var classified interface{ Retryable() bool }
	if errors.As(err, &classified) {
		return classified.Retryable()
	}
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return true
	}
	msg := err.Error()
	for _, marker := range transientMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// transientMarkers lists substrings that mark a TCP-level transient
// error in the surrounding net / crypto / grpc stacks. Conservative —
// adding new entries is preferable to false positives.
var transientMarkers = []string{
	"connection refused",
	"connection reset",
	"i/o timeout",
	"no route to host",
	"EOF",
	"broken pipe",
	"tls: handshake failure", // device under load; retry-eligible
}

// RetryIdempotent runs `fn` with truncated exponential backoff per
// `policy`, retrying only on transient errors per IsTransient. Returns
// the last error if all attempts fail; ctx cancellation aborts
// immediately.
//
// Callers MUST ONLY pass idempotent operations (read-only fetches,
// re-fetches, schema queries). For mutations, use the engine's
// per-tick retry semantics instead.
func RetryIdempotent(ctx context.Context, policy RetryPolicy, fn func() error) error {
	p := policy.normalised()
	wait := p.Initial
	var lastErr error
	for attempt := 0; attempt < p.MaxAttempts; attempt++ {
		if err := fn(); err != nil {
			lastErr = err
			if !IsTransient(err) {
				return err // permanent — don't retry
			}
			if attempt == p.MaxAttempts-1 {
				return err
			}
			// Compute next wait with jitter.
			delay := jitter(wait, p.JitterFraction)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
			wait = capDuration(time.Duration(float64(wait)*p.Growth), p.Cap)
			continue
		}
		return nil
	}
	return lastErr
}

func capDuration(d, max time.Duration) time.Duration {
	if d > max {
		return max
	}
	return d
}

// jitter applies ±jitterFraction multiplicative noise to d. Uses
// math/rand (no need for crypto/rand here — jitter is anti-thundering-
// herd, not security-sensitive).
func jitter(d time.Duration, fraction float64) time.Duration {
	if fraction == 0 {
		return d
	}
	delta := float64(d) * fraction
	off := (rand.Float64()*2 - 1) * delta // [-delta, +delta]
	return time.Duration(float64(d) + off)
}
