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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/classifier"
	gpb "github.com/openconfig/gnmi/proto/gnmi"
)

type Mapper struct {
	mu        sync.Mutex
	series    map[string]*SeriesKeyCache
	starts    map[string]*StartTimestampCache
	caps      map[string]int
	startCaps map[string]int
}

func New() *Mapper {
	return &Mapper{
		series:    map[string]*SeriesKeyCache{},
		starts:    map[string]*StartTimestampCache{},
		caps:      map[string]int{},
		startCaps: map[string]int{},
	}
}

var defaultClassifier = classifier.CuratedClassifier()

func (m *Mapper) Process(notif *gpb.Notification, ctx EventContext) []MappedEvent {
	if notif == nil {
		return nil
	}
	mapping := ctx.Mapping
	var aliases []configv1alpha1.PathAlias
	var resourceAttrs []configv1alpha1.ResourceAttribute
	var filterCfg *configv1alpha1.FilterConfig
	if mapping != nil {
		aliases = mapping.PathAliases
		resourceAttrs = mapping.ResourceAttributes
		filterCfg = mapping.Filter
	}
	resolver := NewAliasResolver(aliases)
	filter := NewFilter(filterCfg)
	extractor := NewResourceAttrExtractor(resourceAttrs)

	baseResource := m.resource(notif, ctx, extractor)
	timestamp, timestampAttrs := timestamps(notif.GetTimestamp(), ctx)
	metricClassifier := ctx.Classifier
	if metricClassifier == nil {
		metricClassifier = defaultClassifier
	}

	out := make([]MappedEvent, 0, len(notif.GetUpdate())+len(notif.GetDelete()))
	for _, update := range notif.GetUpdate() {
		canonical, keys, _ := FlattenPath(notif.GetPrefix(), update.GetPath())
		name := resolver.Resolve(canonical)
		attrs := eventAttributes(ctx, canonical, keys, timestampAttrs)
		if drop := m.evaluate(ctx, filter, canonical, name, keys, baseResource, attrs, timestamp); drop != nil {
			out = append(out, *drop)
			continue
		}
		if body, ok := typedValueString(update.GetVal()); ok && transitionEventsEnabled(ctx) {
			out = append(out, MappedEvent{
				Signal:        SignalKindTrace,
				Name:          name,
				Attributes:    attrs,
				Resource:      baseResource,
				Timestamp:     timestamp,
				Body:          body,
				CanonicalPath: canonical,
				SeriesKey:     BuildSeriesKey(ctx.Subscription, canonical, keys),
			})
		}
		if body, ok := logScalarValue(update.GetVal()); ok {
			if !signalEnabled(ctx.Output, SignalKindLog) {
				continue
			}
			out = append(out, MappedEvent{
				Signal:        SignalKindLog,
				Name:          name,
				Attributes:    attrs,
				Resource:      baseResource,
				Timestamp:     timestamp,
				Body:          body,
				Severity:      inferSeverity(body),
				CanonicalPath: canonical,
				SeriesKey:     BuildSeriesKey(ctx.Subscription, canonical, keys),
			})
			continue
		}
		if value, ok := numericValue(update.GetVal()); ok && signalEnabled(ctx.Output, SignalKindMetric) {
			v := value
			kind := metricClassifier.Classify(canonical)
			seriesKey := BuildSeriesKey(ctx.Subscription, canonical, keys)
			var start time.Time
			if kind == classifier.MetricKindSum {
				start = m.startCache(ctx).Start(ctx.StreamEpoch, seriesKey, ctx.ReceiveTime)
			}
			out = append(out, MappedEvent{
				Signal:         SignalKindMetric,
				Name:           name,
				Attributes:     attrs,
				Resource:       baseResource,
				Timestamp:      timestamp,
				NumberValue:    &v,
				MetricKind:     kind,
				CanonicalPath:  canonical,
				SeriesKey:      seriesKey,
				StartTimestamp: start,
			})
		}
	}
	for _, del := range notif.GetDelete() {
		canonical, keys, _ := FlattenPath(notif.GetPrefix(), del)
		name := resolver.Resolve(canonical)
		attrs := eventAttributes(ctx, canonical, keys, timestampAttrs)
		attrs = append(attrs, KeyValue{Key: "cisco.gnmi.event", Value: "delete"})
		if drop := m.evaluate(ctx, filter, canonical, name, keys, baseResource, attrs, timestamp); drop != nil {
			out = append(out, *drop)
			continue
		}
		if !signalEnabled(ctx.Output, SignalKindLog) {
			if !transitionEventsEnabled(ctx) {
				continue
			}
		}
		if transitionEventsEnabled(ctx) {
			out = append(out, MappedEvent{
				Signal:        SignalKindTrace,
				Name:          name,
				Attributes:    attrs,
				Resource:      baseResource,
				Timestamp:     timestamp,
				CanonicalPath: canonical,
				SeriesKey:     BuildSeriesKey(ctx.Subscription, canonical, keys),
			})
		}
		if !signalEnabled(ctx.Output, SignalKindLog) {
			continue
		}
		out = append(out, MappedEvent{
			Signal:        SignalKindLog,
			Name:          name,
			Attributes:    attrs,
			Resource:      baseResource,
			Timestamp:     timestamp,
			Body:          "deleted: " + canonical,
			Severity:      SeverityInfo,
			CanonicalPath: canonical,
			SeriesKey:     BuildSeriesKey(ctx.Subscription, canonical, keys),
		})
	}
	return out
}

