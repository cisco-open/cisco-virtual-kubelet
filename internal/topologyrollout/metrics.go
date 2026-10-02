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
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	metricsOnce sync.Once

	reconciliationsTotal  *prometheus.CounterVec
	targetTransitions     *prometheus.CounterVec
	admissionWaits        *prometheus.CounterVec
	reconcileDuration     *prometheus.HistogramVec
	ledgerActive          prometheus.Gauge
	ledgerBytes           prometheus.Gauge
	ledgerConflictRetries prometheus.Counter
)

// RegisterMetrics registers topology-rollout metrics. Every label is selected
// from a fixed vocabulary; campaign, device, domain, image, and policy values
// belong in bounded status, Events, and structured logs instead of Prometheus.
func RegisterMetrics(reg prometheus.Registerer) {
	if reg == nil {
		return
	}
	metricsOnce.Do(func() {
		reconciliationsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cisco_vk_topology_rollout_reconciliations_total",
			Help: "Topology rollout reconciliations by bounded result and reason class.",
		}, []string{"result", "reason"})
		targetTransitions = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cisco_vk_topology_rollout_target_transitions_total",
			Help: "Topology rollout target-summary transitions using bounded states and reason classes.",
		}, []string{"from", "to", "reason"})
		admissionWaits = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cisco_vk_topology_rollout_admission_waits_total",
			Help: "Topology rollout admission waits by bounded reason class.",
		}, []string{"reason"})
		reconcileDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "cisco_vk_topology_rollout_reconcile_duration_seconds",
			Help:    "Topology rollout reconciliation latency by bounded result and reason class.",
			Buckets: prometheus.ExponentialBuckets(0.005, 2, 12),
		}, []string{"result", "reason"})
		ledgerActive = prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cisco_vk_topology_rollout_ledger_active_reservations",
			Help: "Active or unresolved reservations in the authoritative topology rollout ledger.",
		})
		ledgerBytes = prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cisco_vk_topology_rollout_ledger_serialized_bytes",
			Help: "Serialized byte size of the authoritative topology rollout ledger.",
		})
		ledgerConflictRetries = prometheus.NewCounter(prometheus.CounterOpts{
			Name: "cisco_vk_topology_rollout_ledger_conflict_retries_total",
			Help: "Kubernetes resourceVersion conflicts retried while mutating the topology rollout ledger.",
		})
		reg.MustRegister(reconciliationsTotal, targetTransitions, admissionWaits, reconcileDuration,
			ledgerActive, ledgerBytes, ledgerConflictRetries)
	})
}

// RecordReconcile records one reconciliation without exporting object names or
// raw errors. Result is normalized even if a future caller supplies a new one.
func RecordReconcile(result string, err error) {
	if reconciliationsTotal == nil {
		return
	}
	result, reason := boundedReconcileLabels(result, err)
	reconciliationsTotal.WithLabelValues(result, reason).Inc()
}

// RecordReconcileDuration records controller latency using the same bounded
// vocabulary as RecordReconcile. Raw object identities and error strings are
// deliberately excluded from labels.
func RecordReconcileDuration(result string, err error, duration time.Duration) {
	if reconcileDuration == nil {
		return
	}
	result, reason := boundedReconcileLabels(result, err)
	if duration < 0 {
		duration = 0
	}
	reconcileDuration.WithLabelValues(result, reason).Observe(duration.Seconds())
}

// RecordLedgerConflictRetry increments a label-free contention signal. It is
// called only when the API server returns an optimistic-lock conflict.
func RecordLedgerConflictRetry() {
	if ledgerConflictRetries != nil {
		ledgerConflictRetries.Inc()
	}
}

func boundedReconcileLabels(result string, err error) (string, string) {
	if result != "complete" && result != "requeue" && result != "error" {
		result = "other"
	}
	return result, reconcileReason(err)
}

// RecordTargetTransition records a persisted target-summary transition.
func RecordTargetTransition(from, to, reason string) {
	if targetTransitions == nil {
		return
	}
	from = targetPhaseClass(from)
	to = targetPhaseClass(to)
	reason = targetReasonClass(reason)
	targetTransitions.WithLabelValues(from, to, reason).Inc()
	if to == "blocked" || to == "waiting_for_admission" {
		admissionWaits.WithLabelValues(reason).Inc()
	}
}

func observeLedger(ledger *Ledger, serializedBytes int) {
	if ledgerActive == nil || ledgerBytes == nil || ledger == nil {
		return
	}
	ledgerActive.Set(float64(len(ledger.Reservations)))
	if serializedBytes >= 0 {
		ledgerBytes.Set(float64(serializedBytes))
	}
}

func reconcileReason(err error) string {
	if err == nil {
		return "none"
	}
	switch {
	case errors.Is(err, ErrBudgetExceeded):
		return "budget"
	case errors.Is(err, ErrTargetUnavailable):
		return "health"
	case errors.Is(err, ErrLedgerIdentity):
		return "ledger_identity"
	case errors.Is(err, ErrLedgerFull):
		return "ledger_capacity"
	case errors.Is(err, ErrAlreadyReserved):
		return "already_reserved"
	case errors.Is(err, ErrStaleControlRevision):
		return "stale_control"
	case errors.Is(err, ErrInvalidTransition):
		return "invalid_transition"
	default:
		return "other"
	}
}

func targetPhaseClass(value string) string {
	switch value {
	case "":
		return "none"
	case "Planned":
		return "planned"
	case "WaitingForAdmission":
		return "waiting_for_admission"
	case "Admitted":
		return "admitted"
	case "Running":
		return "running"
	case "Soaking":
		return "soaking"
	case "Succeeded":
		return "succeeded"
	case "Failed":
		return "failed"
	case "Blocked":
		return "blocked"
	case "Cancelling":
		return "cancelling"
	case "Cancelled":
		return "cancelled"
	default:
		return "other"
	}
}

func targetReasonClass(value string) string {
	switch value {
	case "":
		return "none"
	case "AdmissionBindingRecovered":
		return "binding_recovered"
	case "ReservationGranted":
		return "reservation_granted"
	case "WorkerProtocolPending":
		return "worker_pending"
	case "AdmissionBlocked":
		return "budget_or_health"
	case "MutationOutcomeUnresolved":
		return "mutation_unresolved"
	case "PostMutationHealthGate", "CancellationHealthGate":
		return "health_gate"
	case "HealthyPostMutationSoak":
		return "soak"
	case "HealthGatePassed":
		return "health_passed"
	case "CancelledBeforeAdmission", "CancelledBeforeMutation":
		return "cancelled_unclaimed"
	case "CancelledAfterOutcome":
		return "cancelled_settled"
	case "AcceptedMutationConverging":
		return "claimed_converging"
	case "Pending", "Resolving", "Transferring", "Installing", "Activating", "Verifying", "RollingBack":
		return "leaf_progress"
	case "Succeeded", "Prepared", "StagedForNextBoot":
		return "leaf_success"
	case "Failed", "PreflightFailed", "ValidationFailed", "RolledBack", "RebootTimeout", "Cancelled":
		return "leaf_failure"
	default:
		return "other"
	}
}
