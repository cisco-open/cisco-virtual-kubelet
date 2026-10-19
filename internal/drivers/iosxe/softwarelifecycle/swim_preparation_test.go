// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package softwarelifecycle

import (
	"encoding/json"
	lifecycle "github.com/cisco/virtual-kubelet-cisco/internal/softwarelifecycle"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSWIMNativeFlashQualification(t *testing.T) {
	read := func(n string) []byte {
		b, e := os.ReadFile("testdata/swim/" + n + ".json")
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	install, boot, flash := read("install"), read("boot"), read("flash")
	for name, inputs := range map[string][3][]byte{
		"valid":         {install, boot, flash},
		"partial flash": {install, boot, []byte(`{}`)},
		"wrong boot":    {install, []byte(strings.ReplaceAll(string(boot), "flash:packages.conf", "flash:other.conf")), flash},
		"busy":          {[]byte(strings.ReplaceAll(string(install), "install-no-activity", "install-activity")), boot, flash},
		"wrong alias":   {install, boot, []byte(strings.ReplaceAll(string(flash), "/mnt/sd3/user/", "/unknown/"))},
		"read only":     {install, boot, []byte(strings.ReplaceAll(string(flash), `"is-writable": true`, `"is-writable": false`))},
		"uncommitted":   {[]byte(strings.ReplaceAll(string(install), "install-version-state-provisioned-committed", "install-version-state-provisioned-uncommitted")), boot, flash},
	} {
		t.Run(name, func(t *testing.T) {
			s, e := swimFlashFromJSON(inputs[0], inputs[1], inputs[2], "17.18.04.0.759.1784396682")
			if name == "valid" {
				if e != nil {
					t.Fatal(e)
				}
				if s.FreeBytes != 3281833984 || s.Files["flash:packages.conf"] != 7584 {
					t.Fatalf("bad units/inventory: %+v", s)
				}
			} else if e == nil {
				t.Fatal("unsafe inventory accepted")
			}
		})
	}
}
func TestSWIMPresentArchiveIsNotAnInstalledImage(t *testing.T) {
	version := "17.18.02.0.4112.1766116039"
	name := "gNOI_iosxe_" + version + ".bin"
	key := "flash:" + name
	raw := `{"version":"17.18.02.0.4112","version-extension":"1766116039","current":"install-version-state-present","is-default":false,"src-filename":"/mnt/sd3/user/gNOI_iosxe_17.18.02.0.4112.1766116039.bin","install-package-state-info":[{"package-type":"install-pkg-img","pkg-dir":"/mnt/sd3/user","pkg-name":"gNOI_iosxe_17.18.02.0.4112.1766116039.bin","package-state":"install-state-new"}]}`
	for _, kind := range []string{"valid", "installed", "added", "default", "extracted", "nested-extracted", "unverified", "size"} {
		t.Run(kind, func(t *testing.T) {
			var v map[string]any
			if json.Unmarshal([]byte(raw), &v) != nil {
				t.Fatal("fixture")
			}
			files := map[string]uint64{key: 1024}
			data := map[string]any{"pkg-size": "1024", "verify-status": "install-package-verify-ok"}
			packages := []map[string]any{{"pkg-name": name, "pkg-dir": "/mnt/sd3/user", "pkg-action": "install-package-action-none", "pkg-data": data}}
			switch kind {
			case "installed":
				v["current"] = "install-version-state-installed"
			case "added":
				v["install-package-state-info"].([]any)[0].(map[string]any)["package-state"] = "install-state-added"
			case "default":
				v["is-default"] = true
			case "extracted":
				files["flash:cat9k-rpbase.17.18.02.SPA.pkg"] = 42
			case "nested-extracted":
				files["flash:retained/cat9k-rpbase.17.18.02.SPA.pkg"] = 42
			case "unverified":
				data["verify-status"] = "unknown"
			case "size":
				data["pkg-size"] = "42"
			}
			p, ver, ok := archiveOnlyVersion(v, packages, files)
			if kind == "valid" {
				if !ok || p != key || ver != version {
					t.Fatal("archive-only catalog rejected")
				}
			} else if ok {
				t.Fatalf("unsafe %s accepted", kind)
			}
		})
	}
}

func TestSWIMStagedTargetUsesNativeCompletedAdd(t *testing.T) {
	install, err := os.ReadFile("testdata/swim/staged-install.json")
	if err != nil {
		t.Fatal(err)
	}
	boot, _ := os.ReadFile("testdata/swim/boot.json")
	flash, _ := os.ReadFile("testdata/swim/flash.json")
	var fs map[string]any
	if err := json.Unmarshal(flash, &fs); err != nil {
		t.Fatal(err)
	}
	partition := fs["Cisco-IOS-XE-platform-software-oper:partitions"].([]any)[0].(map[string]any)
	partition["partition-content"] = append(partition["partition-content"].([]any), map[string]any{"full-path": "/mnt/sd3/user/cat9k_iosxe.26.02.01.SPA.bin", "size": "1264266123", "type": "file"})
	flash, _ = json.Marshal(fs)
	parse := func(s string) time.Time {
		v, e := time.Parse(time.RFC3339, s)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	deviceTime := parse("2026-10-09T05:01:13Z")
	localTime := parse("2026-10-09T05:07:00Z")
	request := lifecycle.SWIMFlashRequest{RunningVersion: "17.18.04.0.759.1784396682", StagedTargetVersion: "26.02.01.0.263", DistributionStartedAt: parse("2026-10-09T04:48:51Z")}
	for _, kind := range []string{"valid", "no-authority", "wrong-target", "missing-clock", "newer-distribution", "wrong-size", "active-installer", "failed-add", "unverified-archive", "uncommitted-target"} {
		t.Run(kind, func(t *testing.T) {
			raw, files, req, clock := install, flash, request, deviceTime
			switch kind {
			case "no-authority":
				req.StagedTargetVersion = ""
			case "wrong-target":
				req.StagedTargetVersion = "26.02.02"
			case "missing-clock":
				clock = time.Time{}
			case "newer-distribution":
				req.DistributionStartedAt = localTime.Add(-time.Minute)
			case "wrong-size":
				files = []byte(strings.ReplaceAll(string(flash), "1264266123", "1264266122"))
			case "active-installer":
				raw = []byte(strings.ReplaceAll(string(install), "install-no-activity", "install-activity"))
			case "failed-add":
				raw = []byte(strings.ReplaceAll(string(install), "install-op-succ", "install-op-fail"))
			case "unverified-archive":
				raw = []byte(strings.ReplaceAll(string(install), "install-package-verify-ok", "install-package-verify-not-done"))
			case "uncommitted-target":
				raw = []byte(strings.ReplaceAll(string(install), "install-version-state-in-progress", "install-version-state-provisioned-uncommitted"))
			}
			snapshot, err := swimFlashForRequest(raw, boot, files, req, clock, localTime)
			if kind == "valid" {
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(snapshot.NativeReferences, "cat9k_iosxe.26.02.01.SPA.bin") || snapshot.ArchiveOnlyVersions["flash:cat9k_iosxe.26.02.01.SPA.bin"] != "" {
					t.Fatal("target lost cleanup protection")
				}
			} else if err == nil {
				t.Fatalf("accepted unsafe %s", kind)
			}
		})
	}
}

func TestSWIMCachedTargetKeepsReferencesAndRequiresQuiescence(t *testing.T) {
	install, err := os.ReadFile("testdata/swim/staged-install.json")
	if err != nil {
		t.Fatal(err)
	}
	install = []byte(strings.ReplaceAll(strings.ReplaceAll(string(install), "install-version-state-in-progress", "install-version-state-present"), "install-state-added", "install-state-new"))
	boot, _ := os.ReadFile("testdata/swim/boot.json")
	flash, _ := os.ReadFile("testdata/swim/flash.json")
	root, err := decodeObject(install)
	if err != nil {
		t.Fatal(err)
	}
	locations, _, _ := collectNamedList(root, "install-location-information")
	packages, _, _ := directNamedList(locations[0], "install-packages")
	var fs map[string]any
	if err := json.Unmarshal(flash, &fs); err != nil {
		t.Fatal(err)
	}
	partition := fs["Cisco-IOS-XE-platform-software-oper:partitions"].([]any)[0].(map[string]any)
	for _, pkg := range packages {
		name := stringField(pkg, "pkg-name")
		if !strings.Contains(name, "26.02.01") {
			continue
		}
		data, _ := objectField(pkg, "pkg-data")
		partition["partition-content"] = append(partition["partition-content"].([]any), map[string]any{"full-path": "/mnt/sd3/user/" + name, "size": stringField(data, "pkg-size"), "type": "file"})
	}
	flash, _ = json.Marshal(fs)
	now := time.Now()
	req := lifecycle.SWIMFlashRequest{RunningVersion: "17.18.04.0.759.1784396682", StagedTargetVersion: "26.02.01.0.263", DistributionStartedAt: now.Add(-time.Minute)}
	for _, kind := range []string{"valid", "deferred", "missing-package", "busy", "bad-verification", "added-package", "wrong-size", "uncommitted", "wrong-target"} {
		t.Run(kind, func(t *testing.T) {
			raw, files, request := install, flash, req
			switch kind {
			case "deferred":
				raw = []byte(strings.ReplaceAll(string(raw), "install-package-verify-ok", "install-package-verify-deferred"))
			case "missing-package":
				files = []byte(strings.ReplaceAll(string(files), "cat9k-rpbase.26.02.01.SPA.pkg", "missing.pkg"))
			case "busy":
				raw = []byte(strings.ReplaceAll(string(raw), "install-no-activity", "install-activity"))
			case "bad-verification":
				raw = []byte(strings.ReplaceAll(string(raw), "install-package-verify-ok", "install-package-verify-failed"))
			case "added-package":
				raw = []byte(strings.ReplaceAll(string(raw), "install-state-new", "install-state-added"))
			case "wrong-size":
				files = []byte(strings.ReplaceAll(string(files), "1264266123", "1264266122"))
			case "uncommitted":
				raw = []byte(strings.ReplaceAll(string(raw), "install-version-state-present", "install-version-state-provisioned-uncommitted"))
			case "wrong-target":
				request.StagedTargetVersion = "26.02.02"
			}
			snapshot, err := swimFlashForRequest(raw, boot, files, request, now, now)
			if kind == "valid" || kind == "deferred" {
				if err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"cat9k_iosxe.26.02.01.SPA.bin", "cat9k-rpbase.26.02.01.SPA.pkg"} {
					if !strings.Contains(snapshot.NativeReferences, name) || snapshot.ArchiveOnlyVersions["flash:"+name] != "" {
						t.Fatal("cached target lost deletion protection")
					}
				}
			} else if err == nil {
				t.Fatalf("accepted unsafe cached target: %s", kind)
			}
		})
	}
}
