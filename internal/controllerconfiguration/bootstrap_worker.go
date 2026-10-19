// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"reflect"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apps "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Retained incarnation facts, not actor authorization. They prevent an owned
// fence from silently moving to a replacement worker after reconstruction.
type bootstrapControllerWorker struct {
	Deployment   ops.ConfigurationReservationReference `json:"deployment"`
	ReplicaSet   ops.ConfigurationReservationReference `json:"replicaSet"`
	Pod          ops.ConfigurationReservationReference `json:"pod"`
	Username     string                                `json:"username"`
	ContractHash string                                `json:"contractHash"`
}

func readBootstrapControllerWorker(ctx context.Context, reader client.Reader, expected *cvk.NetworkController, now time.Time) (*bootstrapControllerWorker, error) {
	reject := errors.New("bootstrap controller worker incarnation is unavailable or changed")
	if reader == nil || expected == nil || expected.UID == "" || expected.ResourceVersion == "" || now.IsZero() {
		return nil, reject
	}
	var nc cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), &nc); err != nil {
		return nil, err
	}
	if nc.UID != expected.UID || nc.ResourceVersion != expected.ResourceVersion {
		return nil, reject
	}
	deployment, pod, err := readCandidateConnector(ctx, reader, &nc)
	if err != nil {
		return nil, err
	}
	worker := &bootstrapControllerWorker{
		Deployment:   ops.ConfigurationReservationReference{Name: deployment.Name, UID: string(deployment.UID)},
		Pod:          ops.ConfigurationReservationReference{Name: pod.Name, UID: string(pod.UID)},
		Username:     "system:serviceaccount:" + nc.Namespace + ":" + pod.Spec.ServiceAccountName,
		ContractHash: deployment.Spec.Template.Annotations["cisco.vk/controller-contract-hash"],
	}
	owner := metav1.GetControllerOf(pod)
	if owner == nil {
		return nil, reject
	}
	var rs apps.ReplicaSet
	if err := reader.Get(ctx, client.ObjectKey{Namespace: nc.Namespace, Name: owner.Name}, &rs); err != nil {
		return nil, err
	}
	if rs.UID != owner.UID {
		return nil, reject
	}
	worker.ReplicaSet = ops.ConfigurationReservationReference{Name: rs.Name, UID: string(rs.UID)}
	if err := readControllerWorkerBinding(ctx, reader, nc.Namespace, ops.ConfigurationReservationReference{Name: nc.Name, UID: string(nc.UID)}, nc.Generation, worker.Pod, worker.Username, now); err != nil {
		return nil, err
	}
	// A helper may have validated newer objects with the same UIDs. Re-read
	// the originals after all checks so selection cannot retain stale spec/status.
	for _, original := range []client.Object{&nc, deployment, &rs, pod} {
		fresh := original.DeepCopyObject().(client.Object)
		if err := reader.Get(ctx, client.ObjectKeyFromObject(original), fresh); err != nil {
			return nil, err
		}
		if fresh.GetUID() != original.GetUID() || fresh.GetResourceVersion() != original.GetResourceVersion() || !reflect.DeepEqual(fresh, original) {
			return nil, reject
		}
	}
	return worker, nil
}
