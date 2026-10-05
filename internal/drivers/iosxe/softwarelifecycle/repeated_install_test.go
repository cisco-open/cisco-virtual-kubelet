// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package softwarelifecycle

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	lifecycle "github.com/cisco/virtual-kubelet-cisco/internal/softwarelifecycle"
)

// IOS XE's repeated gNOI add can retain a placeholder src-filename while the
// exact version's IMG package, verified source and new add history agree.
// This shape was observed on the physical 17.18.03 -> 17.18.02 preparation.
func TestInterruptedRepeatedInstallPlaceholderSource(t *testing.T) {
	const source = "gNOI_iosxe_17.18.02.0.4112.1766116039.bin"
	started := time.Date(2026, 10, 5, 6, 46, 0, 0, time.UTC)
	for _, tc := range []struct {
		name   string
		valid  bool
		change func(map[string]any, map[string]any, map[string]any, *lifecycle.InterruptedInstallRequest)
	}{
		{name: "exact package corroborates placeholder", valid: true},
		{name: "no IMG binding", change: func(_, v, _ map[string]any, _ *lifecycle.InterruptedInstallRequest) {
			v["install-package-state-info"] = []any{}
		}},
		{name: "duplicate IMG binding", change: func(_, v, p map[string]any, _ *lifecycle.InterruptedInstallRequest) {
			v["install-package-state-info"] = []any{p, p}
		}},
		{name: "different image", change: func(_, _, p map[string]any, _ *lifecycle.InterruptedInstallRequest) {
			p["pkg-name"] = "gNOI_iosxe_17.18.01.bin"
		}},
		{name: "different directory", change: func(_, _, p map[string]any, _ *lifecycle.InterruptedInstallRequest) { p["pkg-dir"] = "/other" }},
		{name: "not an IMG", change: func(_, _, p map[string]any, _ *lifecycle.InterruptedInstallRequest) {
			p["package-type"] = "install-pkg-pkg"
		}},
		{name: "not added", change: func(_, _, p map[string]any, _ *lifecycle.InterruptedInstallRequest) {
			p["package-state"] = "install-state-activated"
		}},
		{name: "arbitrary source mismatch", change: func(_, v, _ map[string]any, _ *lifecycle.InterruptedInstallRequest) {
			v["src-filename"] = "/mnt/sd3/user/other.bin"
		}},
		{name: "empty source is not placeholder", change: func(_, v, _ map[string]any, _ *lifecycle.InterruptedInstallRequest) { v["src-filename"] = "" }},
		{name: "missing response clock", change: func(_, _, _ map[string]any, r *lifecycle.InterruptedInstallRequest) {
			r.DeviceNotBefore, r.DeviceObservedAt = time.Time{}, time.Time{}
		}},
		{name: "wrong verified size", change: func(_, _, _ map[string]any, r *lifecycle.InterruptedInstallRequest) { r.SourceSize++ }},
		{name: "old add cannot prove new attempt", change: func(_, _, _ map[string]any, r *lifecycle.InterruptedInstallRequest) {
			r.DeviceNotBefore = r.DeviceNotBefore.Add(time.Hour)
			r.DeviceObservedAt = r.DeviceObservedAt.Add(time.Hour)
		}},
		{name: "duplicate add remains ambiguous", change: func(root, _, _ map[string]any, _ *lifecycle.InterruptedInstallRequest) {
			history := root["install-oper-hist"].([]any)
			root["install-oper-hist"] = append(history, history[0])
		}},
		{name: "incomplete second member", change: func(root, _, _ map[string]any, _ *lifecycle.InterruptedInstallRequest) {
			root["install-location-information"] = append(root["install-location-information"].([]any), map[string]any{})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var envelope map[string]any
			if err := json.Unmarshal(interruptedInstallResponse("install-no-activity", "install-state-added", "install-package-verify-ok", "1247897709", "install-op-succ", "op-complete", started), &envelope); err != nil {
				t.Fatal(err)
			}
			root := envelope["Cisco-IOS-XE-install-oper:install-oper-data"].(map[string]any)
			location := root["install-location-information"].([]any)[0].(map[string]any)
			version := location["install-version-info"].([]any)[0].(map[string]any)
			version["src-filename"] = "/mnt/sd3/user/gNOI_iosxe_.bin"
			pkg := map[string]any{"package-type": "install-pkg-img", "pkg-dir": "/mnt/sd3/user", "pkg-name": source, "package-state": "install-state-added"}
			version["install-package-state-info"] = append(version["install-package-state-info"].([]any), pkg)
			request := lifecycle.InterruptedInstallRequest{TargetVersion: "17.18.02", SourceSize: 1247897709, NotBefore: started, ObservedAt: started.Add(3 * time.Minute), DeviceNotBefore: started, DeviceObservedAt: started.Add(3 * time.Minute)}
			if tc.change != nil {
				tc.change(root, version, pkg, &request)
			}
			image, err := inventoryImageFromNode(root, request.TargetVersion)
			if err != nil {
				t.Fatal(err)
			}
			before := string(mustJSON(root))
			completed, err := correlateInterruptedInstall(root, image, request)
			if (err == nil) != tc.valid {
				t.Fatalf("completed=%v error=%v, want valid=%v", completed, err, tc.valid)
			}
			if string(mustJSON(root)) != before {
				t.Fatal("corroboration rewrote native inventory")
			}
			if tc.valid && !completed.Equal(started.Add(2*time.Minute)) {
				t.Fatalf("wrong native completion time: %v", completed)
			}
		})
	}
}

func TestRepeatedInstallCapturedInventoryAndRetirement(t *testing.T) {
	raw, err := os.ReadFile("testdata/repeated-install-iosxe-171802.json")
	if err != nil {
		t.Fatal(err)
	}
	root, err := decodeObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	image, err := inventoryImageFromNode(root, "17.18.02")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Date(2026, 10, 5, 6, 47, 59, 0, time.UTC)
	local := time.Date(2026, 10, 5, 6, 51, 21, 0, time.UTC)
	device := local.Add(-130 * time.Second)
	request := lifecycle.InterruptedInstallRequest{TargetVersion: "17.18.02", SourceSize: 1247897709,
		NotBefore: started, ObservedAt: local, DeviceNotBefore: started.Add(-130 * time.Second), DeviceObservedAt: device}
	if _, err := correlateInterruptedInstall(root, image, request); err != nil {
		t.Fatalf("captured completed repeated add not corroborated: %v", err)
	}
	// A later retirement must still bind the original preparation interval,
	// not match an older add or silently expand that interval to recovery time.
	proof, err := preparationRetirementFromJSON(raw, lifecycle.PreparationRetirementRequest{
		TargetVersion: image.Version, RunningVersion: "17.18.03.0.5496.1776157760", SourceSize: request.SourceSize,
		InstallStartedAt: started, PreparedAt: local,
	}, device.Add(time.Hour), local.Add(time.Hour))
	if err != nil || proof.TargetState != lifecycle.InventoryStateInstalled {
		t.Fatalf("captured retirement proof=%+v err=%v", proof, err)
	}
}
