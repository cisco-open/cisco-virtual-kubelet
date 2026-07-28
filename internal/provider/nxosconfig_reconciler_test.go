// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
	enginewriters "github.com/cisco/virtual-kubelet-cisco/internal/configengine/writers"
	nxosschema "github.com/cisco/virtual-kubelet-cisco/internal/drivers/nxos/configdriver/schema"
	nxoswriters "github.com/cisco/virtual-kubelet-cisco/internal/drivers/nxos/configdriver/writers"
)

type noopErrLogger struct{}

func (noopErrLogger) Error(error, string, ...any) {}

func validNXOSModelSource() *configv1alpha1.NetAsCodeModelSource {
	contract := nxosNetAsCodeContracts["0.3.0"]
	return &configv1alpha1.NetAsCodeModelSource{
		Format:         configv1alpha1.NetAsCodeModelFormatNXOS,
		ModelVersion:   contract.ModelVersion,
		SchemaDigest:   contract.SchemaDigest,
		Resolved:       true,
		Exporter:       "cvk-test@1.0.0",
		SourceRevision: "0123456789abcdef0123456789abcdef01234567",
	}
}

func testedNXOSWriter(family, release string) enginewriters.SectionWriter {
	if release == "" {
		release = "10.3(9)"
	}
	return nxoswriters.GetForRelease(family, release)
}

type fakeNXOSTransport struct {
	hostname     string
	mtu          int
	features     map[string]bool
	featureSets  map[string]bool
	vlans        map[int]string
	deleteOps    []string
	fetches      int
	saves        int
	beforeMutate func() error
}

func (f *fakeNXOSTransport) Capabilities() transport.Capabilities {
	return transport.Capabilities{Kind: transport.KindREST, SupportsWritableRunning: true, SupportsSaveStartup: true}
}

func (f *fakeNXOSTransport) Fetch(_ context.Context, path string) ([]byte, error) {
	f.fetches++
	switch path {
	case nxosschema.PathSystemHostname:
		system := map[string]any{"hostname": f.hostname}
		if f.mtu > 0 {
			system["mtu"] = f.mtu
		}
		return json.Marshal(system)
	case nxosschema.PathFeature:
		out := map[string]any{}
		for key, val := range f.features {
			out[key] = val
		}
		return json.Marshal(out)
	case nxosschema.PathFeatureSet:
		out := map[string]any{}
		for key, val := range f.featureSets {
			out[key] = val
		}
		return json.Marshal(out)
	case nxosschema.PathVLANBrief:
		ids := make([]int, 0, len(f.vlans))
		for id := range f.vlans {
			ids = append(ids, id)
		}
		sort.Ints(ids)
		vlans := make([]any, 0, len(ids))
		for _, id := range ids {
			vlans = append(vlans, map[string]any{"id": id, "name": f.vlans[id]})
		}
		return json.Marshal(map[string]any{"vlans": vlans})
	case nxosschema.PathInterfaceEthernet:
		return json.Marshal(map[string]any{"interfaces": []any{}})
	default:
		return nil, transport.ErrUnsupported
	}
}

func (f *fakeNXOSTransport) StartTransaction(context.Context) (transport.TxHandle, error) {
	return "", transport.ErrUnsupported
}

func (f *fakeNXOSTransport) Mutate(_ context.Context, _ transport.TxHandle, ops []transport.Op) error {
	if f.beforeMutate != nil {
		if err := f.beforeMutate(); err != nil {
			return err
		}
	}
	for _, op := range ops {
		if op.Verb == transport.VerbDelete {
			f.deleteOps = append(f.deleteOps, op.Path)
			if id, ok := fakeVLANIDFromDMEPath(op.Path); ok {
				delete(f.vlans, id)
			}
			continue
		}
		var body map[string]any
		if err := json.Unmarshal(op.Body, &body); err != nil {
			return err
		}
		top, _ := body["topSystem"].(map[string]any)
		attrs, _ := top["attributes"].(map[string]any)
		if name, ok := attrs["name"].(string); ok {
			f.hostname = name
		}
		if mtuRaw := findFakeDMEAttrString(body, "ethpmInst", "systemJumboMtu"); mtuRaw != "" {
			mtu, err := strconv.Atoi(mtuRaw)
			if err != nil {
				return err
			}
			f.mtu = mtu
		}
		for _, attrs := range findFakeDMEClassAttrs(body, "l2BD") {
			encap, _ := attrs["fabEncap"].(string)
			id, ok := vlanIDFromFakeEncap(encap)
			if !ok {
				continue
			}
			if f.vlans == nil {
				f.vlans = map[int]string{}
			}
			name, _ := attrs["name"].(string)
			f.vlans[id] = name
		}
		for _, mapping := range nxosschema.FeatureDMEMappings() {
			if state := findFakeDMEAttrString(body, mapping.Class, "adminSt"); state != "" {
				if f.features == nil {
					f.features = map[string]bool{}
				}
				f.features[mapping.Field] = state == "enabled"
			}
		}
		for _, attrs := range findFakeDMEClassAttrs(body, "fsetFeatureSet") {
			name, _ := attrs["name"].(string)
			state, _ := attrs["adminSt"].(string)
			if name != "" && state != "" {
				if f.featureSets == nil {
					f.featureSets = map[string]bool{}
				}
				f.featureSets[name] = state == "enabled"
			}
		}
	}
	return nil
}

func (*fakeNXOSTransport) Commit(context.Context, transport.TxHandle) error  { return nil }
func (*fakeNXOSTransport) Discard(context.Context, transport.TxHandle) error { return nil }
func (f *fakeNXOSTransport) SaveStartup(context.Context) error {
	f.saves++
	return nil
}
func (*fakeNXOSTransport) Close() error { return nil }

func findFakeDMEAttrString(v any, class, attr string) string {
	switch x := v.(type) {
	case map[string]any:
		if obj, ok := x[class].(map[string]any); ok {
			if attrs, ok := obj["attributes"].(map[string]any); ok {
				if val, ok := attrs[attr].(string); ok {
					return val
				}
			}
		}
		for _, child := range x {
			if val := findFakeDMEAttrString(child, class, attr); val != "" {
				return val
			}
		}
	case []any:
		for _, child := range x {
			if val := findFakeDMEAttrString(child, class, attr); val != "" {
				return val
			}
		}
	}
	return ""
}

func findFakeDMEClassAttrs(v any, class string) []map[string]any {
	var out []map[string]any
	switch x := v.(type) {
	case map[string]any:
		if obj, ok := x[class].(map[string]any); ok {
			if attrs, ok := obj["attributes"].(map[string]any); ok {
				out = append(out, attrs)
			}
		}
		for _, child := range x {
			out = append(out, findFakeDMEClassAttrs(child, class)...)
		}
	case []any:
		for _, child := range x {
			out = append(out, findFakeDMEClassAttrs(child, class)...)
		}
	}
	return out
}

func fakeVLANIDFromDMEPath(path string) (int, bool) {
	start := strings.Index(path, "bd-[vlan-")
	if start < 0 {
		return 0, false
	}
	rest := path[start+len("bd-[vlan-"):]
	end := strings.Index(rest, "]")
	if end < 0 {
		return 0, false
	}
	id, err := strconv.Atoi(rest[:end])
	return id, err == nil
}

func vlanIDFromFakeEncap(encap string) (int, bool) {
	raw := strings.TrimPrefix(encap, "vlan-")
	if raw == encap {
		return 0, false
	}
	id, err := strconv.Atoi(raw)
	return id, err == nil
}

func TestNXOSConfigReconcilerRecordsInSync(t *testing.T) {
	scheme := newTestScheme(t)
	raw := runtime.RawExtension{Raw: []byte(`{"system":{"hostname":"leaf-01","mtu":9216}}`)}
	cr := &configv1alpha1.NXOSConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "leaf-config", Namespace: "network", Generation: 1},
		Spec: configv1alpha1.NXOSConfigSpec{
			DeviceRef:       configv1alpha1.DeviceRef{Name: "leaf-01"},
			ManagedFamilies: []string{"system"},
			ModelSource:     validNXOSModelSource(),
			Source:          configv1alpha1.ConfigurationSource{Inline: &raw},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.NXOSConfig{}).
		WithObjects(cr).
		Build()
	tr := &fakeNXOSTransport{hostname: "old", mtu: 1500}
	r := &NXOSConfigReconciler{
		Client:     c,
		DeviceName: "leaf-01",
		Transport:  tr,
		Lookup:     testedNXOSWriter,
	}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "network", Name: "leaf-config"}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if tr.hostname != "leaf-01" {
		t.Fatalf("hostname=%q", tr.hostname)
	}
	if tr.mtu != 9216 {
		t.Fatalf("mtu=%d", tr.mtu)
	}
	var got configv1alpha1.NXOSConfig
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "network", Name: "leaf-config"}, &got); err != nil {
		t.Fatalf("get updated: %v", err)
	}
	if got.Status.Phase != "InSync" {
		t.Fatalf("phase=%q status=%#v", got.Status.Phase, got.Status)
	}
	if len(got.Status.FamilyStatus) != 1 || got.Status.FamilyStatus[0].Name != "system" {
		t.Fatalf("family status=%#v", got.Status.FamilyStatus)
	}
}

