// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package nxos

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	nxosschema "github.com/cisco/virtual-kubelet-cisco/internal/drivers/nxos/configdriver/schema"
	nxoswriters "github.com/cisco/virtual-kubelet-cisco/internal/drivers/nxos/configdriver/writers"
)

func TestLiveNXOSConfigSmoke(t *testing.T) {
	if os.Getenv("RUN_LIVE_NXOS_CONFIG") != "1" {
		t.Skip("set RUN_LIVE_NXOS_CONFIG=1 to run the live NX-OS config smoke")
	}
	address := os.Getenv("NXOS_LIVE_ADDRESS")
	username := os.Getenv("NXOS_LIVE_USERNAME")
	password := os.Getenv("NXOS_LIVE_PASSWORD")
	if address == "" || username == "" || password == "" {
		t.Fatal("NXOS_LIVE_ADDRESS, NXOS_LIVE_USERNAME, and NXOS_LIVE_PASSWORD are required")
	}
	port := 0
	if raw := os.Getenv("NXOS_LIVE_PORT"); raw != "" {
		p, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("NXOS_LIVE_PORT: %v", err)
		}
		port = p
	}
	tlsEnabled := liveBoolEnv(t, "NXOS_LIVE_TLS")
	insecureSkipVerify := liveBoolEnv(t, "NXOS_LIVE_INSECURE_SKIP_VERIFY")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	tr, err := newNXAPIConfigTransport(&ciskov1.DeviceSpec{
		Driver:   ciskov1.DeviceDriverNXOS,
		Address:  address,
		Port:     port,
		Username: username,
		Password: password,
		TLS: &ciskov1.TLSConfig{
			Enabled:            tlsEnabled,
			InsecureSkipVerify: insecureSkipVerify,
		},
	})
	if err != nil {
		t.Fatalf("newNXAPIConfigTransport: %v", err)
	}
	defer tr.Close()

	for _, path := range []string{
		nxosschema.PathSystemHostname,
		nxosschema.PathFeature,
		nxosschema.PathFeatureSet,
		nxosschema.PathVLANBrief,
		nxosschema.PathInterfaceEthernet,
	} {
		raw, err := tr.Fetch(ctx, path)
		if err != nil {
			t.Fatalf("Fetch(%s): %v", path, err)
		}
		t.Logf("Fetch(%s) ok (%d bytes)", path, len(raw))
	}

	if os.Getenv("RUN_LIVE_NXOS_CONFIG_WRITE") != "1" {
		t.Skip("read-only smoke passed; set RUN_LIVE_NXOS_CONFIG_WRITE=1 to exercise apply/verify")
	}
	vlanID := 3903
	if raw := os.Getenv("NXOS_LIVE_VLAN_ID"); raw != "" {
		id, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("NXOS_LIVE_VLAN_ID: %v", err)
		}
		vlanID = id
	}
	w := nxoswriters.Get(nxosschema.FamilyVLAN)
	observed, err := w.Fetch(ctx, tr)
	if err != nil {
		t.Fatalf("vlan Fetch: %v", err)
	}
	if vlanPresent(observed, vlanID) {
		t.Skipf("VLAN %d already exists; refusing to mutate an existing lab VLAN", vlanID)
	}
	name := fmt.Sprintf("cvk_live_%d", time.Now().Unix()%100000)
	defer func() {
		if ntr, ok := tr.(*nxapiConfigTransport); ok {
			_, _ = ntr.client.conf(context.Background(), "configure terminal", fmt.Sprintf("no vlan %d", vlanID))
		}
	}()
	ops, err := w.Diff(map[string]any{"vlans": []any{map[string]any{"id": vlanID, "name": name}}}, observed)
	if err != nil {
		t.Fatalf("vlan Diff: %v", err)
	}
	if len(ops) == 0 {
		t.Fatalf("vlan Diff produced no ops for absent VLAN %d", vlanID)
	}
	if err := w.Apply(ctx, tr, ops); err != nil {
		t.Fatalf("vlan Apply: %v", err)
	}
	verify, err := w.Fetch(ctx, tr)
	if err != nil {
		t.Fatalf("vlan verify Fetch: %v", err)
	}
	if ops, err := w.Diff(map[string]any{"vlans": []any{map[string]any{"id": vlanID, "name": name}}}, verify); err != nil || len(ops) != 0 {
		t.Fatalf("vlan verify diff ops=%v err=%v", ops, err)
	}
}

func liveBoolEnv(t *testing.T, key string) bool {
	t.Helper()
	raw := os.Getenv(key)
	if raw == "" {
		return false
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		t.Fatalf("%s: %v", key, err)
	}
	return v
}

func vlanPresent(observed any, id int) bool {
	m, ok := observed.(map[string]any)
	if !ok {
		return false
	}
	list, ok := m["vlans"].([]any)
	if !ok {
		return false
	}
	for _, item := range list {
		vm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		switch v := vm["id"].(type) {
		case int:
			if v == id {
				return true
			}
		case float64:
			if int(v) == id {
				return true
			}
		}
	}
	return false
}
