// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package iosxe

import (
	"context"
	"math"
	"testing"
)

func TestGetInterfaceStatsPreservesRatePresenceAndValidity(t *testing.T) {
	driver := newTestDriver(&fakeNetworkClient{getHook: func(path string, result any) error {
		if path != "/restconf/data/Cisco-IOS-XE-interfaces-oper:interfaces" {
			t.Fatalf("path = %q", path)
		}
		root, ok := result.(*Cisco_IOS_XEInterfacesOper_Interfaces)
		if !ok {
			t.Fatalf("result = %T, want interface operational root", result)
		}
		root.Interface = map[string]*Cisco_IOS_XEInterfacesOper_Interfaces_Interface{
			"GigabitEthernet1": {
				Name:  topologyString("GigabitEthernet1"),
				Speed: topologyUint64(1_000_000),
				Statistics: &Cisco_IOS_XEInterfacesOper_Interfaces_Interface_Statistics{
					RxKbps: topologyUint64(0),
					TxKbps: topologyUint64(123),
				},
			},
			"GigabitEthernet2": {
				Name:       topologyString("GigabitEthernet2"),
				Speed:      topologyUint64(1_000_000),
				Statistics: &Cisco_IOS_XEInterfacesOper_Interfaces_Interface_Statistics{},
			},
			"GigabitEthernet3": {
				Name:  topologyString("GigabitEthernet3"),
				Speed: topologyUint64(1_000_000),
				Statistics: &Cisco_IOS_XEInterfacesOper_Interfaces_Interface_Statistics{
					RxKbps: topologyUint64(math.MaxUint64),
					TxKbps: topologyUint64(1),
				},
			},
		}
		return nil
	}})

	stats, err := driver.GetInterfaceStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]int, len(stats))
	for i := range stats {
		byName[stats[i].Name] = i
	}
	if len(byName) != 3 {
		t.Fatalf("stats = %#v, want three interfaces", stats)
	}

	measuredIndex, ok := byName["GigabitEthernet1"]
	if !ok {
		t.Fatalf("stats = %#v, missing GigabitEthernet1", stats)
	}
	measured := stats[measuredIndex]
	if !measured.InRatePresent || !measured.OutRatePresent ||
		!measured.InRateValid || !measured.OutRateValid ||
		measured.InBitsPerSec != 0 || measured.OutBitsPerSec != 123_000 {
		t.Fatalf("measured rate = %#v, want valid measured zero and 123 Kbps", measured)
	}

	missingIndex, ok := byName["GigabitEthernet2"]
	if !ok {
		t.Fatalf("stats = %#v, missing GigabitEthernet2", stats)
	}
	missing := stats[missingIndex]
	if missing.InRatePresent || missing.OutRatePresent ||
		missing.InRateValid || missing.OutRateValid {
		t.Fatalf("missing rate = %#v, want absent and invalid", missing)
	}

	overflowedIndex, ok := byName["GigabitEthernet3"]
	if !ok {
		t.Fatalf("stats = %#v, missing GigabitEthernet3", stats)
	}
	overflowed := stats[overflowedIndex]
	if !overflowed.InRatePresent || !overflowed.OutRatePresent ||
		overflowed.InRateValid || !overflowed.OutRateValid ||
		overflowed.InBitsPerSec != 0 || overflowed.OutBitsPerSec != 1_000 {
		t.Fatalf("overflowed rate = %#v, want invalid RX and valid TX", overflowed)
	}
}

func topologyUint64(value uint64) *uint64 { return &value }

func topologyString(value string) *string { return &value }
