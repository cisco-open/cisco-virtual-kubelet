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

package transport

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

type classifiedRetryError struct {
	retryable bool
	message   string
}

func (e *classifiedRetryError) Error() string   { return e.message }
func (e *classifiedRetryError) Retryable() bool { return e.retryable }

func TestIsTransientHonorsWrappedRetryableClassification(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		retryable bool
		message   string
	}{
		{name: "retryable", retryable: true, message: "HTTP 503"},
		{name: "permanent overrides marker", retryable: false, message: "connection reset validation failure"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := fmt.Errorf("request context: %w", &classifiedRetryError{
				retryable: tt.retryable,
				message:   tt.message,
			})
			if got := IsTransient(err); got != tt.retryable {
				t.Fatalf("IsTransient()=%v, want %v", got, tt.retryable)
			}
		})
	}
}

func TestRetryIdempotentUsesTypedClassification(t *testing.T) {
	t.Parallel()
	calls := 0
	err := RetryIdempotent(context.Background(), RetryPolicy{
		MaxAttempts:    3,
		Initial:        time.Nanosecond,
		Cap:            time.Nanosecond,
		JitterFraction: 0.01,
	}, func() error {
		calls++
		if calls < 3 {
			return fmt.Errorf("read failed: %w", &classifiedRetryError{
				retryable: true,
				message:   "service unavailable",
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RetryIdempotent: %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls=%d, want 3", calls)
	}

	calls = 0
	permanent := &classifiedRetryError{message: "validation failed"}
	err = RetryIdempotent(context.Background(), RetryPolicy{
		MaxAttempts: 3,
		Initial:     time.Nanosecond,
	}, func() error {
		calls++
		return permanent
	})
	if !errors.Is(err, permanent) {
		t.Fatalf("RetryIdempotent err=%v, want original permanent error", err)
	}
	if calls != 1 {
		t.Fatalf("permanent calls=%d, want 1", calls)
	}
}
