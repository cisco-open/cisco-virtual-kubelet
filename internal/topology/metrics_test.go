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

package topology

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestProjectionMetricsUseOnlyBoundedResults(t *testing.T) {
	registry := prometheus.NewPedanticRegistry()
	RegisterMetrics(registry)
	RecordProjectionReconcile("projected")
	RecordProjectionReconcile("a-device-or-error-name")

	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if len(families) != 1 {
		t.Fatalf("metric families = %d, want 1", len(families))
	}
	family := families[0]
	if got := family.GetName(); got != "cisco_vk_topology_projection_reconciliations_total" {
		t.Fatalf("metric name = %q", got)
	}
	want := map[string]float64{"projected": 1, "other": 1}
	for _, metric := range family.Metric {
		if len(metric.Label) != 1 || metric.Label[0].GetName() != "result" {
			t.Fatalf("unexpected labels: %#v", metric.Label)
		}
		value := metric.Label[0].GetValue()
		if got, ok := want[value]; !ok || metric.Counter.GetValue() != got {
			t.Fatalf("unexpected result %q=%v", value, metric.Counter.GetValue())
		}
		delete(want, value)
	}
	if len(want) != 0 {
		t.Fatalf("missing result series: %#v", want)
	}
}
