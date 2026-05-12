// Copyright 2026 Cisco Systems Inc.
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

package yang

import (
	"sync"

	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/classifier"
)

const DefaultCacheCapacity = 4096

type lookupKey struct {
	modulePrefix string
	leafPath     string
}

// Cache stores bounded YANG lookup results. On capacity hit it resets all
// entries instead of evicting individual keys.
type Cache struct {
	mu      sync.Mutex
	cap     int
	entries map[lookupKey]classifier.MetricKind
}

func NewCache(capacity int) *Cache {
	if capacity <= 0 {
		capacity = DefaultCacheCapacity
	}
	return &Cache{
		cap:     capacity,
		entries: make(map[lookupKey]classifier.MetricKind, capacity),
	}
}

func (c *Cache) Get(modulePrefix, leafPath string) (classifier.MetricKind, bool) {
	if c == nil {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	kind, ok := c.entries[lookupKey{modulePrefix: modulePrefix, leafPath: leafPath}]
	return kind, ok
}

func (c *Cache) Set(modulePrefix, leafPath string, kind classifier.MetricKind) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key := lookupKey{modulePrefix: modulePrefix, leafPath: leafPath}
	if _, ok := c.entries[key]; !ok && len(c.entries) >= c.cap {
		c.entries = make(map[lookupKey]classifier.MetricKind, c.cap)
	}
	c.entries[key] = kind
}

func (c *Cache) Size() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}
