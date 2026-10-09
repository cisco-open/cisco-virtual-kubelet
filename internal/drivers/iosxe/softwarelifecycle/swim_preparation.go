// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package softwarelifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
	lifecycle "github.com/cisco/virtual-kubelet-cisco/internal/softwarelifecycle"
	"io"
	"path"
	"strconv"
	"strings"
	"time"
)

const swimFilesystemPath = "/Cisco-IOS-XE-platform-software-oper:cisco-platform-software/q-filesystem"

func (a *Adapter) ObserveSWIMFlash(ctx context.Context, request lifecycle.SWIMFlashRequest) (lifecycle.SWIMFlashSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	var empty lifecycle.SWIMFlashSnapshot
	var install []byte
	var deviceTime, localTime time.Time
	var err error
	if timed, ok := a.transport.(deviceTimeFetcher); ok {
		install, deviceTime, localTime, err = timed.FetchWithDeviceTime(ctx, installOperDataPath)
	} else {
		install, err = a.transport.Fetch(ctx, installOperDataPath)
	}
	if err != nil {
		return empty, err
	}
	boot, err := a.transport.Fetch(ctx, "/Cisco-IOS-XE-native:native/boot")
	if err != nil {
		return empty, err
	}
	keys, err := a.transport.Fetch(ctx, swimFilesystemPath+"?fields=fru;slot;bay;chassis;partitions(name;total-size;used-size;is-primary;is-writable)")
	if err != nil {
		return empty, err
	}
	// This initial profile is deliberately single RP. Never guess stack aliases.
	root, err := decodeObject(keys)
	if err != nil {
		return empty, err
	}
	fs, found, err := collectNamedList(root, "q-filesystem")
	if err != nil || !found || len(fs) != 1 || stringField(fs[0], "fru") != "fru-rp" || fs[0]["slot"] != float64(0) || fs[0]["bay"] != float64(0) || fs[0]["chassis"] != float64(-1) {
		return empty, fmt.Errorf("SWIM preparation requires qualified single-RP filesystem identity")
	}
	flash, err := a.transport.Fetch(ctx, swimFilesystemPath+"=fru-rp,0,0,-1/partitions=flash%3A")
	if err != nil {
		return empty, err
	}
	return swimFlashForRequest(install, boot, flash, request, deviceTime, localTime)
}

func swimFlashFromJSON(install, boot, flash []byte, running string) (lifecycle.SWIMFlashSnapshot, error) {
	return swimFlashForRequest(install, boot, flash, lifecycle.SWIMFlashRequest{RunningVersion: running}, time.Time{}, time.Time{})
}

