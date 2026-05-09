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
	includeListKeysInName := includeListKeysInMetricName(mapping)

	baseResource := m.resource(notif, ctx, extractor)
	// Per-entity resource attrs grouped by list-key scope. This lets configured
	// paths pin per-list strings (state, IP, MAC, image type) onto every
	// metric/log event for the same entity or nested entity.
	entityAttrs := extractor.ExtractByEntity(notif)
	timestamp, timestampAttrs := timestamps(notif.GetTimestamp(), ctx)
	metricClassifier := ctx.Classifier
	if metricClassifier == nil {
		metricClassifier = defaultClassifier
	}

	out := make([]MappedEvent, 0, len(notif.GetUpdate())+len(notif.GetDelete()))
	for _, update := range notif.GetUpdate() {
		canonical, keys, _, tuple := FlattenPath(notif.GetPrefix(), update.GetPath())
		name := eventName(resolver, canonical, tuple, includeListKeysInName)
		seriesKey := eventSeriesKey(ctx.Subscription, canonical, keys, includeListKeysInName)
		attrs := eventAttributes(ctx, canonical, keys, tuple, timestampAttrs)
		eventResource := resourceForEntity(baseResource, entityAttrs, tuple)
		if drop := m.evaluate(ctx, filter, canonical, name, seriesKey, eventResource, attrs, timestamp); drop != nil {
			out = append(out, *drop)
			continue
		}
		if body, ok := typedValueString(update.GetVal()); ok && transitionEventsEnabled(ctx) {
			out = append(out, MappedEvent{
				Signal:        SignalKindTrace,
				Name:          name,
				Attributes:    attrs,
				Resource:      eventResource,
				Timestamp:     timestamp,
				Body:          body,
				CanonicalPath: canonical,
				SeriesKey:     seriesKey,
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
				Resource:      eventResource,
				Timestamp:     timestamp,
				Body:          body,
				Severity:      inferSeverity(body),
				CanonicalPath: canonical,
				SeriesKey:     seriesKey,
			})
			continue
		}
		if value, ok := numericValue(update.GetVal()); ok && signalEnabled(ctx.Output, SignalKindMetric) {
			v := value
			kind := metricClassifier.Classify(canonical)
			var start time.Time
			if kind == classifier.MetricKindSum {
				start = m.startCache(ctx).Start(ctx.StreamEpoch, seriesKey, ctx.ReceiveTime)
			}
			out = append(out, MappedEvent{
				Signal:         SignalKindMetric,
				Name:           name,
				Attributes:     attrs,
				Resource:       eventResource,
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
		canonical, keys, _, tuple := FlattenPath(notif.GetPrefix(), del)
		name := eventName(resolver, canonical, tuple, includeListKeysInName)
		seriesKey := eventSeriesKey(ctx.Subscription, canonical, keys, includeListKeysInName)
		attrs := eventAttributes(ctx, canonical, keys, tuple, timestampAttrs)
		attrs = append(attrs, KeyValue{Key: "cisco.gnmi.event", Value: "delete"})
		eventResource := resourceForEntity(baseResource, entityAttrs, tuple)
		if drop := m.evaluate(ctx, filter, canonical, name, seriesKey, eventResource, attrs, timestamp); drop != nil {
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
				Resource:      eventResource,
				Timestamp:     timestamp,
				CanonicalPath: canonical,
				SeriesKey:     seriesKey,
			})
		}
		if !signalEnabled(ctx.Output, SignalKindLog) {
			continue
		}
		out = append(out, MappedEvent{
			Signal:        SignalKindLog,
			Name:          name,
			Attributes:    attrs,
			Resource:      eventResource,
			Timestamp:     timestamp,
			Body:          "deleted: " + canonical,
			Severity:      SeverityInfo,
			CanonicalPath: canonical,
			SeriesKey:     seriesKey,
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
	seriesKey string,
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
			SeriesKey:     seriesKey,
			DropReason:    DropReasonFilter,
		}
	}
	_, ok, _ := m.seriesCache(ctx).Check(seriesKey)
	if !ok {
		return &MappedEvent{
			Signal:        SignalKindDrop,
			Name:          name,
			Attributes:    attrs,
			Resource:      resource,
			Timestamp:     ts,
			CanonicalPath: canonicalPath,
			SeriesKey:     seriesKey,
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

func eventAttributes(ctx EventContext, canonicalPath string, keys []KeyValue, tuple ListKeyTuple, timestampAttrs []KeyValue) []KeyValue {
	out := make([]KeyValue, 0, len(keys)+len(timestampAttrs)+3)
	out = append(out, pathLabelAttributes(keys, tuple)...)
	out = append(out, KeyValue{Key: "cisco.gnmi.path", Value: canonicalPath})
	if ctx.StreamID != "" {
		out = append(out, KeyValue{Key: "cisco.gnmi.stream_id", Value: ctx.StreamID})
	}
	out = append(out, timestampAttrs...)
	return out
}

func includeListKeysInMetricName(mapping *configv1alpha1.MappingConfig) bool {
	return mapping != nil &&
		mapping.IncludeListKeysInMetricName != nil &&
		*mapping.IncludeListKeysInMetricName
}

func eventName(resolver AliasResolver, canonicalPath string, tuple ListKeyTuple, includeListKeys bool) string {
	if includeListKeys {
		if name, matched := resolver.ResolveWithMatch(canonicalPath); matched {
			return name
		}
		return canonicalPath
	}
	stripped := stripPathListKeys(canonicalPath)
	if name, matched := resolver.ResolveWithMatch(stripped); matched {
		return name
	}
	if len(tuple) == 0 {
		return stripped
	}
	return metricLeafName(stripped, tuple)
}

func eventSeriesKey(subscription, canonicalPath string, keys []KeyValue, includeListKeys bool) string {
	seriesPath := canonicalPath
	if !includeListKeys {
		seriesPath = stripPathListKeys(canonicalPath)
	}
	return BuildSeriesKey(subscription, seriesPath, keys)
}

func metricLeafName(strippedPath string, tuple ListKeyTuple) string {
	namePath := strippedPath
	if len(tuple) > 0 {
		firstListPath := tuple[0].ListPath
		switch {
		case strippedPath == firstListPath:
			namePath = "/" + listName(firstListPath)
		case strings.HasPrefix(strippedPath, firstListPath+"/"):
			namePath = strings.TrimPrefix(strippedPath, firstListPath)
		}
	}
	name := labelComponent(strings.Trim(namePath, "/"))
	if name == "" {
		name = labelComponent(strings.Trim(strippedPath, "/"))
	}
	return name
}

func resourceForEntity(base []KeyValue, scoped map[string][]KeyValue, tuple ListKeyTuple) []KeyValue {
	if len(scoped) == 0 || len(tuple) == 0 {
		return base
	}
	out := base
	copied := false
	for _, scope := range entityScopePrefixKeys(tuple) {
		extra := scoped[scope]
		if len(extra) == 0 {
			continue
		}
		if !copied {
			out = make([]KeyValue, len(base))
			copy(out, base)
			copied = true
		}
		out = mergeKeyValues(out, extra)
	}
	return out
}

func mergeKeyValues(base []KeyValue, extra []KeyValue) []KeyValue {
	for _, kv := range extra {
		replaced := false
		for i := range base {
			if base[i].Key == kv.Key {
				base[i].Value = kv.Value
				replaced = true
				break
			}
		}
		if !replaced {
			base = append(base, kv)
		}
	}
	return base
}

func pathLabelAttributes(keys []KeyValue, tuple ListKeyTuple) []KeyValue {
	if len(keys) == 0 {
		return nil
	}
	if singleListScope(tuple) {
		return keys
	}
	out := make([]KeyValue, 0, len(keys))
	for i, kv := range keys {
		if i >= len(tuple) {
			out = append(out, kv)
			continue
		}
		out = append(out, KeyValue{
			Key:   labelComponent(listName(tuple[i].ListPath)) + "_" + labelComponent(tuple[i].KeyName),
			Value: kv.Value,
		})
	}
	return out
}

func singleListScope(tuple ListKeyTuple) bool {
	if len(tuple) == 0 {
		return true
	}
	listPath := tuple[0].ListPath
	for _, key := range tuple[1:] {
		if key.ListPath != listPath {
			return false
		}
	}
	return true
}

func labelComponent(in string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range in {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		default:
			if !lastUnderscore {
				b.WriteByte('_')
				lastUnderscore = true
			}
		}
	}
	return strings.Trim(b.String(), "_")
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
