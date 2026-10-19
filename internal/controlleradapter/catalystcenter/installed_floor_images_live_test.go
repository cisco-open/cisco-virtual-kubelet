//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// All uploads execute inside the installed worker. This observer uses the same
// source parser/receipt validator and never substitutes an upload closure.
func qualifyInstalledFloorImages(t *testing.T, ctx context.Context, api ctrlclient.Client, c *client, config *configapi.NetworkControllerConfig, nc *cvk.NetworkController, refresh func(), wait func(func() bool)) {
	t.Helper()
	ref := config.Spec.Source.ConfigMapRef
	if ref == nil || !strings.HasPrefix(ref.Name, "installed-floor-assets-") {
		t.Fatal("requires generated installed floor-asset ConfigMap")
	}
	var cm core.ConfigMap
	if err := api.Get(ctx, ctrlclient.ObjectKey{Namespace: config.Namespace, Name: ref.Name}, &cm); err != nil {
		t.Fatal(err)
	}
	uid, generation := cm.UID, config.Generation
	coreRows := nc.Status.Configuration.DeepCopy().ActionApplications
	var model map[string]any
	if json.Unmarshal([]byte(cm.Data[ref.Key]), &model) != nil {
		t.Fatal("invalid installed image source")
	}
	floors := model["catalyst_center"].(map[string]any)["sites"].(map[string]any)["floors"].([]any)
	if len(floors) != 1 {
		t.Fatal("requires sole generated qualification floor")
	}
	floor := floors[0].(map[string]any)
	floorPath := floor["parent_name"].(string) + "/" + floor["name"].(string)
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	floorID := ""
	for _, site := range sites {
		if site.NameHierarchy == floorPath && site.Type == "floor" {
			floorID = site.ID
		}
	}
	if floorID == "" {
		t.Fatal("installed image floor identity missing")
	}
	update := func() {
		t.Helper()
		if cm.UID != uid {
			t.Fatal("floor asset ConfigMap replaced")
		}
		raw, err := json.Marshal(model)
		if err != nil {
			t.Fatal(err)
		}
		cm.Data[ref.Key] = string(raw)
		if err := api.Update(ctx, &cm); err != nil {
			t.Fatal(err)
		}
	}
	verifyCore := func() {
		t.Helper()
		rows := []cvk.NetworkControllerConfigurationActionApplication{}
		for _, row := range nc.Status.Configuration.ActionApplications {
			key, _, err := floorImageRowKey(row, time.Now())
			if err != nil {
				t.Fatal("invalid installed image receipt classification", err)
			}
			if key == "" {
				rows = append(rows, row)
			}
		}
		if !reflect.DeepEqual(coreRows, rows) {
			t.Fatal("image upload changed private geometry creator/latest receipts")
		}
	}
	revisions := 2
	if os.Getenv("CATC_INSTALLED_FLOOR_IMAGES") == "two-revisions-and-path-replace" || os.Getenv("CATC_INSTALLED_FLOOR_IMAGES") == "omit-reintroduce" {
		revisions = 3
	}
	formatMode := os.Getenv("CATC_INSTALLED_FLOOR_IMAGES") == "format-lifetimes"
	if formatMode {
		revisions = 4 // PNG, JPEG, PDF, then identical PDF bytes under a new path.
	}
	previousDigest, previousResource := "", ""
	for revision := 0; revision < revisions; revision++ {
		filename, pixelRevision := "maps/cvk-installed.png", byte(96)
		if revision > 0 {
			pixelRevision = 160
		}
		if revision == 2 {
			filename = "maps/renamed/cvk-installed.png"
		}
		content := liveFloorImagePNG(t, pixelRevision)
		if formatMode {
			switch revision {
			case 1:
				filename, content = "maps/cvk-installed.jpg", liveFloorImageJPEG(t, pixelRevision)
			case 2, 3:
				filename, content = "maps/cvk-installed.pdf", liveFloorImagePDF()
				if revision == 3 {
					filename = "maps/renamed/cvk-installed.pdf"
				}
			}
		}
		floor["image_path"] = filename
		index, _ := json.Marshal(map[string]string{filename: "floor-image"})
		cm.Data[floorImageAssetsKey] = string(index)
		if cm.BinaryData == nil {
			cm.BinaryData = map[string][]byte{}
		}
		cm.BinaryData["floor-image"] = content
		update()
		source, err := loadControllerSource(ctx, api, config)
		if err != nil || source.uid != uid || source.version != cm.ResourceVersion {
			t.Fatal("asset source snapshot mismatch", err)
		}
		_, placements, images, err := parseSitesWithFloorImages(source.raw, source.floorImages)
		if err != nil || len(images) != 1 || len(placements) != 0 {
			t.Fatal("invalid bound image-only revision", err)
		}
		want := images[0]
		pathReplacement := revision == 2 && !formatMode || revision == 3 && formatMode
		if pathReplacement && (want.digest != previousDigest || want.synchronousOperationResource() == previousResource) {
			t.Fatal("source_path replacement must preserve bytes and change operation identity")
		}
		wait(func() bool {
			refresh()
			if config.Generation != generation {
				t.Fatal("asset-only update changed configuration generation")
			}
			state := nc.Status.Configuration
			if state == nil || state.Operation != nil || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
				return false
			}
			found := false
			for _, row := range state.ActionApplications {
				if row.Resource == want.resource() && row.Operation.Resource == want.synchronousOperationResource() && row.Operation.SourceUID == string(uid) && row.Operation.SourceVersion == cm.ResourceVersion && row.Operation.TaskID == "" {
					found = true
				}
			}
			if !found {
				return false
			}
			applied, err := floorImageApplied(want, floorID, config.Name, string(config.UID), state.ActionApplications, time.Now())
			if err != nil || !applied || len(state.ActionApplications) != len(coreRows)+1 {
				t.Fatal("installed synchronous floor receipt invalid or duplicated", err)
			}
			verifyCore()
			return true
		})
		t.Logf("installed synchronous floor image revision %d verified with exact floor identity, ConfigMap revision and source-byte digest in one bounded receipt slot; no asynchronous task inferred", revision+1)
		if formatMode {
			t.Logf("installed source-bound image format verified: %s", filename)
		}
		if pathReplacement {
			t.Log("installed identical-byte source_path replacement completed through v2 filename identity; legacy receipts were not reinterpreted")
		}
		previousDigest, previousResource = want.digest, want.synchronousOperationResource()
	}
	retained := nc.Status.Configuration.DeepCopy().ActionApplications
	if config.Status.LastControllerCheck == nil {
		t.Fatal("image completion lacks installed check timestamp")
	}
	checked := config.Status.LastControllerCheck.DeepCopy()
	lastFilename := floor["image_path"].(string)
	delete(floor, "image_path")
	update()
	wait(func() bool {
		refresh()
		if nc.Status.Configuration == nil || nc.Status.Configuration.Operation != nil || len(retained) != len(nc.Status.Configuration.ActionApplications) {
			t.Fatal("image omission dispatched native work or changed receipt capacity")
		}
		omitted := false
		for i, row := range nc.Status.Configuration.ActionApplications {
			key, w, err := floorImageRowKey(row, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if key == "" {
				if !reflect.DeepEqual(row, retained[i]) {
					t.Fatal("omission changed geometry proof")
				}
				continue
			}
			if !reflect.DeepEqual(row.Operation, retained[i].Operation) || !reflect.DeepEqual(row.VerifiedAt, retained[i].VerifiedAt) {
				t.Fatal("omission replaced original image acknowledgement")
			}
			omitted = w != nil && w.SourceUID == string(uid) && w.SourceVersion == cm.ResourceVersion
		}
		return omitted && config.Generation == generation && config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.LastControllerCheck != nil && config.Status.LastControllerCheck.After(checked.Time) && len(config.Status.SectionStatus) == 1 && config.Status.SectionStatus[0].ResourceCount == 3
	})
	t.Log("installed ConfigMap-only asset revisions preserved CR generation; image_path omission retained upload evidence and returned to geometry-only InSync without claiming remote byte equality")
	if os.Getenv("CATC_INSTALLED_FLOOR_IMAGES") == "omit-reintroduce" || formatMode {
		for cycle := 0; cycle < 2; cycle++ {
			before := nc.Status.Configuration.DeepCopy().ActionApplications
			floor["image_path"] = lastFilename
			update()
			source, err := loadControllerSource(ctx, api, config)
			if err != nil {
				t.Fatal(err)
			}
			_, _, images, err := parseSitesWithFloorImages(source.raw, source.floorImages)
			if err != nil || len(images) != 1 {
				t.Fatal("reintroduced image invalid", err)
			}
			want := images[0]
			if want.digest != previousDigest || want.synchronousOperationResource() != previousResource {
				t.Fatal("reintroduction must preserve both path and bytes")
			}
			wait(func() bool {
				refresh()
				if config.Generation != generation {
					t.Fatal("source-only reintroduction changed generation")
				}
				state := nc.Status.Configuration
				if state == nil || state.Operation != nil || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
					return false
				}
				for i, row := range state.ActionApplications {
					if row.Resource != want.resource() || row.Operation.SourceVersion != cm.ResourceVersion || row.Operation.SourceUID != string(uid) {
						continue
					}
					applied, err := floorImageApplied(want, floorID, config.Name, string(config.UID), state.ActionApplications, time.Now())
					if err != nil || !applied || row.Operation.ID == before[i].Operation.ID || row.Operation.TaskID != "" || len(state.ActionApplications) != len(coreRows)+1 {
						t.Fatal("reintroduction replayed proof or lost bounded synchronous completion", err)
					}
					verifyCore()
					return true
				}
				return false
			})
			t.Logf("installed same-path/same-byte reintroduction cycle %d completed with a new operation and unchanged CR generation in the same receipt slot", cycle+1)
			if cycle == 0 {
				delete(floor, "image_path")
				update()
				wait(func() bool {
					refresh()
					if nc.Status.Configuration == nil || nc.Status.Configuration.Operation != nil || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
						return false
					}
					for _, row := range nc.Status.Configuration.ActionApplications {
						key, w, err := floorImageRowKey(row, time.Now())
						if err != nil {
							t.Fatal(err)
						}
						if key == want.resource() && w != nil && w.SourceUID == string(uid) && w.SourceVersion == cm.ResourceVersion {
							verifyCore()
							return true
						}
					}
					return false
				})
			}
		}
	}
}
