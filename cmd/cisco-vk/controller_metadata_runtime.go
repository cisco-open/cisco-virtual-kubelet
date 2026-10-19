// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"fmt"
	"reflect"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyrollout"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type controllerMetadataParticipant interface {
	ConfigureMetadataHandoff(contract.BootstrapFenceInstallation, string, contract.MetadataWorkerAuthorization) error
}

func configureControllerMetadataProtocol(participant any, protocol string, binding *contract.RuntimeAdmissionBinding, authorize contract.MetadataWorkerAuthorization) error {
	if protocol == "" {
		return nil
	}
	if protocol != cvk.NetworkControllerMetadataHandoffProtocol || binding == nil || binding.Validate() != nil || binding.LeaseNamespace == "" || authorize == nil {
		return fmt.Errorf("controller metadata runtime installation is incomplete")
	}
	metadata, ok := participant.(controllerMetadataParticipant)
	if !ok {
		return fmt.Errorf("controller adapter does not support metadata handoff")
	}
	return metadata.ConfigureMetadataHandoff(contract.BootstrapFenceInstallation{PolicyName: binding.AdmissionPrefix + "-bootstrap-fence", ManagerUsername: binding.ManagerUsername, LeaseNamespace: binding.LeaseNamespace}, binding.AdmissionPrefix+"-metadata-handoff", authorize)
}

// All installation reads are repeated at claim and native guard boundaries.
// No callback writes Kubernetes state, reads controller credentials or issues
// product API calls. Manager settlement does not require a live old worker.
type controllerMetadataRuntime struct {
	api             kubernetes.Interface
	reader          client.Reader
	binding         contract.RuntimeAdmissionBinding
	podName, podUID string
}

