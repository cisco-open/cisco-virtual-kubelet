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

package transport

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const transportTracerName = "cisco-virtual-kubelet/config-transport"

func startTransportSpan(ctx context.Context, kind Kind, verb string, attrs ...attribute.KeyValue) (context.Context, oteltrace.Span) {
	base := []attribute.KeyValue{
		attribute.String("network.protocol.name", string(kind)),
		attribute.String("cvk.transport.kind", string(kind)),
		attribute.String("cvk.transport.verb", verb),
	}
	base = append(base, attrs...)
	return otel.Tracer(transportTracerName).Start(
		ctx,
		"cvk.transport."+string(kind)+"."+verb,
		oteltrace.WithSpanKind(oteltrace.SpanKindClient),
		oteltrace.WithAttributes(base...),
	)
}

func spanPath(path string) attribute.KeyValue {
	return attribute.String("cvk.transport.path", path)
}

func spanOpCount(n int) attribute.KeyValue {
	return attribute.Int("cvk.transport.op_count", n)
}
