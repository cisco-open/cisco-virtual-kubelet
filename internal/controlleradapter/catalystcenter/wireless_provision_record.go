// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

const wirelessProvisionRecordPrefix = "wireless-provision:v1:"

// Compact non-secret execution metadata fits the existing bounded resource
// journal field. No API payload, credential, or template content is retained.
type wirelessProvisionRecord struct {
	DependencyDigest string `json:"b,omitempty"`
	NameKey          string `json:"n"`
	DeviceID         string `json:"d"`
	Serial           string `json:"s"`
	Address          string `json:"a"`
	SiteID           string `json:"p"`
	Timestamp        string `json:"t"`
	ModelRequired    bool   `json:"m"`
	Action           string `json:"o"`
}

func (r wirelessProvisionRecord) resource() (string, error) {
	key, err := hex.DecodeString(r.NameKey)
	if err != nil || len(key) != 32 || hex.EncodeToString(key) != r.NameKey || !validAPIID(r.DeviceID) || !validAPIID(r.SiteID) || r.Serial == "" || len(r.Serial) > 128 || strings.TrimSpace(r.Serial) != r.Serial || strings.ContainsAny(r.Serial, "\x00\r\n") || r.Action != "Create" && r.Action != "Update" {
		return "", errConfigurationTransition
	}
	if r.DependencyDigest != "" {
		digest, err := hex.DecodeString(r.DependencyDigest)
		if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != r.DependencyDigest {
			return "", errConfigurationTransition
		}
	}
	ip, err := netip.ParseAddr(r.Address)
	if err != nil || ip.Zone() != "" {
		return "", errConfigurationTransition
	}
	if r.Timestamp != "" {
		n, err := strconv.ParseUint(r.Timestamp, 10, 64)
		if err != nil || n == 0 || strconv.FormatUint(n, 10) != r.Timestamp {
			return "", errConfigurationTransition
		}
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	resource := wirelessProvisionRecordPrefix + base64.RawURLEncoding.EncodeToString(raw)
	// Reserve space for an enclosing executor namespace in the same journal.
	if len(resource) > 480 {
		return "", errConfigurationTransition
	}
	return resource, nil
}
func wirelessProvisionRecordFor(name, action string, b wirelessProvisionBaseline) (wirelessProvisionRecord, error) {
	if name == "" || len(name) > 256 || strings.TrimSpace(name) != name || strings.ContainsAny(name, "\x00\r\n") {
		return wirelessProvisionRecord{}, errConfigurationTransition
	}
	key, err := wirelessTagDigest(name)
	if err != nil {
		return wirelessProvisionRecord{}, err
	}
	r := wirelessProvisionRecord{NameKey: key, DeviceID: b.device.ID, Serial: b.device.Serial, Address: b.device.ManagementIP, SiteID: b.siteID, Timestamp: b.lastProvisioned, ModelRequired: b.modelConfigRequired, Action: action}
	_, err = r.resource()
	return r, err
}
func decodeWirelessProvisionRecord(resource string) (wirelessProvisionRecord, error) {
	var b wirelessProvisionRecord
	if !strings.HasPrefix(resource, wirelessProvisionRecordPrefix) || len(resource) > 480 {
		return b, errConfigurationTransition
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(resource, wirelessProvisionRecordPrefix))
	if err != nil {
		return b, errConfigurationTransition
	}
	var r wirelessProvisionRecord
	if json.Unmarshal(raw, &r) != nil {
		return b, errConfigurationTransition
	}
	canonical, err := r.resource()
	// Canonical reencoding rejects unknown fields, duplicate keys, trailing data,
	// omitted fields and alternative encodings before using persisted evidence.
	if err != nil || canonical != resource {
		return b, errConfigurationTransition
	}
	return r, nil
}
func restoreWirelessProvisionBaseline(name string, op *cvk.NetworkControllerConfigurationOperation) (wirelessProvisionBaseline, error) {
	var b wirelessProvisionBaseline
	if op == nil {
		return b, errConfigurationTransition
	}
	r, err := decodeWirelessProvisionRecord(op.Resource)
	if err != nil {
		return b, err
	}
	key, err := wirelessTagDigest(name)
	if err != nil || key != r.NameKey || op.TargetID != r.DeviceID || op.TargetSerial != r.Serial || op.Action != r.Action || op.ParentID != "" || len(op.CredentialVersions) > 0 {
		return b, errConfigurationStale
	}
	return wirelessProvisionBaseline{device: Device{ID: r.DeviceID, Serial: r.Serial, ManagementIP: r.Address}, resource: op.Resource, siteID: r.SiteID, lastProvisioned: r.Timestamp, modelConfigRequired: r.ModelRequired}, nil
}

func (c *client) verifyRecordedWirelessProvision(ctx context.Context, name string, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
	baseline, err := restoreWirelessProvisionBaseline(name, op)
	if err != nil {
		return false, err
	}
	return c.verifyWirelessProvision(ctx, op, baseline, now)
}

// Preserve the native baseline and attach a non-secret dependency identity to
// the same durable resource, without changing the submission callback.
func bindWirelessProvisionDependencies(write *configurationWrite, digest string) (*configurationWrite, error) {
	if write == nil {
		return nil, errConfigurationTransition
	}
	record, err := decodeWirelessProvisionRecord(write.resource)
	if err != nil {
		return nil, err
	}
	if record.DependencyDigest != "" && record.DependencyDigest != digest {
		return nil, errConfigurationStale
	}
	record.DependencyDigest = digest
	resource, err := record.resource()
	if err != nil {
		return nil, err
	}
	bound := *write
	bound.resource = resource
	return &bound, nil
}
