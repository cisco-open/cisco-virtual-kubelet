// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type liveFloorImageReceiptTrace struct {
	base      http.RoundTripper
	t         *testing.T
	floorPath string
}

func (trace liveFloorImageReceiptTrace) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := trace.base.RoundTrip(req)
	if err != nil || response == nil || req.Method != http.MethodPost || !strings.HasSuffix(req.URL.Path, "/uploadImage") {
		return response, err
	}
	prefix, readErr := io.ReadAll(io.LimitReader(response.Body, 65537))
	response.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(prefix), response.Body), response.Body}
	if readErr == nil && len(prefix) <= 65536 {
		var root map[string]json.RawMessage
		if json.Unmarshal(prefix, &root) == nil {
			keys := []string{}
			for key := range root {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			var nested map[string]json.RawMessage
			json.Unmarshal(root["response"], &nested)
			nestedKeys := []string{}
			for key := range nested {
				nestedKeys = append(nestedKeys, key)
			}
			sort.Strings(nestedKeys)
			trace.t.Logf("floor upload receipt HTTP=%d JSON keys=%v response keys=%v", response.StatusCode, keys, nestedKeys)
			parts := strings.Split(req.URL.Path, "/")
			target := parts[len(parts)-2]
			var instance, site, hierarchy, group string
			json.Unmarshal(root["instanceUuid"], &instance)
			json.Unmarshal(root["siteHierarchyId"], &site)
			json.Unmarshal(root["hierarchyName"], &hierarchy)
			json.Unmarshal(root["groupInstanceUuid"], &group)
			var imageInfo map[string]json.RawMessage
			json.Unmarshal(root["imageInfo"], &imageInfo)
			trace.t.Logf("floor upload DTO binding: instance=%t site=%t group=%t hierarchy=%t hierarchyAbsent=%t imageMetadata=%t", instance == target, site == target, group == target, strings.TrimPrefix(hierarchy, "/") == trace.floorPath, hierarchy == "", len(imageInfo) > 0)
		}
	}
	return response, err
}

func liveFloorImagePNG(t *testing.T, revision byte) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, 640, 480))
	for y := 0; y < 480; y++ {
		for x := 0; x < 640; x++ {
			pixel := color.RGBA{R: 245, G: 245, B: 245, A: 255}
			if x%80 < 3 || y%80 < 3 {
				pixel = color.RGBA{R: 32, G: 64, B: revision, A: 255}
			}
			picture.SetRGBA(x, y, pixel)
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, picture); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// Exercise actual upload requests through the public manifest reconciler and
// simulated Kubernetes persistence. Site ownership/cleanup belongs to the
// enclosing isolated hierarchy harness, not any existing lab floor.
func qualifyLiveFloorImageManifest(t *testing.T, ctx context.Context, h *liveSiteManifest, settled *bool) {
	t.Helper()
	if h == nil {
		t.Fatal("floor image qualification requires the isolated public manifest harness")
	}
	rest, _, err := h.a.client.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := rest.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	var root map[string]any
	if err := json.Unmarshal(h.config.Spec.Source.Inline.Raw, &root); err != nil {
		t.Fatal(err)
	}
	floors := root["catalyst_center"].(map[string]any)["sites"].(map[string]any)["floors"].([]any)
	if len(floors) != 1 {
		t.Fatal("isolated floor scope")
	}
	floor := floors[0].(map[string]any)
	rest.HTTPClient.Transport = liveFloorImageReceiptTrace{base: base, t: t, floorPath: floor["parent_name"].(string) + "/" + floor["name"].(string)}
	floors[0].(map[string]any)["image_path"] = "maps/cvk-floor.png"
	model, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	cm := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "floor-images", Namespace: h.config.Namespace, UID: "live-floor-image-source"}, Data: map[string]string{"model.json": string(model), floorImageAssetsKey: `{"maps/cvk-floor.png":"floor-png"}`}, BinaryData: map[string][]byte{"floor-png": liveFloorImagePNG(t, 96)}}
	if err := h.a.statusWriter.Create(ctx, cm); err != nil {
		t.Fatal(err)
	}
	if err := h.a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(h.config), h.config); err != nil {
		t.Fatal(err)
	}
	h.config.Generation++
	h.config.Spec.Source.Inline = nil
	h.config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model.json"}
	if err := h.a.statusWriter.Update(ctx, h.config); err != nil {
		t.Fatal(err)
	}
	for revision := 0; revision < 2; revision++ {
		if revision > 0 {
			if err := h.a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(cm), cm); err != nil {
				t.Fatal(err)
			}
			cm.BinaryData["floor-png"] = liveFloorImagePNG(t, 160)
			if err := h.a.statusWriter.Update(ctx, cm); err != nil {
				t.Fatal(err)
			}
		}
		source, err := loadControllerSource(ctx, h.a.statusReader, h.config)
		if err != nil {
			t.Fatal(err)
		}
		_, _, images, err := parseSitesWithFloorImages(source.raw, source.floorImages)
		if err != nil || len(images) != 1 {
			t.Fatal("bound image model", err)
		}
		*settled = false
		h.run(t, ctx, images[0].synchronousOperationResource(), "Update")
		*settled = true
		state := journalState(t, h.a)
		found := 0
		for _, receipt := range state.ActionApplications {
			if receipt.Resource == images[0].resource() {
				found++
				if receipt.Operation.Resource != images[0].synchronousOperationResource() {
					t.Fatal("upload receipt did not retain exact source bytes")
				}
			}
		}
		if found != 1 {
			t.Fatal("upload receipt missing or duplicated")
		}
		t.Logf("floor image revision %d completed through public journal with exact source/floor/synchronous-response receipt; restarted no-op preserved", revision+1)
	}
	if err := h.a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(h.config), h.config); err != nil {
		t.Fatal(err)
	}
	h.config.Generation++
	h.config.Spec.Mode = configapi.NetworkControllerApplyModeReport
	if err := h.a.statusWriter.Update(ctx, h.config); err != nil {
		t.Fatal(err)
	}
	if _, err := (&configReconciler{a: h.a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(h.config)}); err != nil {
		t.Fatal(err)
	}
	if err := h.a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(h.config), h.config); err != nil {
		t.Fatal(err)
	}
	if h.config.Status.Phase != configapi.NetworkControllerConfigPhaseWaiting {
		t.Fatal("image Report claimed complete byte observation")
	}
	t.Log("public Report preserved geometry observations and explicitly left remote image contents unverified")
}
