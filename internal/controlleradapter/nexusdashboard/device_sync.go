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
	"encoding/json"
	"fmt"
	"reflect"
	"sort"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const (
	// CapabilityDeviceAdoption reports whether inventory is being turned into
	// CiscoDevice objects.
	CapabilityDeviceAdoption = "device-adoption"

	// labelController marks a CiscoDevice as created by one NetworkController.
	// Only objects carrying the current controller's UID are ever modified.
	// It is a label rather than an ownerReference on purpose: garbage
	// collection would otherwise delete every adopted device, and with it the
	// Node, when the NetworkController object is removed.
	labelController = "cisco.vk/network-controller-uid"
	labelManagedBy  = "app.kubernetes.io/managed-by"
	managedByValue  = "cvk-nexus-dashboard"

	labelFabric = "nd.cisco.vk/fabric"
	labelRole   = "nd.cisco.vk/role"
	labelModel  = "nd.cisco.vk/model"
)

// deviceSyncResult holds counts only; it never carries serials or addresses.
type deviceSyncResult struct {
	Created, Updated, Unchanged int
	Unreachable, Invalid        int
	Conflicts, Failed           int
}

func (r deviceSyncResult) ok() bool { return r.Failed == 0 }

func (r deviceSyncResult) String() string {
	return fmt.Sprintf("%d created, %d updated, %d unchanged; %d unreachable, %d invalid, %d name conflicts, %d failed",
		r.Created, r.Updated, r.Unchanged, r.Unreachable, r.Invalid, r.Conflicts, r.Failed)
}

// deviceSyncer turns inventory into CiscoDevice objects in one namespace. It
// creates and updates only: it holds no delete permission, and removal of
// switches from ND is never propagated here.
type deviceSyncer struct {
	client    ctrlclient.Client
	namespace string
	uid       string
	adoption  *ciskov1.NetworkControllerDeviceAdoption
}

func (s *deviceSyncer) enabled() bool { return s != nil && s.adoption != nil && s.adoption.Enabled }

func (s *deviceSyncer) owned(d *ciskov1.CiscoDevice) bool {
	return s.uid != "" && d.Labels[labelController] == s.uid
}

// Sync reconciles one complete inventory snapshot. A failure on one switch
// does not stop the others.
func (s *deviceSyncer) Sync(ctx context.Context, items []InventoryItem) (deviceSyncResult, error) {
	var res deviceSyncResult
	var list ciskov1.CiscoDeviceList
	if err := s.client.List(ctx, &list, ctrlclient.InNamespace(s.namespace)); err != nil {
		return res, fmt.Errorf("list CiscoDevices: %w", err)
	}
	existing := make(map[string]*ciskov1.CiscoDevice, len(list.Items))
	takenNodes := make(map[string]struct{}, len(list.Items))
	for i := range list.Items {
		d := &list.Items[i]
		existing[d.Name] = d
		takenNodes[firstNonEmpty(d.Spec.NodeName, d.Name)] = struct{}{}
	}

	adoptable := make([]InventoryItem, 0, len(items))
	hostnames := map[string]int{}
	for _, it := range items {
		if it.SkipReason != "" {
			continue // not NX-OS or no address; already counted by summarize
		}
		if !it.Reachable {
			res.Unreachable++
			continue
		}
		adoptable = append(adoptable, it)
		hostnames[it.Hostname]++
	}
	sort.Slice(adoptable, func(i, j int) bool { return adoptable[i].Serial < adoptable[j].Serial })

	for _, it := range adoptable {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		name, ok := deviceName(it.Serial)
		if !ok || !physicalIdentityRE.MatchString(it.Serial) {
			res.Invalid++
			continue
		}
		access, ok := s.adoption.ResolveAccess(it.Fabric)
		if !ok {
			res.Failed++
			continue
		}
		cur := existing[name]
		if cur != nil && !s.owned(cur) {
			res.Conflicts++ // never touch an object this controller did not create
			continue
		}
		out, err := s.apply(ctx, name, it, access, cur, hostnames[it.Hostname] == 1, takenNodes)
		switch {
		case apierrors.IsAlreadyExists(err):
			res.Conflicts++ // appeared since the list; decided next sync
		case err != nil:
			res.Failed++
		case out == outcomeCreated:
			res.Created++
		case out == outcomeUpdated:
			res.Updated++
		default:
			res.Unchanged++
		}
	}
	return res, nil
}

type outcome int

const (
	outcomeUnchanged outcome = iota
	outcomeCreated
	outcomeUpdated
)

