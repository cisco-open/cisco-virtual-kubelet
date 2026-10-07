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

package nexusdashboard

import (
	"context"
	"errors"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const testUID = "uid-nd1"

func adoptionSpec() *ciskov1.NetworkControllerDeviceAdoption {
	return &ciskov1.NetworkControllerDeviceAdoption{
		Enabled: true,
		Defaults: &ciskov1.NetworkControllerDeviceAccess{
			Username:            "admin",
			CredentialSecretRef: ciskov1.NetworkControllerSecretReference{Name: "switch-creds"},
			TLS:                 &ciskov1.TLSConfig{Enabled: true, InsecureSkipVerify: true},
		},
		ScopeOverrides: []ciskov1.NetworkControllerDeviceScopeOverride{
			{Scope: "fab-b", CredentialSecretRef: &ciskov1.NetworkControllerSecretReference{Name: "fab-b-creds"}},
		},
	}
}

func newSyncer(t *testing.T, objs ...ctrlclient.Object) (*deviceSyncer, ctrlclient.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := ciskov1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	return &deviceSyncer{client: cl, namespace: "ns", uid: testUID, adoption: adoptionSpec()}, cl
}

func item(serial, host, fabric string) InventoryItem {
	return InventoryItem{
		Serial: serial, Hostname: host, MgmtAddress: "192.0.2.10", Model: "N9K-C9300v", Platform: PlatformNXOS,
		Fabric: fabric, Role: "leaf", Reachable: true,
	}
}

func getDevice(t *testing.T, cl ctrlclient.Client, name string) *ciskov1.CiscoDevice {
	t.Helper()
	var d ciskov1.CiscoDevice
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "ns", Name: name}, &d); err != nil {
		t.Fatalf("get %s: %v", name, err)
	}
	return &d
}