func (m *Mapper) SeriesSize(subscription string) int {
	m.mu.Lock()
	cache := m.series[subscription]
	m.mu.Unlock()
	if cache == nil {
		return 0
	}
	return cache.Size()
}

func (m *Mapper) evaluate(
	ctx EventContext,
	filter Filter,
	canonicalPath string,
	name string,
	keys []KeyValue,
	resource []KeyValue,
	attrs []KeyValue,
	ts time.Time,
) *MappedEvent {
	if !filter.AllowWirePath(canonicalPath) || !filter.AllowMetricName(name) {
		return &MappedEvent{
			Signal:        SignalKindDrop,
			Name:          name,
			Attributes:    attrs,
			Resource:      resource,
			Timestamp:     ts,
			CanonicalPath: canonicalPath,
			SeriesKey:     BuildSeriesKey(ctx.Subscription, canonicalPath, keys),
			DropReason:    DropReasonFilter,
		}
	}
	key := BuildSeriesKey(ctx.Subscription, canonicalPath, keys)
	_, ok, _ := m.seriesCache(ctx).Check(key)
	if !ok {
		return &MappedEvent{
			Signal:        SignalKindDrop,
			Name:          name,
			Attributes:    attrs,
			Resource:      resource,
			Timestamp:     ts,
			CanonicalPath: canonicalPath,
			SeriesKey:     key,
			DropReason:    DropReasonCardinalityLimit,
		}
	}
	return nil
}