func TestNXOSConfigReconcilerBlocksUnvalidatedDeviceVersionsBeforeFetch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version string
		reason  string
	}{
		{name: "version pending", reason: "DeviceVersionPending"},
		{name: "unsupported version", version: "10.6(1)", reason: "UnsupportedDeviceVersion"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scheme := newTestScheme(t)
			raw := runtime.RawExtension{Raw: []byte(`{"system":{"hostname":"leaf-01"}}`)}
			cr := &configv1alpha1.NXOSConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "leaf-config", Namespace: "network", Generation: 1},
				Spec: configv1alpha1.NXOSConfigSpec{
					DeviceRef:       configv1alpha1.DeviceRef{Name: "leaf-01"},
					ManagedFamilies: []string{"system"},
					ModelSource:     validNXOSModelSource(),
					Source:          configv1alpha1.ConfigurationSource{Inline: &raw},
				},
			}
			c := fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&configv1alpha1.NXOSConfig{}).
				WithObjects(cr).
				Build()
			tr := &fakeNXOSTransport{hostname: "old"}
			r := &NXOSConfigReconciler{
				Client:                c,
				DeviceName:            "leaf-01",
				Transport:             tr,
				Lookup:                nxoswriters.GetForRelease,
				DeviceVersion:         tc.version,
				ValidateDeviceVersion: nxosschema.ValidateDeviceVersion,
				IsUnsupportedVersion:  nxosschema.IsUnsupportedDeviceVersion,
				RequireDeviceVersion:  true,
			}
			if _, err := r.Reconcile(context.Background(), ctrl.Request{
				NamespacedName: types.NamespacedName{Namespace: "network", Name: "leaf-config"},
			}); err != nil {
				t.Fatalf("Reconcile: %v", err)
			}
			if tr.fetches != 0 {
				t.Fatalf("device configuration fetched %d time(s) while version was blocked", tr.fetches)
			}
			var got configv1alpha1.NXOSConfig
			if err := c.Get(context.Background(), types.NamespacedName{Namespace: "network", Name: "leaf-config"}, &got); err != nil {
				t.Fatalf("get updated: %v", err)
			}
			if got.Status.Phase != engine.PhasePending {
				t.Fatalf("phase=%q, want %q", got.Status.Phase, engine.PhasePending)
			}
			if len(got.Status.Conditions) == 0 || got.Status.Conditions[0].Reason != tc.reason {
				t.Fatalf("conditions=%#v, want reason %q", got.Status.Conditions, tc.reason)
			}
		})
	}
}

func TestNXOSConfigReconcilerRefreshRejectsNewUnsupportedVersion(t *testing.T) {
	scheme := newTestScheme(t)
	raw := runtime.RawExtension{Raw: []byte(`{"system":{"hostname":"leaf-01"}}`)}
	cr := &configv1alpha1.NXOSConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "leaf-config", Namespace: "network", Generation: 1},
		Spec: configv1alpha1.NXOSConfigSpec{
			DeviceRef:       configv1alpha1.DeviceRef{Name: "leaf-01"},
			ManagedFamilies: []string{"system"},
			ModelSource:     validNXOSModelSource(),
			Source:          configv1alpha1.ConfigurationSource{Inline: &raw},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.NXOSConfig{}).
		WithObjects(cr).
		Build()
	tr := &fakeNXOSTransport{hostname: "old"}
	r := &NXOSConfigReconciler{
		Client:                c,
		DeviceName:            "leaf-01",
		Transport:             tr,
		Lookup:                nxoswriters.GetForRelease,
		DeviceVersion:         "10.3(9)",
		FetchDeviceVersion:    func(context.Context, transport.Interface) string { return "10.6(1)" },
		ValidateDeviceVersion: nxosschema.ValidateDeviceVersion,
		IsUnsupportedVersion:  nxosschema.IsUnsupportedDeviceVersion,
		ReleaseTagForVersion:  nxosschema.ReleaseTagForDeviceVersionString,
		RequireDeviceVersion:  true,
		SupportedYANGVersions: nxosschema.SupportedDeviceVersionSet(),
		DefaultYANGVersion:    "10.3(9)",
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "network", Name: "leaf-config"},
	}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if tr.fetches != 0 || tr.hostname != "old" {
		t.Fatalf("configuration touched after unsupported refresh: fetches=%d hostname=%q", tr.fetches, tr.hostname)
	}
	var got configv1alpha1.NXOSConfig
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "network", Name: "leaf-config"}, &got); err != nil {
		t.Fatalf("get updated: %v", err)
	}
	if got.Status.Phase != engine.PhasePending || len(got.Status.Conditions) == 0 || got.Status.Conditions[0].Reason != "UnsupportedDeviceVersion" {
		t.Fatalf("status=%#v", got.Status)
	}
}

func TestNXOSConfigReconcilerRefreshFailsClosedWhenLiveVersionDisappears(t *testing.T) {
	scheme := newTestScheme(t)
	raw := runtime.RawExtension{Raw: []byte(`{"system":{"hostname":"leaf-01"}}`)}
	cr := &configv1alpha1.NXOSConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "leaf-config", Namespace: "network", Generation: 1},
		Spec: configv1alpha1.NXOSConfigSpec{
			DeviceRef:       configv1alpha1.DeviceRef{Name: "leaf-01"},
			ManagedFamilies: []string{"system"},
			ModelSource:     validNXOSModelSource(),
			Source:          configv1alpha1.ConfigurationSource{Inline: &raw},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.NXOSConfig{}).
		WithObjects(cr).
		Build()
	tr := &fakeNXOSTransport{hostname: "old"}
	r := &NXOSConfigReconciler{
		Client:                c,
		DeviceName:            "leaf-01",
		Transport:             tr,
		Lookup:                nxoswriters.GetForRelease,
		DeviceVersion:         "10.3(9)",
		DefaultYANGVersion:    "10.3(9)",
		FetchDeviceVersion:    func(context.Context, transport.Interface) string { return "" },
		ValidateDeviceVersion: nxosschema.ValidateDeviceVersion,
		IsUnsupportedVersion:  nxosschema.IsUnsupportedDeviceVersion,
		ReleaseTagForVersion:  nxosschema.ReleaseTagForDeviceVersionString,
		RequireDeviceVersion:  true,
		SupportedYANGVersions: nxosschema.SupportedDeviceVersionSet(),
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "network", Name: "leaf-config"},
	}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if tr.fetches != 0 || tr.hostname != "old" {
		t.Fatalf("configuration touched with unavailable live version: fetches=%d hostname=%q", tr.fetches, tr.hostname)
	}
	if got := r.common().deviceVersion(); got != "" {
		t.Fatalf("stale deviceVersion retained: %q", got)
	}
	if got := r.common().defaultYANGVersion(); got != "" {
		t.Fatalf("stale defaultYANGVersion retained: %q", got)
	}
	var got configv1alpha1.NXOSConfig
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "network", Name: "leaf-config"}, &got); err != nil {
		t.Fatalf("get updated: %v", err)
	}
	if got.Status.Phase != engine.PhasePending || len(got.Status.Conditions) == 0 || got.Status.Conditions[0].Reason != "DeviceVersionPending" {
		t.Fatalf("status=%#v", got.Status)
	}
}

