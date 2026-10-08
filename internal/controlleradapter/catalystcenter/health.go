// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"errors"
	"net/http"
	"reflect"

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
	var restErr *transport.RESTError
	if errors.Is(err, errCredentials) {
		return healthResult{ciskov1.NetworkControllerPhaseError, false, false, "InvalidCredentials", "Cannot read non-empty Catalyst Center username and password files"}
	}
	if errors.As(err, &restErr) {
		if restErr.StatusCode == http.StatusUnauthorized || (restErr.Path == authPath && restErr.StatusCode == http.StatusForbidden) {
			return healthResult{ciskov1.NetworkControllerPhaseError, false, false, "AuthenticationFailed", "Catalyst Center rejected authentication"}
		}
		if restErr.Path != authPath {
			switch restErr.StatusCode {
			case http.StatusForbidden:
				return healthResult{ciskov1.NetworkControllerPhaseDegraded, true, false, "Forbidden", "Authenticated but not authorized to read device inventory"}
			case http.StatusNotFound:
				return healthResult{ciskov1.NetworkControllerPhaseDegraded, true, false, "InventoryAPIUnavailable", "The required device inventory API is unavailable"}
			}
		}
		return healthResult{ciskov1.NetworkControllerPhaseDegraded, restErr.Path != authPath, false, "APIRequestFailed", "Catalyst Center API request failed"}
	}
	if errors.Is(err, errInvalidResponse) {
		return healthResult{ciskov1.NetworkControllerPhaseDegraded, false, false, "InvalidAPIResponse", "Catalyst Center returned an invalid API response"}
	}
	return healthResult{ciskov1.NetworkControllerPhaseDegraded, false, false, "Unreachable", "Catalyst Center connection could not be established"}
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
	return reflect.DeepEqual(a.Status, b.Status)
}
func condition(typ string, ok bool, reason, message string, generation int64) metav1.Condition {
	status := metav1.ConditionFalse
	if ok {
		status = metav1.ConditionTrue
	}
	return metav1.Condition{Type: typ, Status: status, Reason: reason, Message: message, ObservedGeneration: generation}
}
