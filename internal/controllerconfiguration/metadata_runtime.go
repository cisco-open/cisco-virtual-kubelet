// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"path"
	"reflect"
	"strings"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	core "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ReadMetadataRuntimeManager observes one current manager Pod for the exact
// installation account. It supplies no grant and cannot prove the caller's
// token, compiled policies or local aggregator drain; the composition root must
// corroborate those independently. Settlement can inspect a disabled current
// manager, but new handoff/worker claims require explicit execution enablement.
func ReadMetadataRuntimeManager(ctx context.Context, reader client.Reader, binding RuntimeAdmissionBinding, execution bool) (*core.Pod, error) {
	reject := errors.New("metadata runtime requires one current compatible manager and shared fence namespace")
	if reader == nil || binding.Validate() != nil || binding.LeaseNamespace == "" {
		return nil, reject
	}
	identity := strings.Split(binding.ManagerUsername, ":")
	read := func() (*core.Pod, error) {
		var pods core.PodList
		if err := reader.List(ctx, &pods, client.InNamespace(identity[2])); err != nil {
			return nil, err
		}
		if pods.Continue != "" || pods.RemainingItemCount != nil && *pods.RemainingItemCount > 0 {
			return nil, reject
		}
		var current *core.Pod
		for i := range pods.Items {
			pod := &pods.Items[i]
			if pod.Spec.ServiceAccountName != identity[3] || pod.Status.Phase == core.PodSucceeded || pod.Status.Phase == core.PodFailed {
				continue
			}
			if current != nil {
				return nil, reject
			}
			current = pod
		}
		if current == nil || current.UID == "" || current.ResourceVersion == "" || current.DeletionTimestamp != nil || current.Status.Phase != core.PodRunning || len(current.Spec.Containers) != 1 || len(current.Spec.InitContainers) != 0 || len(current.Spec.EphemeralContainers) != 0 || current.Labels["app.kubernetes.io/component"] != "controller" {
			return nil, reject
		}
		ready := false
		for _, c := range current.Status.Conditions {
			if c.Type == core.PodReady && c.Status == core.ConditionTrue && !c.LastTransitionTime.IsZero() && !c.LastTransitionTime.After(time.Now()) {
				ready = true
			}
		}
		if !ready {
			return nil, reject
		}
		c := current.Spec.Containers[0]
		if c.Name != "controller" || len(c.Command) < 2 || path.Base(c.Command[0]) != "cisco-vk" || c.Command[1] != "manager" {
			return nil, reject
		}
		args := append(append([]string(nil), c.Command[2:]...), c.Args...)
		for _, flag := range []string{"enable-managed-topology", "leader-elect"} {
			enabled, err := metadataRuntimeBoolFlag(args, flag)
			if err != nil || !enabled {
				return nil, reject
			}
		}
		aggregated, err := metadataRuntimeBoolFlag(args, "enable-config-aggregator")
		if err != nil || aggregated {
			return nil, reject
		}
		enabled, err := metadataRuntimeBoolFlag(args, managedprotocol.ControllerMetadataManagerFlag)
		if err != nil || execution && !enabled {
			return nil, reject
		}
		for flag, want := range map[string]string{"topology-policy-namespace": binding.PolicyNamespace, "topology-policy-name": binding.PolicyName, "app-hosting-service-account": binding.AppHostingServiceAccount, "network-management-service-account": binding.NetworkManagementServiceAccount} {
			value, err := metadataRuntimeValueFlag(args, flag)
			if err != nil || value != want {
				return nil, reject
			}
		}
		namespace, err := MetadataRuntimeLiteralEnv(c, "CONFIG_LEASE_NAMESPACE")
		if err != nil || namespace != binding.LeaseNamespace {
			return nil, reject
		}
		statusReady := false
		for _, status := range current.Status.ContainerStatuses {
			if status.Name == c.Name && status.Ready && status.ImageID != "" && status.State.Running != nil && !status.State.Running.StartedAt.IsZero() {
				statusReady = true
			}
		}
		if !statusReady {
			return nil, reject
		}
		return current.DeepCopy(), nil
	}
	first, err := read()
	if err != nil {
		return nil, err
	}
	second, err := read()
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(first, second) {
		return nil, reject
	}
	return first, nil
}

// Literal installation data must occur once on the actual runtime container.
// ConfigMap/Secret/downward substitutions cannot replace manager-verified input.
func MetadataRuntimeLiteralEnv(container core.Container, name string) (string, error) {
	value := ""
	found := 0
	for _, e := range container.Env {
		if e.Name == name {
			found++
			if e.ValueFrom != nil {
				return "", errors.New("metadata installation environment must be literal")
			}
			value = e.Value
		}
	}
	if found != 1 || value == "" {
		return "", errors.New("metadata installation environment is absent or ambiguous")
	}
	return value, nil
}
func metadataRuntimeBoolFlag(args []string, name string) (bool, error) {
	found := 0
	value := false
	for _, arg := range args {
		if arg == "--"+name {
			found++
			value = true
		} else if strings.HasPrefix(arg, "--"+name+"=") {
			found++
			raw := strings.TrimPrefix(arg, "--"+name+"=")
			if raw != "true" && raw != "false" {
				return false, errors.New("invalid metadata runtime boolean")
			}
			value = raw == "true"
		}
	}
	if found > 1 {
		return false, errors.New("duplicate metadata runtime flag")
	}
	return value, nil
}
func metadataRuntimeValueFlag(args []string, name string) (string, error) {
	found := 0
	value := ""
	for i, arg := range args {
		if arg == "--"+name {
			found++
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
				return "", errors.New("missing metadata runtime flag value")
			}
			value = args[i+1]
		} else if strings.HasPrefix(arg, "--"+name+"=") {
			found++
			value = strings.TrimPrefix(arg, "--"+name+"=")
		}
	}
	if found != 1 || value == "" {
		return "", errors.New("metadata runtime value absent or ambiguous")
	}
	return value, nil
}

// ReadMetadataPlanningWorker corroborates the ordinary original worker chain
// before a metadata journal exists. It creates no handoff, slot or permit and
// does not manufacture a physical reservation to reuse the validator.
func ReadMetadataPlanningWorker(ctx context.Context, reader client.Reader, nc *cvk.NetworkController, pod ops.ConfigurationReservationReference, now time.Time) (string, error) {
	if pod.Name == "" || pod.UID == "" {
		return "", errors.New("metadata planning caller is unavailable")
	}
	current, err := readBootstrapControllerWorker(ctx, reader, nc, now)
	if err != nil {
		return "", err
	}
	if current.Pod != pod {
		return "", errors.New("metadata planning caller is not the current connector")
	}
	return current.Username, nil
}
