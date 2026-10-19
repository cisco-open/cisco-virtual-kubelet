// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"encoding/json"
	"errors"
	"io"
	"strings"

	validation "k8s.io/apimachinery/pkg/util/validation"
)

const RuntimeAdmissionEnvironment = "CISCO_VK_CONFIGURATION_ADMISSION"

// RuntimeAdmissionBinding carries non-secret manager-verified installation
// identities into immutable worker templates. It is input to live verification,
// never a cached authorization result or a permission to execute an operation.
type RuntimeAdmissionBinding struct {
	Protocol                        string `json:"protocol"`
	ManagerUsername                 string `json:"managerUsername"`
	AdmissionPrefix                 string `json:"admissionPrefix"`
	PolicyNamespace                 string `json:"policyNamespace"`
	PolicyName                      string `json:"policyName"`
	LedgerName                      string `json:"ledgerName"`
	AppHostingServiceAccount        string `json:"appHostingServiceAccount"`
	NetworkManagementServiceAccount string `json:"networkManagementServiceAccount"`
	// Empty LeaseNamespace retains the admin policy's per-device namespace default.
	LeaseNamespace string `json:"leaseNamespace"`
}

func (b RuntimeAdmissionBinding) Validate() error {
	reject := errors.New("configuration admission installation binding is incomplete or invalid")
	// The binding selects exactly one protocol. Supporting the group schema here
	// does not bypass live manager authorization or enable public reconciliation.
	if b.Protocol != Protocol && b.Protocol != GroupReservationProtocol {
		return reject
	}
	for _, name := range []string{b.PolicyName, b.LedgerName, b.AdmissionPrefix, b.AppHostingServiceAccount, b.NetworkManagementServiceAccount} {
		if name == "" || len(validation.IsDNS1123Subdomain(name)) != 0 {
			return reject
		}
	}
	if b.PolicyNamespace == "" || len(validation.IsDNS1123Label(b.PolicyNamespace)) != 0 {
		return reject
	}
	if b.LeaseNamespace != "" && len(validation.IsDNS1123Label(b.LeaseNamespace)) != 0 {
		return reject
	}
	identity := strings.Split(b.ManagerUsername, ":")
	if len(identity) != 4 || identity[0] != "system" || identity[1] != "serviceaccount" || identity[2] == "" || identity[3] == "" || len(validation.IsDNS1123Label(identity[2])) != 0 || len(validation.IsDNS1123Subdomain(identity[3])) != 0 {
		return reject
	}
	if b.AppHostingServiceAccount == b.NetworkManagementServiceAccount {
		return reject
	}
	return nil
}
func (b RuntimeAdmissionBinding) Encode() (string, error) {
	if err := b.Validate(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(b)
	return string(raw), err
}
func ParseRuntimeAdmissionBinding(raw string) (*RuntimeAdmissionBinding, error) {
	if raw == "" {
		return nil, nil
	} // Existing workers remain recovery-only.
	if len(raw) > 4096 {
		return nil, errors.New("configuration admission binding exceeds maximum size")
	}
	var b RuntimeAdmissionBinding
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&b); err != nil {
		return nil, errors.New("invalid configuration admission binding document")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("configuration admission binding must contain one document")
	}
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return &b, nil
}