func TestNXOSConfigReconcilerRefreshRecoversPendingVersion(t *testing.T) {
	t.Setenv(nxosschema.EnvAllowExperimentalReleases, "true")
	scheme := newTestScheme(t)
	raw := runtime.RawExtension{Raw: []byte(`{"system":{"hostname":"leaf-01"}}`)}
	cr := &configv1alpha1.NXOSConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "leaf-config", Namespace: "network", Generation: 1},
		Spec: configv1alpha1.NXOSConfigSpec{
			DeviceRef:       configv1alpha1.DeviceRef{Name: "leaf-01"},
			ManagedFamilies: []string{"system"},
			ModelSource:     validNXOSModelSource(),
			Source:          configv1alpha1.ConfigurationSource{Inline: &raw},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.NXOSConfig{}).
		WithObjects(cr).
		Build()
	tr := &fakeNXOSTransport{hostname: "old"}
	reportedVersion := "10.3(9)"
	r := &NXOSConfigReconciler{
		Client:                c,
		DeviceName:            "leaf-01",
		Transport:             tr,
		Lookup:                nxoswriters.GetForRelease,
		FetchDeviceVersion:    func(context.Context, transport.Interface) string { return reportedVersion },
		ValidateDeviceVersion: nxosschema.ValidateDeviceVersion,
		IsUnsupportedVersion:  nxosschema.IsUnsupportedDeviceVersion,
		ReleaseTagForVersion:  nxosschema.ReleaseTagForDeviceVersionString,
		RequireDeviceVersion:  true,
		SupportedYANGVersions: nxosschema.SupportedDeviceVersionSet(),
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "network", Name: "leaf-config"},
	}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if tr.hostname != "leaf-01" {
		t.Fatalf("hostname=%q, want recovered apply", tr.hostname)
	}
	if got := r.common().deviceVersion(); got != "10.3(9)" {
		t.Fatalf("deviceVersion=%q", got)
	}
	if got := r.common().defaultYANGVersion(); got != "10.3(9)" {
		t.Fatalf("defaultYANGVersion=%q", got)
	}

	// A supported-to-supported software change must invalidate the intent
	// hash and force immediate Fetch/Diff/Verify even when the CR generation
	// and canonical configuration are unchanged.
	reportedVersion = "10.5(4)"
	tr.fetches = 0
	if _, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "network", Name: "leaf-config"},
	}); err != nil {
		t.Fatalf("Reconcile after supported upgrade: %v", err)
	}
	if tr.fetches == 0 {
		t.Fatal("supported release change was hidden by the unchanged-intent short circuit")
	}
	var got configv1alpha1.NXOSConfig
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "network", Name: "leaf-config"}, &got); err != nil {
		t.Fatalf("get upgraded status: %v", err)
	}
	if got.Status.SourceYangVersion != "10.5(4)" {
		t.Fatalf("sourceYangVersion=%q, want 10.5(4)", got.Status.SourceYangVersion)
	}
}

func TestNXOSConfigReconcilerRejectsTargetReleaseDifferentFromLiveProfile(t *testing.T) {
	scheme := newTestScheme(t)
	raw := runtime.RawExtension{Raw: []byte(`{"system":{"hostname":"leaf-01"}}`)}
	cr := &configv1alpha1.NXOSConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "leaf-config", Namespace: "network", Generation: 1},
		Spec: configv1alpha1.NXOSConfigSpec{
			DeviceRef:         configv1alpha1.DeviceRef{Name: "leaf-01"},
			ManagedFamilies:   []string{"system"},
			TargetYangVersion: "10.5(4)",
			ModelSource:       validNXOSModelSource(),
			Source:            configv1alpha1.ConfigurationSource{Inline: &raw},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.NXOSConfig{}).
		WithObjects(cr).
		Build()
	tr := &fakeNXOSTransport{hostname: "old"}
	r := &NXOSConfigReconciler{
		Client:                c,
		DeviceName:            "leaf-01",
		Transport:             tr,
		Lookup:                nxoswriters.GetForRelease,
		DeviceVersion:         "10.3(9)",
		ValidateDeviceVersion: nxosschema.ValidateDeviceVersion,
		IsUnsupportedVersion:  nxosschema.IsUnsupportedDeviceVersion,
		RequireDeviceVersion:  true,
	}
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "network", Name: "leaf-config"},
	})
	if err == nil || !strings.Contains(err.Error(), "does not match live NX-OS") {
		t.Fatalf("Reconcile error=%v, want target/live mismatch", err)
	}
	if tr.fetches != 0 || tr.hostname != "old" {
		t.Fatalf("device touched for target/live mismatch: fetches=%d hostname=%q", tr.fetches, tr.hostname)
	}
}

func TestNXOSConfigReconcilerAppliesFeatureFamilies(t *testing.T) {
	scheme := newTestScheme(t)
	raw := runtime.RawExtension{Raw: []byte(`{
		"feature": {
			"lldp": true,
			"bgp": false,
			"fabric_forwarding": true
		},
		"feature_set": {
			"fex": true,
			"mpls": false
		}
	}`)}
	cr := &configv1alpha1.NXOSConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "leaf-config", Namespace: "network", Generation: 1},
		Spec: configv1alpha1.NXOSConfigSpec{
			DeviceRef:       configv1alpha1.DeviceRef{Name: "leaf-01"},
			ManagedFamilies: []string{"feature", "feature_set"},
			ModelSource:     validNXOSModelSource(),
			Source:          configv1alpha1.ConfigurationSource{Inline: &raw},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.NXOSConfig{}).
		WithObjects(cr).
		Build()
	tr := &fakeNXOSTransport{
		features:    map[string]bool{"lldp": false, "bgp": true, "fabric_forwarding": false},
		featureSets: map[string]bool{"fex": false, "mpls": true},
	}
	r := &NXOSConfigReconciler{
		Client:      c,
		DeviceName:  "leaf-01",
		Transport:   tr,
		Lookup:      testedNXOSWriter,
		FamilyOrder: nxosschema.FamilyOrder,
	}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "network", Name: "leaf-config"}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if tr.features["lldp"] != true || tr.features["bgp"] != false || tr.features["fabric_forwarding"] != true {
		t.Fatalf("features=%#v", tr.features)
	}
	if tr.featureSets["fex"] != true || tr.featureSets["mpls"] != false {
		t.Fatalf("featureSets=%#v", tr.featureSets)
	}
	var got configv1alpha1.NXOSConfig
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "network", Name: "leaf-config"}, &got); err != nil {
		t.Fatalf("get updated: %v", err)
	}
	if got.Status.Phase != "InSync" {
		t.Fatalf("phase=%q status=%#v", got.Status.Phase, got.Status)
	}
	verified := map[string]bool{}
	for _, family := range got.Status.VerifiedFamilies {
		verified[family] = true
	}
	if !verified["feature"] || !verified["feature_set"] {
		t.Fatalf("verifiedFamilies=%#v", got.Status.VerifiedFamilies)
	}
}

func TestNXOSConfigReconcilerRecordsInSyncFromFullNetAsCodeEnvelope(t *testing.T) {
	scheme := newTestScheme(t)
	raw := runtime.RawExtension{Raw: []byte(`{
		"nxos": {
			"global": {
				"variables": {"hostname": "leaf-01"},
				"configuration": {"system": {"hostname": "${hostname}"}}
			},
			"devices": [{"name": "leaf-01"}]
		}
	}`)}
	cr := &configv1alpha1.NXOSConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "leaf-config", Namespace: "network", Generation: 1},
		Spec: configv1alpha1.NXOSConfigSpec{
			DeviceRef:       configv1alpha1.DeviceRef{Name: "leaf-01"},
			ManagedFamilies: []string{"system"},
			Source:          configv1alpha1.ConfigurationSource{Inline: &raw},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.NXOSConfig{}).
		WithObjects(cr).
		Build()
	tr := &fakeNXOSTransport{hostname: "old"}
	r := &NXOSConfigReconciler{
		Client:     c,
		DeviceName: "leaf-01",
		Transport:  tr,
		Lookup:     testedNXOSWriter,
	}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "network", Name: "leaf-config"}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if tr.hostname != "leaf-01" {
		t.Fatalf("hostname=%q", tr.hostname)
	}
}

func TestNXOSConfigReconcilerRejectsResolvedHierarchicalEnvelopeBeforeConfigFetch(t *testing.T) {
	scheme := newTestScheme(t)
	raw := runtime.RawExtension{Raw: []byte(`{
		"nxos": {"devices": [{"name": "leaf-01", "configuration": {"system": {"hostname": "leaf-01"}}}]}
	}`)}
	cr := &configv1alpha1.NXOSConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "leaf-config", Namespace: "network", Generation: 1},
		Spec: configv1alpha1.NXOSConfigSpec{
			DeviceRef:       configv1alpha1.DeviceRef{Name: "leaf-01"},
			ManagedFamilies: []string{"system"},
			ModelSource:     validNXOSModelSource(),
			Source:          configv1alpha1.ConfigurationSource{Inline: &raw},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.NXOSConfig{}).
		WithObjects(cr).
		Build()
	tr := &fakeNXOSTransport{hostname: "old"}
	r := &NXOSConfigReconciler{Client: c, DeviceName: "leaf-01", Transport: tr, Lookup: testedNXOSWriter}
	_, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "network", Name: "leaf-config"},
	})
	if err == nil || !strings.Contains(err.Error(), "payload is not flattened") {
		t.Fatalf("Reconcile error=%v, want flattened-source rejection", err)
	}
	if tr.fetches != 0 || tr.hostname != "old" {
		t.Fatalf("device touched for unresolved provenance: fetches=%d hostname=%q", tr.fetches, tr.hostname)
	}
}

