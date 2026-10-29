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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"github.com/virtual-kubelet/virtual-kubelet/node/api"
	"github.com/virtual-kubelet/virtual-kubelet/node/nodeutil"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	statsv1alpha1 "k8s.io/kubelet/pkg/apis/stats/v1alpha1"
)

type virtualKubeletHTTPTestDriver struct {
	*nodeCapacityTestDriver
}

func (*virtualKubeletHTTPTestDriver) ListPods(context.Context) ([]*v1.Pod, error) {
	return []*v1.Pod{{ObjectMeta: metav1.ObjectMeta{
		Namespace: "workloads", Name: "device-pod", UID: "device-pod-uid",
	}}}, nil
}

func newVirtualKubeletHTTPCompatibilityHandler(t *testing.T) http.Handler {
	t.Helper()
	ctx := t.Context()
	driver := &virtualKubeletHTTPTestDriver{nodeCapacityTestDriver: &nodeCapacityTestDriver{
		operData: &common.AppHostingOperData{
			IoxEnabled: true,
			SystemCPU:  common.AppResource{Quota: 100, Available: 25, Unit: "percentage"},
			Memory:     common.AppResource{Quota: 4096, Available: 512, Unit: "MB"},
			Storage:    common.AppResource{Quota: 11012, Available: 9025, Unit: "MB"},
		},
	}}
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{
		cache.NamespaceIndex: cache.MetaNamespaceIndexFunc,
	})
	if err := indexer.Add(&v1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: "workloads", Name: "scheduled-pod", UID: "scheduled-pod-uid",
	}}); err != nil {
		t.Fatal(err)
	}
	vkCfg := nodeutil.ProviderConfig{Pods: corev1listers.NewPodLister(indexer)}
	spec := &v1alpha1.DeviceSpec{}
	node := NewAppHostingNode(ctx, "edge-01", spec, driver)
	podProvider, err := NewAppHostingProvider(ctx, spec, vkCfg, driver, node, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Exercise the same supported-operation boundary as cmd/cisco-vk/run.go.
	// Calling unsupported provider stubs would return 500 instead of 501.
	mux := http.NewServeMux()
	mux.Handle("/", api.PodHandler(api.PodHandlerConfig{
		GetPods: podProvider.GetPods,
		GetPodsFromKubernetes: func(context.Context) ([]*v1.Pod, error) {
			return vkCfg.Pods.List(labels.Everything())
		},
		StreamIdleTimeout:     0,
		StreamCreationTimeout: 0,
		RunInContainer:        nil,
		AttachToContainer:     nil,
		GetContainerLogs:      nil,
		PortForward:           nil,
		GetStatsSummary:       podProvider.GetStatsSummary,
		GetMetricsResource:    podProvider.GetMetricsResource,
	}, true))
	return mux
}

func TestVirtualKubeletHTTPUnsupportedOperations(t *testing.T) {
	handler := newVirtualKubeletHTTPCompatibilityHandler(t)
	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/containerLogs/workloads/pod/container"},
		{http.MethodGet, "/exec/workloads/pod/container"},
		{http.MethodPost, "/exec/workloads/pod/container"},
		{http.MethodGet, "/attach/workloads/pod/container"},
		{http.MethodPost, "/attach/workloads/pod/container"},
		{http.MethodGet, "/portForward/workloads/pod"},
		{http.MethodPost, "/portForward/workloads/pod"},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, nil))
			if response.Code != http.StatusNotImplemented {
				t.Fatalf("status = %d, want 501; body = %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestVirtualKubeletHTTPStatsSummary(t *testing.T) {
	handler := newVirtualKubeletHTTPCompatibilityHandler(t)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/stats/summary", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", response.Code, response.Body.String())
	}
	var summary statsv1alpha1.Summary
	if err := json.Unmarshal(response.Body.Bytes(), &summary); err != nil {
		t.Fatalf("decode kubelet stats summary: %v", err)
	}
	if summary.Node.NodeName != "edge-01" {
		t.Errorf("node name = %q, want edge-01", summary.Node.NodeName)
	}
	if summary.Node.CPU == nil || summary.Node.CPU.UsageNanoCores == nil || *summary.Node.CPU.UsageNanoCores != 750000000 {
		t.Errorf("CPU stats = %#v, want 750000000 nanocores", summary.Node.CPU)
	}
	if summary.Node.Memory == nil || summary.Node.Memory.AvailableBytes == nil || *summary.Node.Memory.AvailableBytes != 512*1024*1024 {
		t.Errorf("memory stats = %#v, want 512 MiB available", summary.Node.Memory)
	}
	if summary.Node.Fs == nil || summary.Node.Fs.AvailableBytes == nil || *summary.Node.Fs.AvailableBytes != 9025*1024*1024 {
		t.Errorf("filesystem stats = %#v, want 9025 MiB available", summary.Node.Fs)
	}
}

func TestVirtualKubeletHTTPMetricsResource(t *testing.T) {
	handler := newVirtualKubeletHTTPCompatibilityHandler(t)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics/resource", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != api.PrometheusTextFormatContentType {
		t.Errorf("Content-Type = %q, want %q", got, api.PrometheusTextFormatContentType)
	}
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(response.Body)
	if err != nil {
		t.Fatalf("decode Prometheus resource metrics: %v", err)
	}
	for name, want := range map[string]float64{
		"node_memory_working_set_bytes":   (4096 - 512) * 1024 * 1024,
		"cisco_device_cpu_usage_percent":  75,
		"cisco_device_storage_used_bytes": (11012 - 9025) * 1024 * 1024,
	} {
		family := families[name]
		if family == nil || len(family.Metric) != 1 || family.Metric[0].Gauge == nil {
			t.Errorf("missing gauge %s: %#v", name, family)
			continue
		}
		if got := family.Metric[0].Gauge.GetValue(); got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}
}

func TestVirtualKubeletHTTPPodLists(t *testing.T) {
	handler := newVirtualKubeletHTTPCompatibilityHandler(t)
	for _, tc := range []struct {
		path string
		name string
	}{
		{"/pods", "scheduled-pod"},
		{"/runningpods/", "device-pod"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body = %s", response.Code, response.Body.String())
			}
			var pods v1.PodList
			if err := json.Unmarshal(response.Body.Bytes(), &pods); err != nil {
				t.Fatalf("decode Kubernetes PodList: %v", err)
			}
			if pods.Kind != "PodList" || pods.APIVersion != "v1" {
				t.Errorf("response type = %s/%s, want v1/PodList", pods.APIVersion, pods.Kind)
			}
			if len(pods.Items) != 1 || pods.Items[0].Namespace != "workloads" || pods.Items[0].Name != tc.name {
				t.Fatalf("pods = %#v, want only workloads/%s", pods.Items, tc.name)
			}
		})
	}
}
