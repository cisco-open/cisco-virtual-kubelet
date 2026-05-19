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

	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	metricsOnce sync.Once

	rpcTotal         *prometheus.CounterVec
	capabilityEvents *prometheus.CounterVec
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
		reg.MustRegister(rpcTotal, capabilityEvents)
	})
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
