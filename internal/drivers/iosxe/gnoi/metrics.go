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

package gnoi

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	metricsOnce sync.Once

	rpcTotal                     *prometheus.CounterVec
	capabilityEvents             *prometheus.CounterVec
	certificateEarliestExpiry    prometheus.Gauge
	certificateInventoryObserved prometheus.Gauge
	certificateInventoryUnparsed prometheus.Gauge
	certificateInventoryMu       sync.Mutex
	certificateInventoryTime     time.Time
)

// RegisterMetrics registers gNOI client metrics. It is safe to call more
// than once in a process; the first registry wins.
func RegisterMetrics(reg prometheus.Registerer) {
	metricsOnce.Do(func() {
		rpcTotal = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cisco_vk_gnoi_rpc_total",
				Help: "Count of gNOI RPC outcomes observed by the IOS-XE gNOI client.",
			},
			[]string{"service", "outcome"},
		)
		capabilityEvents = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "cisco_vk_gnoi_capability_cache_total",
				Help: "Count of gNOI capability-cache hits, misses, expirations, pins, and fail-fast decisions.",
			},
			[]string{"service", "result"},
		)
		certificateEarliestExpiry = prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cisco_vk_gnoi_certificate_earliest_expiry_timestamp_seconds",
			Help: "Earliest expiry of parseable certificates in the last successful gNOI inventory, including inactive identities; zero when none are parseable.",
		})
		certificateInventoryObserved = prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cisco_vk_gnoi_certificate_inventory_observed_timestamp_seconds",
			Help: "Unix time of the last successful gNOI GetCertificates inventory; zero until observed. Scraping metrics does not refresh inventory.",
		})
		certificateInventoryUnparsed = prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cisco_vk_gnoi_certificate_inventory_unparsed",
			Help: "Certificates without parseable X.509 validity in the last successful gNOI inventory.",
		})
		reg.MustRegister(rpcTotal, capabilityEvents, certificateEarliestExpiry, certificateInventoryObserved, certificateInventoryUnparsed)
	})
}

func recordCertificateInventory(certs []CertificateInfo, observed time.Time) {
	if certificateInventoryObserved == nil {
		return
	}
	var earliest *time.Time
	unparsed := 0
	for _, cert := range certs {
		if cert.NotAfter == nil {
			unparsed++
		} else if earliest == nil || cert.NotAfter.Before(*earliest) {
			earliest = cert.NotAfter
		}
	}
	expiry := float64(0)
	if earliest != nil {
		expiry = float64(earliest.Unix())
	}
	// Concurrent read-only probes and provisioning share these per-worker
	// gauges. Keep a completed observation consistent and never regress its
	// timestamp when an older caller reaches this lock after a newer one.
	certificateInventoryMu.Lock()
	defer certificateInventoryMu.Unlock()
	if observed.Before(certificateInventoryTime) {
		return
	}
	certificateInventoryTime = observed
	certificateEarliestExpiry.Set(expiry)
	certificateInventoryUnparsed.Set(float64(unparsed))
	certificateInventoryObserved.Set(float64(observed.Unix()))
}

func recordRPC(svc Service, err error) {
	if rpcTotal == nil {
		return
	}
	rpcTotal.WithLabelValues(string(svc), rpcOutcome(err)).Inc()
}

func recordCapabilityEvent(svc Service, result string) {
	if capabilityEvents == nil {
		return
	}
	capabilityEvents.WithLabelValues(string(svc), result).Inc()
}

func rpcOutcome(err error) string {
	if err == nil {
		return "ok"
	}
	switch status.Code(err) {
	case codes.Unauthenticated:
		return "unauthenticated"
	case codes.PermissionDenied:
		return "permission_denied"
	case codes.FailedPrecondition:
		return "failed_precondition"
	case codes.Unimplemented:
		return "unimplemented"
	case codes.DeadlineExceeded:
		return "deadline_exceeded"
	case codes.Canceled:
		return "canceled"
	case codes.Unavailable:
		return "unavailable"
	default:
		return "error"
	}
}
