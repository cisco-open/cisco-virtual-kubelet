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

package engine

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

// Metrics are registered exactly once and exposed via the shared
// controller-runtime registry when the reconciler starts. The
// per-reconcile observation calls are inlined into the engine's hot
// path so every tick produces a comparable timeseries.

var (
	metricsOnce sync.Once

	reconcileDuration     *prometheus.HistogramVec
	applyDuration         *prometheus.HistogramVec
	driftDetected         *prometheus.CounterVec
	driftCorrected        *prometheus.CounterVec
	driftEntriesTruncated *prometheus.CounterVec
	applyErrors           *prometheus.CounterVec
	familyState           *prometheus.GaugeVec

	// Transport-aware counters added per the pre-PR test enrichment
	// plan §3 — production-readiness live tests must be able to
	// assert that the *intended* transport performed the work, not
	// just that the device ended up with the right state. The three
	// counters below give live tests hard evidence labelled by
	// transport kind.
	transactionsTotal *prometheus.CounterVec // outcome ∈ {commit, discard, start_failed, commit_failed}
	saveStartupTotal  *prometheus.CounterVec // outcome ∈ {ok, failed}
	mutateOpsTotal    *prometheus.CounterVec // verb ∈ {REPLACE, MERGE, DELETE, CLI}
)

// MaxDriftEntries caps status.drift[] on each IOSXEConfig CR. Drift
// is fundamentally unbounded — a brand-new device pointed at a
// detailed CR could surface thousands of leaves on the first tick —
// and an unbounded slice on a status subresource bloats etcd writes
// and informer cache memory linearly. Truncation surfaces in the
// cisco_vk_config_drift_entries_truncated_total counter so operators
// can alert on it without inspecting every CR.
const MaxDriftEntries = 50

// CapDrift returns the slice trimmed to MaxDriftEntries plus the
// number of entries that didn't make the cut. The retained slice is
// the head of the input — callers that want a different selection
// (e.g. priority-ranked) should sort before calling.
func CapDrift(in []DriftEntry) (out []DriftEntry, dropped int) {
	if len(in) <= MaxDriftEntries {
		return in, 0
	}
	return in[:MaxDriftEntries], len(in) - MaxDriftEntries
}

// RecordDriftTruncated bumps the truncation counter by dropped. A
// no-op when metrics aren't registered (unit tests, in-process
// callers).
func RecordDriftTruncated(device string, dropped int) {
	if driftEntriesTruncated == nil || dropped <= 0 {
		return
	}
	driftEntriesTruncated.WithLabelValues(device).Add(float64(dropped))
}

// RegisterMetrics registers the engine's metric set on reg. It is
// idempotent — repeated calls are safe — because controller-runtime's
// metrics registry is reused across reconcilers.
func RegisterMetrics(reg prometheus.Registerer) {
	metricsOnce.Do(func() {
		reconcileDuration = prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "cisco_vk_config_reconcile_duration_seconds",
				Help:    "Duration of one IOSXEConfig reconcile tick (engine.Reconcile).",
				Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
			},
			[]string{"device", "phase"},
		)
		applyDuration = prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "cisco_vk_config_apply_duration_seconds",
				Help:    "Duration of per-family Apply calls.",
				Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
			},
			[]string{"device", "family"},
		)
		driftDetected = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cisco_vk_config_drift_detected_total",
				Help: "Number of reconcile ticks where a family was found drifted.",
			},
			[]string{"device", "family"},
		)
		driftCorrected = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cisco_vk_config_drift_corrected_total",
				Help: "Number of reconcile ticks where drift was corrected (apply succeeded and verify was clean).",
			},
			[]string{"device", "family"},
		)
		driftEntriesTruncated = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cisco_vk_config_drift_entries_truncated_total",
				Help: "Number of drift entries dropped when status.drift[] was capped at MaxDriftEntries on the CR.",
			},
			[]string{"device"},
		)
		applyErrors = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cisco_vk_config_apply_errors_total",
				Help: "Number of per-family apply errors (Fetch / Diff / Apply / Verify).",
			},
			[]string{"device", "family", "stage"},
		)
		familyState = prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "cisco_vk_config_family_state",
				Help: "Current per-family state (0=InSync, 1=Drifted, 2=ApplyError, 3=Skipped, 4=Unsupported).",
			},
			[]string{"device", "family"},
		)
		transactionsTotal = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cisco_vk_config_transactions_total",
				Help: "Transactional reconcile lifecycle outcomes. outcome=commit when the engine successfully committed a candidate datastore; discard when the deferred cleanup ran (apply failure or non-clean phase); start_failed / commit_failed for the corresponding RPC errors. Live tests use this to prove the NETCONF candidate path actually ran instead of the engine silently degrading to a non-transactional apply.",
			},
			[]string{"device", "transport", "outcome"},
		)
		saveStartupTotal = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cisco_vk_config_save_startup_total",
				Help: "Outcomes of the post-apply running-to-startup copy. Only fires when spec.writeStartup is true AND the transport reports SupportsSaveStartup AND the apply phase reached InSync. outcome=ok or failed.",
			},
			[]string{"device", "transport", "outcome"},
		)
		mutateOpsTotal = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cisco_vk_config_mutate_ops_total",
				Help: "Per-verb count of mutation ops the engine emitted into the transport (REPLACE, MERGE, DELETE, CLI). Labelled by transport kind so live tests can assert the verbs landed on the intended wire format. Pure-read reconciles (Phase=InSync with no drift) increment nothing.",
			},
			[]string{"device", "transport", "verb"},
		)

		reg.MustRegister(
			reconcileDuration,
			applyDuration,
			driftDetected,
			driftCorrected,
			driftEntriesTruncated,
			applyErrors,
			familyState,
			transactionsTotal,
			saveStartupTotal,
			mutateOpsTotal,
		)
	})
}

