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

package gnoi

import (
	"errors"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// capabilityTTL bounds how long a per-service "supported / unsupported"
// verdict is cached. A short TTL would re-probe too often; an
// unbounded TTL would pin a stale "unsupported" verdict against a
// device that has since been upgraded to a release that adds the
// service. 24h is a pragmatic middle.
const capabilityTTL = 24 * time.Hour

// CapabilityCache memoises per-service availability verdicts.
//
// gNMI Capabilities does not enumerate gNOI services, so each service's
// support state is discovered by issuing a cheap read-only RPC and
// observing whether the device returns codes.Unimplemented. The
// per-method helpers in this package call Observe with the RPC error
// after every invocation, so the cache stays current without explicit
// probing.
//
// Callers can also pre-seed verdicts via Pin — useful for operator
// opt-in via CiscoDevice.spec.capabilities.gnoi.services and for unit
// tests that want a known cache shape.
type CapabilityCache struct {
	now func() time.Time

	mu      sync.RWMutex
	entries map[Service]capEntry
}

type capEntry struct {
	supported bool
	pinned    bool
	checkedAt time.Time
	lastErr   error
}

// NewCapabilityCache constructs a cache. now=nil defaults to time.Now;
// tests inject a fake clock.
func NewCapabilityCache(now func() time.Time) *CapabilityCache {
	if now == nil {
		now = time.Now
	}
	return &CapabilityCache{
		now:     now,
		entries: make(map[Service]capEntry),
	}
}

// Pin records an operator-supplied verdict. Pinned verdicts survive
// the TTL and are not overwritten by Observe; callers that pin
// supported=true and then encounter codes.Unimplemented still get
// the wrapped error from method calls but the cache remains pinned.
func (c *CapabilityCache) Pin(svc Service, supported bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[svc] = capEntry{
		supported: supported,
		pinned:    true,
		checkedAt: c.now(),
	}
}

// Observe records the outcome of an RPC: nil err marks the service
// supported; codes.Unimplemented marks it unsupported. Other errors
// (Unavailable, DeadlineExceeded, etc.) are NOT cached — they're
// transient and would otherwise pin a stale verdict on a transient
// blip.
func (c *CapabilityCache) Observe(svc Service, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[svc]
	if e.pinned {
		return // operator decisions stand
	}
	switch {
	case err == nil:
		c.entries[svc] = capEntry{supported: true, checkedAt: c.now()}
	case status.Code(err) == codes.Unimplemented:
		c.entries[svc] = capEntry{supported: false, checkedAt: c.now(), lastErr: err}
	default:
		// transient — leave cache alone
	}
}

// Supported reports the current verdict for svc. When the cache has
// no record (or the record is older than capabilityTTL and not
// pinned), supported is true and known is false — the caller should
// attempt the RPC and let Observe populate the cache on the way back.
//
// known=true with supported=false is the signal to fail fast with
// ErrServiceUnsupported rather than hit the device.
func (c *CapabilityCache) Supported(svc Service) (supported, known bool, lastErr error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[svc]
	if !ok {
		return true, false, nil
	}
	if e.pinned {
		return e.supported, true, e.lastErr
	}
	if c.now().Sub(e.checkedAt) > capabilityTTL {
		return true, false, nil
	}
	return e.supported, true, e.lastErr
}

// ensureSupported short-circuits an RPC when the cache holds a
// definitive "unsupported" verdict for svc. Returns nil when the
// caller should proceed.
func (c *CapabilityCache) ensureSupported(svc Service) error {
	supported, known, lastErr := c.Supported(svc)
	if known && !supported {
		cause := lastErr
		if cause == nil {
			cause = errors.New("device reported codes.Unimplemented")
		}
		return &ErrServiceUnsupported{Service: svc, Cause: cause}
	}
	return nil
}