func TestNXOSConfigReconcilerReturnsResolveErrorAfterRecordingFailure(t *testing.T) {
	scheme := newTestScheme(t)
	cr := &configv1alpha1.NXOSConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "leaf-config", Namespace: "network", Generation: 1},
		Spec: configv1alpha1.NXOSConfigSpec{
			DeviceRef:       configv1alpha1.DeviceRef{Name: "leaf-01"},
			ManagedFamilies: []string{"system"},
			ModelSource:     validNXOSModelSource(),
			Source: configv1alpha1.ConfigurationSource{
				ConfigMapRef: &configv1alpha1.ConfigMapKeyRef{Name: "missing", Key: "config.yaml"},
			},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.NXOSConfig{}).
		WithObjects(cr).
		Build()
	r := &NXOSConfigReconciler{
		Client:     c,
		DeviceName: "leaf-01",
	}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "network", Name: "leaf-config"}})
	if err == nil {
		t.Fatal("Reconcile error=nil, want resolve error returned to controller-runtime")
	}
	if !strings.Contains(err.Error(), "get ConfigMap network/missing") {
		t.Fatalf("Reconcile error=%q, want missing ConfigMap context", err)
	}
	var got configv1alpha1.NXOSConfig
	if getErr := c.Get(context.Background(), types.NamespacedName{Namespace: "network", Name: "leaf-config"}, &got); getErr != nil {
		t.Fatalf("get updated: %v", getErr)
	}
	if got.Status.Phase != "Failed" {
		t.Fatalf("phase=%q status=%#v", got.Status.Phase, got.Status)
	}
	if len(got.Status.Conditions) == 0 || got.Status.Conditions[0].Reason != "ReconcileFailed" {
		t.Fatalf("conditions=%#v", got.Status.Conditions)
	}
}

func TestNXOSConfigReconcilerRejectsUnpinnedModelContractBeforeDeviceFetch(t *testing.T) {
	scheme := newTestScheme(t)
	raw := runtime.RawExtension{Raw: []byte(`{"system":{"hostname":"leaf-01"}}`)}
	modelSource := validNXOSModelSource()
	modelSource.SchemaDigest = "sha256:" + strings.Repeat("0", 64)
	cr := &configv1alpha1.NXOSConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "leaf-config", Namespace: "network", Generation: 1},
		Spec: configv1alpha1.NXOSConfigSpec{
			DeviceRef:       configv1alpha1.DeviceRef{Name: "leaf-01"},
			ManagedFamilies: []string{"system"},
			ModelSource:     modelSource,
			Source:          configv1alpha1.ConfigurationSource{Inline: &raw},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.NXOSConfig{}).
		WithObjects(cr).
		Build()
	tr := &fakeNXOSTransport{hostname: "old"}
	r := &NXOSConfigReconciler{
		Client:     c,
		DeviceName: "leaf-01",
		Transport:  tr,
		Lookup:     testedNXOSWriter,
	}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "network", Name: "leaf-config"}})
	if err == nil || !strings.Contains(err.Error(), "does not match modelVersion") {
		t.Fatalf("Reconcile error=%v, want schema contract rejection", err)
	}
	if tr.fetches != 0 {
		t.Fatalf("device fetched before model contract validation, fetches=%d", tr.fetches)
	}
}

func TestNXOSStrictSourceValidatesBeforeRuntimeSecretOverlay(t *testing.T) {
	scheme := newTestScheme(t)
	raw := runtime.RawExtension{Raw: []byte(`{
		"interfaces":{"ethernets":[{"id":"1/1","description":"public"}]}
	}`)}
	cr := &configv1alpha1.NXOSConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "leaf-config", Namespace: "network", Generation: 1},
		Spec: configv1alpha1.NXOSConfigSpec{
			DeviceRef:       configv1alpha1.DeviceRef{Name: "leaf-01"},
			ManagedFamilies: []string{"interface_ethernet"},
			ModelSource:     validNXOSModelSource(),
			Source:          configv1alpha1.ConfigurationSource{Inline: &raw},
			SecretRefs: []configv1alpha1.FamilySecretRef{{
				Family: "interface_ethernet", Name: "interface-overlay", Key: "config.yaml",
			}},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "interface-overlay", Namespace: "network"},
		Data: map[string][]byte{
			"config.yaml": []byte("interfaces:\n  - name: 1/1\n    description: secret-overlay\n"),
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cr, secret).Build()
	r := &NXOSConfigReconciler{Client: c, DeviceName: "leaf-01"}

	resolved, err := r.common().resolveIntent(context.Background(), cr)
	if err != nil {
		t.Fatalf("resolveIntent: %v", err)
	}
	if _, present := resolved.Configuration["interfaces"]; present {
		t.Fatalf("canonical interfaces was not normalized: %#v", resolved.Configuration)
	}
	runtimeFamily, ok := resolved.Configuration["interface_ethernet"].(map[string]any)
	if !ok {
		t.Fatalf("runtime interface family has type %T", resolved.Configuration["interface_ethernet"])
	}
	interfaces, ok := runtimeFamily["interfaces"].([]any)
	if !ok || len(interfaces) != 1 {
		t.Fatalf("runtime interfaces=%#v", runtimeFamily["interfaces"])
	}
	item, _ := interfaces[0].(map[string]any)
	if item["description"] != "secret-overlay" {
		t.Fatalf("secret overlay was not applied after normalization: %#v", item)
	}
}

func TestNXOSConfigReconcilerRejectsUnsupportedRollbackTo(t *testing.T) {
	scheme := newTestScheme(t)
	raw := runtime.RawExtension{Raw: []byte(`{"system":{"hostname":"leaf-01"}}`)}
	cr := &configv1alpha1.NXOSConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "leaf-config", Namespace: "network", Generation: 1},
		Spec: configv1alpha1.NXOSConfigSpec{
			DeviceRef:       configv1alpha1.DeviceRef{Name: "leaf-01"},
			ManagedFamilies: []string{"system"},
			Source:          configv1alpha1.ConfigurationSource{Inline: &raw},
			RollbackTo:      "leaf-config-rev-1",
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.NXOSConfig{}).
		WithObjects(cr).
		Build()
	tr := &fakeNXOSTransport{hostname: "old"}
	r := &NXOSConfigReconciler{
		Client:     c,
		DeviceName: "leaf-01",
		Transport:  tr,
		Lookup:     testedNXOSWriter,
	}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "network", Name: "leaf-config"}})
	if err == nil || !strings.Contains(err.Error(), "spec.rollbackTo is not supported") {
		t.Fatalf("Reconcile error=%v, want unsupported rollbackTo", err)
	}
	if tr.fetches != 0 {
		t.Fatalf("transport fetches=%d, want rollback gate before device IO", tr.fetches)
	}
	var got configv1alpha1.NXOSConfig
	if getErr := c.Get(context.Background(), types.NamespacedName{Namespace: "network", Name: "leaf-config"}, &got); getErr != nil {
		t.Fatalf("get updated: %v", getErr)
	}
	if got.Status.Phase != "Failed" {
		t.Fatalf("phase=%q status=%#v", got.Status.Phase, got.Status)
	}
	if len(got.Status.Conditions) == 0 || !strings.Contains(got.Status.Conditions[0].Message, "rollbackTo") {
		t.Fatalf("conditions=%#v", got.Status.Conditions)
	}
}

func TestNXOSConfigReconcilerRejectsExplicitRevisionHistory(t *testing.T) {
	scheme := newTestScheme(t)
	raw := runtime.RawExtension{Raw: []byte(`{"system":{"hostname":"leaf-01"}}`)}
	limit := int32(5)
	cr := &configv1alpha1.NXOSConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "leaf-config", Namespace: "network", Generation: 1},
		Spec: configv1alpha1.NXOSConfigSpec{
			DeviceRef:            configv1alpha1.DeviceRef{Name: "leaf-01"},
			ManagedFamilies:      []string{"system"},
			Source:               configv1alpha1.ConfigurationSource{Inline: &raw},
			RevisionHistoryLimit: &limit,
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.NXOSConfig{}).
		WithObjects(cr).
		Build()
	tr := &fakeNXOSTransport{hostname: "old"}
	r := &NXOSConfigReconciler{
		Client:     c,
		DeviceName: "leaf-01",
		Transport:  tr,
		Lookup:     testedNXOSWriter,
	}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "network", Name: "leaf-config"}})
	if err == nil || !strings.Contains(err.Error(), "spec.revisionHistoryLimit is not supported") {
		t.Fatalf("Reconcile error=%v, want unsupported revisionHistoryLimit", err)
	}
	if tr.fetches != 0 {
		t.Fatalf("transport fetches=%d, want revision gate before device IO", tr.fetches)
	}
}

