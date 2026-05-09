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

package provider

import (
	"context"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestNewOTELTopologyExporterUsesProvidedTracerProvider(t *testing.T) {
	tp := sdktrace.NewTracerProvider()
	defer func() { _ = tp.Shutdown(context.Background()) }()

	exporter, err := NewOTELTopologyExporter(
		context.Background(),
		nil,
		nil,
		&v1alpha1.OTELConfig{},
		"edge-01",
		"192.0.2.10",
		tp,
	)
	if err != nil {
		t.Fatalf("NewOTELTopologyExporter: %v", err)
	}
	if exporter.TracerProvider() != tp {
		t.Fatal("exporter did not use provided tracer provider")
	}
	if exporter.ownedTP != nil {
		t.Fatal("exporter should not own a shared tracer provider")
	}
}
