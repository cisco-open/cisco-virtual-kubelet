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

package softwareupgrade

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestImageTransferMetricsUseBoundedLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	RegisterMetrics(registry)
	recordPhaseTransition("device-a.example", "secret-version", "Pending", "Transferring", "test")
	recordImageTransfer("source/device-a.example", "https://secret.example", "digest-a", "future-result", 1234, time.Second)

	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if len(families) != 3 {
		t.Fatalf("metric family count = %d, want 3", len(families))
	}
	for _, family := range families {
		if !strings.Contains(family.GetName(), "transfer_") {
			continue
		}
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if strings.Contains(label.GetValue(), "device-a") || strings.Contains(label.GetValue(), "secret") || strings.Contains(label.GetValue(), "digest") {
					t.Fatalf("unbounded transfer label %s=%q", label.GetName(), label.GetValue())
				}
			}
		}
	}
}