func TestNXOSConfigReconcilerRejectsUnsupportedManagedFamily(t *testing.T) {
	scheme := newTestScheme(t)
	raw := runtime.RawExtension{Raw: []byte(`{"banner":{"motd":"managed later"}}`)}
	cr := &configv1alpha1.NXOSConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "leaf-config", Namespace: "network", Generation: 1},
		Spec: configv1alpha1.NXOSConfigSpec{
			DeviceRef:       configv1alpha1.DeviceRef{Name: "leaf-01"},
			ManagedFamilies: []string{"banner"},
			Source:          configv1alpha1.ConfigurationSource{Inline: &raw},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.NXOSConfig{}).
		WithObjects(cr).
		Build()
	tr := &fakeNXOSTransport{hostname: "old"}
	r := &NXOSConfigReconciler{
		Client:     c,
		DeviceName: "leaf-01",
		Transport:  tr,
		Lookup:     testedNXOSWriter,
	}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "network", Name: "leaf-config"}})
	if err == nil || !strings.Contains(err.Error(), "unsupported families") || !strings.Contains(err.Error(), "banner") {
		t.Fatalf("Reconcile error=%v, want unsupported banner family", err)
	}
	if tr.fetches != 0 {
		t.Fatalf("transport fetches=%d, want family gate before device IO", tr.fetches)
	}
	var got configv1alpha1.NXOSConfig
	if getErr := c.Get(context.Background(), types.NamespacedName{Namespace: "network", Name: "leaf-config"}, &got); getErr != nil {
		t.Fatalf("get updated: %v", getErr)
	}
	if got.Status.Phase != "Failed" {
		t.Fatalf("phase=%q status=%#v", got.Status.Phase, got.Status)
	}
}

func TestNXOSConfigReconcilerRejectsInvalidManagedFamilies(t *testing.T) {
	tests := []struct {
		name       string
		families   []string
		wantErr    string
		wantStatus string
	}{
		{
			name:       "blank family",
			families:   []string{"system", " "},
			wantErr:    "must not be empty",
			wantStatus: "must not be empty",
		},
		{
			name:       "duplicate family",
			families:   []string{"system", "system"},
			wantErr:    "duplicate family",
			wantStatus: "duplicate family",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newTestScheme(t)
			raw := runtime.RawExtension{Raw: []byte(`{"system":{"hostname":"leaf-01"}}`)}
			cr := &configv1alpha1.NXOSConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "leaf-config", Namespace: "network", Generation: 1},
				Spec: configv1alpha1.NXOSConfigSpec{
					DeviceRef:       configv1alpha1.DeviceRef{Name: "leaf-01"},
					ManagedFamilies: tt.families,
					Source:          configv1alpha1.ConfigurationSource{Inline: &raw},
				},
			}
			c := fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&configv1alpha1.NXOSConfig{}).
				WithObjects(cr).
				Build()
			tr := &fakeNXOSTransport{hostname: "old"}
			r := &NXOSConfigReconciler{
				Client:     c,
				DeviceName: "leaf-01",
				Transport:  tr,
				Lookup:     testedNXOSWriter,
			}
			_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "network", Name: "leaf-config"}})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Reconcile error=%v, want %q", err, tt.wantErr)
			}
			if tr.fetches != 0 {
				t.Fatalf("transport fetches=%d, want validation gate before device IO", tr.fetches)
			}
			var got configv1alpha1.NXOSConfig
			if getErr := c.Get(context.Background(), types.NamespacedName{Namespace: "network", Name: "leaf-config"}, &got); getErr != nil {
				t.Fatalf("get updated: %v", getErr)
			}
			if got.Status.Phase != "Failed" {
				t.Fatalf("phase=%q status=%#v", got.Status.Phase, got.Status)
			}
			if len(got.Status.Conditions) == 0 || !strings.Contains(got.Status.Conditions[0].Message, tt.wantStatus) {
				t.Fatalf("conditions=%#v, want %q", got.Status.Conditions, tt.wantStatus)
			}
		})
	}
}

func TestNXOSConfigReconcilerRuntimeOptions(t *testing.T) {
	scheme := newTestScheme(t)
	raw := runtime.RawExtension{Raw: []byte(`{"system":{"hostname":"leaf-01"}}`)}
	cr := &configv1alpha1.NXOSConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "leaf-config", Namespace: "network", Generation: 1},
		Spec: configv1alpha1.NXOSConfigSpec{
			DeviceRef:       configv1alpha1.DeviceRef{Name: "leaf-01"},
			ManagedFamilies: []string{"system"},
			ModelSource:     validNXOSModelSource(),
			Source:          configv1alpha1.ConfigurationSource{Inline: &raw},
			WriteStartup:    true,
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.NXOSConfig{}).
		WithObjects(cr).
		Build()
	tr := &fakeNXOSTransport{hostname: "old"}
	r := &NXOSConfigReconciler{
		Client:        c,
		DeviceName:    "leaf-01",
		Transport:     tr,
		Lookup:        testedNXOSWriter,
		DeviceVersion: "10.3(9)",
	}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "network", Name: "leaf-config"}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if tr.saves != 1 {
		t.Fatalf("SaveStartup calls=%d", tr.saves)
	}
	var got configv1alpha1.NXOSConfig
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "network", Name: "leaf-config"}, &got); err != nil {
		t.Fatalf("get updated: %v", err)
	}
	if got.Status.SourceYangVersion != "10.3(9)" {
		t.Fatalf("sourceYangVersion=%q", got.Status.SourceYangVersion)
	}
	if got.Status.PlannedOps == 0 || got.Status.AppliedOps == 0 {
		t.Fatalf("planned/applied ops not recorded: planned=%d applied=%d", got.Status.PlannedOps, got.Status.AppliedOps)
	}
	if len(got.Status.VerifiedFamilies) != 1 || got.Status.VerifiedFamilies[0] != "system" {
		t.Fatalf("verifiedFamilies=%#v", got.Status.VerifiedFamilies)
	}
	if got.Status.PostApplyObservedHash == "" {
		t.Fatal("postApplyObservedHash was not recorded")
	}
}

func TestNXOSConfigReconcilerPrunesOwnedVLAN(t *testing.T) {
	scheme := newTestScheme(t)
	raw := runtime.RawExtension{Raw: []byte(`{"vlan":{"vlans":[{"id":101,"name":"keep"}]}}`)}
	cr := &configv1alpha1.NXOSConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "leaf-config", Namespace: "network", Generation: 2},
		Spec: configv1alpha1.NXOSConfigSpec{
			DeviceRef:         configv1alpha1.DeviceRef{Name: "leaf-01"},
			ManagedFamilies:   []string{"vlan"},
			ModelSource:       validNXOSModelSource(),
			Source:            configv1alpha1.ConfigurationSource{Inline: &raw},
			PruneOnRelinquish: true,
		},
		Status: configv1alpha1.NXOSConfigStatus{
			AtomicReplaceOwnedKeys: map[string][]string{"vlan": {"101", "102"}},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.NXOSConfig{}).
		WithObjects(cr).
		Build()
	tr := &fakeNXOSTransport{vlans: map[int]string{101: "keep", 102: "owned-orphan", 200: "baseline"}}
	r := &NXOSConfigReconciler{
		Client:      c,
		DeviceName:  "leaf-01",
		Transport:   tr,
		Lookup:      testedNXOSWriter,
		FamilyOrder: nxosschema.FamilyOrder,
	}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "network", Name: "leaf-config"}})
	if err != nil {
		var failed configv1alpha1.NXOSConfig
		_ = c.Get(context.Background(), types.NamespacedName{Namespace: "network", Name: "leaf-config"}, &failed)
		t.Fatalf("Reconcile: %v status=%#v", err, failed.Status)
	}
	if _, ok := tr.vlans[102]; ok {
		t.Fatalf("owned orphan VLAN 102 was not pruned: %#v", tr.vlans)
	}
	if _, ok := tr.vlans[200]; !ok {
		t.Fatalf("baseline VLAN 200 should not be pruned: %#v", tr.vlans)
	}
	if len(tr.deleteOps) != 1 || tr.deleteOps[0] != nxosschema.DNBridgeDomain+"/bd-[vlan-102]" {
		t.Fatalf("deleteOps=%#v", tr.deleteOps)
	}
	var got configv1alpha1.NXOSConfig
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "network", Name: "leaf-config"}, &got); err != nil {
		t.Fatalf("get updated: %v", err)
	}
	if got.Status.Phase != "InSync" {
		t.Fatalf("phase=%q status=%#v", got.Status.Phase, got.Status)
	}
	if keys := got.Status.AtomicReplaceOwnedKeys["vlan"]; !reflect.DeepEqual(keys, []string{"101", "102"}) {
		t.Fatalf("owned keys=%#v", got.Status.AtomicReplaceOwnedKeys)
	}
}

