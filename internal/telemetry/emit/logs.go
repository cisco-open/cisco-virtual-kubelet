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

package emit

import (
	"context"
	"strings"
	"sync"
	"time"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/mapper"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/noop"
)

const loggerName = "cisco_vk_telemetry"

type LogsEmitter struct {
	logger log.Logger
	self   *SelfMetrics

	mu             sync.Mutex
	sampleCounters map[string]uint64
	rateBuckets    map[string]*tokenBucket
}

// LogsEmitterOption configures a LogsEmitter at construction.
type LogsEmitterOption func(*LogsEmitter)

// WithLogsSelfMetrics wires the shared SelfMetrics so log emission counts are
// reported on the OTel pipeline.
func WithLogsSelfMetrics(self *SelfMetrics) LogsEmitterOption {
	return func(e *LogsEmitter) { e.self = self }
}

func NewLogsEmitter(provider log.LoggerProvider, opts ...LogsEmitterOption) *LogsEmitter {
	if provider == nil {
		provider = noop.NewLoggerProvider()
	}
	e := &LogsEmitter{
		logger:         provider.Logger(loggerName),
		sampleCounters: map[string]uint64{},
		rateBuckets:    map[string]*tokenBucket{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(e)
		}
	}
	return e
}

// Emit writes log mapped events and returns the number of emitted LogRecords.
func (e *LogsEmitter) Emit(ctx context.Context, events []mapper.MappedEvent) int {
	return e.EmitWithPolicy(ctx, events,
		configv1alpha1.LogsOutputConfig{Enabled: true, SampleEveryN: 1},
		configv1alpha1.DefaultBudgetConfig(nil),
		"",
	)
}

// EmitWithPolicy writes log mapped events after applying the CR's resolved log
// output policy and signal budget.
func (e *LogsEmitter) EmitWithPolicy(
	ctx context.Context,
	events []mapper.MappedEvent,
	policy configv1alpha1.LogsOutputConfig,
	budgets configv1alpha1.BudgetConfig,
	policyKey string,
) int {
	if e == nil || e.logger == nil {
		return 0
	}
	policy = policy.Resolved()
	if !policy.Enabled {
		return 0
	}
	budgets = configv1alpha1.DefaultBudgetConfig(&budgets)
	emitted := 0
	var device, subscription string
	for _, event := range events {
		if event.Signal != mapper.SignalKindLog {
			continue
		}
		if !pathAllowed(policy.Paths, event.CanonicalPath) {
			continue
		}
		if !e.allowSample(policyKey, event.CanonicalPath, policy.SampleEveryN) {
			continue
		}
		eventDevice := attrValue(event.Resource, "device")
		if eventDevice == "" {
			eventDevice = "unknown"
		}
		if !e.allowLogRecord(policyKey, eventDevice, budgets.MaxLogRecordsPerSecond) {
			RecordBudgetDropped(ctx, "logs", "rate_limit_log_records", eventDevice)
			continue
		}
		var rec log.Record
		rec.SetTimestamp(event.Timestamp)
		rec.SetObservedTimestamp(event.Timestamp)
		rec.SetSeverity(toOTelSeverity(event.Severity))
		rec.SetSeverityText(string(event.Severity))
		rec.SetEventName(logEventName(event.Name))
		rec.SetBody(log.StringValue(event.Body))
		attrs := make([]mapper.KeyValue, 0, len(event.Resource)+len(event.Attributes))
		attrs = append(attrs, event.Resource...)
		attrs = append(attrs, event.Attributes...)
		rec.AddAttributes(toLogAttrs(attrs)...)
		e.logger.Emit(ctx, rec)
		emitted++
		if device == "" {
			device = eventDevice
		}
		if subscription == "" {
			subscription = attrValue(event.Resource, "subscription")
		}
	}
	if emitted > 0 {
		e.self.AddLogRecords(ctx, int64(emitted), device, subscription)
	}
	return emitted
}

func pathAllowed(paths []string, path string) bool {
	if len(paths) == 0 {
		return true
	}
	for _, allowed := range paths {
		if allowed == path {
			return true
		}
	}
	return false
}

func (e *LogsEmitter) allowSample(policyKey, path string, everyN int) bool {
	if everyN <= 1 {
		return true
	}
	key := policyKey + "\x00" + path
	e.mu.Lock()
	defer e.mu.Unlock()
	next := e.sampleCounters[key] + 1
	e.sampleCounters[key] = next
	return (next-1)%uint64(everyN) == 0
}

// allowLogRecord enforces the per-subscription log rate budget. The
// bucket key combines policyKey (the CR/subscription identifier) and
// the device, so two CRs targeting the same device do not share a
// bucket and the *current* maxPerSecond is applied — not whatever
// the first CR to emit set up.
//
// Adversarial-review Finding #6: previously the bucket was keyed only
// on `device` and initialized once with the first CR's
// maxPerSecond. The first emitting CR effectively defined the log
// budget for every other CR on the same device, and a later
// MaxLogRecordsPerSecond change did not resize the bucket. The
// key+resize logic below fixes both.
func (e *LogsEmitter) allowLogRecord(policyKey, device string, maxPerSecond int) bool {
	if maxPerSecond <= 0 {
		maxPerSecond = 500
	}
	rate := float64(maxPerSecond)
	bucketKey := policyKey + "\x00" + device
	e.mu.Lock()
	defer e.mu.Unlock()
	bucket := e.rateBuckets[bucketKey]
	if bucket == nil {
		bucket = &tokenBucket{
			capacity:   rate,
			refillRate: rate,
		}
		e.rateBuckets[bucketKey] = bucket
	} else if bucket.capacity != rate || bucket.refillRate != rate {
		// MaxLogRecordsPerSecond changed since the bucket was last
		// touched. Resize so the budget tracks the current CR setting
		// rather than the value baked in when the bucket was created.
		bucket.capacity = rate
		bucket.refillRate = rate
		if bucket.tokens > rate {
			bucket.tokens = rate
		}
	}
	return bucket.allow(time.Now())
}

func logEventName(name string) string {
	if strings.HasPrefix(name, "cvk.") {
		return name
	}
	return "cvk." + name
}

func toOTelSeverity(sev mapper.Severity) log.Severity {
	switch sev {
	case mapper.SeverityWarn:
		return log.SeverityWarn
	case mapper.SeverityError:
		return log.SeverityError
	default:
		return log.SeverityInfo
	}
}

func toLogAttrs(attrs []mapper.KeyValue) []log.KeyValue {
	out := make([]log.KeyValue, 0, len(attrs))
	for _, attr := range attrs {
		if mapper.IsForbiddenDataPointAttribute(attr.Key) {
			continue
		}
		out = append(out, log.String(attr.Key, attr.Value))
	}
	return out
}