func TestSyncCreatesDeviceFromInventory(t *testing.T) {
	s, cl := newSyncer(t)
	res, err := s.Sync(context.Background(), []InventoryItem{item("9OM44OSGXYE", "nx-os-test", "fab-a")})
	if err != nil || res.Created != 1 || !res.ok() {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	d := getDevice(t, cl, "nd-9om44osgxye")
	if d.Spec.Driver != ciskov1.DeviceDriverNXOS || d.Spec.Address != "192.0.2.10" || d.Spec.PhysicalIdentity != "9OM44OSGXYE" {
		t.Fatalf("unexpected spec: %+v", d.Spec)
	}
	if d.Spec.NodeName != "nx-os-test" {
		t.Fatalf("nodeName = %q, want hostname", d.Spec.NodeName)
	}
	if d.Spec.Username != "admin" || d.Spec.CredentialSecretRef == nil || d.Spec.CredentialSecretRef.Name != "switch-creds" {
		t.Fatalf("credentials not applied: %+v", d.Spec)
	}
	if d.Spec.TLS == nil || !d.Spec.TLS.InsecureSkipVerify {
		t.Fatalf("tls not applied: %+v", d.Spec.TLS)
	}
	if d.Labels[labelController] != testUID || d.Labels[labelManagedBy] != managedByValue ||
		d.Labels[labelFabric] != "fab-a" || d.Spec.Labels[labelRole] != "leaf" {
		t.Fatalf("labels: meta=%v node=%v", d.Labels, d.Spec.Labels)
	}
	if d.Spec.Password != "" {
		t.Fatal("a password must never be written to a CiscoDevice")
	}
	if len(d.OwnerReferences) != 0 {
		t.Fatal("must not set ownerReferences: GC would delete devices with the NetworkController")
	}
}

func TestSyncIsIdempotent(t *testing.T) {
	s, _ := newSyncer(t)
	in := []InventoryItem{item("S1", "leaf1", "fab-a"), item("S2", "leaf2", "fab-a")}
	if res, _ := s.Sync(context.Background(), in); res.Created != 2 {
		t.Fatalf("first sync: %+v", res)
	}
	res, err := s.Sync(context.Background(), in)
	if err != nil || res.Created != 0 || res.Updated != 0 || res.Unchanged != 2 {
		t.Fatalf("second sync must change nothing: %+v err=%v", res, err)
	}
}

func TestSyncUsesPerFabricSecret(t *testing.T) {
	s, cl := newSyncer(t)
	_, _ = s.Sync(context.Background(), []InventoryItem{item("S1", "a", "fab-a"), item("S2", "b", "fab-b")})
	if got := getDevice(t, cl, "nd-s1").Spec.CredentialSecretRef.Name; got != "switch-creds" {
		t.Fatalf("default fabric used %q", got)
	}
	d := getDevice(t, cl, "nd-s2")
	if d.Spec.CredentialSecretRef.Name != "fab-b-creds" || d.Spec.Username != "admin" {
		t.Fatalf("override should swap only the secret: %+v", d.Spec)
	}
}

func TestSyncUpdatesOwnedFieldsAndKeepsOthers(t *testing.T) {
	s, cl := newSyncer(t)
	ctx := context.Background()
	_, _ = s.Sync(ctx, []InventoryItem{item("S1", "leaf1", "fab-a")})

	// Another actor customizes the device.
	d := getDevice(t, cl, "nd-s1")
	d.Spec.Labels["team"] = "dc"
	d.Spec.Port = 8443
	d.Labels["custom"] = "x"
	if err := cl.Update(ctx, d); err != nil {
		t.Fatal(err)
	}

	moved := item("S1", "renamed-host", "fab-a")
	moved.MgmtAddress = "192.0.2.99"
	s.adoption.Defaults.CredentialSecretRef.Name = "rotated-creds"
	res, err := s.Sync(ctx, []InventoryItem{moved})
	if err != nil || res.Updated != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	got := getDevice(t, cl, "nd-s1")
	if got.Spec.Address != "192.0.2.99" || got.Spec.CredentialSecretRef.Name != "rotated-creds" {
		t.Fatalf("owned fields not updated: %+v", got.Spec)
	}
	if got.Spec.NodeName != "leaf1" {
		t.Fatalf("nodeName is immutable and must not follow the hostname, got %q", got.Spec.NodeName)
	}
	if got.Spec.Labels["team"] != "dc" || got.Spec.Port != 8443 || got.Labels["custom"] != "x" {
		t.Fatalf("foreign fields were clobbered: spec.labels=%v port=%d labels=%v", got.Spec.Labels, got.Spec.Port, got.Labels)
	}
}

func TestSyncNeverTouchesUnownedDevices(t *testing.T) {
	foreign := func(name string, labels map[string]string) *ciskov1.CiscoDevice {
		return &ciskov1.CiscoDevice{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: name, Labels: labels},
			Spec:       ciskov1.DeviceSpec{Driver: ciskov1.DeviceDriverNXOS, Address: "198.51.100.1", Username: "hand"},
		}
	}
	s, cl := newSyncer(t,
		foreign("nd-s1", nil), // hand-made, same name
		foreign("nd-s2", map[string]string{labelController: "other"}), // another controller's
	)
	res, err := s.Sync(context.Background(), []InventoryItem{item("S1", "a", "f"), item("S2", "b", "f")})
	if err != nil || res.Conflicts != 2 || res.Created+res.Updated != 0 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	for _, n := range []string{"nd-s1", "nd-s2"} {
		if got := getDevice(t, cl, n); got.Spec.Address != "198.51.100.1" || got.Spec.Username != "hand" {
			t.Fatalf("%s was modified: %+v", n, got.Spec)
		}
	}
}

func TestSyncNodeNameCollisions(t *testing.T) {
	existing := &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "other-device"},
		Spec:       ciskov1.DeviceSpec{Driver: ciskov1.DeviceDriverNXOS, Address: "198.51.100.1", Username: "u", NodeName: "taken-host"},
	}
	s, cl := newSyncer(t, existing)
	_, _ = s.Sync(context.Background(), []InventoryItem{
		item("S1", "taken-host", "f"), // collides with an existing Node name
		item("S2", "dup", "f"),        // duplicate hostnames in ND
		item("S3", "dup", "f"),
		item("S4", "Not_A_DNS_Name", "f"), // not a valid Node name
		item("S5", "unique-host", "f"),
	})
	for n, want := range map[string]string{
		"nd-s1": "", "nd-s2": "", "nd-s3": "", "nd-s4": "", "nd-s5": "unique-host",
	} {
		if got := getDevice(t, cl, n).Spec.NodeName; got != want {
			t.Errorf("%s nodeName = %q, want %q", n, got, want)
		}
	}
}

func TestSyncSkipsUnreachableSkippedAndInvalid(t *testing.T) {
	s, cl := newSyncer(t)
	down := item("DOWN1", "d", "f")
	down.Reachable = false
	skipped := item("SKIP1", "s", "f")
	skipped.SkipReason = "platform is not NX-OS"
	res, err := s.Sync(context.Background(), []InventoryItem{down, skipped, item("bad serial!!", "x", "f"), item("---", "y", "f")})
	if err != nil || res.Created != 0 || res.Unreachable != 1 || res.Invalid != 2 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	var list ciskov1.CiscoDeviceList
	if err := cl.List(context.Background(), &list); err != nil || len(list.Items) != 0 {
		t.Fatalf("nothing should have been created: %d err=%v", len(list.Items), err)
	}
}

