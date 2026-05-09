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

package emit

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/mapper"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

const (
	tracerName                  = "cisco_vk_telemetry"
	stateTransitionsTotalMetric = "cisco_vk_telemetry_state_transitions_total"
)

type TracesEmitter struct {
	tracer trace.Tracer

	mu       sync.Mutex
	tracker  *transitionTracker
	rulesKey string

	stateTransitionsTotal metric.Int64Counter
}

func NewTracesEmitter(tracerProvider trace.TracerProvider, meterProvider metric.MeterProvider, transitions []configv1alpha1.Transition) *TracesEmitter {
	if tracerProvider == nil {
		tracerProvider = tracenoop.NewTracerProvider()
	}
	if meterProvider == nil {
		meterProvider = metricnoop.NewMeterProvider()
	}
	meter := meterProvider.Meter(meterName)
	counter, _ := meter.Int64Counter(stateTransitionsTotalMetric)
	return &TracesEmitter{
		tracer:                tracerProvider.Tracer(tracerName),
		tracker:               newTransitionTracker(transitions),
		rulesKey:              transitionRulesKey(transitions),
		stateTransitionsTotal: counter,
	}
}

func (e *TracesEmitter) SetTransitions(transitions []configv1alpha1.Transition) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	rulesKey := transitionRulesKey(transitions)
	if rulesKey == e.rulesKey {
		return
	}
	e.tracker = newTransitionTracker(transitions)
	e.rulesKey = rulesKey
}

func (e *TracesEmitter) Emit(ctx context.Context, events []mapper.MappedEvent) int {
	if e == nil || e.tracer == nil {
		return 0
	}
	emitted := 0
	for _, event := range events {
		if event.Signal != mapper.SignalKindTrace {
			continue
		}
		value := strings.TrimSpace(event.Body)
		ts := event.Timestamp
		if ts.IsZero() {
			ts = time.Now()
		}
		entityKey := event.SeriesKey
		if entityKey == "" {
			entityKey = buildEntityKey(event)
		}
		e.mu.Lock()
		tracker := e.tracker
		var out transition
		if tracker != nil {
			if rule := tracker.matchRule(event.CanonicalPath); rule != nil {
				if isDelete(event) {
					tracker.forget(rule.path, entityKey)
				} else {
					out = tracker.observe(rule, entityKey, value, ts, pathKeys(event))
				}
			}
		}
		e.mu.Unlock()
		if !out.recovered {
			continue
		}
		e.emitRecoverySpan(ctx, event, out, ts)
		emitted++
	}
	return emitted
}

func (e *TracesEmitter) emitRecoverySpan(ctx context.Context, event mapper.MappedEvent, out transition, end time.Time) {
	start := out.openedAt
	if start.IsZero() {
		start = end
	}
	attrs := []attribute.KeyValue{
		attribute.String("from-state", out.previousValue),
		attribute.String("to-state", event.Body),
		attribute.String("duration", end.Sub(start).String()),
		attribute.String("cisco.gnmi.path", event.CanonicalPath),
	}
	for _, kv := range sortedKeyValues(out.keys) {
		attrs = append(attrs, attribute.String(kv.Key, kv.Value))
	}
	_, span := e.tracer.Start(ctx, transitionSpanName(event),
		trace.WithTimestamp(start),
		trace.WithAttributes(attrs...),
	)
	span.End(trace.WithTimestamp(end))
	if e.stateTransitionsTotal != nil {
		e.stateTransitionsTotal.Add(ctx, 1, metric.WithAttributes(
			attribute.String("device", attrValue(event.Resource, "device")),
			attribute.String("subscription", attrValue(event.Resource, "subscription")),
			attribute.String("path", out.rule.path),
			attribute.String("from", out.previousValue),
			attribute.String("to", event.Body),
		))
	}
}

type transitionTracker struct {
	rules  []compiledTransitionRule
	states map[string]entityState
}

type compiledTransitionRule struct {
	path         string
	healthySet   map[string]struct{}
	unhealthySet map[string]struct{}
}

type entityState struct {
	healthy   bool
	since     time.Time
	lastValue string
	keys      map[string]string
}

type transition struct {
	rule          *compiledTransitionRule
	recovered     bool
	openedAt      time.Time
	previousValue string
	keys          map[string]string
}