// recordTransaction bumps the transactional-lifecycle counter. No-op
// when metrics are unregistered (unit tests).
func recordTransaction(device, transportKind, outcome string) {
	if transactionsTotal == nil {
		return
	}
	transactionsTotal.WithLabelValues(device, transportKind, outcome).Inc()
}

// recordSaveStartup bumps the save-startup counter. No-op when
// metrics are unregistered.
func recordSaveStartup(device, transportKind, outcome string) {
	if saveStartupTotal == nil {
		return
	}
	saveStartupTotal.WithLabelValues(device, transportKind, outcome).Inc()
}

// recordMutateOps bumps the per-verb mutation-ops counter once per
// op in the slice. No-op when metrics are unregistered.
func recordMutateOps(device, transportKind string, ops []transport.Op) {
	if mutateOpsTotal == nil {
		return
	}
	for _, op := range ops {
		mutateOpsTotal.WithLabelValues(device, transportKind, string(op.Verb)).Inc()
	}
}

// transportKindLabel pulls the transport's Kind for use as a metric
// label. Returns "unknown" when the engine's Transport is nil
// (defensive — production code paths always set it; some unit tests
// drive reconcileFamily without a transport).
func transportKindLabel(t transport.Interface) string {
	if t == nil {
		return "unknown"
	}
	return string(t.Capabilities().Kind)
}

// recordResult folds a Result into the registered metric set. It is a
// no-op when RegisterMetrics has not been called, so unit tests do not
// need to worry about registration bookkeeping.
func recordResult(device string, r Result, duration float64) {
	if reconcileDuration == nil {
		return
	}
	reconcileDuration.WithLabelValues(device, r.Phase).Observe(duration)
	for _, fs := range r.FamilyStatuses {
		familyState.WithLabelValues(device, fs.Name).Set(familyStateValue(fs.State))
		switch fs.State {
		case "Drifted":
			driftDetected.WithLabelValues(device, fs.Name).Inc()
		case "InSync":
			// Only count as corrected when the tick actually wrote.
			if fs.OpCount > 0 {
				driftCorrected.WithLabelValues(device, fs.Name).Inc()
			}
		case "ApplyError":
			applyErrors.WithLabelValues(device, fs.Name, stageFromMessage(fs.Message)).Inc()
		}
	}
}

// familyStateValue maps the string state to a stable integer.
// Alerting rules can express "state != 0" cleanly.
func familyStateValue(state string) float64 {
	switch state {
	case "InSync":
		return 0
	case "Drifted":
		return 1
	case "ApplyError":
		return 2
	case "Skipped":
		return 3
	case "Unsupported":
		return 4
	default:
		return -1
	}
}

// stageFromMessage parses the "Fetch:"/"Diff:"/"Apply:"/"Verify:"
// prefix the engine prepends to FamilyStatus.Message. Unknown shapes
// fall back to "unknown" so the metric cardinality stays bounded.
func stageFromMessage(msg string) string {
	switch {
	case len(msg) >= 6 && msg[:6] == "Fetch:":
		return "fetch"
	case len(msg) >= 5 && msg[:5] == "Diff:":
		return "diff"
	case len(msg) >= 6 && msg[:6] == "Apply:":
		return "apply"
	case len(msg) >= 7 && msg[:7] == "Verify:":
		return "verify"
	default:
		return "unknown"
	}
}