// B1: a per-device worker for tenant-a must never reconcile a same-named
// device's config that lives in tenant-b, even though the deviceRef.name
// matches. The cluster-wide cache would surface the foreign CR; the
// namespace filter must drop it before any device write or status update.
func TestNXOSConfigReconcilerIgnoresForeignNamespace(t *testing.T) {
	scheme := newTestScheme(t)
	raw := runtime.RawExtension{Raw: []byte(`{"system":{"hostname":"attacker","mtu":1500}}`)}
	foreign := &configv1alpha1.NXOSConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "leaf-config", Namespace: "tenant-b", Generation: 1},
		Spec: configv1alpha1.NXOSConfigSpec{
			DeviceRef:       configv1alpha1.DeviceRef{Name: "leaf-01"},
			ManagedFamilies: []string{"system"},
			ModelSource:     validNXOSModelSource(),
			Source:          configv1alpha1.ConfigurationSource{Inline: &raw},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.NXOSConfig{}).
		WithObjects(foreign).
		Build()
	tr := &fakeNXOSTransport{hostname: "untouched", mtu: 9216}
	r := &NXOSConfigReconciler{
		Client:          c,
		DeviceName:      "leaf-01",
		DeviceNamespace: "tenant-a",
		Transport:       tr,
		Lookup:          testedNXOSWriter,
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "tenant-b", Name: "leaf-config"}}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if tr.hostname != "untouched" {
		t.Fatalf("foreign-namespace CR wrote to the device: hostname=%q", tr.hostname)
	}
	var got configv1alpha1.NXOSConfig
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "tenant-b", Name: "leaf-config"}, &got); err != nil {
		t.Fatalf("get foreign: %v", err)
	}
	if got.Status.Phase != "" {
		t.Fatalf("foreign-namespace CR status was written: phase=%q", got.Status.Phase)
	}
}

// B2: the facade must build and register ONE CommonConfigReconciler and
// route SetTransport/SetDeviceVersion to that same instance. Before the fix
// common() returned a fresh throwaway per call, so the manager registered a
// throwaway while deferred-dial SetTransport mutated only the facade and a
// device down at startup never recovered.
func TestNXOSConfigReconcilerFacadeIsSingletonAndForwardsTransport(t *testing.T) {
	r := &NXOSConfigReconciler{DeviceName: "leaf-01"}
	if r.common() != r.common() {
		t.Fatal("common() returned different instances; the registered reconciler would not see deferred SetTransport")
	}
	if r.GetTransport() != nil {
		t.Fatalf("expected nil transport before dial, got %#v", r.GetTransport())
	}
	tr := &fakeNXOSTransport{hostname: "leaf-01"}
	r.SetTransport(tr)
	if r.common().GetTransport() != transport.Interface(tr) {
		t.Fatal("SetTransport did not reach the registered common reconciler")
	}
	r.SetDeviceVersion("9.3(8)")
	if got := r.common().deviceVersion(); got != "9.3(8)" {
		t.Fatalf("SetDeviceVersion did not reach the registered common reconciler: %q", got)
	}
}

func TestNXOSCommonConfigPlatformStopsOnRevertFailure(t *testing.T) {
	platform := NXOSCommonConfigPlatform()
	if !platform.ReconcilePolicy.StopOnRevertFailure {
		t.Fatal("NX-OS platform must enable fail-fast ordered-family processing under driftPolicy=revert")
	}
}

// B3: deleting a CR that owns atomic-replace keys must prune them off the
// device (relinquish) before the finalizer is removed, otherwise the owned
// keys are orphaned on the device.
func TestNXOSConfigReconcilerRelinquishesOwnedKeysOnDelete(t *testing.T) {
	scheme := newTestScheme(t)
	// Include the map-shaped "system" family (no owned keys) alongside the
	// list-shaped "vlan" family. Relinquish must prune only vlan; pushing
	// system an empty-list desired would fail with "want map, got []" (caught
	// on the live ubuntu17 nexus9300v-01).
	raw := runtime.RawExtension{Raw: []byte(`{"system":{"hostname":"leaf-01"},"vlan":{"vlans":[{"id":101,"name":"keep"}]}}`)}
	cr := &configv1alpha1.NXOSConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "leaf-config", Namespace: "network", Generation: 3,
			Finalizers: []string{nxosConfigFinalizer},
		},
		Spec: configv1alpha1.NXOSConfigSpec{
			DeviceRef:         configv1alpha1.DeviceRef{Name: "leaf-01"},
			ManagedFamilies:   []string{"system", "vlan"},
			ModelSource:       validNXOSModelSource(),
			Source:            configv1alpha1.ConfigurationSource{Inline: &raw},
			PruneOnRelinquish: true,
		},
		Status: configv1alpha1.NXOSConfigStatus{
			AtomicReplaceOwnedKeys: map[string][]string{"vlan": {"101", "102"}},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.NXOSConfig{}).
		WithObjects(cr).
		Build()
	if err := c.Delete(context.Background(), cr); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	tr := &fakeNXOSTransport{vlans: map[int]string{101: "keep", 102: "owned-orphan", 200: "baseline"}}
	r := &NXOSConfigReconciler{
		Client:      c,
		DeviceName:  "leaf-01",
		Transport:   tr,
		Lookup:      testedNXOSWriter,
		FamilyOrder: nxosschema.FamilyOrder,
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "network", Name: "leaf-config"}}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if _, ok := tr.vlans[102]; ok {
		t.Fatalf("owned VLAN 102 was not relinquished on delete: %#v", tr.vlans)
	}
	if _, ok := tr.vlans[200]; !ok {
		t.Fatalf("baseline VLAN 200 must not be pruned on delete: %#v", tr.vlans)
	}
	var got configv1alpha1.NXOSConfig
	err := c.Get(context.Background(), types.NamespacedName{Namespace: "network", Name: "leaf-config"}, &got)
	if err == nil && containsFinalizer(got.Finalizers, nxosConfigFinalizer) {
		t.Fatalf("finalizer not removed after relinquish: %#v", got.Finalizers)
	}
}

// Codex finding (high): the reconciler must FAIL CLOSED if the finalizer can't
// be persisted — mutating the device without a durable finalizer lets a later
// delete bypass relinquish/prune cleanup and orphan owned config.
func TestNXOSConfigReconcilerFailsClosedWhenFinalizerUpdateFails(t *testing.T) {
	scheme := newTestScheme(t)
	raw := runtime.RawExtension{Raw: []byte(`{"system":{"hostname":"leaf-01","mtu":9216}}`)}
	cr := &configv1alpha1.NXOSConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "leaf-config", Namespace: "network", Generation: 1},
		Spec: configv1alpha1.NXOSConfigSpec{
			DeviceRef:       configv1alpha1.DeviceRef{Name: "leaf-01"},
			ManagedFamilies: []string{"system"},
			ModelSource:     validNXOSModelSource(),
			Source:          configv1alpha1.ConfigurationSource{Inline: &raw},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.NXOSConfig{}).
		WithObjects(cr).
		WithInterceptorFuncs(interceptor.Funcs{
			// Fail the finalizer-add Update with a non-conflict error
			// (e.g. an RBAC regression).
			Update: func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.UpdateOption) error {
				return apierrors.NewInternalError(fmt.Errorf("simulated finalizer persistence failure"))
			},
		}).
		Build()
	tr := &fakeNXOSTransport{hostname: "untouched", mtu: 1500}
	r := &NXOSConfigReconciler{
		Client:     c,
		DeviceName: "leaf-01",
		Transport:  tr,
		Lookup:     testedNXOSWriter,
		// A non-nil Leaser is what gates the finalizer-add path.
		Leaser: &engine.FamilyLeaser{Client: c, Namespace: "network"},
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "network", Name: "leaf-config"}}); err == nil {
		t.Fatal("expected Reconcile to fail closed when the finalizer update fails")
	}
	if tr.hostname != "untouched" {
		t.Fatalf("device was mutated without a durable finalizer: hostname=%q", tr.hostname)
	}
}

