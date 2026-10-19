// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controller

import (
	"context"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	apps "k8s.io/api/apps/v1"
	rbac "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
)

func TestConfigurationReadAccessLifecycle(t *testing.T) {
	for _, scenario := range []string{"default lease", "separate lease", "other adapter", "no binding", "role drift", "foreign collision"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			nc := validNetworkController("cc", "catalyst-center")
			r, c := newNetworkControllerReconciler(t, nc)
			r.APIReader = c
			r.ManageConfigurationReservations = true
			r.ConfigurationAdmissionBinding = &contract.RuntimeAdmissionBinding{Protocol: contract.Protocol, ManagerUsername: "system:serviceaccount:system:manager", AdmissionPrefix: "cvk", PolicyNamespace: "system", PolicyName: "policy", LedgerName: "ledger", AppHostingServiceAccount: "app", NetworkManagementServiceAccount: "network"}
			if scenario == "separate lease" {
				r.ConfigurationAdmissionBinding.LeaseNamespace = "leases"
			}
			if scenario == "other adapter" {
				nc.Spec.Type = "nexus-dashboard"
			}
			if scenario == "no binding" {
				r.ConfigurationAdmissionBinding = nil
			}
			for _, name := range []string{managedprotocol.NetworkManagementGlobalReadClusterRole, managedprotocol.NetworkManagementLeaseReadOnlyClusterRole} {
				role := &rbac.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: name}, Rules: managedprotocol.WorkerClusterRoleContracts()[name]}
				if scenario == "role drift" && name == managedprotocol.NetworkManagementGlobalReadClusterRole {
					role.Rules = append(role.Rules, rbac.PolicyRule{APIGroups: []string{"*"}, Resources: []string{"*"}, Verbs: []string{"*"}})
				}
				if err := c.Create(ctx, role); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "foreign collision" {
				if err := c.Create(ctx, &rbac.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: configurationAccessName(nc)}}); err != nil {
					t.Fatal(err)
				}
			}
			err := r.reconcileConfigurationReadAccess(ctx, nc)
			if (err != nil) != (scenario == "role drift" || scenario == "foreign collision") {
				t.Fatalf("err=%v", err)
			}
			if err != nil {
				return
			}
			var globals rbac.ClusterRoleBindingList
			var leases rbac.RoleBindingList
			if err := c.List(ctx, &globals); err != nil {
				t.Fatal(err)
			}
			if err := c.List(ctx, &leases); err != nil {
				t.Fatal(err)
			}
			want := scenario == "default lease" || scenario == "separate lease"
			if (len(globals.Items) == 1) != want {
				t.Fatalf("global grants=%d", len(globals.Items))
			}
			if (len(leases.Items) == 1) != (scenario == "separate lease") {
				t.Fatalf("lease grants=%d", len(leases.Items))
			}
			if !want {
				return
			}
			if globals.Items[0].RoleRef.Name != managedprotocol.NetworkManagementGlobalReadClusterRole || globals.Items[0].Subjects[0].Namespace != nc.Namespace || globals.Items[0].Subjects[0].Name != networkControllerWorkerName(nc.Name) {
				t.Fatal("wrong global grant")
			}
			if len(leases.Items) > 0 && leases.Items[0].RoleRef.Name != managedprotocol.NetworkManagementLeaseReadOnlyClusterRole {
				t.Fatal("connector received Lease mutation authority")
			}
			requests := networkControllerWorkerRequests(ctx, &globals.Items[0])
			if len(requests) != 1 || requests[0].Namespace != nc.Namespace {
				t.Fatal("cluster binding did not map to controller namespace")
			}
			if err := r.reconcileConfigurationReadAccess(ctx, nc); err != nil {
				t.Fatal("idempotence", err)
			}
			deployment := &apps.Deployment{ObjectMeta: metav1.ObjectMeta{Name: networkControllerWorkerName(nc.Name), Namespace: nc.Namespace}}
			setNetworkControllerWorkerMetadata(deployment, nc, nil)
			if err := c.Create(ctx, deployment); err != nil {
				t.Fatal(err)
			}
			if done, err := r.quiesceWorker(ctx, nc); err != nil || done {
				t.Fatal("worker foreground deletion did not defer grant cleanup", done, err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(&globals.Items[0]), &rbac.ClusterRoleBinding{}); err != nil {
				t.Fatal("observation grant removed before worker deletion completed", err)
			}
			// Retirement no longer depends on current installation configuration, which
			// may have changed its Lease namespace or been disabled since creation.
			r.ConfigurationAdmissionBinding = nil
			if pending, err := r.retireConfigurationReadAccess(ctx, nc); err != nil || !pending {
				t.Fatal(pending, err)
			}
			if pending, err := r.retireConfigurationReadAccess(ctx, nc); err != nil || pending {
				t.Fatal(pending, err)
			}
			if err := c.List(ctx, &globals, client.MatchingLabels{configurationAccessControllerUID: string(nc.UID)}); err != nil || len(globals.Items) != 0 {
				t.Fatal("global grant retained", err)
			}
		})
	}
}
