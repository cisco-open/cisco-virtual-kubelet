// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"os"
	"testing"
	"time"
)

func TestLiveCatalystCenterManagedBindings(t *testing.T) {
	raw := os.Getenv("CATC_MANAGED_DEVICE_PROJECTION")
	namespace := os.Getenv("CATC_MANAGED_NAMESPACE")
	if raw == "" || namespace == "" {
		t.Skip("managed device projection not provided")
	}
	var devices []cvk.CiscoDevice
	if json.Unmarshal([]byte(raw), &devices) != nil {
		t.Fatal("invalid managed device projection")
	}
	var nodes []corev1.Node
	if json.Unmarshal([]byte(os.Getenv("CATC_MANAGED_NODE_PROJECTION")), &nodes) != nil {
		t.Fatal("invalid node projection")
	}
	byName := map[string]*corev1.Node{}
	for i := range nodes {
		if byName[nodes[i].Name] != nil {
			t.Fatal("duplicate node projection")
		}
		byName[nodes[i].Name] = &nodes[i]
	}
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, ip := range []string{"198.51.100.100", "198.51.100.101", "198.51.100.103"} {
		target, err := resolveConfigurationDevice(configurationDeviceReference{IP: ip}, inventory)
		if err != nil {
			t.Fatal("inventory target unavailable", err)
		}
		device, err := configurationManagedDevice(namespace, target, devices)
		if err != nil {
			t.Fatal("managed target binding unavailable", err)
		}
		if err := configurationManagedNode(device, byName[device.Status.NodeIdentity.NodeName]); err != nil {
			t.Fatal("live managed node binding unavailable", err)
		}
		checked++
	}
	t.Logf("verified %d controller-to-managed-device bindings; no credentials or device identifiers printed; no writes", checked)
}
