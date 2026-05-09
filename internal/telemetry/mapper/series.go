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

package mapper

import (
	"strings"
	"sync"
)

const DefaultMaxSeriesPerSubscription = 10000

type SeriesKeyCache struct {
	mu     sync.Mutex
	cap    int
	series map[string]struct{}
}

func NewSeriesKeyCache(capacity int) *SeriesKeyCache {
	if capacity <= 0 {
		capacity = DefaultMaxSeriesPerSubscription
	}
	return &SeriesKeyCache{
		cap:    capacity,
		series: make(map[string]struct{}, capacity),
	}
}

func (c *SeriesKeyCache) Check(key string) (known bool, accepted bool, size int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.series[key]; ok {
		return true, true, len(c.series)
	}
	if len(c.series) >= c.cap {
		return false, false, len(c.series)
	}
	c.series[key] = struct{}{}
	return false, true, len(c.series)
}

func (c *SeriesKeyCache) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.series)
}

func (c *SeriesKeyCache) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.series = make(map[string]struct{}, c.cap)
}

func BuildSeriesKey(subscription, canonicalPath string, keys []KeyValue) string {
	var b strings.Builder
	b.WriteString(subscription)
	b.WriteByte('\x00')
	b.WriteString(canonicalPath)
	for _, kv := range keys {
		b.WriteByte('\x00')
		b.WriteString(kv.Key)
		b.WriteByte('=')
		b.WriteString(kv.Value)
	}
	return b.String()
}
