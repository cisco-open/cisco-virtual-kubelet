// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controller

import (
	"context"
	"fmt"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sort"
	"strings"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/appartifact"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func artifactJournalRules(name string) []rbacv1.PolicyRule {
	return []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"configmaps"}, ResourceNames: []string{name}, Verbs: []string{"get", "update"}}}
}
func artifactGrantMetadata(meta metav1.Object, namespace, name, uid string) {
	applySharedWorkerAnnotations(meta, namespace, managedprotocol.WorkerModeAppHosting, meta.GetName())
	annotations := meta.GetAnnotations()
	for k, v := range appartifact.JournalAnnotations(namespace, name, uid) {
		annotations[k] = v
	}
	meta.SetAnnotations(annotations)
}
func artifactBindingLooksCanonical(binding *rbacv1.RoleBinding, namespace, appSA string) bool {
	uid := binding.Annotations[appartifact.JournalDeviceUID]
	name := appartifact.ManagedJournalName(uid)
	return uid != "" && binding.Namespace == namespace && binding.Name == name && binding.Annotations[appartifact.JournalKindAnnotation] == "v1" && binding.Annotations[appartifact.JournalDeviceNamespace] == namespace && binding.Annotations[appartifact.JournalDeviceName] != "" && sharedWorkerMetadataMatches(binding, namespace, managedprotocol.WorkerModeAppHosting, name) && binding.RoleRef == (rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: name}) && reflect.DeepEqual(binding.Subjects, sharedWorkerSubject(namespace, appSA)) && binding.DeletionTimestamp == nil
}

// Validate the live Role and journal as well as the binding. Metadata alone
// never permits broad rules to become a trusted exception to the RBAC audit.
func (r *CiscoDeviceReconciler) validateArtifactGrant(ctx context.Context, binding *rbacv1.RoleBinding, namespace, appSA string) error {
	if !artifactBindingLooksCanonical(binding, namespace, appSA) {
		return fmt.Errorf("invalid artifact journal RoleBinding %s", client.ObjectKeyFromObject(binding))
	}
	var role rbacv1.Role
	if err := r.reader().Get(ctx, client.ObjectKeyFromObject(binding), &role); err != nil {
		return err
	}
	if !sharedWorkerMetadataMatches(&role, namespace, managedprotocol.WorkerModeAppHosting, role.Name) || role.DeletionTimestamp != nil || !reflect.DeepEqual(role.Rules, artifactJournalRules(binding.Name)) {
		return fmt.Errorf("artifact journal Role %s has unexpected authority", client.ObjectKeyFromObject(&role))
	}
	for _, key := range []string{appartifact.JournalKindAnnotation, appartifact.JournalDeviceNamespace, appartifact.JournalDeviceName, appartifact.JournalDeviceUID} {
		if role.Annotations[key] != binding.Annotations[key] {
			return fmt.Errorf("artifact journal Role metadata mismatch")
		}
	}
	var journal corev1.ConfigMap
	if err := r.reader().Get(ctx, client.ObjectKeyFromObject(binding), &journal); err != nil {
		return err
	}
	return appartifact.ValidateManagedJournal(&journal, namespace, binding.Annotations[appartifact.JournalDeviceName], binding.Annotations[appartifact.JournalDeviceUID])
}
func (r *CiscoDeviceReconciler) desiredArtifactDevices(ctx context.Context, namespace string, current *ciskov1.CiscoDevice) (map[string]*ciskov1.CiscoDevice, error) {
	var devices ciskov1.CiscoDeviceList
	if err := r.reader().List(ctx, &devices, client.InNamespace(namespace)); err != nil {
		return nil, err
	}
	if current != nil {
		found := false
		for i := range devices.Items {
			if devices.Items[i].Name == current.Name {
				devices.Items[i] = *current
				found = true
			}
		}
		if !found {
			devices.Items = append(devices.Items, *current)
		}
	}
	desired := map[string]*ciskov1.CiscoDevice{}
	for i := range devices.Items {
		d := &devices.Items[i]
		if d.UID != "" && d.Status.NodeIdentity != nil && d.Spec.AppArtifacts.EffectiveMode() == ciskov1.AppArtifactModeVerified {
			if !d.DeletionTimestamp.IsZero() {
				stopped, err := r.managedWriterWorkloadsStopped(ctx, d)
				if err != nil {
					return nil, err
				}
				if stopped {
					continue
				}
			}
			desired[appartifact.ManagedJournalName(string(d.UID))] = d
		}
	}
	return desired, nil
}
func (r *CiscoDeviceReconciler) ensureArtifactGrant(ctx context.Context, d *ciskov1.CiscoDevice, appSA string) error {
	name := appartifact.ManagedJournalName(string(d.UID))
	key := types.NamespacedName{Namespace: d.Namespace, Name: name}
	var cm corev1.ConfigMap
	if err := r.reader().Get(ctx, key, &cm); apierrors.IsNotFound(err) {
		// A retained Role is evidence of prior provisioning. Never replace lost
		// installation history with an empty journal on a worker restart.
		var role rbacv1.Role
		if e := r.reader().Get(ctx, key, &role); !apierrors.IsNotFound(e) {
			return fmt.Errorf("artifact journal %s is missing or unreadable after prior provisioning: %v", key, e)
		}
		cm = corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: d.Namespace, Annotations: appartifact.JournalAnnotations(d.Namespace, d.Name, string(d.UID))}}
		if err = r.Create(ctx, &cm); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := appartifact.ValidateManagedJournal(&cm, d.Namespace, d.Name, string(d.UID)); err != nil {
		return err
	}
	var role rbacv1.Role
	if err := r.reader().Get(ctx, key, &role); apierrors.IsNotFound(err) {
		role = rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: d.Namespace}, Rules: artifactJournalRules(name)}
		artifactGrantMetadata(&role, d.Namespace, d.Name, string(d.UID))
		if err = r.Create(ctx, &role); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	// Do not adopt an existing Role, even when its rules happen to match.
	expected := rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: d.Namespace}, Rules: artifactJournalRules(name)}
	artifactGrantMetadata(&expected, d.Namespace, d.Name, string(d.UID))
	if !reflect.DeepEqual(role.Rules, expected.Rules) || !sharedWorkerMetadataMatches(&role, d.Namespace, managedprotocol.WorkerModeAppHosting, name) || role.DeletionTimestamp != nil {
		return fmt.Errorf("artifact journal Role collision or drift: %s", key)
	}
	for k, v := range expected.Annotations {
		if role.Annotations[k] != v {
			return fmt.Errorf("artifact journal Role identity mismatch: %s", key)
		}
	}
	var binding rbacv1.RoleBinding
	if err := r.reader().Get(ctx, key, &binding); apierrors.IsNotFound(err) {
		binding = rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: d.Namespace}, RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: name}, Subjects: sharedWorkerSubject(d.Namespace, appSA)}
		artifactGrantMetadata(&binding, d.Namespace, d.Name, string(d.UID))
		if err = r.Create(ctx, &binding); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return r.validateArtifactGrant(ctx, &binding, d.Namespace, appSA)
}

