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

package topologyrollout

import (
	"errors"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestTopologyRolloutMetricsUseOnlyBoundedLabels(t *testing.T) {
	registry := prometheus.NewRegistry()
	RegisterMetrics(registry)

	RecordReconcile("unexpected-result", errors.Join(ErrBudgetExceeded, errors.New("device-a.example")))
	RecordTargetTransition("future-state-device-a", "Blocked", "unbounded-device-a.example")
	observeLedger(&Ledger{Reservations: map[string]Reservation{"sensitive-reservation": {}}}, 1234)

	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	if len(families) != 5 {
		t.Fatalf("metric family count = %d, want 5", len(families))
	}
	for _, family := range families {
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				switch label.GetName() {
				case "result":
					if label.GetValue() != "other" {
						t.Fatalf("result label = %q, want bounded other", label.GetValue())
					}
				case "reason":
					if label.GetValue() != "budget" && label.GetValue() != "other" {
						t.Fatalf("reason label = %q, want bounded class", label.GetValue())
					}
				case "from":
					if label.GetValue() != "other" {
						t.Fatalf("from label = %q, want bounded other", label.GetValue())
					}
				case "to":
					if label.GetValue() != "blocked" {
						t.Fatalf("to label = %q, want blocked", label.GetValue())
					}
				default:
					t.Fatalf("unexpected metric label %q", label.GetName())
				}
			}
		}
	}
}
