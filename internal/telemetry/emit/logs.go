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

	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/mapper"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/noop"
)

const loggerName = "cisco_vk_telemetry"

type LogsEmitter struct {
	logger log.Logger
	self   *SelfMetrics
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
	e := &LogsEmitter{logger: provider.Logger(loggerName)}
	for _, opt := range opts {
		if opt != nil {
			opt(e)
		}
	}
	return e
}

// Emit writes log mapped events and returns the number of emitted LogRecords.
func (e *LogsEmitter) Emit(ctx context.Context, events []mapper.MappedEvent) int {
	if e == nil || e.logger == nil {
		return 0
	}
	emitted := 0
	var device, subscription string
	for _, event := range events {
		if event.Signal != mapper.SignalKindLog {
			continue
		}
		var rec log.Record
		rec.SetTimestamp(event.Timestamp)
		rec.SetObservedTimestamp(event.Timestamp)
		rec.SetSeverity(toOTelSeverity(event.Severity))
		rec.SetSeverityText(string(event.Severity))
		rec.SetBody(log.StringValue(event.Body))
		attrs := make([]mapper.KeyValue, 0, len(event.Resource)+len(event.Attributes))
		attrs = append(attrs, event.Resource...)
		attrs = append(attrs, event.Attributes...)
		rec.AddAttributes(toLogAttrs(attrs)...)
		e.logger.Emit(ctx, rec)
		emitted++
		if device == "" {
			device = attrValue(event.Resource, "device")
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