// Reconcile grants namespace-wide because the account is shared. Preserve
// journals (and unbound Roles) when a device opts out or leaves the namespace.
func (r *CiscoDeviceReconciler) reconcileArtifactAccess(ctx context.Context, namespace, appSA, access string, current *ciskov1.CiscoDevice) error {
	desired, err := r.desiredArtifactDevices(ctx, namespace, current)
	if err != nil {
		return err
	}
	if access != managedprotocol.WorkerAccessReadWrite {
		desired = map[string]*ciskov1.CiscoDevice{}
	}
	var bindings rbacv1.RoleBindingList
	if err = r.reader().List(ctx, &bindings, client.InNamespace(namespace)); err != nil {
		return err
	}
	for i := range bindings.Items {
		binding := &bindings.Items[i]
		if binding.Annotations[appartifact.JournalKindAnnotation] != "v1" {
			continue
		}
		if _, ok := desired[binding.Name]; ok {
			continue
		}
		if err = r.validateArtifactGrant(ctx, binding, namespace, appSA); err != nil {
			return err
		}
		if err = deleteWithUIDPrecondition(ctx, r.Client, binding); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	names := make([]string, 0, len(desired))
	for name := range desired {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err = r.ensureArtifactGrant(ctx, desired[name], appSA); err != nil {
			return err
		}
	}
	return nil
}
func (r *CiscoDeviceReconciler) revokeArtifactDeviceAccess(ctx context.Context, d *ciskov1.CiscoDevice) error {
	var bindings rbacv1.RoleBindingList
	if err := r.reader().List(ctx, &bindings, client.InNamespace(d.Namespace)); err != nil {
		return err
	}
	for i := range bindings.Items {
		binding := &bindings.Items[i]
		if binding.Annotations[appartifact.JournalKindAnnotation] != "v1" || binding.Annotations[appartifact.JournalDeviceUID] != string(d.UID) {
			continue
		}
		if err := r.validateArtifactGrant(ctx, binding, d.Namespace, r.appHostingServiceAccountName()); err != nil {
			return err
		}
		if err := deleteWithUIDPrecondition(ctx, r.Client, binding); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// Ignore ordinary data updates: writing an installation intent must not cause
// a namespace-wide controller reconciliation on every lifecycle step.
func artifactJournalEvents() predicate.Funcs {
	relevant := func(object client.Object) bool {
		return object != nil && (object.GetAnnotations()[appartifact.JournalKindAnnotation] == "v1" || strings.HasPrefix(object.GetName(), "cvk-artifacts-"))
	}
	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return relevant(e.Object) },
		DeleteFunc:  func(e event.DeleteEvent) bool { return relevant(e.Object) },
		GenericFunc: func(e event.GenericEvent) bool { return relevant(e.Object) },
		UpdateFunc: func(e event.UpdateEvent) bool {
			if !relevant(e.ObjectOld) && !relevant(e.ObjectNew) {
				return false
			}
			old, ok := e.ObjectOld.(*corev1.ConfigMap)
			if !ok {
				return false
			}
			next, ok := e.ObjectNew.(*corev1.ConfigMap)
			if !ok {
				return false
			}
			return !reflect.DeepEqual(old.Annotations, next.Annotations) || !reflect.DeepEqual(old.OwnerReferences, next.OwnerReferences) || !reflect.DeepEqual(old.Immutable, next.Immutable) || !reflect.DeepEqual(old.DeletionTimestamp, next.DeletionTimestamp)
		},
	}
}
