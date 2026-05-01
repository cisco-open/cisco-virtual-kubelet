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
	"testing"
	"time"
)

func TestIsTransient(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err  error
		want bool
		name string
	}{
		{nil, false, "nil"},
		{errors.New("connection refused"), true, "connection refused"},
		{errors.New("read tcp: i/o timeout"), true, "i/o timeout"},
		{errors.New("EOF"), true, "EOF"},
		{errors.New("rpc-error: unknown-element bar"), false, "rpc-error not transient"},
		{errors.New("access-denied"), false, "access-denied not transient"},
		{errors.New("tls: handshake failure"), true, "tls handshake retry-eligible"},
		{errors.New("connection reset by peer"), true, "connection reset"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsTransient(tc.err); got != tc.want {
				t.Errorf("IsTransient(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestRetryIdempotent_SucceedsAfterTransient(t *testing.T) {
	t.Parallel()
	calls := 0
	err := RetryIdempotent(context.Background(), RetryPolicy{
		MaxAttempts: 3,
		Initial:     1 * time.Millisecond,
	}, func() error {
		calls++
		if calls < 2 {
			return errors.New("connection refused")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected nil after retry, got %v", err)
	}
	if calls != 2 {
		t.Errorf("got %d calls, want 2", calls)
	}
}

func TestRetryIdempotent_PermanentNoRetry(t *testing.T) {
	t.Parallel()
	calls := 0
	want := errors.New("rpc-error: unknown-element x")
	err := RetryIdempotent(context.Background(), RetryPolicy{
		MaxAttempts: 5,
		Initial:     1 * time.Millisecond,
	}, func() error {
		calls++
		return want
	})
	if err != want {
		t.Errorf("got err=%v, want %v", err, want)
	}
	if calls != 1 {
		t.Errorf("got %d calls, want 1 (permanent error should not retry)", calls)
	}
}

func TestRetryIdempotent_AllAttemptsFail(t *testing.T) {
	t.Parallel()
	calls := 0
	want := errors.New("connection refused")
	err := RetryIdempotent(context.Background(), RetryPolicy{
		MaxAttempts: 3,
		Initial:     1 * time.Millisecond,
	}, func() error {
		calls++
		return want
	})
	if err != want {
		t.Errorf("got err=%v, want %v", err, want)
	}
	if calls != 3 {
		t.Errorf("got %d calls, want 3", calls)
	}
}

func TestRetryIdempotent_ContextCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancelled before any attempts
	calls := 0
	err := RetryIdempotent(ctx, RetryPolicy{
		MaxAttempts: 3,
		Initial:     50 * time.Millisecond,
	}, func() error {
		calls++
		return errors.New("connection refused")
	})
	if err == nil {
		t.Fatal("expected error from cancelled ctx")
	}
	// First attempt always runs; cancellation aborts before retry #2.
	if calls > 1 {
		t.Errorf("got %d calls, want 1 (cancellation should stop retries)", calls)
	}
}

func TestRetryPolicy_Defaults(t *testing.T) {
	t.Parallel()
	p := RetryPolicy{}.normalised()
	if p.MaxAttempts != 3 {
		t.Errorf("MaxAttempts default = %d, want 3", p.MaxAttempts)
	}
	if p.Initial != 200*time.Millisecond {
		t.Errorf("Initial default = %v, want 200ms", p.Initial)
	}
	if p.Growth != 2 {
		t.Errorf("Growth default = %v, want 2", p.Growth)
	}
	if p.Cap != 2*time.Second {
		t.Errorf("Cap default = %v, want 2s", p.Cap)
	}
	if p.JitterFraction != 0.2 {
		t.Errorf("JitterFraction default = %v, want 0.2", p.JitterFraction)
	}
}
