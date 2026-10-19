// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/util/validation"
)

// Only metadata is decoded. Passwords and returned key material never enter
// the observation model, journal, drift reports or diagnostics.
type policyISEMetadata struct {
	IP       string `json:"ipAddress"`
	Username string `json:"userName"`
	FQDN     string `json:"fqdn"`
}

func parsePolicyISEMetadata(entry map[string]any) (*policyISEMetadata, *bool, *bool, error) {
	username, ok := entry["username"].(string)
	if !ok || strings.TrimSpace(username) != username || username == "" || len(username) > 256 || strings.ContainsAny(username, "\x00\r\n") {
		return nil, nil, nil, errConfigurationSecret
	}
	fqdn, ok := entry["fqdn"].(string)
	if !ok || len(validation.IsDNS1123Subdomain(fqdn)) != 0 {
		return nil, nil, nil, errConfigurationSecret
	}
	pxgrid, ok := entry["pxgrid_enabled"].(bool)
	if !ok {
		return nil, nil, nil, errConfigurationSecret
	}
	cert, ok := entry["use_catc_cert_for_pxgrid"].(bool)
	if !ok {
		return nil, nil, nil, errConfigurationSecret
	}
	for _, key := range []string{"username", "fqdn", "pxgrid_enabled", "use_catc_cert_for_pxgrid"} {
		delete(entry, key)
	}
	return &policyISEMetadata{Username: username, FQDN: fqdn}, &pxgrid, &cert, nil
}
func parsePolicyISE(entry map[string]any, base string) (*policyISEMetadata, *bool, *bool, configurationCredentialIntent, error) {
	bad := func() (*policyISEMetadata, *bool, *bool, configurationCredentialIntent, error) {
		return nil, nil, nil, configurationCredentialIntent{}, errConfigurationSecret
	}
	metadata, pxgrid, cert, err := parsePolicyISEMetadata(entry)
	if err != nil || entry["password"] != nil {
		return bad()
	}
	raw, err := json.Marshal(entry["password_version"])
	var version int64
	if err != nil || json.Unmarshal(raw, &version) != nil || version < 1 {
		return bad()
	}
	delete(entry, "password")
	delete(entry, "password_version")
	return metadata, pxgrid, cert, configurationCredentialIntent{path: base + "/password", field: "isePassword", version: version}, nil
}

func parseISEPolicyServerReport(value any) (authenticationPolicyServer, error) {
	var empty authenticationPolicyServer
	raw, ok := value.(map[string]any)
	if !ok {
		return empty, errConfigurationSecret
	}
	entry := make(map[string]any, len(raw))
	for key, value := range raw {
		entry[key] = value
	}
	metadata, pxgrid, cert, err := parsePolicyISEMetadata(entry)
	if err != nil {
		return empty, err
	}
	// The shared metadata parser rejects all credential values and versions.
	server, err := parsePolicyServerMetadata(entry)
	if err != nil {
		return empty, err
	}
	yes := true
	metadata.IP = server.IP
	server.ISE = &yes
	server.CiscoISE = []policyISEMetadata{*metadata}
	server.Pxgrid, server.UseCatcCert = pxgrid, cert
	return server, nil
}

const policyISEIntegrationPath = "/dna/intent/api/v1/ise-integration-status"
const policyISEIntegrationResource = "ise_integration/"

type policyISEIntegrationStatus struct {
	ID     string `json:"aaaServerSettingId"`
	Status string `json:"overallStatus"`
	// Do not surface upstream failure text; it can contain request material.
	Error string `json:"overallErrorMessage"`
	Steps []struct {
		ID       string `json:"stepId"`
		Status   string `json:"stepStatus"`
		Accepted *bool  `json:"certAcceptedByUser"`
	} `json:"steps"`
}

