// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controller

import (
	"fmt"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	core "k8s.io/api/core/v1"
)

// Applied after separating the network template from app hosting. A missing
// binding deliberately leaves old/default deployments in recovery-only mode.
func injectConfigurationAdmissionBinding(template *core.PodTemplateSpec, binding *contract.RuntimeAdmissionBinding, eligible bool) (string, error) {
	if !eligible || binding == nil {
		return "", nil
	}
	if template == nil || len(template.Spec.Containers) != 1 {
		return "", fmt.Errorf("configuration admission requires a single worker container")
	}
	encoded, err := binding.Encode()
	if err != nil {
		return "", err
	}
	for _, env := range template.Spec.Containers[0].Env {
		if env.Name == contract.RuntimeAdmissionEnvironment {
			return "", fmt.Errorf("duplicate configuration admission environment")
		}
	}
	template.Spec.Containers[0].Env = append(template.Spec.Containers[0].Env, core.EnvVar{Name: contract.RuntimeAdmissionEnvironment, Value: encoded})
	return encoded, nil
}