func TestCommonConfigPollingPathPersistsFinalizerBeforeMutating(t *testing.T) {
	scheme := newSchemeWithLeases(t)
	raw := runtime.RawExtension{Raw: []byte(`{"system":{"hostname":"leaf-01","mtu":9216}}`)}
	cr := &configv1alpha1.NXOSConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "leaf-config", Namespace: "network", Generation: 1},
		Spec: configv1alpha1.NXOSConfigSpec{
			DeviceRef:       configv1alpha1.DeviceRef{Name: "leaf-01"},
			ManagedFamilies: []string{"system"},
			ModelSource:     validNXOSModelSource(),
			Source:          configv1alpha1.ConfigurationSource{Inline: &raw},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.NXOSConfig{}).
		WithObjects(cr).
		Build()
	sawFinalizer := false
	tr := &fakeNXOSTransport{hostname: "old", mtu: 1500}
	tr.beforeMutate = func() error {
		var got configv1alpha1.NXOSConfig
		if err := c.Get(context.Background(), types.NamespacedName{Namespace: "network", Name: "leaf-config"}, &got); err != nil {
			t.Errorf("get NXOSConfig during mutate: %v", err)
			return nil
		}
		sawFinalizer = containsFinalizer(got.Finalizers, nxosConfigFinalizer)
		return nil
	}
	r := &NXOSConfigReconciler{
		Client:     c,
		DeviceName: "leaf-01",
		Transport:  tr,
		Lookup:     testedNXOSWriter,
		Leaser:     &engine.FamilyLeaser{Client: c, Namespace: "network"},
	}

	r.common().reconcileAll(context.Background(), nil, triggerPoll)

	if !sawFinalizer {
		t.Fatal("NX-OS polling path mutated device before finalizer was persisted")
	}
	if tr.hostname != "leaf-01" || tr.mtu != 9216 {
		t.Fatalf("device was not reconciled after finalizer persistence: hostname=%q mtu=%d", tr.hostname, tr.mtu)
	}
	var got configv1alpha1.NXOSConfig
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "network", Name: "leaf-config"}, &got); err != nil {
		t.Fatalf("get NXOSConfig: %v", err)
	}
	if !containsFinalizer(got.Finalizers, nxosConfigFinalizer) {
		t.Fatalf("finalizer not persisted: %#v", got.Finalizers)
	}
}

func TestCommonConfigPollingPathRelinquishesOwnedKeysOnDelete(t *testing.T) {
	scheme := newSchemeWithLeases(t)
	raw := runtime.RawExtension{Raw: []byte(`{"system":{"hostname":"leaf-01"},"vlan":{"vlans":[{"id":101,"name":"keep"}]}}`)}
	cr := &configv1alpha1.NXOSConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "leaf-config", Namespace: "network", Generation: 3,
			Finalizers: []string{nxosConfigFinalizer},
		},
		Spec: configv1alpha1.NXOSConfigSpec{
			DeviceRef:         configv1alpha1.DeviceRef{Name: "leaf-01"},
			ManagedFamilies:   []string{"system", "vlan"},
			ModelSource:       validNXOSModelSource(),
			Source:            configv1alpha1.ConfigurationSource{Inline: &raw},
			PruneOnRelinquish: true,
		},
		Status: configv1alpha1.NXOSConfigStatus{
			AtomicReplaceOwnedKeys: map[string][]string{"vlan": {"101", "102"}},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.NXOSConfig{}).
		WithObjects(cr).
		Build()
	if err := c.Delete(context.Background(), cr); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	tr := &fakeNXOSTransport{vlans: map[int]string{101: "keep", 102: "owned-orphan", 200: "baseline"}}
	r := &NXOSConfigReconciler{
		Client:      c,
		DeviceName:  "leaf-01",
		Transport:   tr,
		Lookup:      testedNXOSWriter,
		FamilyOrder: nxosschema.FamilyOrder,
		Leaser:      &engine.FamilyLeaser{Client: c, Namespace: "network"},
	}

	r.common().reconcileAll(context.Background(), nil, triggerPoll)

	if _, ok := tr.vlans[102]; ok {
		t.Fatalf("owned VLAN 102 was not relinquished on delete: %#v", tr.vlans)
	}
	if _, ok := tr.vlans[200]; !ok {
		t.Fatalf("baseline VLAN 200 must not be pruned on delete: %#v", tr.vlans)
	}
	var got configv1alpha1.NXOSConfig
	err := c.Get(context.Background(), types.NamespacedName{Namespace: "network", Name: "leaf-config"}, &got)
	if err == nil && containsFinalizer(got.Finalizers, nxosConfigFinalizer) {
		t.Fatalf("finalizer not removed after polling relinquish: %#v", got.Finalizers)
	}
}

// Codex finding (critical): the polling/cohort path (used by the aggregator
// worker) must scope to the device namespace. A same-named device's config in
// another namespace must never enter the cohort.
func TestCommonConfigReconcilerCohortExcludesForeignNamespace(t *testing.T) {
	scheme := newTestScheme(t)
	mk := func(ns string) *configv1alpha1.NXOSConfig {
		return &configv1alpha1.NXOSConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "leaf-config", Namespace: ns},
			Spec: configv1alpha1.NXOSConfigSpec{
				DeviceRef:       configv1alpha1.DeviceRef{Name: "leaf-01"},
				ManagedFamilies: []string{"system"},
			},
		}
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(mk("tenant-a"), mk("tenant-b")).Build()
	r := &CommonConfigReconciler{
		Client:          c,
		DeviceName:      "leaf-01",
		DeviceNamespace: "tenant-a",
		Platform:        NXOSCommonConfigPlatform(),
	}
	forDevice, _ := r.cohort(context.Background(), noopErrLogger{})
	if len(forDevice) != 1 || forDevice[0].GetNamespace() != "tenant-a" {
		t.Fatalf("cohort returned %d objects (%v); want only the tenant-a CR", len(forDevice), forDevice)
	}
}

func TestNXOSConfigReconcilerRecordsConfirmedCommitFallback(t *testing.T) {
	scheme := newTestScheme(t)
	raw := runtime.RawExtension{Raw: []byte(`{"system":{"hostname":"leaf-01"}}`)}
	cr := &configv1alpha1.NXOSConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "leaf-config", Namespace: "network", Generation: 1},
		Spec: configv1alpha1.NXOSConfigSpec{
			DeviceRef:             configv1alpha1.DeviceRef{Name: "leaf-01"},
			ManagedFamilies:       []string{"system"},
			ModelSource:           validNXOSModelSource(),
			Source:                configv1alpha1.ConfigurationSource{Inline: &raw},
			Transactional:         true,
			ConfirmTimeoutSeconds: 60,
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.NXOSConfig{}).
		WithObjects(cr).
		Build()
	r := &NXOSConfigReconciler{
		Client:     c,
		DeviceName: "leaf-01",
		Transport:  &fakeNXOSTransport{hostname: "old"},
		Lookup:     testedNXOSWriter,
	}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "network", Name: "leaf-config"}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got configv1alpha1.NXOSConfig
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "network", Name: "leaf-config"}, &got); err != nil {
		t.Fatalf("get updated: %v", err)
	}
	if got.Status.Phase != "InSync" {
		t.Fatalf("phase=%q status=%#v", got.Status.Phase, got.Status)
	}
	if len(got.Status.TransportFallbacks) != 1 {
		t.Fatalf("transportFallbacks=%#v, want confirmed-commit fallback", got.Status.TransportFallbacks)
	}
	fallback := got.Status.TransportFallbacks[0]
	if fallback.Type != "ConfirmedCommit" || fallback.Reason != "non-transactional reconcile" {
		t.Fatalf("fallback=%#v", fallback)
	}
}

func TestNXOSConfigReconcilerSubscribeBypassesHashShortCircuit(t *testing.T) {
	scheme := newTestScheme(t)
	raw := runtime.RawExtension{Raw: []byte(`{"system":{"hostname":"leaf-01"}}`)}
	cr := &configv1alpha1.NXOSConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "leaf-config", Namespace: "network", Generation: 1},
		Spec: configv1alpha1.NXOSConfigSpec{
			DeviceRef:       configv1alpha1.DeviceRef{Name: "leaf-01"},
			ManagedFamilies: []string{"system"},
			ModelSource:     validNXOSModelSource(),
			Source:          configv1alpha1.ConfigurationSource{Inline: &raw},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.NXOSConfig{}).
		WithObjects(cr).
		Build()
	tr := &fakeNXOSTransport{hostname: "leaf-01"}
	r := &NXOSConfigReconciler{
		Client:     c,
		DeviceName: "leaf-01",
		Transport:  tr,
		Lookup:     testedNXOSWriter,
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "network", Name: "leaf-config"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("initial Reconcile: %v", err)
	}
	if tr.fetches == 0 {
		t.Fatal("initial reconcile did not touch transport")
	}

	tr.fetches = 0
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("short-circuit Reconcile: %v", err)
	}
	if tr.fetches != 0 {
		t.Fatalf("normal event should have short-circuited before Fetch, fetches=%d", tr.fetches)
	}

	time.Sleep(2 * time.Millisecond)
	r.NotifySubscribeFired()
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("subscribe Reconcile: %v", err)
	}
	if tr.fetches == 0 {
		t.Fatal("subscribe event should bypass hash short-circuit and Fetch")
	}
}

