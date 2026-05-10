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

// Package correlation handles trace-context handoff between Kubernetes objects
// and telemetry consumers. It intentionally follows W3C Trace Context's single
// `traceparent` carrier so downstream consumers can parse it without CVK-only
// conventions.
package correlation

import (
	"container/list"
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	TraceparentAnnotation       = "cisco.vk/traceparent"
	TraceWindowEndAnnotation    = "cisco.vk/trace-window-end"
	LastTraceIDAnnotation       = "cisco.vk/last-trace-id"
	LastTraceDurationAnnotation = "cisco.vk/last-trace-duration"
	LastErrorTraceIDAnnotation  = "cisco.vk/last-error-trace-id"

	DefaultTTL          = 15 * time.Minute
	DefaultCap          = 4096
	DefaultParentWindow = 30 * time.Second
)

type Relationship string

const (
	RelationshipParent Relationship = "parent"
	RelationshipLink   Relationship = "link"
)

type Key struct {
	Device string
	AppID  string
}

type Cache struct {
	mu           sync.Mutex
	ttl          time.Duration
	parentWindow time.Duration
	cap          int
	items        map[Key]*list.Element
	order        *list.List
	nowFunc      func() time.Time
}

type entry struct {
	key       Key
	sc        trace.SpanContext
	createdAt time.Time
	expiresAt time.Time
}

func NewCache(ttl time.Duration, cap int, parentWindow time.Duration) *Cache {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if parentWindow <= 0 {
		parentWindow = DefaultParentWindow
	}
	if cap <= 0 {
		cap = DefaultCap
	}
	return &Cache{
		ttl:          ttl,
		parentWindow: parentWindow,
		cap:          cap,
		items:        map[Key]*list.Element{},
		order:        list.New(),
		nowFunc:      time.Now,
	}
}

func (c *Cache) Upsert(device, appID string, sc trace.SpanContext) {
	if c == nil || device == "" || appID == "" || !sc.IsValid() {
		return
	}
	key := Key{Device: device, AppID: appID}
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gcLocked(now)
	if elem := c.items[key]; elem != nil {
		en := elem.Value.(*entry)
		en.sc = sc
		en.createdAt = now
		en.expiresAt = now.Add(c.ttl)
		c.order.MoveToFront(elem)
		return
	}
	elem := c.order.PushFront(&entry{key: key, sc: sc, createdAt: now, expiresAt: now.Add(c.ttl)})
	c.items[key] = elem
	for len(c.items) > c.cap {
		c.removeOldestLocked()
	}
}

func (c *Cache) Get(device, appID string) (trace.SpanContext, time.Duration, bool) {
	if c == nil || device == "" || appID == "" {
		return trace.SpanContext{}, 0, false
	}
	key := Key{Device: device, AppID: appID}
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	elem := c.items[key]
	if elem == nil {
		return trace.SpanContext{}, 0, false
	}
	en := elem.Value.(*entry)
	if !now.Before(en.expiresAt) {
		c.removeLocked(elem)
		return trace.SpanContext{}, 0, false
	}
	c.order.MoveToFront(elem)
	age := now.Sub(en.createdAt)
	if age < 0 {
		age = 0
	}
	return en.sc, age, true
}

func (c *Cache) RelationshipForAge(age time.Duration) Relationship {
	if c == nil {
		return RelationshipLink
	}
	if age <= c.parentWindow {
		return RelationshipParent
	}
	return RelationshipLink
}

func FormatTraceparent(sc trace.SpanContext) string {
	if !sc.IsValid() {
		return ""
	}
	return fmt.Sprintf("00-%s-%s-%02x", sc.TraceID().String(), sc.SpanID().String(), byte(sc.TraceFlags()))
}

func ParseTraceparent(raw string) (trace.SpanContext, error) {
	parts := strings.Split(strings.TrimSpace(raw), "-")
	if len(parts) != 4 {
		return trace.SpanContext{}, fmt.Errorf("traceparent must have 4 hyphen-separated fields")
	}
	if parts[0] != "00" {
		return trace.SpanContext{}, fmt.Errorf("unsupported traceparent version %q", parts[0])
	}
	traceID, err := trace.TraceIDFromHex(parts[1])
	if err != nil {
		return trace.SpanContext{}, fmt.Errorf("trace id: %w", err)
	}
	spanID, err := trace.SpanIDFromHex(parts[2])
	if err != nil {
		return trace.SpanContext{}, fmt.Errorf("span id: %w", err)
	}
	flags, err := strconv.ParseUint(parts[3], 16, 8)
	if err != nil {
		return trace.SpanContext{}, fmt.Errorf("trace flags: %w", err)
	}
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.TraceFlags(flags),
		Remote:     true,
	})
	if !sc.IsValid() {
		return trace.SpanContext{}, fmt.Errorf("invalid traceparent")
	}
	return sc, nil
}

func SpanContextFromAnnotations(annotations map[string]string, now time.Time) (trace.SpanContext, bool) {
	if len(annotations) == 0 {
		return trace.SpanContext{}, false
	}
	windowEnd, err := time.Parse(time.RFC3339, annotations[TraceWindowEndAnnotation])
	if err != nil || (!windowEnd.IsZero() && now.After(windowEnd)) {
		return trace.SpanContext{}, false
	}
	sc, err := ParseTraceparent(annotations[TraceparentAnnotation])
	if err != nil {
		return trace.SpanContext{}, false
	}
	return sc, true
}

func WithSpanContext(parent context.Context, sc trace.SpanContext) context.Context {
	if parent == nil {
		parent = context.Background()
	}
	if !sc.IsValid() {
		return parent
	}
	return trace.ContextWithRemoteSpanContext(parent, sc)
}

type spanLinksKey struct{}

func WithSpanLink(parent context.Context, sc trace.SpanContext) context.Context {
	if parent == nil {
		parent = context.Background()
	}
	if !sc.IsValid() {
		return parent
	}
	link := trace.Link{
		SpanContext: sc,
		Attributes: []attribute.KeyValue{
			attribute.String("cvk.cause.trace_id", sc.TraceID().String()),
			attribute.String("cvk.cause.span_id", sc.SpanID().String()),
			attribute.String("cvk.correlation.source", "cache"),
			attribute.String("cvk.correlation.type", "span_link"),
		},
	}
	links := SpanLinksFromContext(parent)
	links = append(links, link)
	return context.WithValue(parent, spanLinksKey{}, links)
}

func SpanLinksFromContext(ctx context.Context) []trace.Link {
	if ctx == nil {
		return nil
	}
	links, _ := ctx.Value(spanLinksKey{}).([]trace.Link)
	if len(links) == 0 {
		return nil
	}
	return append([]trace.Link(nil), links...)
}

func (c *Cache) now() time.Time {
	if c.nowFunc != nil {
		return c.nowFunc()
	}
	return time.Now()
}

func (c *Cache) gcLocked(now time.Time) {
	for elem := c.order.Back(); elem != nil; {
		prev := elem.Prev()
		en := elem.Value.(*entry)
		if now.Before(en.expiresAt) {
			break
		}
		c.removeLocked(elem)
		elem = prev
	}
}

func (c *Cache) removeOldestLocked() {
	if elem := c.order.Back(); elem != nil {
		c.removeLocked(elem)
	}
}

func (c *Cache) removeLocked(elem *list.Element) {
	en := elem.Value.(*entry)
	delete(c.items, en.key)
	c.order.Remove(elem)
}