// desiredLabels are the labels both on the CiscoDevice and on its Node.
func desiredLabels(it InventoryItem) map[string]string {
	out := map[string]string{}
	for k, v := range map[string]string{labelFabric: it.Fabric, labelRole: it.Role, labelModel: it.Model} {
		if lv := labelValue(v); lv != "" {
			out[k] = lv
		}
	}
	return out
}

func (s *deviceSyncer) apply(ctx context.Context, name string, it InventoryItem, access ciskov1.NetworkControllerDeviceAccess,
	cur *ciskov1.CiscoDevice, uniqueHostname bool, takenNodes map[string]struct{}) (outcome, error) {
	nodeLabels := desiredLabels(it)
	if cur == nil {
		dev := &ciskov1.CiscoDevice{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: s.namespace,
				Labels: mergeLabels(nodeLabels, map[string]string{labelController: s.uid, labelManagedBy: managedByValue}),
			},
			Spec: ciskov1.DeviceSpec{
				Driver:              ciskov1.DeviceDriverNXOS,
				Address:             it.MgmtAddress,
				PhysicalIdentity:    it.Serial,
				Username:            access.Username,
				CredentialSecretRef: localRef(access.CredentialSecretRef.Name),
				TLS:                 access.TLS,
				Labels:              nodeLabels,
			},
		}
		// spec.nodeName is immutable, so it is chosen once, at creation, and
		// only when the hostname is unambiguous and free.
		if nn := nodeNameFromHostname(it.Hostname); nn != "" && uniqueHostname {
			if _, taken := takenNodes[nn]; !taken {
				dev.Spec.NodeName = nn
				takenNodes[nn] = struct{}{}
			}
		}
		return outcomeCreated, s.client.Create(ctx, dev)
	}

	patch, changed := s.mergePatch(cur, it, access, nodeLabels)
	if !changed {
		return outcomeUnchanged, nil
	}
	raw, err := json.Marshal(patch)
	if err != nil {
		return outcomeUnchanged, err
	}
	if err := s.client.Patch(ctx, cur, ctrlclient.RawPatch(types.MergePatchType, raw)); err != nil {
		return outcomeUnchanged, err
	}
	return outcomeUpdated, nil
}

// mergePatch builds a JSON merge patch holding only the fields the adapter
// owns, and reports whether any of them differ. Merge-patch semantics on maps
// keep labels that users or other controllers added; spec.nodeName is never
// part of it because it is immutable.
func (s *deviceSyncer) mergePatch(cur *ciskov1.CiscoDevice, it InventoryItem, access ciskov1.NetworkControllerDeviceAccess,
	nodeLabels map[string]string) (map[string]any, bool) {
	spec := map[string]any{}
	if cur.Spec.Address != it.MgmtAddress {
		spec["address"] = it.MgmtAddress
	}
	if cur.Spec.Username != access.Username {
		spec["username"] = access.Username
	}
	if cur.Spec.CredentialSecretRef == nil || cur.Spec.CredentialSecretRef.Name != access.CredentialSecretRef.Name {
		spec["credentialSecretRef"] = map[string]any{"name": access.CredentialSecretRef.Name}
	}
	// An omitted TLS block means "leave the device's setting alone", so it is
	// only compared and written when the operator configured one.
	if access.TLS != nil && !reflect.DeepEqual(cur.Spec.TLS, access.TLS) {
		spec["tls"] = access.TLS
	}
	// physicalIdentity is write-once. It is set only if the device has none.
	if cur.Spec.PhysicalIdentity == "" {
		spec["physicalIdentity"] = it.Serial
	}
	if l := labelDiff(cur.Spec.Labels, nodeLabels); len(l) > 0 {
		spec["labels"] = l
	}
	meta := map[string]any{}
	if l := labelDiff(cur.Labels, mergeLabels(nodeLabels, map[string]string{labelManagedBy: managedByValue})); len(l) > 0 {
		meta["labels"] = l
	}
	patch := map[string]any{}
	if len(spec) > 0 {
		patch["spec"] = spec
	}
	if len(meta) > 0 {
		patch["metadata"] = meta
	}
	return patch, len(patch) > 0
}

// labelDiff returns the entries of want that differ from have.
func labelDiff(have, want map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range want {
		if have[k] != v {
			out[k] = v
		}
	}
	return out
}

func localRef(name string) *corev1.LocalObjectReference {
	return &corev1.LocalObjectReference{Name: name}
}

func mergeLabels(a, b map[string]string) map[string]string {
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}
