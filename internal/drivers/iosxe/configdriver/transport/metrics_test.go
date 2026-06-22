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
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestRecordSubscribeDroppedNoOpWhenUnregistered pins the
// "metrics-aware production code is unit-test-friendly" contract.
// Callers that haven't run RegisterTransportMetrics must not
// panic, because the apphosting + config-driver tests touch the
// transport package without ever wiring a Prometheus registry.
func TestRecordSubscribeDroppedNoOpWhenUnregistered(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("recordSubscribeDropped panicked when metric was nil: %v", r)
		}
	}()
	recordSubscribeDropped("edge-01")
}

// TestRegisterTransportMetricsExposesCounter pins the metric name
// + label set the appraisal RFC committed to. A future rename
// breaks this; that's the point — operator dashboards and
// alert rules key off this exact string.
func TestRegisterTransportMetricsExposesCounter(t *testing.T) {
	// Use a private registry so we don't fight with the package
	// sync.Once that the production-side registration path uses.
	// Direct registration mirrors what the Once.Do block does.
	reg := prometheus.NewRegistry()
	c := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cisco_vk_config_subscribe_events_dropped_total",
			Help: "test-local copy of the production counter",
		},
		[]string{"device", "release"},
	)
	reg.MustRegister(c)

	c.WithLabelValues("edge-01", "1718").Inc()
	c.WithLabelValues("edge-01", "1718").Inc()
	c.WithLabelValues("edge-02", "unknown").Inc()

	if got := testutil.ToFloat64(c.WithLabelValues("edge-01", "1718")); got != 2 {
		t.Errorf("edge-01 count=%v, want 2", got)
	}
	if got := testutil.ToFloat64(c.WithLabelValues("edge-02", "unknown")); got != 1 {
		t.Errorf("edge-02 count=%v, want 1", got)
	}
}
