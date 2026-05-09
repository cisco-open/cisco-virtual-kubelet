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
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	v1 "k8s.io/api/core/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
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

func TestEmitTopologyUsesCycleSpanAndLinkCap(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer func() { _ = tp.Shutdown(context.Background()) }()

	driver := &fakeTopologyDriver{
		deviceInfo: &common.DeviceInfo{Hostname: "edge-01", ProductID: "C9300", SoftwareVersion: "17.18.2"},
		cdp: []common.CDPNeighbor{
			{DeviceID: "n1", LocalInterface: "GigabitEthernet1/0/1"},
			{DeviceID: "n2", LocalInterface: "GigabitEthernet1/0/2"},
			{DeviceID: "n3", LocalInterface: "GigabitEthernet1/0/3"},
		},
	}
	exporter, err := NewOTELTopologyExporter(
		context.Background(),
		driver,
		driver,
		&v1alpha1.OTELConfig{MaxLinkSpans: 2},
		"edge-01",
		"192.0.2.10",
		tp,
	)
	if err != nil {
		t.Fatalf("NewOTELTopologyExporter: %v", err)
	}
	exporter.emitTopology(context.Background())
	spans := recorder.Ended()
	if len(spans) != 3 {
		t.Fatalf("spans=%d want 3 (root + 2 capped links)", len(spans))
	}
	var rootFound bool
	for _, span := range spans {
		if span.Name() != "cvk.topology.cycle" {
			continue
		}
		rootFound = true
		attrs := span.Attributes()
		if got := attrInt(attrs, "topology.emitted_link_count"); got != 2 {
			t.Fatalf("emitted links=%d want 2", got)
		}
		if got := attrInt(attrs, "topology.dropped_link_count"); got != 1 {
			t.Fatalf("dropped links=%d want 1", got)
		}
		if attrString(attrs, "topology.cycle.id") == "" {
			t.Fatal("root span missing topology.cycle.id")
		}
	}
	if !rootFound {
		t.Fatal("missing cvk.topology.cycle root span")
	}
}

type fakeTopologyDriver struct {
	deviceInfo *common.DeviceInfo
	cdp        []common.CDPNeighbor
}

func (f *fakeTopologyDriver) GetDeviceResources(context.Context) (*v1.ResourceList, error) {
	return &v1.ResourceList{}, nil
}
func (f *fakeTopologyDriver) GetDeviceInfo(context.Context) (*common.DeviceInfo, error) {
	return f.deviceInfo, nil
}
func (f *fakeTopologyDriver) DeployPod(context.Context, *v1.Pod, corev1listers.SecretNamespaceLister, corev1listers.ConfigMapNamespaceLister) error {
	return nil
}
func (f *fakeTopologyDriver) UpdatePod(context.Context, *v1.Pod) error { return nil }
func (f *fakeTopologyDriver) DeletePod(context.Context, *v1.Pod) error { return nil }
func (f *fakeTopologyDriver) GetPodStatus(context.Context, *v1.Pod) (*v1.Pod, error) {
	return nil, nil
}
func (f *fakeTopologyDriver) ListPods(context.Context) ([]*v1.Pod, error) { return nil, nil }
func (f *fakeTopologyDriver) GetGlobalOperationalData(context.Context) (*common.AppHostingOperData, error) {
	return nil, nil
}
func (f *fakeTopologyDriver) GetCDPNeighbors(context.Context) ([]common.CDPNeighbor, error) {
	return f.cdp, nil
}
func (f *fakeTopologyDriver) GetOSPFNeighbors(context.Context) ([]common.OSPFNeighbor, error) {
	return nil, nil
}
func (f *fakeTopologyDriver) GetInterfaceStats(context.Context) ([]common.InterfaceStats, error) {
	return nil, nil
}
func (f *fakeTopologyDriver) GetInterfaceIPs(context.Context) ([]common.InterfaceIP, error) {
	return nil, nil
}
func (f *fakeTopologyDriver) GetHostedApps(context.Context) ([]common.HostedApp, error) {
	return nil, nil
}

func attrInt(attrs []attribute.KeyValue, key string) int64 {
	for _, attr := range attrs {
		if string(attr.Key) == key {
			return attr.Value.AsInt64()
		}
	}
	return 0
}

func attrString(attrs []attribute.KeyValue, key string) string {
	for _, attr := range attrs {
		if string(attr.Key) == key {
			return attr.Value.AsString()
		}
	}
	return ""
}
