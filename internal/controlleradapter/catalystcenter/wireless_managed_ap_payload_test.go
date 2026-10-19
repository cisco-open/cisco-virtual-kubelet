// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"reflect"
	"testing"
)

func TestWirelessManagedAPPayload(t *testing.T) {
	sites := map[string]string{"Global/A": "a", "Global/B": "b", "Global/C": "c"}
	old := wirelessManagedAPSnapshot{primary: map[string]string{"Global/A": "a"}, secondary: map[string]string{"Global/B": "b"}, anchor: map[string]string{"Global/A": "a"}}
	for _, anchor := range []bool{false, true} {
		want := wirelessManagedAPIntent{primary: []string{"Global/C", "Global/A"}, anchor: []string{"Global/C", "Global/A"}}
		got, err := want.payload(old, sites, anchor)
		if err != nil {
			t.Fatal(err)
		}
		expected := map[string][]string{"primaryManagedAPLocationsSiteIds": {"a", "c"}, "secondaryManagedAPLocationsSiteIds": {"b"}}
		if anchor {
			expected = map[string][]string{"anchorManagedAPLocationsSiteIds": {"a", "c"}}
		}
		if !reflect.DeepEqual(got, expected) {
			t.Fatal(got)
		}
	}
	if len(old.primary) != 1 || len(old.secondary) != 1 {
		t.Fatal("mutated observation")
	}
}
func TestWirelessManagedAPPayloadRefusesInvalidChanges(t *testing.T) {
	sites := map[string]string{"Global/A": "a", "Global/B": "b", "Global/Alias": "a"}
	old := wirelessManagedAPSnapshot{primary: map[string]string{"Global/A": "a"}, secondary: map[string]string{}}
	for _, want := range []wirelessManagedAPIntent{
		{}, {primary: []string{}}, {primary: []string{"Global/B"}},
		{primary: []string{"Global/Missing"}}, {primary: []string{"Global/A", "Global/Alias"}},
	} {
		if _, err := want.payload(old, sites, false); err == nil {
			t.Fatal("invalid intent accepted", want)
		}
	}
	if _, err := (wirelessManagedAPIntent{secondary: []string{"Global/B"}}).payload(old, map[string]string{"Global/A": "replacement", "Global/B": "b"}, false); err == nil {
		t.Fatal("retained replaced site accepted")
	}
	if _, err := (wirelessManagedAPIntent{anchor: []string{}}).payload(wirelessManagedAPSnapshot{anchor: map[string]string{"Global/A": "a"}}, sites, true); err == nil {
		t.Fatal("anchor removal accepted")
	}
}
