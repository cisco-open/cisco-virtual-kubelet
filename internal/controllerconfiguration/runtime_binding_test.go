// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"reflect"
	"strings"
	"testing"
)

func TestRuntimeAdmissionBinding(t *testing.T) {
	valid := RuntimeAdmissionBinding{Protocol: Protocol, ManagerUsername: "system:serviceaccount:system:manager", AdmissionPrefix: "cvk", PolicyNamespace: "system", PolicyName: "policy", LedgerName: "ledger", AppHostingServiceAccount: "app", NetworkManagementServiceAccount: "network", LeaseNamespace: "leases"}
	encoded, err := valid.Encode()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := ParseRuntimeAdmissionBinding(encoded)
	if err != nil || !reflect.DeepEqual(decoded, &valid) {
		t.Fatal(decoded, err)
	}
	group := valid
	group.Protocol = GroupReservationProtocol
	groupEncoded, err := group.Encode()
	if err != nil {
		t.Fatal(err)
	}
	groupDecoded, err := ParseRuntimeAdmissionBinding(groupEncoded)
	if err != nil || !reflect.DeepEqual(groupDecoded, &group) {
		t.Fatal("group binding round trip", err)
	}
	for _, protocol := range []string{"", Protocol + "," + GroupReservationProtocol, "*"} {
		invalid := valid
		invalid.Protocol = protocol
		if _, err := invalid.Encode(); err == nil {
			t.Fatal("ambiguous protocol binding accepted")
		}
	}
	absent, err := ParseRuntimeAdmissionBinding("")
	if absent != nil || err != nil {
		t.Fatal("legacy missing binding", err)
	}
	for _, raw := range []string{"null", "{}", encoded + encoded, strings.Replace(encoded, `"protocol":`, `"unknown":true,"protocol":`, 1), strings.Repeat("x", 4097)} {
		if _, err := ParseRuntimeAdmissionBinding(raw); err == nil {
			t.Fatal("invalid binding accepted")
		}
	}
	defaultNamespace := valid
	defaultNamespace.LeaseNamespace = ""
	if _, err := defaultNamespace.Encode(); err != nil {
		t.Fatal("per-device lease namespace default rejected", err)
	}
	for name, mutate := range map[string]func(*RuntimeAdmissionBinding){
		"version":               func(b *RuntimeAdmissionBinding) { b.Protocol = "v2" },
		"manager user":          func(b *RuntimeAdmissionBinding) { b.ManagerUsername = "admin" },
		"empty manager account": func(b *RuntimeAdmissionBinding) { b.ManagerUsername = "system:serviceaccount:system:" },
		"policy namespace":      func(b *RuntimeAdmissionBinding) { b.PolicyNamespace = "other/namespace" },
		"policy name":           func(b *RuntimeAdmissionBinding) { b.PolicyName = "" },
		"lease namespace":       func(b *RuntimeAdmissionBinding) { b.LeaseNamespace = "invalid/namespace" },
		"shared account":        func(b *RuntimeAdmissionBinding) { b.NetworkManagementServiceAccount = b.AppHostingServiceAccount },
	} {
		t.Run(name, func(t *testing.T) {
			b := valid
			mutate(&b)
			if _, err := b.Encode(); err == nil {
				t.Fatal("invalid identity accepted")
			}
		})
	}
}