func newTransitionTracker(transitions []configv1alpha1.Transition) *transitionTracker {
	t := &transitionTracker{states: map[string]entityState{}}
	for _, transition := range transitions {
		path := normalizeTransitionPath(transition.Path)
		if path == "" || len(transition.HealthyValues) == 0 || len(transition.UnhealthyValues) == 0 {
			continue
		}
		t.rules = append(t.rules, compiledTransitionRule{
			path:         path,
			healthySet:   valueSet(transition.HealthyValues),
			unhealthySet: valueSet(transition.UnhealthyValues),
		})
	}
	return t
}

func (t *transitionTracker) matchRule(fullPath string) *compiledTransitionRule {
	if t == nil {
		return nil
	}
	path := normalizeTransitionPath(fullPath)
	for i := range t.rules {
		if path == t.rules[i].path {
			return &t.rules[i]
		}
	}
	return nil
}

func (t *transitionTracker) observe(
	rule *compiledTransitionRule,
	entityKey string,
	value string,
	ts time.Time,
	keys map[string]string,
) transition {
	out := transition{rule: rule}
	healthy, ok := rule.classify(value)
	if !ok {
		return out
	}
	stateKey := rule.path + "|" + entityKey
	prev, seen := t.states[stateKey]
	if !seen {
		t.states[stateKey] = entityState{
			healthy:   healthy,
			since:     ts,
			lastValue: value,
			keys:      cloneStringMap(keys),
		}
		return out
	}
	if prev.healthy == healthy {
		prev.lastValue = value
		prev.keys = cloneStringMap(keys)
		if !healthy {
			prev.since = ts
		}
		t.states[stateKey] = prev
		return out
	}
	if !prev.healthy && healthy {
		out.recovered = true
		out.openedAt = prev.since
		out.previousValue = prev.lastValue
		out.keys = cloneStringMap(prev.keys)
	}
	t.states[stateKey] = entityState{
		healthy:   healthy,
		since:     ts,
		lastValue: value,
		keys:      cloneStringMap(keys),
	}
	return out
}

func (t *transitionTracker) forget(rulePath, entityKey string) {
	if t == nil {
		return
	}
	delete(t.states, rulePath+"|"+entityKey)
}

func (r *compiledTransitionRule) classify(value string) (healthy bool, ok bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	if _, ok := r.healthySet[value]; ok {
		return true, true
	}
	if _, ok := r.unhealthySet[value]; ok {
		return false, true
	}
	return false, false
}

func valueSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			out[value] = struct{}{}
		}
	}
	return out
}

func isDelete(event mapper.MappedEvent) bool {
	for _, attr := range event.Attributes {
		if attr.Key == "cisco.gnmi.event" && attr.Value == "delete" {
			return true
		}
	}
	return false
}

func normalizeTransitionPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || path == "/" {
		return path
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if idx := strings.IndexByte(part, '['); idx >= 0 {
			part = part[:idx]
		}
		if idx := strings.IndexByte(part, ':'); idx >= 0 {
			part = part[idx+1:]
		}
		if part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return "/"
	}
	return "/" + strings.Join(out, "/")
}

func buildEntityKey(event mapper.MappedEvent) string {
	keys := pathKeys(event)
	names := make([]string, 0, len(keys))
	for name := range keys {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString(event.CanonicalPath)
	for _, name := range names {
		b.WriteByte('\x00')
		b.WriteString(name)
		b.WriteByte('=')
		b.WriteString(keys[name])
	}
	return b.String()
}

func pathKeys(event mapper.MappedEvent) map[string]string {
	out := map[string]string{}
	for _, attr := range event.Attributes {
		if strings.HasPrefix(attr.Key, "cisco.") {
			continue
		}
		out[attr.Key] = attr.Value
	}
	return out
}

func sortedKeyValues(in map[string]string) []mapper.KeyValue {
	keys := make([]string, 0, len(in))
	for key := range in {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]mapper.KeyValue, 0, len(keys))
	for _, key := range keys {
		out = append(out, mapper.KeyValue{Key: key, Value: in[key]})
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func transitionSpanName(event mapper.MappedEvent) string {
	name := strings.Trim(event.Name, "/")
	if name == "" {
		name = strings.Trim(event.CanonicalPath, "/")
	}
	name = strings.ReplaceAll(name, "/", ".")
	name = sanitizeMetricName(name)
	return "state.transition." + name
}

func transitionRulesKey(transitions []configv1alpha1.Transition) string {
	var b strings.Builder
	for _, transition := range transitions {
		b.WriteString(transition.Path)
		b.WriteByte('\x00')
		b.WriteString(strings.Join(transition.HealthyValues, "\x00"))
		b.WriteByte('\x00')
		b.WriteString(strings.Join(transition.UnhealthyValues, "\x00"))
		b.WriteByte('\x00')
	}
	return b.String()
}