func (m *Mapper) seriesCache(ctx EventContext) *SeriesKeyCache {
	capacity := DefaultMaxSeriesPerSubscription
	if ctx.CardinalityLimits != nil && ctx.CardinalityLimits.MaxSeriesPerSubscription > 0 {
		capacity = int(ctx.CardinalityLimits.MaxSeriesPerSubscription)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.series == nil {
		m.series = map[string]*SeriesKeyCache{}
		m.starts = map[string]*StartTimestampCache{}
		m.caps = map[string]int{}
		m.startCaps = map[string]int{}
	}
	if cache := m.series[ctx.Subscription]; cache != nil && m.caps[ctx.Subscription] == capacity {
		return cache
	}
	cache := NewSeriesKeyCache(capacity)
	m.series[ctx.Subscription] = cache
	m.caps[ctx.Subscription] = capacity
	return cache
}

func (m *Mapper) startCache(ctx EventContext) *StartTimestampCache {
	capacity := DefaultMaxSeriesPerSubscription
	if ctx.CardinalityLimits != nil && ctx.CardinalityLimits.MaxSeriesPerSubscription > 0 {
		capacity = int(ctx.CardinalityLimits.MaxSeriesPerSubscription)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.starts == nil {
		m.starts = map[string]*StartTimestampCache{}
	}
	if m.startCaps == nil {
		m.startCaps = map[string]int{}
	}
	if cache := m.starts[ctx.Subscription]; cache != nil && m.startCaps[ctx.Subscription] == capacity {
		return cache
	}
	cache := NewStartTimestampCache(capacity)
	m.starts[ctx.Subscription] = cache
	m.startCaps[ctx.Subscription] = capacity
	return cache
}

func (m *Mapper) resource(
	notif *gpb.Notification,
	ctx EventContext,
	extractor ResourceAttrExtractor,
) []KeyValue {
	out := make([]KeyValue, 0, 4+len(ctx.ResourceAttributes))
	if ctx.Device != "" {
		out = append(out, KeyValue{Key: "device", Value: ctx.Device})
	}
	if ctx.Subscription != "" {
		out = append(out, KeyValue{Key: "subscription", Value: ctx.Subscription})
	}
	for _, kv := range sortedMap(ctx.ResourceAttributes) {
		out = append(out, kv)
	}
	origin := pathOrigin(notif.GetPrefix(), nil)
	if origin == "" {
		for _, update := range notif.GetUpdate() {
			origin = pathOrigin(notif.GetPrefix(), update.GetPath())
			if origin != "" {
				break
			}
		}
	}
	if origin == "" {
		for _, del := range notif.GetDelete() {
			origin = pathOrigin(notif.GetPrefix(), del)
			if origin != "" {
				break
			}
		}
	}
	if origin != "" {
		out = append(out, KeyValue{Key: "cisco.gnmi.origin", Value: origin})
	}
	out = append(out, extractor.Extract(notif)...)
	return out
}

func sortedMap(in map[string]string) []KeyValue {
	if len(in) == 0 {
		return nil
	}
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]KeyValue, 0, len(keys))
	for _, k := range keys {
		out = append(out, KeyValue{Key: k, Value: in[k]})
	}
	return out
}

func eventAttributes(ctx EventContext, canonicalPath string, keys []KeyValue, timestampAttrs []KeyValue) []KeyValue {
	out := make([]KeyValue, 0, len(keys)+len(timestampAttrs)+3)
	out = append(out, keys...)
	out = append(out, KeyValue{Key: "cisco.gnmi.path", Value: canonicalPath})
	if ctx.StreamID != "" {
		out = append(out, KeyValue{Key: "cisco.gnmi.stream_id", Value: ctx.StreamID})
	}
	out = append(out, timestampAttrs...)
	return out
}

func timestamps(deviceTimestamp int64, ctx EventContext) (time.Time, []KeyValue) {
	if useCollectorTimestamp(ctx.Timestamps) {
		ts := ctx.ReceiveTime
		if ts.IsZero() {
			ts = time.Now()
		}
		attrs := []KeyValue{{Key: "cisco.device.timestamp", Value: strconvI64(deviceTimestamp)}}
		return ts, attrs
	}
	if deviceTimestamp == 0 {
		return time.Time{}, nil
	}
	return time.Unix(0, deviceTimestamp), nil
}

func useCollectorTimestamp(cfg *configv1alpha1.TimestampConfig) bool {
	return cfg == nil || cfg.UseCollectorTimestamp == nil || *cfg.UseCollectorTimestamp
}

func inferSeverity(body string) Severity {
	upper := strings.ToUpper(body)
	lower := strings.ToLower(body)
	switch {
	case strings.Contains(upper, "UP"), strings.Contains(upper, "ESTABLISHED"):
		return SeverityInfo
	case strings.Contains(upper, "DOWN"):
		return SeverityWarn
	case strings.Contains(lower, "critical"), strings.Contains(lower, "error"):
		return SeverityError
	default:
		return SeverityInfo
	}
}

func strconvI64(v int64) string {
	return strconv.FormatInt(v, 10)
}
