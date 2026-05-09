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

// Package classifier classifies canonical telemetry paths into OTel metric
// kinds. Unknown paths intentionally fall back to gauges.
package classifier

import (
	"sort"
	"strings"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

type Classifier interface {
	Classify(canonicalPath string) MetricKind
}

type MetricKind string

const (
	MetricKindGauge MetricKind = "gauge"
	MetricKindSum   MetricKind = "sum"
)

type rule struct {
	prefix string
	kind   MetricKind
}

type prefixClassifier struct {
	rules    []rule
	fallback Classifier
}

type gaugeClassifier struct{}

func OverrideClassifier(rules []configv1alpha1.MetricTypeOverride, fallback Classifier) Classifier {
	out := make([]rule, 0, len(rules))
	for _, r := range rules {
		kind := MetricKind(r.Type)
		switch kind {
		case MetricKindGauge, MetricKindSum:
		default:
			continue
		}
		prefix := normalizePath(r.Prefix)
		if prefix == "" {
			continue
		}
		out = append(out, rule{prefix: prefix, kind: kind})
	}
	sortRules(out)
	return prefixClassifier{rules: out, fallback: fallback}
}

func CuratedClassifier() Classifier {
	rules := append([]rule(nil), curatedRules...)
	sortRules(rules)
	return prefixClassifier{rules: rules, fallback: gaugeClassifier{}}
}

func (c prefixClassifier) Classify(canonicalPath string) MetricKind {
	path := normalizePath(canonicalPath)
	for _, r := range c.rules {
		if pathMatchesPrefix(path, r.prefix) {
			return r.kind
		}
	}
	if c.fallback != nil {
		return c.fallback.Classify(path)
	}
	return MetricKindGauge
}

func (g gaugeClassifier) Classify(string) MetricKind {
	return MetricKindGauge
}

func sortRules(rules []rule) {
	sort.SliceStable(rules, func(i, j int) bool {
		return len(rules[i].prefix) > len(rules[j].prefix)
	})
}

func pathMatchesPrefix(path, prefix string) bool {
	if path == prefix {
		return true
	}
	return strings.HasPrefix(path, strings.TrimSuffix(prefix, "/")+"/")
}

func normalizePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if path == "/" {
		return "/"
	}
	path = strings.TrimPrefix(path, "/")
	parts := strings.Split(path, "/")
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if idx := strings.IndexByte(part, ':'); idx >= 0 {
			part = part[idx+1:]
		}
		if idx := strings.IndexByte(part, '['); idx >= 0 {
			part = part[:idx]
		}
		if part != "" {
			normalized = append(normalized, part)
		}
	}
	if len(normalized) == 0 {
		return "/"
	}
	return "/" + strings.Join(normalized, "/")
}