func swimFlashForRequest(install, boot, flash []byte, request lifecycle.SWIMFlashRequest, deviceTime, localTime time.Time) (lifecycle.SWIMFlashSnapshot, error) {
	var out lifecycle.SWIMFlashSnapshot
	running := request.RunningVersion
	if lifecycle.ValidateTargetVersion(running) != nil {
		return out, fmt.Errorf("invalid running version")
	}
	root, err := decodeObject(install)
	if err != nil {
		return out, err
	}
	locations, found, err := collectNamedList(root, "install-location-information")
	if err != nil || !found || len(locations) != 1 {
		return out, fmt.Errorf("complete single-location installer evidence required")
	}
	b, err := decodeObject(boot)
	if err != nil {
		return out, err
	}
	names, found, err := collectNamedList(b, "boot-filename")
	if err != nil || !found || len(names) != 1 || stringField(names[0], "filename") != "flash:packages.conf" {
		return out, fmt.Errorf("qualified committed packages.conf boot reference required")
	}
	f, err := decodeObject(flash)
	if err != nil {
		return out, err
	}
	parts, found, err := collectNamedList(f, "partitions")
	if err != nil || !found || len(parts) != 1 {
		return out, fmt.Errorf("complete flash partition required")
	}
	p := parts[0]
	total, e1 := strconv.ParseUint(stringField(p, "total-size"), 10, 64)
	used, e2 := strconv.ParseUint(stringField(p, "used-size"), 10, 64)
	if e1 != nil || e2 != nil || total == 0 || used > total || total > 1<<40 || p["is-primary"] != true || p["is-writable"] != true || stringField(p, "name") != "flash:" {
		return out, fmt.Errorf("invalid writable flash capacity")
	}
	contents, found, err := directNamedList(p, "partition-content")
	if err != nil || !found || len(contents) == 0 {
		return out, fmt.Errorf("complete flash contents required")
	}
	out.Files = map[string]uint64{}
	for _, file := range contents {
		full := stringField(file, "full-path")
		if !strings.HasPrefix(full, "/mnt/sd3/user/") || path.Clean(full) != full {
			return out, fmt.Errorf("unqualified filesystem alias")
		}
		size, err := strconv.ParseUint(stringField(file, "size"), 10, 64)
		if err != nil {
			return out, fmt.Errorf("missing file size")
		}
		rel := strings.TrimPrefix(full, "/mnt/sd3/user/")
		if stringField(file, "type") != "file" {
			continue
		}
		key := "flash:" + rel
		if _, ok := out.Files[key]; ok {
			return out, fmt.Errorf("duplicate flash entry")
		}
		out.Files[key] = size
	}
	if out.Files["flash:packages.conf"] == 0 {
		return out, fmt.Errorf("boot manifest absent")
	}
	// Reuse native quiescence and interrupted-add correlation without changing
	// retirement semantics or removing any target references from the snapshot.
	retirement := lifecycle.PreparationRetirementRequest{RunningVersion: running, TargetVersion: "0.0.0"}
	if request.StagedTargetVersion != "" {
		if lifecycle.ValidateTargetVersion(request.StagedTargetVersion) != nil || request.DistributionStartedAt.IsZero() || deviceTime.IsZero() || localTime.IsZero() || request.DistributionStartedAt.After(localTime) {
			return out, fmt.Errorf("staged SWIM observation requires target, distribution interval and device clock")
		}
		target, err := inventoryImageFromNode(root, request.StagedTargetVersion)
		if err != nil || target.Version == running || (target.State != lifecycle.InventoryStatePresent && target.State != lifecycle.InventoryStateInProgress && !target.State.Activatable()) {
			return out, fmt.Errorf("staged SWIM target is absent, ambiguous or not inactive")
		}
		size := out.Files["flash:"+installSourceName(target.SourcePath)]
		if size == 0 || size > 16<<30 {
			return out, fmt.Errorf("staged SWIM target archive is missing or invalid")
		}
		if target.State == lifecycle.InventoryStatePresent {
			// A successful controller distribution can reuse a cached archive
			// without install-add. This is only a quiescent preparation snapshot,
			// never proof of installation, content verification or activation.
			// Keep every target reference protected. The ordinary baseline
			// quiescence checks below and subsequent SWIM readiness still apply.
			if err := validateCachedSWIMTarget(locations[0], target, out.Files); err != nil {
				return out, err
			}
		} else {
			retirement.TargetVersion = target.Version
			retirement.SourceSize = int64(size)
			retirement.InstallStartedAt = request.DistributionStartedAt
			retirement.PreparedAt = localTime
			if err := corroborateRetirement(root, target, retirement, deviceTime, localTime); err != nil {
				return out, err
			}
		}
	}
	if _, err := preparationRetirementFromJSON(install, retirement, deviceTime, localTime); err != nil {
		return out, err
	}
	out.FreeBytes = (total - used) * 1024
	// A copied BIN is automatically cataloged as Present on XE. Remove only
	// qualified archive-only catalog entries from the protection view; all
	// installed/package/boot references remain. Hash the original raw evidence.
	out.ArchiveOnlyVersions = map[string]string{}
	versions, _, _ := directNamedList(locations[0], "install-version-info")
	packages, _, _ := directNamedList(locations[0], "install-packages")
	for _, v := range versions {
		if key, version, ok := archiveOnlyVersion(v, packages, out.Files); ok {
			out.ArchiveOnlyVersions[key] = version
		}
	}
	for _, field := range []string{"install-version-info", "install-packages"} {
		entries, _, err := directNamedList(locations[0], field)
		if err != nil {
			return out, err
		}
		retained := []any{}
		for _, entry := range entries {
			key := "flash:" + stringField(entry, "pkg-name")
			if field == "install-version-info" {
				key = "flash:" + path.Base(stringField(entry, "src-filename"))
			}
			if _, ok := out.ArchiveOnlyVersions[key]; !ok {
				retained = append(retained, entry)
			}
		}
		for k := range locations[0] {
			if localName(k) == field {
				locations[0][k] = retained
			}
		}
	}
	// Preserve all remaining native references conservatively.
	native, _ := json.Marshal(root)
	out.NativeReferences = string(native) + string(boot)
	out.EvidenceHash = fmt.Sprintf("%x", sha256.Sum256(append(append(append([]byte{}, install...), boot...), flash...)))
	return out, nil
}

