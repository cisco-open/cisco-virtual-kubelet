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

package telemetry

import (
	"time"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

const (
	defaultInitialBackoff = time.Second
	defaultMaxBackoff     = 30 * time.Second
)

// ReconnectState is a small exponential-backoff state machine. MaxRetries=0
// means infinite retries; otherwise it limits consecutive failed attempts.
type ReconnectState struct {
	initial    time.Duration
	max        time.Duration
	maxRetries int32
	attempts   int32
	current    time.Duration
}

func NewReconnectState(cfg *configv1alpha1.ReconnectConfig) *ReconnectState {
	initial := defaultInitialBackoff
	max := defaultMaxBackoff
	var maxRetries int32
	if cfg != nil {
		if cfg.InitialBackoff.Duration > 0 {
			initial = cfg.InitialBackoff.Duration
		}
		if cfg.MaxBackoff.Duration > 0 {
			max = cfg.MaxBackoff.Duration
		}
		maxRetries = cfg.MaxRetries
	}
	if max < initial {
		max = initial
	}
	return &ReconnectState{initial: initial, max: max, maxRetries: maxRetries}
}

// Next returns the delay for the next retry and whether another retry is
// allowed.
func (b *ReconnectState) Next() (time.Duration, bool) {
	if b == nil {
		return defaultInitialBackoff, true
	}
	if b.maxRetries > 0 && b.attempts >= b.maxRetries {
		return 0, false
	}
	if b.current == 0 {
		b.current = b.initial
	} else {
		b.current *= 2
		if b.current > b.max {
			b.current = b.max
		}
	}
	b.attempts++
	return b.current, true
}

func (b *ReconnectState) Reset() {
	if b == nil {
		return
	}
	b.attempts = 0
	b.current = 0
}

func (b *ReconnectState) Current() time.Duration {
	if b == nil {
		return 0
	}
	return b.current
}

func (b *ReconnectState) Attempts() int32 {
	if b == nil {
		return 0
	}
	return b.attempts
}
