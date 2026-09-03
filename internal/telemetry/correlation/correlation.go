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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	TraceparentAnnotation         = "cisco.vk/traceparent"
	TracestateAnnotation          = "cisco.vk/tracestate"
	TraceWindowEndAnnotation      = "cisco.vk/trace-window-end"
	LifecycleIDAnnotation         = "cisco.vk/lifecycle-id"
	UpstreamTraceparentAnnotation = "cisco.vk/upstream-traceparent"
	LastTraceIDAnnotation         = "cisco.vk/last-trace-id"
	LastTraceDurationAnnotation   = "cisco.vk/last-trace-duration"
	LastErrorTraceIDAnnotation    = "cisco.vk/last-error-trace-id"

	DefaultTTL          = 15 * time.Minute
	DefaultCap          = 4096
	DefaultParentWindow = 30 * time.Second
	// MaxTraceWindow is the longest time a Kubernetes annotation may force
	// direct remote parenting from the moment it is consumed. A farther-future
	// deadline is downgraded to a span link, preventing a malicious or mistaken
	// object from keeping an operational trace open indefinitely.
	MaxTraceWindow = 15 * time.Minute

	// W3C Trace Context limits tracestate to 512 characters. LifecycleID is a
	// CVK correlation key rather than arbitrary baggage: keeping it short and
	// character-restricted bounds user-controlled data propagated to spans.
	// Syntax validation cannot establish sensitivity; producers must never put
	// credentials or other secrets in this field.
	MaxTracestateLength  = 512
	MaxLifecycleIDLength = 128
)

const LifecycleIDAttribute = "cvk.lifecycle.id"

var lifecycleIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+\-]{0,127}$`)

type Relationship string

const (
	RelationshipParent Relationship = "parent"
	RelationshipLink   Relationship = "link"
)

// AnnotationContext describes the validated correlation metadata extracted
// from a Kubernetes object's annotations. Primary is the direct Argo context;
// Upstream is the optional GitHub context that is always represented as a
// span link because the GitHub-to-Argo boundary is asynchronous.
type AnnotationContext struct {
	Primary      trace.SpanContext
	Upstream     trace.SpanContext
	Relationship Relationship
	LifecycleID  string
}

func (c AnnotationContext) HasPrimary() bool  { return c.Primary.IsValid() }
func (c AnnotationContext) HasUpstream() bool { return c.Upstream.IsValid() }

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
	key         Key
	sc          trace.SpanContext
	lifecycleID string
	createdAt   time.Time
	expiresAt   time.Time
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

func (c *Cache) Upsert(device, appID string, sc trace.SpanContext, lifecycleIDs ...string) {
	if c == nil || device == "" || appID == "" || !sc.IsValid() {
		return
	}
	lifecycleID := ""
	if len(lifecycleIDs) > 0 && ValidLifecycleID(lifecycleIDs[0]) {
		lifecycleID = lifecycleIDs[0]
	}
	key := Key{Device: device, AppID: appID}
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gcLocked(now)
	if elem := c.items[key]; elem != nil {
		en := elem.Value.(*entry)
		en.sc = sc
		en.lifecycleID = lifecycleID
		en.createdAt = now
		en.expiresAt = now.Add(c.ttl)
		c.order.MoveToFront(elem)
		return
	}
	elem := c.order.PushFront(&entry{key: key, sc: sc, lifecycleID: lifecycleID, createdAt: now, expiresAt: now.Add(c.ttl)})
	c.items[key] = elem
	for len(c.items) > c.cap {
		c.removeOldestLocked()
	}
}

func (c *Cache) Get(device, appID string) (trace.SpanContext, time.Duration, bool) {
	sc, _, age, ok := c.GetWithLifecycle(device, appID)
	return sc, age, ok
}

// GetWithLifecycle returns the cached span context and stable lifecycle ID.
// Lifecycle identity is stored alongside the span rather than reconstructed
// from the app ID so delayed MDT transitions retain the CI/CD search key.
func (c *Cache) GetWithLifecycle(device, appID string) (trace.SpanContext, string, time.Duration, bool) {
	if c == nil || device == "" || appID == "" {
		return trace.SpanContext{}, "", 0, false
	}
	key := Key{Device: device, AppID: appID}
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	elem := c.items[key]
	if elem == nil {
		return trace.SpanContext{}, "", 0, false
	}
	en := elem.Value.(*entry)
	if !now.Before(en.expiresAt) {
		c.removeLocked(elem)
		return trace.SpanContext{}, "", 0, false
	}
	c.order.MoveToFront(elem)
	age := now.Sub(en.createdAt)
	if age < 0 {
		age = 0
	}
	return en.sc, en.lifecycleID, age, true
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
	// Version 00 has a fixed 55-character representation. Be deliberately
	// stricter than strconv/OTel's hex helpers: W3C requires lower-case hex and
	// fixed field widths, and accepting an abbreviated value makes Kubernetes
	// annotations an ambiguous trust boundary.
	if len(raw) != 55 || raw != strings.TrimSpace(raw) {
		return trace.SpanContext{}, fmt.Errorf("traceparent version 00 must be exactly 55 characters")
	}
	for i, r := range raw {
		if i == 2 || i == 35 || i == 52 {
			if r != '-' {
				return trace.SpanContext{}, fmt.Errorf("traceparent has an invalid field separator")
			}
			continue
		}
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return trace.SpanContext{}, fmt.Errorf("traceparent contains non-lowercase-hex data")
		}
	}
	parts := strings.Split(raw, "-")
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
	if flags&^uint64(trace.FlagsSampled|trace.FlagsRandom) != 0 {
		return trace.SpanContext{}, fmt.Errorf("trace flags contain unsupported version-00 bits")
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

func parseAnnotatedSpanContext(traceparent, tracestate string) (trace.SpanContext, error) {
	sc, err := ParseTraceparent(traceparent)
	if err != nil {
		return trace.SpanContext{}, err
	}
	if tracestate == "" {
		return sc, nil
	}
	if len(tracestate) > MaxTracestateLength || tracestate != strings.TrimSpace(tracestate) {
		return trace.SpanContext{}, fmt.Errorf("tracestate exceeds W3C bounds or has surrounding whitespace")
	}
	ts, err := trace.ParseTraceState(tracestate)
	if err != nil {
		return trace.SpanContext{}, fmt.Errorf("tracestate: %w", err)
	}
	return sc.WithTraceState(ts), nil
}

// ApplyAnnotations validates and attaches a Kubernetes correlation carrier to
// parent. Before trace-window-end, the Argo context is the direct remote
// parent. Once that bounded window has elapsed it becomes a span link, which
// preserves causality across retries without creating an hours-long trace.
// The optional GitHub upstream context is always a link. Invalid fields are
// ignored without ever echoing their values to logs or span attributes.
func ApplyAnnotations(parent context.Context, annotations map[string]string, now time.Time) (context.Context, AnnotationContext) {
	if parent == nil {
		parent = context.Background()
	}
	result := AnnotationContext{Relationship: RelationshipLink}
	if len(annotations) == 0 {
		return parent, result
	}

	if lifecycleID := annotations[LifecycleIDAnnotation]; ValidLifecycleID(lifecycleID) {
		result.LifecycleID = lifecycleID
		parent = WithLifecycleID(parent, lifecycleID)
	}

	primary, primaryErr := parseAnnotatedSpanContext(
		annotations[TraceparentAnnotation],
		annotations[TracestateAnnotation],
	)
	windowEnd, windowErr := time.Parse(time.RFC3339, annotations[TraceWindowEndAnnotation])
	if primaryErr == nil && windowErr == nil {
		result.Primary = primary
		active := trace.SpanContextFromContext(parent)
		fresh := !now.After(windowEnd) && !windowEnd.After(now.Add(MaxTraceWindow))
		if fresh && !active.IsValid() && !isDetachedContext(parent) {
			result.Relationship = RelationshipParent
			parent = WithSpanContext(parent, primary)
		} else if !active.IsValid() ||
			active.TraceID() != primary.TraceID() || active.SpanID() != primary.SpanID() {
			// Never sever an in-process parent chain (for example Virtual
			// Kubelet's syncPod spans). At an already-instrumented boundary the
			// object carrier is an additional asynchronous cause, represented
			// as a link even while its direct-parent window remains open.
			parent = withSpanLink(parent, primary, "kubernetes.annotation")
		}
	}

	upstream, err := ParseTraceparent(annotations[UpstreamTraceparentAnnotation])
	if err == nil && (!result.Primary.IsValid() ||
		upstream.TraceID() != result.Primary.TraceID() || upstream.SpanID() != result.Primary.SpanID()) {
		result.Upstream = upstream
		parent = withSpanLink(parent, upstream, "kubernetes.annotation.upstream")
	}
	return parent, result
}

// SanitizedAnnotations returns only bounded, syntax-validated correlation
// fields. Producers remain responsible for never putting secrets in them.
// It is intended for controllers that need to copy causality from an owner to
// generated child objects. The returned map is nil when nothing is safe to
// propagate.
func SanitizedAnnotations(annotations map[string]string) map[string]string {
	return SanitizedAnnotationsAt(annotations, time.Now())
}

// SanitizedAnnotationsAt is the deterministic/testable form of
// SanitizedAnnotations.
func SanitizedAnnotationsAt(annotations map[string]string, now time.Time) map[string]string {
	out := map[string]string{}
	if len(annotations) == 0 {
		return nil
	}
	if _, err := parseAnnotatedSpanContext(annotations[TraceparentAnnotation], annotations[TracestateAnnotation]); err == nil {
		if windowEnd, err := time.Parse(time.RFC3339, annotations[TraceWindowEndAnnotation]); err == nil &&
			!windowEnd.After(now.Add(MaxTraceWindow)) {
			out[TraceparentAnnotation] = annotations[TraceparentAnnotation]
			out[TraceWindowEndAnnotation] = annotations[TraceWindowEndAnnotation]
			if annotations[TracestateAnnotation] != "" {
				out[TracestateAnnotation] = annotations[TracestateAnnotation]
			}
		}
	}
	if _, err := ParseTraceparent(annotations[UpstreamTraceparentAnnotation]); err == nil {
		out[UpstreamTraceparentAnnotation] = annotations[UpstreamTraceparentAnnotation]
	}
	if ValidLifecycleID(annotations[LifecycleIDAnnotation]) {
		out[LifecycleIDAnnotation] = annotations[LifecycleIDAnnotation]
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func ValidLifecycleID(value string) bool {
	return len(value) > 0 && len(value) <= MaxLifecycleIDLength && lifecycleIDPattern.MatchString(value)
}

func SpanContextFromAnnotations(annotations map[string]string, now time.Time) (trace.SpanContext, bool) {
	if len(annotations) == 0 {
		return trace.SpanContext{}, false
	}
	windowEnd, err := time.Parse(time.RFC3339, annotations[TraceWindowEndAnnotation])
	if err != nil || (!windowEnd.IsZero() && now.After(windowEnd)) || windowEnd.After(now.Add(MaxTraceWindow)) {
		return trace.SpanContext{}, false
	}
	sc, err := parseAnnotatedSpanContext(annotations[TraceparentAnnotation], annotations[TracestateAnnotation])
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

type lifecycleIDKey struct{}

type detachedContextKey struct{}

// AsyncCorrelation is an immutable snapshot of the trace linkage and stable
// lifecycle identity needed by queued work. It intentionally excludes the
// source context's cancellation, deadline, and arbitrary values.
type AsyncCorrelation struct {
	links       []trace.Link
	lifecycleID string
}

func WithSpanLink(parent context.Context, sc trace.SpanContext) context.Context {
	return withSpanLink(parent, sc, "cache")
}

// DetachedContext returns a context for bounded asynchronous work. It retains
// caller values (including log fields and LifecycleID) while deliberately
// removing cancellation, deadlines, and the active span as a direct parent.
// A valid scheduling span is retained as a link so work that outlives the
// request remains causally connected without creating a long-running child.
// Callers must add their own finite timeout before starting the async span.
func DetachedContext(parent context.Context) context.Context {
	if parent == nil {
		return context.Background()
	}
	schedulingSpan := trace.SpanContextFromContext(parent)
	detached := context.WithoutCancel(parent)
	detached = trace.ContextWithSpanContext(detached, trace.SpanContext{})
	detached = context.WithValue(detached, detachedContextKey{}, true)
	if schedulingSpan.IsValid() {
		detached = withSpanLink(detached, schedulingSpan, "async.scheduled")
	}
	return detached
}

// CaptureAsyncCorrelation snapshots correlation for a queue or other
// asynchronous handoff. Any active span becomes a link; existing links and a
// validated lifecycle ID are copied. The returned value does not retain the
// source context or its cancellation tree.
func CaptureAsyncCorrelation(ctx context.Context) AsyncCorrelation {
	detached := DetachedContext(ctx)
	return AsyncCorrelation{
		links:       cloneLinks(SpanLinksFromContext(detached)),
		lifecycleID: LifecycleIDFromContext(detached),
	}
}

// Context restores a captured asynchronous correlation snapshot onto the
// consumer's context. Cancellation and deadlines come from parent, while any
// active span or correlation values on parent are replaced so queued work
// starts a root span with links rather than a long-lived direct child.
func (c AsyncCorrelation) Context(parent context.Context) context.Context {
	if parent == nil {
		parent = context.Background()
	}
	ctx := trace.ContextWithSpanContext(parent, trace.SpanContext{})
	ctx = context.WithValue(ctx, detachedContextKey{}, true)
	ctx = context.WithValue(ctx, spanLinksKey{}, cloneLinks(c.links))
	ctx = context.WithValue(ctx, lifecycleIDKey{}, c.lifecycleID)
	return ctx
}

func isDetachedContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	detached, _ := ctx.Value(detachedContextKey{}).(bool)
	return detached
}

func withSpanLink(parent context.Context, sc trace.SpanContext, source string) context.Context {
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
			attribute.String("cvk.correlation.source", source),
			attribute.String("cvk.correlation.type", "span_link"),
		},
	}
	links := SpanLinksFromContext(parent)
	links = append(links, link)
	return context.WithValue(parent, spanLinksKey{}, links)
}

func WithLifecycleID(parent context.Context, lifecycleID string) context.Context {
	if parent == nil {
		parent = context.Background()
	}
	if !ValidLifecycleID(lifecycleID) {
		return parent
	}
	return context.WithValue(parent, lifecycleIDKey{}, lifecycleID)
}

func LifecycleIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(lifecycleIDKey{}).(string)
	return value
}

// Start starts a span while applying links and stable lifecycle identity that
// were previously attached by ApplyAnnotations. Callers use this rather than
// tracer.Start at Kubernetes/async boundaries; child driver and transport
// calls then receive the returned context unchanged.
func Start(ctx context.Context, tracer trace.Tracer, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	if links := SpanLinksFromContext(ctx); len(links) > 0 {
		opts = append(opts, trace.WithLinks(links...))
	}
	if lifecycleID := LifecycleIDFromContext(ctx); lifecycleID != "" {
		opts = append(opts, trace.WithAttributes(attribute.String(LifecycleIDAttribute, lifecycleID)))
	}
	return tracer.Start(ctx, name, opts...)
}

func SpanLinksFromContext(ctx context.Context) []trace.Link {
	if ctx == nil {
		return nil
	}
	links, _ := ctx.Value(spanLinksKey{}).([]trace.Link)
	if len(links) == 0 {
		return nil
	}
	return cloneLinks(links)
}

func cloneLinks(links []trace.Link) []trace.Link {
	if len(links) == 0 {
		return nil
	}
	cloned := make([]trace.Link, len(links))
	for i, link := range links {
		cloned[i] = link
		cloned[i].Attributes = append([]attribute.KeyValue(nil), link.Attributes...)
	}
	return cloned
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
