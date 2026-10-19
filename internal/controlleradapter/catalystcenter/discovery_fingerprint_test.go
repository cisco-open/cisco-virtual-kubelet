// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDiscoveryOwnedFieldMaskPreservesOriginalPublicShape(t *testing.T) {
	fields := map[string]any{"name": "fixture", "discoveryType": "SINGLE", "ipAddressList": "192.0.2.1", "protocolOrder": "SSH", "globalCredentialIdList": []string{"two", "one"}, "retry": 3, "ipFilterList": []string{"192.0.2.8"}}
	resource := ownedDiscoveryResource("fixture", fields)
	mask, hash, valid := ownedDiscoveryFingerprintParts(resource)
	if !valid || mask == 0 {
		t.Fatal("selected public shape missing")
	}
	retry := 3
	got := discoveryMetadata{Name: "fixture", Type: "Range", Addresses: "192.0.2.1-192.0.2.1", Protocol: "ssh", CredentialIDs: []string{"one", "two"}, Retry: &retry, Filters: json.RawMessage(`"192.0.2.8-192.0.2.8"`)}
	native, err := discoveryNativeEffect(got, mask)
	if err != nil || discoveryPublicDigest(native) != hash {
		t.Fatal("canonical native effect mismatch", err)
	}
	delete(fields, "retry")
	delete(fields, "ipFilterList")
	changed := ownedDiscoveryResource("fixture", fields)
	if changed == resource {
		t.Fatal("source shape loss not detected")
	}
	original, err := discoveryNativeEffect(got, mask)
	if err != nil || discoveryPublicDigest(original) != hash {
		t.Fatal("original shape depends on current source", err)
	}
	retry = 4
	altered, err := discoveryNativeEffect(got, mask)
	if err != nil || discoveryPublicDigest(altered) == hash {
		t.Fatal("selected native setting changed without fingerprint change", err)
	}
	for _, key := range []string{"passwordList", "enablePasswordList", "snmpRoCommunity", "futureSecret"} {
		fields[key] = "secret-sentinel"
		if ownedDiscoveryResource("fixture", fields) != "" {
			t.Fatal("sensitive or unknown field hashed", key)
		}
		delete(fields, key)
	}
	for _, bad := range []string{"00", "1e", "ffff", "01f", "1F"} {
		if _, _, ok := ownedDiscoveryFingerprintParts(ownedDiscoveryPrefix + strings.Repeat("a", 64) + ":" + bad + ":" + strings.Repeat("b", 64)); ok {
			t.Fatal("invalid field mask accepted", bad)
		}
	}
}
