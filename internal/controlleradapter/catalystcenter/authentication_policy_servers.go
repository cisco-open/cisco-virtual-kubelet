// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const authenticationPolicyServersPath = "/dna/intent/api/v1/authentication-policy-servers"

// Deliberately project metadata only: controller responses may contain masked
// or recoverable credentials, which are never authoritative comparison values.
type authenticationPolicyServer struct {
	CiscoISE           []policyISEMetadata `json:"ciscoIseDtos,omitempty"`
	Pxgrid             *bool               `json:"pxgridEnabled,omitempty"`
	UseCatcCert        *bool               `json:"useDnacCertForPxgrid,omitempty"`
	ID                 string              `json:"instanceUuid,omitempty"`
	IP                 string              `json:"ipAddress"`
	ISE                *bool               `json:"isIseEnabled,omitempty"`
	Protocol           string              `json:"protocol"`
	Role               string              `json:"role,omitempty"`
	AuthenticationPort *int                `json:"authenticationPort,omitempty"`
	AccountingPort     *int                `json:"accountingPort,omitempty"`
	Port               *int                `json:"port,omitempty"`
	Retries            *int                `json:"retries,omitempty"`
	Timeout            *int                `json:"timeoutSeconds,omitempty"`
	EncryptionScheme   string              `json:"encryptionScheme,omitempty"`
}

func (c *client) authenticationPolicyServers(ctx context.Context) (map[string]authenticationPolicyServer, error) {
	raw, err := c.get(ctx, authenticationPolicyServersPath, nil, 200)
	if err != nil {
		return nil, err
	}
	var body struct {
		Response []authenticationPolicyServer `json:"response"`
	}
	if json.Unmarshal(raw, &body) != nil || body.Response == nil || len(body.Response) > 1000 {
		return nil, errInvalidResponse
	}
	out := map[string]authenticationPolicyServer{}
	ids := map[string]bool{}
	for _, server := range body.Response {
		ip, err := canonicalAAAAddress(server.IP)
		if err != nil || !validAPIID(server.ID) || ids[server.ID] {
			return nil, errInvalidResponse
		}
		if _, exists := out[ip]; exists {
			return nil, errInvalidResponse
		}
		switch server.Protocol {
		case "RADIUS", "TACACS", "RADIUS_TACACS":
		default:
			return nil, errInvalidResponse
		}
		server.IP = ip
		out[ip] = server
		ids[server.ID] = true
	}
	return out, nil
}
func policyServerMetadata(server authenticationPolicyServer) map[string]any {
	server.ID = ""
	raw, _ := json.Marshal(server)
	var value map[string]any
	_ = json.Unmarshal(raw, &value)
	return value
}
func policyServerMatches(want, actual authenticationPolicyServer) bool {
	return templateValueMatches(policyServerMetadata(want), policyServerMetadata(actual))
}

// This isolated registration helper creates only plaintext non-ISE fixtures.
// Manifest execution uses policyServerExecutor with version-bound projections
// and durable application receipts. Readback here verifies metadata only.
func (c *client) planAuthenticationPolicyServerRegistration(ctx context.Context, desired authenticationPolicyServer, sharedSecret string) (*configurationWrite, error) {
	if desired.ID != "" || desired.EncryptionScheme != "" || desired.ISE == nil || *desired.ISE || desired.Role != "primary" || len(sharedSecret) == 0 || len(sharedSecret) > 4096 {
		return nil, errors.New("invalid AAA registration")
	}
	ip, err := canonicalAAAAddress(desired.IP)
	if err != nil {
		return nil, err
	}
	desired.IP = ip
	switch desired.Protocol {
	case "RADIUS", "TACACS", "RADIUS_TACACS":
	default:
		return nil, errors.New("invalid AAA protocol")
	}
	if desired.Protocol != "TACACS" && (desired.AuthenticationPort == nil || desired.AccountingPort == nil) {
		return nil, errors.New("RADIUS ports must be explicit")
	}
	if desired.Protocol != "RADIUS" && desired.Port == nil {
		return nil, errors.New("TACACS port must be explicit")
	}
	for _, port := range []*int{desired.AuthenticationPort, desired.AccountingPort, desired.Port} {
		if port != nil && (*port < 1 || *port > 65535) {
			return nil, errors.New("invalid AAA port")
		}
	}
	if desired.Retries != nil && (*desired.Retries < 0 || *desired.Retries > 10) {
		return nil, errors.New("invalid AAA retries")
	}
	if desired.Timeout != nil && (*desired.Timeout < 1 || *desired.Timeout > 60) {
		return nil, errors.New("invalid AAA timeout")
	}
	actual, err := c.authenticationPolicyServers(ctx)
	if err != nil {
		return nil, err
	}
	if _, exists := actual[ip]; exists {
		return nil, errors.New("AAA registration cannot adopt an existing server")
	}
	payload := policyServerMetadata(desired)
	payload["sharedSecret"] = sharedSecret
	body, _ := json.Marshal(payload)
	return &configurationWrite{resource: ip, action: "Create", submit: func(ctx context.Context) (Task, error) {
		raw, err := c.mutate(ctx, http.MethodPost, authenticationPolicyServersPath, body)
		if err != nil {
			return Task{}, err
		}
		var receipt struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
			return Task{}, errInvalidResponse
		}
		return Task{ID: receipt.Response.ID}, nil
	}}, nil
}
func (c *client) verifyAuthenticationPolicyServerRegistration(ctx context.Context, desired authenticationPolicyServer, op *ciskov1.NetworkControllerConfigurationOperation) (string, bool, error) {
	ip, err := canonicalAAAAddress(desired.IP)
	if err != nil {
		return "", false, err
	}
	if op.Action != "Create" || op.Resource != ip || op.ParentID != "" || op.TargetSerial != "" {
		return "", false, errConfigurationTransition
	}
	actual, err := c.authenticationPolicyServers(ctx)
	if err != nil {
		return "", false, err
	}
	server, exists := actual[ip]
	if !exists {
		return "", false, nil
	}
	if op.TargetID != "" && op.TargetID != server.ID {
		return "", false, errors.New("authentication policy server identity changed")
	}
	desired.IP = ip
	return server.ID, policyServerMatches(desired, server), nil
}
