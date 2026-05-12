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

package classifier

import (
	"testing"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

func TestOverrideLongestPrefixWins(t *testing.T) {
	c := OverrideClassifier([]configv1alpha1.MetricTypeOverride{
		{Prefix: "/interfaces/interface/state", Type: string(MetricKindSum)},
		{Prefix: "/interfaces/interface/state/counters/in-octets", Type: string(MetricKindGauge)},
	}, CuratedClassifier())

	got := c.Classify("/interfaces/interface[name=GigabitEthernet1]/state/counters/in-octets")
	if got != MetricKindGauge {
		t.Fatalf("Classify()=%s, want longest-prefix gauge override", got)
	}
}

func TestCuratedClassifierKnownCounters(t *testing.T) {
	c := CuratedClassifier()
	tests := []string{
		"/interfaces/interface[name=GigabitEthernet1]/state/counters/in-octets",
		"/interfaces/interface[name=GigabitEthernet1]/state/counters/out-errors",
		"/Cisco-IOS-XE-interfaces-oper:interfaces/interface[name=GigabitEthernet1]/statistics/in-crc-errors",
		"/Cisco-IOS-XE-interfaces-oper:interfaces/interface[name=GigabitEthernet1]/statistics/out-octets-64",
		"/Cisco-IOS-XE-interfaces-oper:interfaces/interface[name=GigabitEthernet1]/v4-protocol-stats/in-octets",
		"/Cisco-IOS-XE-interfaces-oper:interfaces/interface[name=GigabitEthernet1]/v6-protocol-stats/out-pkts",
		"/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data/app[name=guestshell]/network-utils/network-util[id=eth0]/rx-packets",
		"/Cisco-IOS-XE-bgp-oper:bgp-state-data/neighbors/neighbor[id=10.0.0.1]/messages/sent/updates",
		"/network-instances/network-instance[name=default]/protocols/protocol[identifier=BGP][name=65000]/bgp/neighbors/neighbor[neighbor-address=10.0.0.1]/state/messages/received/keepalives",
		"/Cisco-IOS-XE-tcp-oper:tcp-statistics/tcp-in-segs",
		"/Cisco-IOS-XE-udp-oper:udp-statistics/udp-out-datagrams",
	}
	for _, path := range tests {
		if got := c.Classify(path); got != MetricKindSum {
			t.Fatalf("Classify(%q)=%s, want sum", path, got)
		}
	}
}

func TestCuratedClassifierKnownGauges(t *testing.T) {
	c := CuratedClassifier()
	tests := []string{
		"/Cisco-IOS-XE-process-cpu-oper:cpu-usage/cpu-utilization/five-seconds",
		"/process-cpu-ios-xe-oper:cpu-usage/cpu-utilization/five-minutes",
		"/Cisco-IOS-XE-memory-oper:memory-statistics/memory-statistic[name=processor]/used-memory",
		"/Cisco-IOS-XE-environment-oper:environment-sensors/environment-sensor[name=Temp1]/current-reading",
		"/Cisco-IOS-XE-poe-oper:poe-oper-data/poe-port[name=Gi1/0/1]/power-source",
		"/components/component[name=Fan1]/state/temperature/instant",
	}
	for _, path := range tests {
		if got := c.Classify(path); got != MetricKindGauge {
			t.Fatalf("Classify(%q)=%s, want gauge", path, got)
		}
	}
}

func TestUnknownPathFallsBackToGauge(t *testing.T) {
	got := CuratedClassifier().Classify("/unknown/module/path/value")
	if got != MetricKindGauge {
		t.Fatalf("Classify()=%s, want gauge fallback", got)
	}
}
