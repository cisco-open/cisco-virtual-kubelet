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

// Package state maintains a small, MDT-fed view of device operational state.
// It is intentionally a cache, not an authority: consumers use records as
// push wakeups and read-through hints, while existing driver paths remain the
// source of truth for full Kubernetes status objects.
package state

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/mapper"
)

const (
	DefaultTTL      = 15 * time.Minute
	DefaultMaxItems = 4096

	KindApp = "app"
)

type AppEventConsumer interface {
	ObserveAppEvent(context.Context, AppEvent) bool
}

type AppEvent struct {
	Device       string
	AppID        string
	State        string
	HealthStatus string
	Deleted      bool
	LastSeen     time.Time
}

type Record struct {
	Device   string
	Kind     string
	Key      string
	Values   map[string]string
	Deleted  bool
	LastSeen time.Time
}

type Cache struct {
	mu       sync.Mutex
	ttl      time.Duration
	maxItems int
	records  map[recordKey]Record
}

type recordKey struct {
	device string
	kind   string
	key    string
}

func NewCache() *Cache {
	return &Cache{
		ttl:      DefaultTTL,
		maxItems: DefaultMaxItems,
		records:  map[recordKey]Record{},
	}
}

func (c *Cache) ApplyMappedEvents(events []mapper.MappedEvent) []AppEvent {
	apps := ExtractAppEvents(events)
	if c == nil || len(apps) == 0 {
		return apps
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.records == nil {
		c.records = map[recordKey]Record{}
	}
	c.gcLocked(now)
	for _, ev := range apps {
		k := recordKey{device: ev.Device, kind: KindApp, key: ev.AppID}
		rec := c.records[k]
		if rec.Values == nil {
			rec.Values = map[string]string{}
		}
		rec.Device = ev.Device
		rec.Kind = KindApp
		rec.Key = ev.AppID
		rec.Deleted = ev.Deleted
		rec.LastSeen = ev.LastSeen
		if rec.LastSeen.IsZero() {
			rec.LastSeen = now
		}
		if ev.State != "" {
			rec.Values["state"] = ev.State
		}
		if ev.HealthStatus != "" {
			rec.Values["health-status"] = ev.HealthStatus
		}
		c.records[k] = rec
	}
	c.trimLocked()
	return apps
}

func (c *Cache) Get(device, kind, key string) (Record, bool) {
	if c == nil {
		return Record{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	rec, ok := c.records[recordKey{device: device, kind: kind, key: key}]
	if !ok {
		return Record{}, false
	}
	if c.ttl > 0 && time.Since(rec.LastSeen) > c.ttl {
		delete(c.records, recordKey{device: device, kind: kind, key: key})
		return Record{}, false
	}
	out := rec
	if rec.Values != nil {
		out.Values = make(map[string]string, len(rec.Values))
		for k, v := range rec.Values {
			out.Values[k] = v
		}
	}
	return out, true
}

func ExtractAppEvents(events []mapper.MappedEvent) []AppEvent {
	var out []AppEvent
	for _, ev := range events {
		appID := appIDFromEvent(ev)
		if appID == "" {
			continue
		}
		leaf := pathLeaf(ev.CanonicalPath)
		if leaf == "" {
			continue
		}
		ae := AppEvent{
			Device:   attr(ev.Resource, "device"),
			AppID:    appID,
			Deleted:  isDelete(ev),
			LastSeen: ev.Timestamp,
		}
		if ae.LastSeen.IsZero() {
			ae.LastSeen = time.Now()
		}
		switch leaf {
		case "state", "app-state":
			ae.State = strings.TrimSpace(ev.Body)
		case "health-status", "status":
			ae.HealthStatus = strings.TrimSpace(ev.Body)
		default:
			continue
		}
		out = append(out, ae)
	}
	return out
}

func (c *Cache) gcLocked(now time.Time) {
	if c.ttl <= 0 {
		return
	}
	for k, rec := range c.records {
		if now.Sub(rec.LastSeen) > c.ttl {
			delete(c.records, k)
		}
	}
}

func (c *Cache) trimLocked() {
	if c.maxItems <= 0 || len(c.records) <= c.maxItems {
		return
	}
	var oldestKey recordKey
	var oldest time.Time
	for k, rec := range c.records {
		if oldest.IsZero() || rec.LastSeen.Before(oldest) {
			oldest = rec.LastSeen
			oldestKey = k
		}
	}
	delete(c.records, oldestKey)
}

func appIDFromEvent(ev mapper.MappedEvent) string {
	for _, kv := range ev.Attributes {
		switch kv.Key {
		case "name", "app-name", "app_name", "application-name", "application_name", "app-id", "app_id":
			if strings.TrimSpace(kv.Value) != "" {
				return strings.TrimSpace(kv.Value)
			}
		}
	}
	return appIDFromPath(ev.CanonicalPath)
}

func appIDFromPath(path string) string {
	const marker = "/app["
	i := strings.Index(path, marker)
	if i < 0 {
		return ""
	}
	rest := path[i+len(marker):]
	end := strings.IndexByte(rest, ']')
	if end < 0 {
		return ""
	}
	keys := strings.Split(rest[:end], "][")
	for _, key := range keys {
		name, value, ok := strings.Cut(key, "=")
		if !ok {
			continue
		}
		switch name {
		case "name", "app-name", "application-name", "app-id":
			return value
		}
	}
	return ""
}

func pathLeaf(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		path = path[i+1:]
	}
	if i := strings.IndexByte(path, '['); i >= 0 {
		path = path[:i]
	}
	return strings.TrimSpace(path)
}

func isDelete(ev mapper.MappedEvent) bool {
	for _, kv := range ev.Attributes {
		if kv.Key == "cisco.gnmi.event" && kv.Value == "delete" {
			return true
		}
	}
	return false
}

func attr(kvs []mapper.KeyValue, key string) string {
	for _, kv := range kvs {
		if kv.Key == key {
			return kv.Value
		}
	}
	return ""
}
