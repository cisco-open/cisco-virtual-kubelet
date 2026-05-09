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

package mapper

import (
	"sync"
	"time"
)

type StartTimestampCache struct {
	mu      sync.Mutex
	cap     int
	epoch   uint64
	started map[startKey]time.Time
}

type startKey struct {
	epoch  uint64
	series string
}

func NewStartTimestampCache(capacity int) *StartTimestampCache {
	if capacity <= 0 {
		capacity = DefaultMaxSeriesPerSubscription
	}
	return &StartTimestampCache{
		cap:     capacity,
		started: make(map[startKey]time.Time, capacity),
	}
}

func (c *StartTimestampCache) Start(epoch uint64, seriesKey string, receiveTime time.Time) time.Time {
	if c == nil || seriesKey == "" {
		return time.Time{}
	}
	if receiveTime.IsZero() {
		receiveTime = time.Now()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.epoch != epoch {
		for k := range c.started {
			if k.epoch != epoch {
				delete(c.started, k)
			}
		}
		c.epoch = epoch
	}
	key := startKey{epoch: epoch, series: seriesKey}
	if started, ok := c.started[key]; ok {
		return started
	}
	if len(c.started) >= c.cap {
		for k := range c.started {
			delete(c.started, k)
			break
		}
	}
	c.started[key] = receiveTime
	return receiveTime
}