func TestSyncNeverDeletes(t *testing.T) {
	s, cl := newSyncer(t)
	ctx := context.Background()
	_, _ = s.Sync(ctx, []InventoryItem{item("S1", "a", "f"), item("S2", "b", "f")})
	// S2 vanishes from ND, and then ND reports nothing at all.
	_, _ = s.Sync(ctx, []InventoryItem{item("S1", "a", "f")})
	_, _ = s.Sync(ctx, nil)
	var list ciskov1.CiscoDeviceList
	if err := cl.List(ctx, &list); err != nil || len(list.Items) != 2 {
		t.Fatalf("devices must survive disappearing from inventory: %d err=%v", len(list.Items), err)
	}
}

func TestSyncDisabledOrNilDoesNothing(t *testing.T) {
	var nilSyncer *deviceSyncer
	if nilSyncer.enabled() {
		t.Fatal("nil syncer must be disabled")
	}
	s, _ := newSyncer(t)
	s.adoption.Enabled = false
	if s.enabled() {
		t.Fatal("disabled adoption must not run")
	}
}

func TestDeviceNameAndNodeNameHelpers(t *testing.T) {
	long := strings.Repeat("A", 90)
	n1, ok1 := deviceName(long)
	n2, ok2 := deviceName(long + "B")
	if !ok1 || !ok2 || len(n1) > 63 || len(n2) > 63 || n1 == n2 {
		t.Fatalf("long serials must shorten uniquely: %q %q", n1, n2)
	}
	if n, ok := deviceName("9OM44-OSG.XYE"); !ok || n != "nd-9om44-osg-xye" {
		t.Fatalf("got %q %v", n, ok)
	}
	if _, ok := deviceName("***"); ok {
		t.Fatal("serial with no usable characters must be rejected")
	}
	if nodeNameFromHostname("Leaf1") != "" || nodeNameFromHostname("") != "" || nodeNameFromHostname("a_b") != "" {
		t.Fatal("hostnames must not be rewritten into node names")
	}
	if nodeNameFromHostname("leaf-1.dc") != "leaf-1.dc" {
		t.Fatal("valid DNS-1123 hostname should be kept")
	}
}

func TestSyncInventoryAdoptsOnlyAfterSuccessfulRefresh(t *testing.T) {
	a, _, get := newTestAdapter(t, func(context.Context) error { return nil })
	a.uid = testUID
	scheme := runtime.NewScheme()
	if err := ciskov1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	devices := fake.NewClientBuilder().WithScheme(scheme).Build()
	a.devices = &deviceSyncer{client: devices, namespace: "ns", uid: testUID, adoption: adoptionSpec()}
	ctx := context.Background()

	a.list = func(context.Context) ([]InventoryItem, error) { return nil, errNetwork }
	a.syncInventory(ctx)
	var list ciskov1.CiscoDeviceList
	if err := devices.List(ctx, &list); err != nil || len(list.Items) != 0 {
		t.Fatalf("a failed refresh must not create devices: %d err=%v", len(list.Items), err)
	}
	if hasCapability(get(), CapabilityDeviceAdoption) {
		t.Fatal("no adoption status should be published without a successful refresh")
	}

	a.list = func(context.Context) ([]InventoryItem, error) {
		return []InventoryItem{item("SER1", "leaf1", "fab-a")}, nil
	}
	a.syncInventory(ctx)
	if err := devices.List(ctx, &list); err != nil || len(list.Items) != 1 {
		t.Fatalf("expected one device, got %d err=%v", len(list.Items), err)
	}
	msg := capabilityMessage(get(), CapabilityDeviceAdoption)
	if !strings.Contains(msg, "1 created") || strings.Contains(msg, "SER1") || strings.Contains(msg, "192.0.2") {
		t.Fatalf("status must carry counts only, got %q", msg)
	}
}

var errNetwork = errors.New("connection refused")

func capabilityMessage(nc *ciskov1.NetworkController, name string) string {
	for _, c := range nc.Status.Capabilities {
		if c.Name == name {
			return c.Message
		}
	}
	return ""
}

func hasCapability(nc *ciskov1.NetworkController, name string) bool {
	for _, c := range nc.Status.Capabilities {
		if c.Name == name {
			return true
		}
	}
	return false
}