func (r controllerMetadataRuntime) installation(ctx context.Context, execution bool) (*core.Pod, error) {
	if r.api == nil || r.reader == nil || r.binding.Validate() != nil || r.binding.LeaseNamespace == "" {
		return nil, fmt.Errorf("controller metadata installation is incomplete")
	}
	b := r.binding
	if _, err := verifyManagedAdmissionBindings(ctx, r.api, admissionContractBindings{AdmissionPrefix: b.AdmissionPrefix, ManagerUsername: b.ManagerUsername, PolicyNamespace: b.PolicyNamespace, PolicyName: b.PolicyName, LedgerName: b.LedgerName, AppHostingServiceAccount: b.AppHostingServiceAccount, NetworkManagementServiceAccount: b.NetworkManagementServiceAccount}); err != nil {
		return nil, err
	}
	if err := verifyManagedWorkerClusterRoles(ctx, r.reader); err != nil {
		return nil, err
	}
	if _, err := contract.ReadBootstrapFenceProtection(ctx, r.reader, b.AdmissionPrefix+"-bootstrap-fence", b.ManagerUsername, b.LeaseNamespace); err != nil {
		return nil, err
	}
	if _, err := contract.ReadMetadataHandoffProtection(ctx, r.reader, b.AdmissionPrefix+"-metadata-handoff", b.ManagerUsername, b.LeaseNamespace); err != nil {
		return nil, err
	}
	return contract.ReadMetadataRuntimeManager(ctx, r.reader, b, execution)
}
func (r controllerMetadataRuntime) manager(ctx context.Context, execution bool) (*core.Pod, error) {
	if r.api == nil || r.reader == nil || r.binding.Validate() != nil || r.podName == "" || r.podUID == "" {
		return nil, fmt.Errorf("metadata caller installation is incomplete")
	}

	if err := verifyManagedWorkerPodIdentity(ctx, r.api, r.binding.ManagerUsername, r.podName, r.podUID); err != nil {
		return nil, err
	}
	p, err := r.installation(ctx, execution)
	if err != nil {
		return nil, err
	}
	if p.Name != r.podName || string(p.UID) != r.podUID {
		return nil, fmt.Errorf("metadata manager caller differs from current installation Pod")
	}
	b := r.binding
	policy, err := topologyrollout.ReadAdminPolicyPreflight(ctx, r.reader, types.NamespacedName{Namespace: b.PolicyNamespace, Name: b.PolicyName})
	if err != nil {
		return nil, err
	}
	if policy.AdmissionPrefix != b.AdmissionPrefix || policy.Config.LedgerName != b.LedgerName || policy.Config.ConfigLeaseNamespace != b.LeaseNamespace {
		return nil, fmt.Errorf("metadata manager policy installation changed")
	}
	if err := validateManagedWorkerPolicyIdentity(policy.Config, b.AppHostingServiceAccount, b.NetworkManagementServiceAccount, b.LeaseNamespace); err != nil {
		return nil, err
	}
	return p, nil
}
func (r controllerMetadataRuntime) connector(ctx context.Context, nc *cvk.NetworkController, ref ops.ConfigurationReservationReference, manager *core.Pod) error {
	if err := verifyControllerWorkerPodDescriptor(ctx, r.reader, nc, ref); err != nil {
		return err
	}
	var pod core.Pod
	if err := r.reader.Get(ctx, client.ObjectKey{Namespace: nc.Namespace, Name: ref.Name}, &pod); err != nil {
		return err
	}
	if string(pod.UID) != ref.UID || pod.DeletionTimestamp != nil || len(pod.Spec.Containers) != 1 || len(pod.Spec.InitContainers) != 0 || len(pod.Spec.EphemeralContainers) != 0 {
		return fmt.Errorf("metadata connector incarnation is incompatible")
	}
	c := pod.Spec.Containers[0]
	if c.Name != "controller-worker" {
		return fmt.Errorf("metadata connector container is incompatible")
	}
	protocol, err := contract.MetadataRuntimeLiteralEnv(c, managedprotocol.EnvControllerMetadataProtocol)
	if err != nil || protocol != cvk.NetworkControllerMetadataHandoffProtocol {
		return fmt.Errorf("metadata connector protocol is incompatible")
	}
	raw, err := contract.MetadataRuntimeLiteralEnv(c, contract.RuntimeAdmissionEnvironment)
	if err != nil {
		return err
	}
	binding, err := contract.ParseRuntimeAdmissionBinding(raw)
	if err != nil || binding == nil || *binding != r.binding {
		return fmt.Errorf("metadata connector installation differs from manager")
	}
	imageID := func(p *core.Pod, name string) string {
		for _, s := range p.Status.ContainerStatuses {
			if s.Name == name && s.Ready && s.State.Running != nil {
				return s.ImageID
			}
		}
		return ""
	}
	if manager == nil || imageID(manager, "controller") == "" || imageID(&pod, "controller-worker") != imageID(manager, "controller") {
		return fmt.Errorf("metadata connector and manager must run the same resolved image")
	}
	return nil
}
func (r controllerMetadataRuntime) handoff(ctx context.Context, nc *cvk.NetworkController, observation contract.MetadataScopeWorkerObservation, _ contract.AdmissionProtection) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	manager, err := r.manager(ctx, true)
	if err != nil {
		return err
	}
	if nc == nil || nc.Spec.Type != "catalyst-center" || observation.Controller.Name != nc.Name || observation.Controller.UID != string(nc.UID) {
		return fmt.Errorf("metadata handoff runtime is not complete")
	}
	if err := r.connector(ctx, nc, observation.Pod, manager); err != nil {
		return err
	}
	if err := contract.ReadMetadataConsumerPods(ctx, r.reader, r.binding, observation.PhysicalIdentities); err != nil {
		return err
	}
	return nil
}
func (r controllerMetadataRuntime) settlement(ctx context.Context, nc *cvk.NetworkController) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if nc == nil || nc.Spec.Type != "catalyst-center" || nc.Status.Configuration == nil || nc.Status.Configuration.MetadataHandoff == nil {
		return fmt.Errorf("metadata settlement has no retained custody")
	}
	_, err := r.manager(ctx, false)
	return err
}
func (r controllerMetadataRuntime) worker(ctx context.Context, nc *cvk.NetworkController) error {
	if r.api == nil || r.reader == nil || r.binding.Validate() != nil || r.podName == "" || r.podUID == "" {
		return fmt.Errorf("metadata caller installation is incomplete")
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if nc == nil || nc.Status.Configuration == nil {
		return fmt.Errorf("metadata worker has no configuration ownership")
	}
	ref := ops.ConfigurationReservationReference{Name: r.podName, UID: r.podUID}
	username := ""
	if h := nc.Status.Configuration.MetadataHandoff; h != nil {
		if h.WorkerPod != ref {
			return fmt.Errorf("metadata worker caller differs from original custody")
		}
		username = h.WorkerUsername
	} else {
		if nc.Status.Configuration.Operation != nil {
			return fmt.Errorf("metadata planning cannot adopt an unsealed existing operation")
		}
		var err error
		username, err = contract.ReadMetadataPlanningWorker(ctx, r.reader, nc, ref, time.Now())
		if err != nil {
			return err
		}
	}
	if err := verifyManagedWorkerPodIdentity(ctx, r.api, username, r.podName, r.podUID); err != nil {
		return err
	}
	manager, err := r.installation(ctx, true)
	if err != nil {
		return err
	}
	before := manager.DeepCopy()
	if err := r.connector(ctx, nc, ref, manager); err != nil {
		return err
	}
	current, err := contract.ReadMetadataRuntimeManager(ctx, r.reader, r.binding, true)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(before, current) {
		return fmt.Errorf("metadata manager changed during worker authorization")
	}
	return nil
}

func validateControllerMetadataOptions(enabled, managed, leader, aggregated bool, leaseNamespace string) error {
	if enabled && (!managed || !leader || aggregated || leaseNamespace == "") {
		return fmt.Errorf("controller metadata handoff requires managed topology, isolated workers, leader election and an explicit shared Lease namespace")
	}
	return nil
}
