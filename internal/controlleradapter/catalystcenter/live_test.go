// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
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
	if (user == "") != (password == "") {
		t.Fatal("set both CATC_USERNAME and CATC_PASSWORD")
	}
	if user == "" {
		data, err := os.ReadFile("/tmp/catc")
		if err != nil {
			t.Skip("set CATC_USERNAME/CATC_PASSWORD or provide /tmp/catc")
		}
		var values []string
		for _, line := range strings.Split(string(data), "\n") {
			if value := strings.TrimSpace(line); value != "" {
				values = append(values, value)
			}
		}
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
	c := newClient(clientConfig{Endpoint: endpoint, CredentialPath: dir, InsecureSkipVerify: true, RequestTimeout: 20 * time.Second, MaxSessionLifetime: 15 * time.Minute})
	t.Cleanup(c.Invalidate)
	return c
}

// This opt-in test consumes previously persisted readiness receipts. It never
// starts or replays a readiness, distribution or activation POST. A successful
// parent task with no validation results must fail qualification.
func TestLiveCatalystCenterSWIMReadiness(t *testing.T) {
	path := os.Getenv("CATC_READINESS_RECEIPTS")
	if path == "" {
		t.Skip("set CATC_READINESS_RECEIPTS to the saved readiness receipts JSON")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var receipts map[string]struct {
		DeviceID  string  `json:"device_id"`
		StartedAt float64 `json:"started_at"`
		Receipt   struct {
			Response Task `json:"response"`
		} `json:"receipt"`
	}
	if json.Unmarshal(data, &receipts) != nil || len(receipts) == 0 {
		t.Fatal("invalid readiness receipt file")
	}
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	devices, err := c.ListDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	images, err := c.ListImages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for address, receipt := range receipts {
		t.Run(address, func(t *testing.T) {
			var device *Device
			for i := range devices {
				if devices[i].ID == receipt.DeviceID && sameManagementIP(devices[i].ManagementIP, address) {
					device = &devices[i]
				}
			}
			if device == nil {
				t.Fatal("receipt device does not match inventory")
			}
			details, err := c.GetDeviceImageDetails(ctx, device.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, id := range strings.Split(os.Getenv("CATC_TEST_IMAGE_IDS"), ",") {
				if id == "" {
					continue
				}
				image, err := resolveSWIMImage(id, images)
				if err != nil {
					t.Fatal(err)
				}
				if err := validateImageProduct(image, *device, details); err != nil {
					t.Fatal(err)
				}
				t.Logf("image %s matches bound product %s (integrity: %s)", image.ID, details.NetworkDevice.ID, image.IntegrityStatus)
			}
			body, err := c.GetTask(ctx, receipt.Receipt.Response.ID)
			if err != nil {
				t.Fatal(err)
			}
			state, err := parseSWIMTask(body, receipt.Receipt.Response.ID)
			if err != nil || !state.complete {
				t.Fatalf("readiness task is not successfully complete: %v", err)
			}
			results, err := c.ListReadinessResults(ctx, receipt.DeviceID)
			if err != nil {
				t.Fatal(err)
			}
			submitted := time.UnixMilli(int64(receipt.StartedAt * 1000))
			if err := validateReadinessResults(results, receipt.DeviceID, receipt.Receipt.Response.ID, submitted, time.Now(), 15*time.Minute); err != nil {
				t.Fatalf("readiness not qualified: %v", err)
			}
		})
	}
}

func TestLiveCatalystCenterHealthInventoryAndSWIMRead(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := c.Probe(ctx); err != nil {
		t.Fatal(err)
	}
	devices, err := c.ListDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Catalyst Center inventory: %d devices", len(devices))
	images, err := c.ListImages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Catalyst Center SWIM image inventory: %d images", len(images))
}
