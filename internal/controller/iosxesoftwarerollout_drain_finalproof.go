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

package controller

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
)

// patchDrainPodDeviceClean commits the durable acquisition fence only from the
// newest status resourceVersion. WorkerDrain is worker-owned and can advance
// between the reconcile read and this CAS, so the evidence revision must be
// selected inside the optimistic manager-status patch rather than passed in.
func (r *IOSXESoftwareRolloutReconciler) patchDrainPodDeviceClean(
	ctx context.Context,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
	uid string,
	now time.Time,
) error {
	return r.patchLeafManagerFields(ctx, client.ObjectKeyFromObject(leaf), func(current *opsv1alpha1.IOSXESoftwareUpgrade) error {
		if current.UID != leaf.UID || current.Status.ManagerDrain == nil || leaf.Status.ManagerDrain == nil ||
			current.Status.ManagerDrain.SessionToken != leaf.Status.ManagerDrain.SessionToken {
			return fmt.Errorf("drain session changed while fencing final device-clean proof")
		}
		for i := range current.Status.ManagerDrain.Pods {
			pod := &current.Status.ManagerDrain.Pods[i]
			if pod.UID != uid {
				continue
			}
			if pod.Phase == opsv1alpha1.UpgradeDrainPodDeviceClean {
				return nil
			}
			if pod.Phase != opsv1alpha1.UpgradeDrainPodTerminationObserved {
				return fmt.Errorf("drain Pod %q left TerminationObserved before final device-clean proof", uid)
			}
			revision, ok := drainWorkerProvesPodClean(current, pod)
			if !ok {
				// A newer worker publication superseded the preliminary proof. Leave
				// the Pod protected and let the exact teardown callback retry.
				return nil
			}
			pod.Phase = opsv1alpha1.UpgradeDrainPodDeviceClean
			timestamp := metav1.NewTime(now)
			pod.DeviceCleanAt = &timestamp
			pod.DeviceCleanInventoryRevision = revision
			current.Status.ManagerDrain.UpdatedAt = timestamp
			return nil
		}
		return fmt.Errorf("selected drain Pod UID %q is absent", uid)
	})
}

// revalidateDrainPodFinalProof performs the last manager-side read of both
// worker evidence and the exact canonical mutation Lease before removing the
// Pod finalizer. DeviceClean is an acquisition fence in the worker: an idle
// Lease can no longer be newly acquired, and stale pre-fence authorization is
// rejected by a fresh pre-dispatch check.
func (r *IOSXESoftwareRolloutReconciler) revalidateDrainPodFinalProof(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	leaf *opsv1alpha1.IOSXESoftwareUpgrade,
	uid string,
) (*opsv1alpha1.IOSXESoftwareUpgrade, *opsv1alpha1.UpgradeDrainPodStatus, bool, error) {
	var current opsv1alpha1.IOSXESoftwareUpgrade
	if err := r.reader().Get(ctx, client.ObjectKeyFromObject(leaf), &current); err != nil {
		return nil, nil, false, err
	}
	if current.UID != leaf.UID || current.Status.ManagerDrain == nil || leaf.Status.ManagerDrain == nil ||
		current.Status.ManagerDrain.SessionToken != leaf.Status.ManagerDrain.SessionToken {
		return nil, nil, false, fmt.Errorf("drain session changed while revalidating final device-clean proof")
	}
	for i := range current.Status.ManagerDrain.Pods {
		pod := &current.Status.ManagerDrain.Pods[i]
		if pod.UID != uid {
			continue
		}
		if pod.Phase != opsv1alpha1.UpgradeDrainPodDeviceClean || pod.DeviceCleanAt == nil ||
			pod.DeviceCleanAt.IsZero() || pod.DeviceCleanInventoryRevision <= pod.DeletionObservedInventoryRevision {
			return nil, nil, false, fmt.Errorf("drain Pod %q has invalid DeviceClean evidence", uid)
		}
		revision, ok := drainWorkerProvesPodClean(&current, pod)
		if !ok || revision < pod.DeviceCleanInventoryRevision {
			return &current, pod, false, nil
		}
		if err := r.ensureDrainLeaseIdle(ctx, rollout, target, &current); err != nil {
			return &current, pod, false, err
		}
		return &current, pod, true, nil
	}
	return nil, nil, false, fmt.Errorf("selected drain Pod UID %q is absent", uid)
}