var curatedRules = mustNormalizeRules([]rule{
	{"/interfaces/interface/state/counters/in-octets", MetricKindSum},
	{"/interfaces/interface/state/counters/out-octets", MetricKindSum},
	{"/interfaces/interface/state/counters/in-unicast-pkts", MetricKindSum},
	{"/interfaces/interface/state/counters/in-broadcast-pkts", MetricKindSum},
	{"/interfaces/interface/state/counters/in-multicast-pkts", MetricKindSum},
	{"/interfaces/interface/state/counters/in-discards", MetricKindSum},
	{"/interfaces/interface/state/counters/in-errors", MetricKindSum},
	{"/interfaces/interface/state/counters/in-unknown-protos", MetricKindSum},
	{"/interfaces/interface/state/counters/out-unicast-pkts", MetricKindSum},
	{"/interfaces/interface/state/counters/out-broadcast-pkts", MetricKindSum},
	{"/interfaces/interface/state/counters/out-multicast-pkts", MetricKindSum},
	{"/interfaces/interface/state/counters/out-discards", MetricKindSum},
	{"/interfaces/interface/state/counters/out-errors", MetricKindSum},
	{"/interfaces/interface/state/counters/carrier-transitions", MetricKindSum},
	{"/interfaces/interface/state/counters", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/statistics/in-broadcast-pkts", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/statistics/in-crc-errors", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/statistics/in-discards", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/statistics/in-discards-64", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/statistics/in-errors", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/statistics/in-errors-64", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/statistics/in-multicast-pkts", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/statistics/in-octets", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/statistics/in-unicast-pkts", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/statistics/in-unknown-protos", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/statistics/in-unknown-protos-64", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/statistics/num-flaps", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/statistics/out-broadcast-pkts", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/statistics/out-discards", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/statistics/out-errors", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/statistics/out-multicast-pkts", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/statistics/out-octets", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/statistics/out-octets-64", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/statistics/out-unicast-pkts", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/statistics", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/v4-protocol-stats/in-pkts", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/v4-protocol-stats/in-octets", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/v4-protocol-stats/out-pkts", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/v4-protocol-stats/out-octets", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/v6-protocol-stats/in-pkts", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/v6-protocol-stats/in-octets", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/v6-protocol-stats/out-pkts", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/v6-protocol-stats/out-octets", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/diffserv-info/diffserv-target-classifier-stats/marking-stats/marking-atm-clp-stats-val/marked-pkts", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/diffserv-info/diffserv-target-classifier-stats/marking-stats/marking-cos-inner-stats-val/marked-pkts", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/diffserv-info/diffserv-target-classifier-stats/marking-stats/marking-cos-stats-val/marked-pkts", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/diffserv-info/diffserv-target-classifier-stats/marking-stats/marking-dei-imp-stats-val/marked-pkts", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/diffserv-info/diffserv-target-classifier-stats/marking-stats/marking-dei-stats-val/marked-pkts", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/diffserv-info/diffserv-target-classifier-stats/marking-stats/marking-discard-class-stats-val/marked-pkts", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/diffserv-info/diffserv-target-classifier-stats/marking-stats/marking-dscp-stats-val/marked-pkts", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/diffserv-info/diffserv-target-classifier-stats/marking-stats/marking-mpls-exp-top-stats-val/marked-pkts", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/diffserv-info/diffserv-target-classifier-stats/meter-stats/accepted-pkts", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/diffserv-info/diffserv-target-classifier-stats/meter-stats/accepted-bytes", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/diffserv-info/diffserv-target-classifier-stats/meter-stats/exceeded-pkts", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/diffserv-info/diffserv-target-classifier-stats/meter-stats/exceeded-bytes", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/diffserv-info/diffserv-target-classifier-stats/queuing-stats/output-pkts", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/diffserv-info/diffserv-target-classifier-stats/queuing-stats/output-bytes", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/diffserv-info/diffserv-target-classifier-stats/queuing-stats/drop-pkts", MetricKindSum},
	{"/Cisco-IOS-XE-interfaces-oper:interfaces/interface/diffserv-info/diffserv-target-classifier-stats/queuing-stats/drop-bytes", MetricKindSum},
	{"/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data/app/network-utils/network-util/rx-bytes", MetricKindSum},
	{"/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data/app/network-utils/network-util/rx-errors", MetricKindSum},
	{"/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data/app/network-utils/network-util/rx-packets", MetricKindSum},
	{"/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data/app/network-utils/network-util/tx-bytes", MetricKindSum},
	{"/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data/app/network-utils/network-util/tx-errors", MetricKindSum},
	{"/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data/app/network-utils/network-util/tx-packets", MetricKindSum},
	{"/Cisco-IOS-XE-bgp-oper:bgp-state-data/neighbors/neighbor/messages/sent/updates", MetricKindSum},
	{"/Cisco-IOS-XE-bgp-oper:bgp-state-data/neighbors/neighbor/messages/sent/notifications", MetricKindSum},
	{"/Cisco-IOS-XE-bgp-oper:bgp-state-data/neighbors/neighbor/messages/sent/opens", MetricKindSum},
	{"/Cisco-IOS-XE-bgp-oper:bgp-state-data/neighbors/neighbor/messages/sent/keepalives", MetricKindSum},
	{"/Cisco-IOS-XE-bgp-oper:bgp-state-data/neighbors/neighbor/messages/sent/route-refreshes", MetricKindSum},
	{"/Cisco-IOS-XE-bgp-oper:bgp-state-data/neighbors/neighbor/messages/received/updates", MetricKindSum},
	{"/Cisco-IOS-XE-bgp-oper:bgp-state-data/neighbors/neighbor/messages/received/notifications", MetricKindSum},
	{"/Cisco-IOS-XE-bgp-oper:bgp-state-data/neighbors/neighbor/messages/received/opens", MetricKindSum},
	{"/Cisco-IOS-XE-bgp-oper:bgp-state-data/neighbors/neighbor/messages/received/keepalives", MetricKindSum},
	{"/Cisco-IOS-XE-bgp-oper:bgp-state-data/neighbors/neighbor/messages/received/route-refreshes", MetricKindSum},
	{"/Cisco-IOS-XE-bgp-oper:bgp-state-data/neighbors/neighbor/prefix-activity/sent/current-prefixes", MetricKindGauge},
	{"/Cisco-IOS-XE-bgp-oper:bgp-state-data/neighbors/neighbor/prefix-activity/received/current-prefixes", MetricKindGauge},
	{"/network-instances/network-instance/protocols/protocol/bgp/neighbors/neighbor/state/messages/sent/updates", MetricKindSum},
	{"/network-instances/network-instance/protocols/protocol/bgp/neighbors/neighbor/state/messages/sent/notifications", MetricKindSum},
	{"/network-instances/network-instance/protocols/protocol/bgp/neighbors/neighbor/state/messages/sent/opens", MetricKindSum},
	{"/network-instances/network-instance/protocols/protocol/bgp/neighbors/neighbor/state/messages/sent/keepalives", MetricKindSum},
	{"/network-instances/network-instance/protocols/protocol/bgp/neighbors/neighbor/state/messages/sent/route-refreshes", MetricKindSum},
	{"/network-instances/network-instance/protocols/protocol/bgp/neighbors/neighbor/state/messages/received/updates", MetricKindSum},
	{"/network-instances/network-instance/protocols/protocol/bgp/neighbors/neighbor/state/messages/received/notifications", MetricKindSum},
	{"/network-instances/network-instance/protocols/protocol/bgp/neighbors/neighbor/state/messages/received/opens", MetricKindSum},
	{"/network-instances/network-instance/protocols/protocol/bgp/neighbors/neighbor/state/messages/received/keepalives", MetricKindSum},
	{"/network-instances/network-instance/protocols/protocol/bgp/neighbors/neighbor/state/messages/received/route-refreshes", MetricKindSum},
	{"/Cisco-IOS-XE-process-cpu-oper:cpu-usage/cpu-utilization/five-seconds", MetricKindGauge},
	{"/Cisco-IOS-XE-process-cpu-oper:cpu-usage/cpu-utilization/one-minute", MetricKindGauge},
	{"/Cisco-IOS-XE-process-cpu-oper:cpu-usage/cpu-utilization/five-minutes", MetricKindGauge},
	{"/process-cpu-ios-xe-oper:cpu-usage/cpu-utilization/five-seconds", MetricKindGauge},
	{"/process-cpu-ios-xe-oper:cpu-usage/cpu-utilization/one-minute", MetricKindGauge},
	{"/process-cpu-ios-xe-oper:cpu-usage/cpu-utilization/five-minutes", MetricKindGauge},
	{"/Cisco-IOS-XE-memory-oper:memory-statistics/memory-statistic/used-memory", MetricKindGauge},
	{"/Cisco-IOS-XE-memory-oper:memory-statistics/memory-statistic/free-memory", MetricKindGauge},
	{"/Cisco-IOS-XE-process-memory-oper:memory-usage-processes/memory-usage-process/allocated-memory", MetricKindGauge},
	{"/Cisco-IOS-XE-process-memory-oper:memory-usage-processes/memory-usage-process/freed-memory", MetricKindGauge},
	{"/Cisco-IOS-XE-platform-software-oper:cisco-platform-software/control-processes/control-process/load-average", MetricKindGauge},
	{"/Cisco-IOS-XE-platform-software-oper:cisco-platform-software/control-processes/control-process/memory-used", MetricKindGauge},
	{"/Cisco-IOS-XE-platform-software-oper:cisco-platform-software/control-processes/control-process/memory-committed", MetricKindGauge},
	{"/Cisco-IOS-XE-environment-oper:environment-sensors/environment-sensor/current-reading", MetricKindGauge},
	{"/environment-ios-xe-oper:environment-sensors/environment-sensor/current-reading", MetricKindGauge},
	{"/Cisco-IOS-XE-platform-oper:components/component/state/temperature/instant", MetricKindGauge},
	{"/Cisco-IOS-XE-platform-oper:components/component/state/temperature/avg", MetricKindGauge},
	{"/Cisco-IOS-XE-platform-oper:components/component/state/temperature/min", MetricKindGauge},
	{"/Cisco-IOS-XE-platform-oper:components/component/state/temperature/max", MetricKindGauge},
	{"/components/component/state/temperature/instant", MetricKindGauge},
	{"/components/component/state/temperature/avg", MetricKindGauge},
	{"/components/component/state/temperature/min", MetricKindGauge},
	{"/components/component/state/temperature/max", MetricKindGauge},
	{"/Cisco-IOS-XE-poe-oper:poe-oper-data/poe-port/power-source", MetricKindGauge},
	{"/Cisco-IOS-XE-poe-oper:poe-oper-data/poe-port/power-consumption", MetricKindGauge},
	{"/Cisco-IOS-XE-poe-oper:poe-oper-data/poe-port/power-allocated", MetricKindGauge},
	{"/poe-ios-xe-oper:poe-oper-data/poe-port/power-source", MetricKindGauge},
	{"/poe-ios-xe-oper:poe-oper-data/poe-port/power-consumption", MetricKindGauge},
	{"/poe-ios-xe-oper:poe-oper-data/poe-port/power-allocated", MetricKindGauge},
	{"/Cisco-IOS-XE-tcp-oper:tcp-statistics/tcp-active-opens", MetricKindSum},
	{"/Cisco-IOS-XE-tcp-oper:tcp-statistics/tcp-passive-opens", MetricKindSum},
	{"/Cisco-IOS-XE-tcp-oper:tcp-statistics/tcp-attempt-fails", MetricKindSum},
	{"/Cisco-IOS-XE-tcp-oper:tcp-statistics/tcp-estab-resets", MetricKindSum},
	{"/Cisco-IOS-XE-tcp-oper:tcp-statistics/tcp-in-segs", MetricKindSum},
	{"/Cisco-IOS-XE-tcp-oper:tcp-statistics/tcp-out-segs", MetricKindSum},
	{"/Cisco-IOS-XE-tcp-oper:tcp-statistics/tcp-retrans-segs", MetricKindSum},
	{"/Cisco-IOS-XE-tcp-oper:tcp-statistics/tcp-in-errs", MetricKindSum},
	{"/Cisco-IOS-XE-tcp-oper:tcp-statistics/tcp-out-rsts", MetricKindSum},
	{"/Cisco-IOS-XE-udp-oper:udp-statistics/udp-in-datagrams", MetricKindSum},
	{"/Cisco-IOS-XE-udp-oper:udp-statistics/udp-no-ports", MetricKindSum},
	{"/Cisco-IOS-XE-udp-oper:udp-statistics/udp-in-errors", MetricKindSum},
	{"/Cisco-IOS-XE-udp-oper:udp-statistics/udp-out-datagrams", MetricKindSum},
})

func mustNormalizeRules(in []rule) []rule {
	out := make([]rule, 0, len(in))
	for _, r := range in {
		prefix := normalizePath(r.prefix)
		if prefix == "" {
			continue
		}
		out = append(out, rule{prefix: prefix, kind: r.kind})
	}
	return out
}