func (c *client) policyISEIntegration(ctx context.Context, id string) (policyISEIntegrationStatus, error) {
	var state policyISEIntegrationStatus
	raw, err := c.get(ctx, policyISEIntegrationPath, nil, 200)
	if err != nil {
		return state, err
	}
	if json.Unmarshal(raw, &state) != nil || state.ID != id || !validAPIID(id) || len(state.Steps) == 0 {
		return state, errInvalidResponse
	}
	if state.Error != "" || state.Status == "FAILED" {
		return state, errors.New("ISE integration failed; inspect the appliance integration status")
	}
	if state.Status != "INPROGRESS" && state.Status != "COMPLETE" {
		return state, errInvalidResponse
	}
	seen := map[string]bool{}
	for _, step := range state.Steps {
		if step.ID == "" || seen[step.ID] || step.Accepted == nil {
			return state, errInvalidResponse
		}
		seen[step.ID] = true
		switch step.Status {
		case "COMPLETE", "NOT_APPLICABLE":
		case "NOT_STARTED", "IN_PROGRESS", "WAITING_USER_INPUT":
			if state.Status == "COMPLETE" {
				return state, errInvalidResponse
			}
		case "FAILED":
			return state, errors.New("ISE integration step failed")
		default:
			return state, errInvalidResponse
		}
	}
	return state, nil
}
func (e *policyServerExecutor) nextISEIntegration(ctx context.Context, actual map[string]authenticationPolicyServer) (*configurationWrite, error) {
	for _, want := range e.desired {
		if want.server.ISE == nil || !*want.server.ISE {
			continue
		}
		got, exists := actual[want.server.IP]
		if !exists || !policyServerMatches(want.server, got) {
			return nil, errConfigurationStale
		}
		state, err := e.client.policyISEIntegration(ctx, got.ID)
		if err != nil {
			return nil, err
		}
		if state.Status == "COMPLETE" {
			continue
		}
		waiting := false
		for _, step := range state.Steps {
			waiting = waiting || (step.Status == "WAITING_USER_INPUT" && !*step.Accepted)
		}
		if !waiting {
			return nil, errors.New("ISE integration is pending before dependent configuration can proceed")
		}
		id := got.ID
		return &configurationWrite{resource: policyISEIntegrationResource + id, action: "Update", identity: configurationResourceIdentity{target: id}, submitCompleted: func(ctx context.Context) error {
			// The API returns a synchronous empty-object acknowledgement. Integration
			// completion is verified independently, correlated to the same server UUID.
			raw, err := e.client.mutateQueryStatus(ctx, http.MethodPut, "/dna/intent/api/v1/integrate-ise/"+url.PathEscape(id), nil, []byte(`{"isCertAcceptedByUser":true}`), 200)
			if err != nil {
				return err
			}
			var response map[string]json.RawMessage
			if json.Unmarshal(raw, &response) != nil || response == nil || len(response) != 0 {
				return errInvalidResponse
			}
			return nil
		}}, nil
	}
	return nil, nil
}
func (e *policyServerExecutor) verifyISEIntegration(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil || op.Section != "system_settings" || op.ConfigUID != e.owner || op.Action != "Update" || op.Resource != policyISEIntegrationResource+op.TargetID || !validAPIID(op.TargetID) || op.ParentID != "" || op.TargetSerial != "" || len(op.CredentialVersions) != 0 || op.TaskID != "" {
		return false, errConfigurationTransition
	}
	actual, err := e.client.authenticationPolicyServers(ctx)
	if err != nil {
		return false, err
	}
	for _, want := range e.desired {
		if want.server.ISE == nil || !*want.server.ISE {
			continue
		}
		got, ok := actual[want.server.IP]
		if !ok || got.ID != op.TargetID || !policyServerMatches(want.server, got) {
			return false, errConfigurationStale
		}
		state, err := e.client.policyISEIntegration(ctx, got.ID)
		return err == nil && state.Status == "COMPLETE", err
	}
	return false, errConfigurationTransition
}
