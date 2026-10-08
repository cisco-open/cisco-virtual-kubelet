// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"errors"
	"reflect"
	"strings"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type healthResult struct {
	phase                     ciskov1.NetworkControllerPhase
	authenticated, compatible bool
	reason, message           string
}

func (r healthResult) ready() bool { return r.authenticated && r.compatible }

func classify(err error) healthResult {
	if err == nil {
		return healthResult{ciskov1.NetworkControllerPhaseReady, true, true, "Connected", "Catalyst Center API is reachable"}
	}
	msg := transport.RedactCredentials(err.Error())
	var restErr *transport.RESTError
	if errors.As(err, &restErr) && restErr.AuthFailure() {
		return healthResult{ciskov1.NetworkControllerPhaseError, false, false, "AuthenticationFailed", "Catalyst Center rejected the supplied credentials"}
	}
	if strings.Contains(strings.ToLower(msg), "credential") {
		return healthResult{ciskov1.NetworkControllerPhaseError, false, false, "InvalidCredentials", msg}
	}
	return healthResult{ciskov1.NetworkControllerPhaseDegraded, false, false, "Unreachable", msg}
}

func setCapability(st *ciskov1.NetworkControllerStatus, name string, supported bool, message string) {
	for i := range st.Capabilities {
		if st.Capabilities[i].Name == name {
			st.Capabilities[i].Supported, st.Capabilities[i].Message = supported, message
			return
		}
	}
	st.Capabilities = append(st.Capabilities, ciskov1.NetworkControllerCapabilityStatus{Name: name, Supported: supported, Message: message})
}
func equalStatus(a, b *ciskov1.NetworkController) bool {
	x, y := a.Status.DeepCopy(), b.Status.DeepCopy()
	x.LastAttemptTime, y.LastAttemptTime = nil, nil
	return reflect.DeepEqual(x, y)
}
func condition(typ string, ok bool, reason, message string, generation int64) metav1.Condition {
	status := metav1.ConditionFalse
	if ok {
		status = metav1.ConditionTrue
	}
	return metav1.Condition{Type: typ, Status: status, Reason: reason, Message: message, ObservedGeneration: generation}
}