// Qualify retained cat9k archive/package metadata, not archive authenticity.
// Catalyst Center owns image validation. Unlike removable retired archives,
// these cached target files are never removed from NativeReferences.
func validateCachedSWIMTarget(location map[string]any, target lifecycle.InventoryImage, files map[string]uint64) error {
	bad := fmt.Errorf("cached SWIM target requires complete retained archive/package evidence")
	parts := strings.Split(target.Version, ".")
	if len(parts) < 3 {
		return bad
	}
	base := strings.Join(parts[:3], ".")
	archive := "cat9k_iosxe." + base + ".SPA.bin"
	if target.SourcePath != "/mnt/sd3/user/"+archive || files["flash:"+archive] == 0 {
		return bad
	}
	versions, found, err := directNamedList(location, "install-version-info")
	if err != nil || !found {
		return bad
	}
	packages, found, err := directNamedList(location, "install-packages")
	if err != nil || !found {
		return bad
	}
	for _, v := range versions {
		if (inventoryVersion{Version: stringField(v, "version"), VersionExtension: stringField(v, "version-extension")}).identity() != target.Version {
			continue
		}
		if v["is-default"] != false || stringField(v, "current") != "install-version-state-present" {
			return bad
		}
		states, found, err := directNamedList(v, "install-package-state-info")
		if err != nil || !found || len(states) < 2 {
			return bad
		}
		seen := map[string]bool{}
		for _, state := range states {
			name := stringField(state, "pkg-name")
			kind := stringField(state, "package-type")
			if seen[name] || files["flash:"+name] == 0 || stringField(state, "pkg-dir") != "/mnt/sd3/user" || stringField(state, "package-state") != "install-state-new" ||
				!((kind == "install-pkg-img" && name == archive) || (kind == "install-pkg-pkg" && strings.HasPrefix(name, "cat9k-") && strings.HasSuffix(name, "."+base+".SPA.pkg") && path.Base(name) == name)) {
				return bad
			}
			seen[name] = true
			matches := 0
			for _, pkg := range packages {
				if stringField(pkg, "pkg-name") != name {
					continue
				}
				matches++
				data, ok := objectField(pkg, "pkg-data")
				size, err := strconv.ParseUint(stringField(data, "pkg-size"), 10, 64)
				verification := stringField(data, "verify-status")
				if !ok || err != nil || size != files["flash:"+name] || stringField(pkg, "pkg-dir") != "/mnt/sd3/user" || stringField(pkg, "pkg-action") != "install-package-action-none" ||
					(verification != "install-package-verify-ok" && verification != "install-package-verify-deferred" && verification != "install-package-verify-not-done") {
					return bad
				}
			}
			if matches != 1 {
				return bad
			}
		}
		if !seen[archive] {
			return bad
		}
		return nil
	}
	return bad
}

func (a *Adapter) ReadRetiredArchive(ctx context.Context, path string, size int64, dst io.Writer) error {
	t, ok := a.transport.(interface {
		ReadRetiredArchive(context.Context, string, int64, io.Writer) error
	})
	if !ok {
		return lifecycle.ErrUnsupported
	}
	return t.ReadRetiredArchive(ctx, path, size, dst)
}
func (a *Adapter) RemoveRetiredArchive(ctx context.Context, path string) error {
	t, ok := a.transport.(interface {
		RemoveRetiredArchive(context.Context, string) error
	})
	if !ok {
		return lifecycle.ErrUnsupported
	}
	return t.RemoveRetiredArchive(ctx, path)
}
func (a *Adapter) ObserveSWIMBootManifest(ctx context.Context) (string, error) {
	t, ok := a.transport.(transport.DiagnosticExecer)
	if !ok {
		return "", lifecycle.ErrUnsupported
	}
	result, err := t.DiagnosticExec(ctx, []string{"more flash:packages.conf"})
	if err != nil {
		return "", err
	}
	if len(result) != 1 || result[0].Err != "" || len(result[0].Output) == 0 || len(result[0].Output) > 1<<20 {
		return "", fmt.Errorf("boot manifest unavailable")
	}
	return result[0].Output, nil
}

// Qualify the observed XE Present/IMG/new shape only, never Installed, added
// packages, or a default/boot image. A historical rollback entry is not proof
// that extracted packages exist; retain the committed baseline separately.
func archiveOnlyVersion(v map[string]any, packages []map[string]any, files map[string]uint64) (string, string, bool) {
	version := (inventoryVersion{Version: stringField(v, "version"), VersionExtension: stringField(v, "version-extension")}).identity()
	if lifecycle.ValidateTargetVersion(version) != nil || stringField(v, "current") != "install-version-state-present" || v["is-default"] != false {
		return "", "", false
	}
	name := "gNOI_iosxe_" + version + ".bin"
	key := "flash:" + name
	if stringField(v, "src-filename") != "/mnt/sd3/user/"+name || files[key] == 0 {
		return "", "", false
	}
	states, found, err := directNamedList(v, "install-package-state-info")
	if err != nil || !found || len(states) != 1 {
		return "", "", false
	}
	state := states[0]
	if stringField(state, "package-type") != "install-pkg-img" || stringField(state, "package-state") != "install-state-new" || stringField(state, "pkg-name") != name || stringField(state, "pkg-dir") != "/mnt/sd3/user" {
		return "", "", false
	}
	parts := strings.Split(version, ".")
	if len(parts) < 3 {
		return "", "", false
	}
	base := strings.Join(parts[:3], ".")
	for p := range files {
		if strings.HasSuffix(p, ".pkg") && strings.Contains(p, base) {
			return "", "", false
		}
	}
	matches := 0
	for _, pkg := range packages {
		if stringField(pkg, "pkg-name") != name {
			continue
		}
		matches++
		data, ok := objectField(pkg, "pkg-data")
		if !ok {
			return "", "", false
		}
		size, err := strconv.ParseUint(stringField(data, "pkg-size"), 10, 64)
		if err != nil || size != files[key] || stringField(pkg, "pkg-dir") != "/mnt/sd3/user" || stringField(pkg, "pkg-action") != "install-package-action-none" || stringField(data, "verify-status") != "install-package-verify-ok" {
			return "", "", false
		}
	}
	return key, version, matches == 1
}