func TestNormalizeNXOSNetAsCodeSourceEthernets(t *testing.T) {
	got, err := normalizeNXOSNetAsCodeSource(map[string]any{
		"system": map[string]any{"hostname": "leaf-01"},
		"interfaces": map[string]any{
			"ethernets": []any{
				map[string]any{"id": "1/49", "description": "uplink", "shutdown": false},
			},
		},
	}, "leaf-01")
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if _, ok := got["interfaces"]; ok {
		t.Fatalf("interfaces should be consumed when only ethernets are present: %#v", got)
	}
	intfFamily, ok := got["interface_ethernet"].(map[string]any)
	if !ok {
		t.Fatalf("interface_ethernet missing: %#v", got)
	}
	list, ok := intfFamily["interfaces"].([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("interfaces list=%#v", intfFamily["interfaces"])
	}
	item, ok := list[0].(map[string]any)
	if !ok {
		t.Fatalf("item=%#v", list[0])
	}
	want := map[string]any{
		"id":          "1/49",
		"name":        "1/49",
		"type":        "Ethernet",
		"description": "uplink",
		"shutdown":    false,
	}
	if !reflect.DeepEqual(item, want) {
		t.Fatalf("item=%#v, want %#v", item, want)
	}
}

func TestNormalizeNXOSNetAsCodeSourceExtractsDeviceConfiguration(t *testing.T) {
	got, err := normalizeNXOSNetAsCodeSource(map[string]any{
		"devices": []any{
			map[string]any{
				"name":          "other",
				"configuration": map[string]any{"system": map[string]any{"hostname": "other"}},
			},
			map[string]any{
				"name":          "leaf-01",
				"configuration": map[string]any{"system": map[string]any{"hostname": "leaf-01"}},
			},
		},
	}, "leaf-01")
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	system, ok := got["system"].(map[string]any)
	if !ok || system["hostname"] != "leaf-01" {
		t.Fatalf("system=%#v", got["system"])
	}
}

func TestNormalizeNXOSNetAsCodeSourceResolvesScopedEnvelope(t *testing.T) {
	got, err := normalizeNXOSNetAsCodeSource(map[string]any{
		"nxos": map[string]any{
			"templates": []any{
				map[string]any{
					"name":  "BASE_VLAN",
					"order": 10,
					"configuration": map[string]any{
						"vlan": map[string]any{"vlans": []any{
							map[string]any{"id": 20, "name": "${access_vlan_name}"},
						}},
					},
				},
				map[string]any{
					"name": "SAME_A",
					"configuration": map[string]any{
						"vlan": map[string]any{"vlans": []any{
							map[string]any{"id": 30, "name": "A"},
						}},
					},
				},
				map[string]any{
					"name": "SAME_B",
					"configuration": map[string]any{
						"vlan": map[string]any{"vlans": []any{
							map[string]any{"id": 30, "name": "B"},
						}},
					},
				},
			},
			"interface_groups": []any{
				map[string]any{
					"name": "EDGE",
					"configuration": map[string]any{
						"description": "group ${hostname}",
						"shutdown":    false,
						"mtu":         9216,
					},
				},
			},
			"global": map[string]any{
				"variables": map[string]any{
					"hostname":         "global",
					"access_vlan_name": "global-access",
				},
				"templates": []any{"BASE_VLAN"},
				"configuration": map[string]any{
					"system": map[string]any{"hostname": "${hostname}"},
					"vlan": map[string]any{"vlans": []any{
						map[string]any{"id": 10, "name": "GLOBAL"},
					}},
				},
			},
			"device_groups": []any{
				map[string]any{
					"name":    "LEAFS",
					"devices": []any{"leaf-01"},
					"variables": map[string]any{
						"hostname":         "group",
						"access_vlan_name": "group-access",
					},
					"configuration": map[string]any{
						"vlan": map[string]any{"vlans": []any{
							map[string]any{"id": 10, "name": "GROUP"},
						}},
						"interfaces": map[string]any{"ethernets": []any{
							map[string]any{"id": "1/2", "interface_groups": []any{"EDGE"}},
						}},
					},
				},
			},
			"devices": []any{
				map[string]any{
					"name":          "leaf-01",
					"device_groups": []any{"LEAFS"},
					"variables":     map[string]any{"hostname": "leaf-01"},
					"templates":     []any{"SAME_A", "SAME_B"},
					"configuration": map[string]any{
						"vlan": map[string]any{"vlans": []any{
							map[string]any{"id": 10, "name": "DEVICE"},
						}},
						"interfaces": map[string]any{"ethernets": []any{
							map[string]any{"id": "1/1", "interface_groups": []any{"EDGE"}, "description": "local"},
						}},
					},
				},
			},
		},
	}, "leaf-01")
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	system, ok := got["system"].(map[string]any)
	if !ok || system["hostname"] != "leaf-01" {
		t.Fatalf("system=%#v", got["system"])
	}
	vlan, ok := got["vlan"].(map[string]any)
	if !ok {
		t.Fatalf("vlan missing: %#v", got)
	}
	vlans, ok := vlan["vlans"].([]any)
	if !ok || len(vlans) != 3 {
		t.Fatalf("vlans=%#v", vlan["vlans"])
	}
	if item := nxosTestMapByKey(t, vlans, "id", 10); item["name"] != "DEVICE" {
		t.Fatalf("vlan 10=%#v", item)
	}
	if item := nxosTestMapByKey(t, vlans, "id", 20); item["name"] != "group-access" {
		t.Fatalf("vlan 20=%#v", item)
	}
	if item := nxosTestMapByKey(t, vlans, "id", 30); item["name"] != "B" {
		t.Fatalf("same-order template merge did not preserve reference order: %#v", item)
	}
	if _, ok := got["interfaces"]; ok {
		t.Fatalf("interfaces should be consumed after Ethernet normalization: %#v", got["interfaces"])
	}
	intfFamily, ok := got["interface_ethernet"].(map[string]any)
	if !ok {
		t.Fatalf("interface_ethernet missing: %#v", got)
	}
	intfs, ok := intfFamily["interfaces"].([]any)
	if !ok || len(intfs) != 2 {
		t.Fatalf("interfaces=%#v", intfFamily["interfaces"])
	}
	groupOnly := nxosTestMapByKey(t, intfs, "name", "1/2")
	if groupOnly["description"] != "group leaf-01" || groupOnly["shutdown"] != false || groupOnly["mtu"] != 9216 {
		t.Fatalf("group-expanded interface=%#v", groupOnly)
	}
	deviceOverride := nxosTestMapByKey(t, intfs, "name", "1/1")
	if deviceOverride["description"] != "local" || deviceOverride["shutdown"] != false || deviceOverride["mtu"] != 9216 {
		t.Fatalf("device override interface=%#v", deviceOverride)
	}
}

func TestNormalizeNXOSNetAsCodeSourceRejectsUnsupportedTemplateType(t *testing.T) {
	_, err := normalizeNXOSNetAsCodeSource(map[string]any{
		"nxos": map[string]any{
			"templates": []any{
				map[string]any{
					"name":          "CLI_TEMPLATE",
					"type":          "cli",
					"configuration": map[string]any{"system": map[string]any{"hostname": "leaf-01"}},
				},
			},
			"devices": []any{map[string]any{"name": "leaf-01", "templates": []any{"CLI_TEMPLATE"}}},
		},
	}, "leaf-01")
	if err == nil || !strings.Contains(err.Error(), `template "CLI_TEMPLATE" type "cli" is not supported`) {
		t.Fatalf("err=%v", err)
	}
}

func TestNormalizeNXOSNetAsCodeSourceRejectsUnresolvedVariable(t *testing.T) {
	_, err := normalizeNXOSNetAsCodeSource(map[string]any{
		"nxos": map[string]any{
			"global": map[string]any{
				"configuration": map[string]any{"system": map[string]any{"hostname": "${hostname}"}},
			},
			"devices": []any{map[string]any{"name": "leaf-01"}},
		},
	}, "leaf-01")
	if err == nil || !strings.Contains(err.Error(), `unresolved variable "hostname"`) {
		t.Fatalf("err=%v", err)
	}
}

func TestNormalizeNXOSNetAsCodeSourceRejectsAmbiguousEthernetShapes(t *testing.T) {
	_, err := normalizeNXOSNetAsCodeSource(map[string]any{
		"interface_ethernet": map[string]any{"interfaces": []any{}},
		"interfaces":         map[string]any{"ethernets": []any{}},
	}, "leaf-01")
	if err == nil || !strings.Contains(err.Error(), "both interface_ethernet and interfaces.ethernets") {
		t.Fatalf("err=%v", err)
	}
}

func nxosTestMapByKey(t *testing.T, list []any, key string, value any) map[string]any {
	t.Helper()
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if reflect.DeepEqual(m[key], value) {
			return m
		}
	}
	t.Fatalf("no map with %s=%#v in %#v", key, value, list)
	return nil
}
