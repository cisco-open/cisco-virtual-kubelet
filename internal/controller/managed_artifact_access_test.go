// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controller

import (
	"context"
	"strings"
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/appartifact"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

func verifiedAccessDevice(name string) *ciskov1.CiscoDevice {
	d := managedAccessDevice(name)
	d.Spec.AppArtifacts = &ciskov1.AppArtifactConfig{Mode: ciskov1.AppArtifactModeVerified}
	return d
}
func artifactKey(d *ciskov1.CiscoDevice) types.NamespacedName {
	return types.NamespacedName{Namespace: d.Namespace, Name: appartifact.ManagedJournalName(string(d.UID))}
}
func TestManagedVerifiedJournalProvisioningAndStandardDefault(t *testing.T) {
	ctx := context.Background()
	first := verifiedAccessDevice("verified-one")
	second := verifiedAccessDevice("verified-two")
	standard := managedAccessDevice("standard")
	r := reconcilerFor(t, first, second, standard)
	r.ManagedTopology = true
	if err := r.ensureManagedSharedWorkerAccess(ctx, first); err != nil {
		t.Fatal(err)
	}
	for _, d := range []*ciskov1.CiscoDevice{first, second} {
		key := artifactKey(d)
		var cm corev1.ConfigMap
		if err := r.Get(ctx, key, &cm); err != nil {
			t.Fatal(err)
		}
		if err := appartifact.ValidateManagedJournal(&cm, d.Namespace, d.Name, string(d.UID)); err != nil {
			t.Fatal(err)
		}
		var binding rbacv1.RoleBinding
		if err := r.Get(ctx, key, &binding); err != nil {
			t.Fatal(err)
		}
		if err := r.validateArtifactGrant(ctx, &binding, d.Namespace, r.appHostingServiceAccountName()); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Get(ctx, artifactKey(standard), &corev1.ConfigMap{}); !apierrors.IsNotFound(err) {
		t.Fatalf("Standard created journal: %v", err)
	}
	// Reconciliation must preserve live installation records.
	key := artifactKey(first)
	var cm corev1.ConfigMap
	if err := r.Get(ctx, key, &cm); err != nil {
		t.Fatal(err)
	}
	cm.Data = map[string]string{"app-test": "retained-installation-intent"}
	if err := r.Update(ctx, &cm); err != nil {
		t.Fatal(err)
	}
	if err := r.ensureManagedSharedWorkerAccess(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(ctx, key, &cm); err != nil || cm.Data["app-test"] != "retained-installation-intent" {
		t.Fatalf("history replaced: %v %+v", err, cm.Data)
	}
	// One device's opt-out removes only its grant, retaining both histories.
	first.Spec.AppArtifacts = nil
	if err := r.Update(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := r.ensureManagedSharedWorkerAccess(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(ctx, key, &rbacv1.RoleBinding{}); !apierrors.IsNotFound(err) {
		t.Fatalf("opt-out kept grant: %v", err)
	}
	if err := r.Get(ctx, key, &cm); err != nil || cm.Data["app-test"] != "retained-installation-intent" {
		t.Fatal("opt-out erased history")
	}
	if err := r.Get(ctx, artifactKey(second), &rbacv1.RoleBinding{}); err != nil {
		t.Fatalf("peer grant removed: %v", err)
	}
}
func TestManagedVerifiedJournalDriftQuarantinesSharedAccess(t *testing.T) {
	for _, drift := range []string{"broad-role", "extra-subject", "foreign-journal", "missing-journal", "foreign-role"} {
		t.Run(drift, func(t *testing.T) {
			ctx := context.Background()
			d := verifiedAccessDevice("drift")
			r := reconcilerFor(t, d)
			r.ManagedTopology = true
			if err := r.ensureManagedSharedWorkerAccess(ctx, d); err != nil {
				t.Fatal(err)
			}
			key := artifactKey(d)
			switch drift {
			case "broad-role", "foreign-role":
				var role rbacv1.Role
				if err := r.Get(ctx, key, &role); err != nil {
					t.Fatal(err)
				}
				if drift == "broad-role" {
					role.Rules[0].ResourceNames = nil
					role.Rules[0].Verbs = append(role.Rules[0].Verbs, "delete")
				} else {
					role.Annotations = nil
				}
				if err := r.Update(ctx, &role); err != nil {
					t.Fatal(err)
				}
			case "extra-subject":
				var b rbacv1.RoleBinding
				if err := r.Get(ctx, key, &b); err != nil {
					t.Fatal(err)
				}
				b.Subjects = append(b.Subjects, rbacv1.Subject{Kind: "User", Name: "other"})
				if err := r.Update(ctx, &b); err != nil {
					t.Fatal(err)
				}
			case "foreign-journal":
				var cm corev1.ConfigMap
				if err := r.Get(ctx, key, &cm); err != nil {
					t.Fatal(err)
				}
				cm.Annotations[appartifact.JournalDeviceUID] = "other"
				if err := r.Update(ctx, &cm); err != nil {
					t.Fatal(err)
				}
			case "missing-journal":
				if err := r.Delete(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}}); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.ensureManagedSharedWorkerAccess(ctx, d); err == nil {
				t.Fatal("unsafe grant accepted")
			}
			crb := types.NamespacedName{Name: vkAccessClusterRoleBindingName(d.Namespace, r.appHostingServiceAccountName())}
			if err := r.Get(ctx, crb, &rbacv1.ClusterRoleBinding{}); !apierrors.IsNotFound(err) {
				t.Fatalf("failed to quarantine app authority: %v", err)
			}
			if drift == "missing-journal" {
				if err := r.ensureManagedSharedWorkerAccess(ctx, d); err == nil {
					t.Fatal("lost history silently recreated")
				}
				if err := r.Get(ctx, key, &corev1.ConfigMap{}); !apierrors.IsNotFound(err) {
					t.Fatal("missing history replaced with empty journal")
				}
			}
		})
	}
}
func TestManagedVerifiedJournalRefusesInitialForeignCollision(t *testing.T) {
	for _, object := range []string{"journal", "role", "binding"} {
		t.Run(object, func(t *testing.T) {
			ctx := context.Background()
			d := verifiedAccessDevice("collision")
			r := reconcilerFor(t, d)
			r.ManagedTopology = true
			key := artifactKey(d)
			var obj client.Object
			switch object {
			case "journal":
				obj = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}, Data: map[string]string{"keep": "foreign"}}
			case "role":
				obj = &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}, Rules: artifactJournalRules(key.Name)}
			case "binding":
				obj = &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}, Subjects: []rbacv1.Subject{{Kind: "User", Name: "foreign"}}}
			}
			if err := r.Create(ctx, obj); err != nil {
				t.Fatal(err)
			}
			if err := r.ensureManagedSharedWorkerAccess(ctx, d); err == nil {
				t.Fatal("adopted foreign object")
			}
			if err := r.Get(ctx, key, obj); err != nil {
				t.Fatalf("deleted foreign collision: %v", err)
			}
		})
	}
}
func TestManagedVerifiedJournalNamespaceProtection(t *testing.T) {
	ctx := context.Background()
	d := verifiedAccessDevice("protection")
	r := reconcilerFor(t, d)
	r.ManagedTopology = true
	if err := r.ensureManagedSharedWorkerAccess(ctx, d); err != nil {
		t.Fatal(err)
	}
	role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "ordinary-editor", Namespace: d.Namespace}, Rules: []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"configmaps"}, Verbs: []string{"patch"}, ResourceNames: []string{artifactKey(d).Name}}}}
	binding := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: role.Name, Namespace: d.Namespace}, RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: role.Name}, Subjects: []rbacv1.Subject{{Kind: "User", Name: "workload-editor"}}}
	if err := r.Create(ctx, role); err != nil {
		t.Fatal(err)
	}
	if err := r.Create(ctx, binding); err != nil {
		t.Fatal(err)
	}
	if err := r.inspectManagedWorkerNamespaceRBAC(ctx, d, r.appHostingServiceAccountName(), r.networkManagementServiceAccountName()); err == nil || !strings.Contains(err.Error(), "installation journals") {
		t.Fatalf("journal editor accepted: %v", err)
	}
}
func TestManagedVerifiedJournalRevokedForReadOnlyAndCleanup(t *testing.T) {
	ctx := context.Background()
	d := verifiedAccessDevice("cleanup")
	r := reconcilerFor(t, d, managedAccessNode(d, false))
	r.ManagedTopology = true
	convergeManagedSharedWorkerAccess(t, r, d)
	r.AppHostingAccessMode = managedprotocol.WorkerAccessReadOnly
	convergeManagedSharedWorkerAccess(t, r, d)
	if err := r.Get(ctx, artifactKey(d), &rbacv1.RoleBinding{}); !apierrors.IsNotFound(err) {
		t.Fatal("read-only kept write access")
	}
	r.AppHostingAccessMode = managedprotocol.WorkerAccessReadWrite
	convergeManagedSharedWorkerAccess(t, r, d)
	if err := r.cleanupManagedSharedWorkerAccess(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(ctx, artifactKey(d), &rbacv1.RoleBinding{}); !apierrors.IsNotFound(err) {
		t.Fatal("cleanup kept write access")
	}
	if err := r.Get(ctx, artifactKey(d), &corev1.ConfigMap{}); err != nil {
		t.Fatal("cleanup deleted history")
	}
}

func TestManagedArtifactJournalEventsIgnoreInstallationWrites(t *testing.T) {
	pred := artifactJournalEvents()
	before := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cvk-artifacts-test", Namespace: "edge", Annotations: map[string]string{appartifact.JournalKindAnnotation: "v1"}}, Data: map[string]string{"app": "old"}}
	after := before.DeepCopy()
	after.Data["app"] = "new"
	after.ResourceVersion = "2"
	if pred.Update(event.UpdateEvent{ObjectOld: before, ObjectNew: after}) {
		t.Fatal("ordinary installation write triggered namespace reconciliation")
	}
	after.Annotations = nil
	if !pred.Update(event.UpdateEvent{ObjectOld: before, ObjectNew: after}) {
		t.Fatal("journal provenance removal not watched")
	}
	if !pred.Delete(event.DeleteEvent{Object: before}) {
		t.Fatal("journal deletion not watched")
	}
}

type artifactDriftReader struct {
	client.Reader
	key types.NamespacedName
}

func (r artifactDriftReader) Get(ctx context.Context, key client.ObjectKey, object client.Object, opts ...client.GetOption) error {
	if err := r.Reader.Get(ctx, key, object, opts...); err != nil {
		return err
	}
	if role, ok := object.(*rbacv1.Role); ok && key == r.key {
		role.Rules[0].ResourceNames = nil
	}
	return nil
}
func TestManagedVerifiedJournalAuditUsesDirectReader(t *testing.T) {
	ctx := context.Background()
	d := verifiedAccessDevice("direct-reader")
	r := reconcilerFor(t, d)
	r.ManagedTopology = true
	if err := r.ensureManagedSharedWorkerAccess(ctx, d); err != nil {
		t.Fatal(err)
	}
	// The cache still shows the narrow Role; only the uncached reader sees drift.
	r.APIReader = artifactDriftReader{Reader: r.Client, key: artifactKey(d)}
	if err := r.auditManagedSharedAppWorkerBindings(ctx, d.Namespace, d.Namespace, r.appHostingServiceAccountName(), vkAccessClusterRoleBindingName(d.Namespace, r.appHostingServiceAccountName()), managedprotocol.AppHostingReadWriteClusterRole, managedprotocol.AppHostingDeviceReadClusterRole, managedprotocol.WorkerAccessReadWrite); err == nil {
		t.Fatal("cached Role hid live broad grant")
	}
}

func TestManagedVerifiedJournalRetainedUntilDeletingWorkerStops(t *testing.T) {
	ctx := context.Background()
	d := verifiedAccessDevice("deleting")
	d.Finalizers = []string{"test-retain"}
	peer := managedAccessDevice("peer")
	r := reconcilerFor(t, d, peer)
	r.ManagedTopology = true
	if err := r.ensureManagedSharedWorkerAccess(ctx, d); err != nil {
		t.Fatal(err)
	}
	deployment, _, _ := managedWorkerObjects(d, managedprotocol.WorkerModeAppHosting, r.appHostingServiceAccountName())
	if err := r.Create(ctx, deployment); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := r.ensureManagedSharedWorkerAccess(ctx, peer); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(ctx, artifactKey(d), &rbacv1.RoleBinding{}); err != nil {
		t.Fatalf("grant revoked before active worker stopped: %v", err)
	}
	if err := r.Delete(ctx, deployment); err != nil {
		t.Fatal(err)
	}
	if err := r.ensureManagedSharedWorkerAccess(ctx, peer); err != nil {
		t.Fatal(err)
	}
	if err := r.Get(ctx, artifactKey(d), &rbacv1.RoleBinding{}); !apierrors.IsNotFound(err) {
		t.Fatal("stopped deleting worker retained grant")
	}
	if err := r.Get(ctx, artifactKey(d), &corev1.ConfigMap{}); err != nil {
		t.Fatal("deletion erased journal")
	}
}

func TestManagedVerifiedRetainedJournalProtectionSurvivesMarkerRemoval(t *testing.T) {
	ctx := context.Background()
	d := verifiedAccessDevice("retained")
	r := reconcilerFor(t, d)
	r.ManagedTopology = true
	if err := r.ensureManagedSharedWorkerAccess(ctx, d); err != nil {
		t.Fatal(err)
	}
	d.Spec.AppArtifacts = nil
	if err := r.Update(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := r.ensureManagedSharedWorkerAccess(ctx, d); err != nil {
		t.Fatal(err)
	}
	var cm corev1.ConfigMap
	if err := r.Get(ctx, artifactKey(d), &cm); err != nil {
		t.Fatal(err)
	}
	cm.Annotations = nil
	if err := r.Update(ctx, &cm); err != nil {
		t.Fatal(err)
	}
	role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: "journal-editor", Namespace: d.Namespace}, Rules: []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"configmaps"}, Verbs: []string{"update"}, ResourceNames: []string{cm.Name}}}}
	binding := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: role.Name, Namespace: d.Namespace}, RoleRef: rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "Role", Name: role.Name}, Subjects: []rbacv1.Subject{{Kind: "User", Name: "editor"}}}
	if err := r.Create(ctx, role); err != nil {
		t.Fatal(err)
	}
	if err := r.Create(ctx, binding); err != nil {
		t.Fatal(err)
	}
	if err := r.inspectManagedWorkerNamespaceRBAC(ctx, d, r.appHostingServiceAccountName(), r.networkManagementServiceAccountName()); err == nil {
		t.Fatal("retained journal lost protection after metadata removal")
	}
}
