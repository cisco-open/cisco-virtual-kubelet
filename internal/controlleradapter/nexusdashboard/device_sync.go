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
	"strings"
	"time"

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

	// annotationMissingSince records, in RFC 3339, when a device was first seen
	// missing from a complete inventory refresh. It is cleared when the switch
	// returns and is the clock for the Prune grace period.
	annotationMissingSince = "cisco.vk/nd-missing-since"

	// annotationManagedLabels lists the label keys the adapter applied (comma
	// separated), so a label dropped from the NetworkController or from ND is
	// removed again, while labels added by users are never touched.
	annotationManagedLabels = "cisco.vk/nd-managed-labels"

	labelFabric = "nd.cisco.vk/fabric"
	labelRole   = "nd.cisco.vk/role"
	labelModel  = "nd.cisco.vk/model"
)

// deviceSyncResult holds counts only; it never carries serials or addresses.
type deviceSyncResult struct {
	Created, Updated, Unchanged int
	Unreachable, Invalid        int
	Conflicts, Failed           int
	// Missing devices are annotated, Pruned ones deleted, and PruneGuarded
	// ones were due for deletion but held back by the mass-delete guard.
	Missing, Pruned, PruneGuarded int
}

func (r deviceSyncResult) ok() bool { return r.Failed == 0 }

func (r deviceSyncResult) String() string {
	return fmt.Sprintf("%d created, %d updated, %d unchanged; %d unreachable, %d invalid, %d name conflicts, %d failed; %d missing, %d pruned, %d prune-guarded",
		r.Created, r.Updated, r.Unchanged, r.Unreachable, r.Invalid, r.Conflicts, r.Failed, r.Missing, r.Pruned, r.PruneGuarded)
}

// deviceSyncer turns inventory into CiscoDevice objects in one namespace. It
// creates and updates devices, annotates the ones whose switch left ND, and
// deletes them only under the opt-in Prune policy, after a grace period and
// behind a mass-delete guard.
type deviceSyncer struct {
	client    ctrlclient.Client
	namespace string
	uid       string
	adoption  *ciskov1.NetworkControllerDeviceAdoption
	now       func() time.Time
}

func (s *deviceSyncer) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
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
	s.reconcileMissing(ctx, list.Items, items, &res)
	return res, nil
}

// reconcileMissing handles owned devices whose switch is not in the snapshot.
// An unreachable or skipped switch is still in ND and therefore not missing.
// An empty snapshot is treated as suspect and changes nothing.
func (s *deviceSyncer) reconcileMissing(ctx context.Context, devices []ciskov1.CiscoDevice, items []InventoryItem, res *deviceSyncResult) {
	if len(items) == 0 {
		return
	}
	names := make(map[string]struct{}, len(items))
	serials := make(map[string]struct{}, len(items))
	for _, it := range items {
		if it.Serial == "" {
			continue
		}
		serials[it.Serial] = struct{}{}
		if n, ok := deviceName(it.Serial); ok {
			names[n] = struct{}{}
		}
	}

	removal := s.adoption.Removal
	now := s.clock()
	var owned int
	var due []*ciskov1.CiscoDevice
	for i := range devices {
		d := &devices[i]
		if !s.owned(d) || d.DeletionTimestamp != nil {
			continue
		}
		owned++
		_, byName := names[d.Name]
		_, bySerial := serials[d.Spec.PhysicalIdentity]
		since, annotated := d.Annotations[annotationMissingSince]
		if byName || (d.Spec.PhysicalIdentity != "" && bySerial) {
			if annotated && s.patchMissingSince(ctx, d, nil) != nil {
				res.Failed++
			}
			continue
		}
		res.Missing++
		first, perr := time.Parse(time.RFC3339, since)
		if !annotated || perr != nil {
			stamp := now.UTC().Format(time.RFC3339)
			if s.patchMissingSince(ctx, d, &stamp) != nil {
				res.Failed++
			}
			continue
		}
		if removal.EffectivePolicy() == ciskov1.DeviceRemovalPrune && now.Sub(first) >= removal.EffectiveGracePeriod() {
			due = append(due, d)
		}
	}
	if len(due) == 0 {
		return
	}
	// At least one device may always be pruned; beyond that the fraction of
	// the fleet is capped so a bad inventory cannot empty the cluster.
	limit := int(int64(owned) * int64(removal.EffectiveMaxPrunePercent()) / 100)
	if limit < 1 {
		limit = 1
	}
	if len(due) > limit {
		res.PruneGuarded += len(due)
		return
	}
	for _, d := range due {
		uid := d.UID
		err := s.client.Delete(ctx, d, ctrlclient.Preconditions{UID: &uid})
		switch {
		case err == nil, apierrors.IsNotFound(err):
			res.Pruned++
		default:
			res.Failed++
		}
	}
}

// patchMissingSince sets the annotation to *stamp, or removes it when nil.
func (s *deviceSyncer) patchMissingSince(ctx context.Context, d *ciskov1.CiscoDevice, stamp *string) error {
	var v any
	if stamp != nil {
		v = *stamp
	}
	raw, err := json.Marshal(map[string]any{"metadata": map[string]any{"annotations": map[string]any{annotationMissingSince: v}}})
	if err != nil {
		return err
	}
	return s.client.Patch(ctx, d, ctrlclient.RawPatch(types.MergePatchType, raw))
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
	nodeLabels := mergeLabels(access.Labels, desiredLabels(it))
	if cur == nil {
		dev := &ciskov1.CiscoDevice{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: s.namespace,
				Labels:      mergeLabels(nodeLabels, map[string]string{labelController: s.uid, labelManagedBy: managedByValue}),
				Annotations: map[string]string{annotationManagedLabels: managedKeys(nodeLabels)},
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
	// Keys applied earlier but no longer wanted are set to null, which a merge
	// patch turns into a removal. Only keys in the managed annotation qualify.
	stale := staleKeys(cur.Annotations[annotationManagedLabels], nodeLabels)
	if l := labelPatch(cur.Spec.Labels, nodeLabels, stale); len(l) > 0 {
		spec["labels"] = l
	}
	meta := map[string]any{}
	if l := labelPatch(cur.Labels, mergeLabels(nodeLabels, map[string]string{labelManagedBy: managedByValue}), stale); len(l) > 0 {
		meta["labels"] = l
	}
	if want := managedKeys(nodeLabels); cur.Annotations[annotationManagedLabels] != want {
		meta["annotations"] = map[string]any{annotationManagedLabels: want}
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

// labelPatch returns the merge-patch entries that bring have to want: changed
// or missing values, and nil (removal) for stale keys that are present.
func labelPatch(have, want map[string]string, stale []string) map[string]any {
	out := map[string]any{}
	for k, v := range want {
		if cur, ok := have[k]; !ok || cur != v {
			out[k] = v
		}
	}
	for _, k := range stale {
		if _, ok := have[k]; ok {
			out[k] = nil
		}
	}
	return out
}

// managedKeys is the sorted, comma-separated key list stored in the
// annotationManagedLabels annotation.
func managedKeys(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

// staleKeys returns the keys named in the annotation that want no longer has.
func staleKeys(annotation string, want map[string]string) []string {
	var out []string
	for _, k := range strings.Split(annotation, ",") {
		if _, ok := want[k]; k != "" && !ok {
			out = append(out, k)
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
