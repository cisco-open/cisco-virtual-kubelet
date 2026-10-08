// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func liveClient(t *testing.T) *client {
	t.Helper()
	endpoint := os.Getenv("CATC_ENDPOINT")
	if endpoint == "" {
		endpoint = "https://127.0.0.1:19443"
	}
	user, password := os.Getenv("CATC_USERNAME"), os.Getenv("CATC_PASSWORD")
	if user == "" || password == "" {
		data, err := os.ReadFile("/tmp/catc")
		if err != nil {
			t.Skip("set CATC_USERNAME/CATC_PASSWORD or provide /tmp/catc")
		}
		values := strings.Fields(string(data))
		if len(values) < 2 {
			t.Fatal("CATC credentials require username and password")
		}
		user, password = values[0], values[1]
	}
	dir := t.TempDir()
	for _, f := range []struct{ name, value string }{{"username", user}, {"password", password}} {
		if err := os.WriteFile(filepath.Join(dir, f.name), []byte(f.value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return newClient(clientConfig{Endpoint: endpoint, CredentialPath: dir, InsecureSkipVerify: true, RequestTimeout: 20 * time.Second, MaxSessionLifetime: 15 * time.Minute})
}

func TestLiveCatalystCenterHealthInventoryAndSWIMRead(t *testing.T) {
	c := liveClient(t)
	if err := c.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	devices, err := c.ListDevices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Catalyst Center inventory: %d devices", len(devices))
	images, err := c.ListImages(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Catalyst Center SWIM image inventory: %d images", len(images))
}
