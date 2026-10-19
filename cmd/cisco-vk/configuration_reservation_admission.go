// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package main

import (
	"context"
	"fmt"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"k8s.io/client-go/kubernetes"
	"strings"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyrollout"
	"github.com/cisco/virtual-kubelet-cisco/internal/workloaddrain"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// The manager reuses its startup policy/admission/RBAC checks on each new
// reservation. This callback is not installed in topology retirement mode.
// Device preparation and the controller dispatch permit independently gate
// later work; creating this evidence does not authorize a remote request.
func configurationReservationManagerAuthorizer(cfg *rest.Config, reader client.Reader, policyKey types.NamespacedName, appAccount, networkAccount, leaseNamespace string) func(context.Context, *ops.ControllerConfigurationReservation) error {
	return func(ctx context.Context, r *ops.ControllerConfigurationReservation) error {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if cfg == nil || reader == nil || r == nil || (r.Spec.Protocol != contract.Protocol && r.Spec.Protocol != contract.GroupReservationProtocol) || r.Namespace == "" || r.Spec.DeviceWorkerUsername != "system:serviceaccount:"+r.Namespace+":"+networkAccount {
			return fmt.Errorf("configuration reservation manager authority is incomplete")
		}
		policy, err := topologyrollout.ReadAdminPolicyPreflight(ctx, reader, policyKey)
		if err != nil {
			return err
		}
		if err = validateManagedWorkerPolicyIdentity(policy.Config, appAccount, networkAccount, leaseNamespace); err != nil {
			return err
		}
		verification, err := verifyManagedAdmissionContract(ctx, cfg, policy.AdmissionPrefix, policyKey, policy.Config.LedgerName, appAccount, networkAccount)
		if err != nil {
			return err
		}
		binding := contract.RuntimeAdmissionBinding{Protocol: r.Spec.Protocol, ManagerUsername: verification.ManagerUsername, AdmissionPrefix: policy.AdmissionPrefix, PolicyNamespace: policyKey.Namespace, PolicyName: policyKey.Name, LedgerName: policy.Config.LedgerName, AppHostingServiceAccount: appAccount, NetworkManagementServiceAccount: networkAccount, LeaseNamespace: policy.Config.ConfigLeaseNamespace}
		if err = contract.ReadRuntimeInstallationBinding(ctx, reader, r, binding); err != nil {
			return err
		}
		if err = verifyManagedWorkerClusterRoles(ctx, reader); err != nil {
			return err
		}
		if err = verifyConfigurationConnectorDescriptor(ctx, reader, r); err != nil {
			return err
		}
		pods, err := workloaddrain.BoundActiveWorkloads(ctx, reader, r.Spec.Node.Name)
		if err != nil {
			return err
		}
		if len(pods) != 0 {
			return fmt.Errorf("configuration reservation requires an empty workload node")
		}
		return ctx.Err()
	}
}

// The worker already checks this digest at process startup. Corroborate its
// explicit argument against the manager's registered adapter contract; a
// retained worker from a different contract must finish rolling out first.
func verifyConfigurationConnectorDescriptor(ctx context.Context, reader client.Reader, r *ops.ControllerConfigurationReservation) error {
	reject := fmt.Errorf("configuration controller worker descriptor is not current")
	if reader == nil || r == nil || r.Spec.ControllerWorkerPod.Name == "" || r.Spec.ControllerWorkerPod.UID == "" {
		return reject
	}
	var nc cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: r.Spec.Controller.Name}, &nc); err != nil {
		return err
	}
	if string(nc.UID) != r.Spec.Controller.UID || nc.Generation != r.Spec.ControllerGeneration || nc.Spec.Type != "catalyst-center" {
		return reject
	}
	return verifyControllerWorkerPodDescriptor(ctx, reader, &nc, r.Spec.ControllerWorkerPod)
}

// Shared exact descriptor verification without manufacturing a physical
// reservation for metadata-only work.
func verifyControllerWorkerPodDescriptor(ctx context.Context, reader client.Reader, nc *cvk.NetworkController, podRef ops.ConfigurationReservationReference) error {
	reject := fmt.Errorf("configuration controller worker descriptor is not current")
	if reader == nil || nc == nil || nc.UID == "" || nc.Generation < 1 || nc.Spec.Type != "catalyst-center" || podRef.Name == "" || podRef.UID == "" {
		return reject
	}
	descriptor, ok := controlleradapter.DescriptorFor(string(nc.Spec.Type))
	if !ok {
		return reject
	}
	var pod core.Pod
	if err := reader.Get(ctx, client.ObjectKey{Namespace: nc.Namespace, Name: podRef.Name}, &pod); err != nil {
		return err
	}
	if string(pod.UID) != podRef.UID || pod.DeletionTimestamp != nil {
		return reject
	}
	matches := 0
	found := 0
	for _, container := range pod.Spec.Containers {
		if container.Name != "controller-worker" {
			continue
		}
		matches++
		for _, arg := range container.Args {
			if arg == "--controller-descriptor-digest" {
				return reject
			}
			if strings.HasPrefix(arg, "--controller-descriptor-digest=") {
				if arg != "--controller-descriptor-digest="+controlleradapter.DescriptorDigest(descriptor) {
					return reject
				}
				found++
			}
		}
	}
	if matches != 1 || found != 1 {
		return reject
	}
	return nil
}

// Live verification is shared by the device preparation and connector claim
// boundaries. Binding data identifies what must be verified; it grants nothing.
func configurationReservationWorkerAuthorizer(api kubernetes.Interface, reader client.Reader, binding contract.RuntimeAdmissionBinding, leaseNamespace string) func(context.Context, *ops.ControllerConfigurationReservation) error {
	return func(ctx context.Context, r *ops.ControllerConfigurationReservation) error {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if api == nil || reader == nil || r == nil || binding.Validate() != nil {
			return fmt.Errorf("configuration runtime authorization is incomplete")
		}
		expectedLeaseNamespace := binding.LeaseNamespace
		if expectedLeaseNamespace == "" {
			expectedLeaseNamespace = r.Namespace
		}
		if expectedLeaseNamespace != leaseNamespace {
			return fmt.Errorf("configuration runtime Lease namespace differs from installation")
		}
		if err := contract.ReadRuntimeInstallationBinding(ctx, reader, r, binding); err != nil {
			return err
		}
		if _, err := verifyManagedAdmissionBindings(ctx, api, admissionContractBindings{AdmissionPrefix: binding.AdmissionPrefix, ManagerUsername: binding.ManagerUsername, PolicyNamespace: binding.PolicyNamespace, PolicyName: binding.PolicyName, LedgerName: binding.LedgerName, AppHostingServiceAccount: binding.AppHostingServiceAccount, NetworkManagementServiceAccount: binding.NetworkManagementServiceAccount}); err != nil {
			return err
		}
		if err := verifyManagedWorkerClusterRoles(ctx, reader); err != nil {
			return err
		}
		if err := contract.ReadNetworkMutationRole(ctx, reader, r); err != nil {
			return err
		}
		if err := verifyConfigurationConnectorDescriptor(ctx, reader, r); err != nil {
			return err
		}
		return contract.ReadRuntimeInstallationBinding(ctx, reader, r, binding)
	}
}
