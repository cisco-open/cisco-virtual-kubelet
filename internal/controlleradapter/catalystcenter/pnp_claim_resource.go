// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Persist only a digest of nonsecret resolved identities/version metadata.
// Template content, comments, parameters and credentials must not enter this
// key. Source UID/version and configuration generation bind those separately.
func pnpClaimResource(want pnpDeviceIntent, dependencies *pnpClaimDependencies) (string, error) {
	serials, err := validatePnPDeviceIntent(want)
	if err != nil {
		return "", err
	}
	if dependencies == nil || dependencies.site.ID != dependencies.refs.siteID || dependencies.site.NameHierarchy != want.Site {
		return "", errConfigurationStale
	}
	if _, err := pnpClaimPayload(want, dependencies.refs); err != nil {
		return "", err
	}
	if want.ImageSkip != nil && !*want.ImageSkip && dependencies.refs.imageID == "" {
		return "", errConfigurationBusy
	}
	sort.Strings(serials)
	identity := struct {
		Contract                                         string
		DeviceID, SiteID, ImageID, ImageVersion          string
		TemplateID, TemplateProjectID, TemplateVersionID string
		TemplateVersion                                  int64
		RFProfileID                                      string
		Serials                                          []string
	}{Contract: "pnp-site-claim-v1", DeviceID: dependencies.refs.deviceID, SiteID: dependencies.refs.siteID, ImageID: dependencies.refs.imageID, TemplateID: dependencies.refs.templateID, Serials: serials}
	if identity.ImageID != "" {
		if dependencies.image == nil || dependencies.image.ID != identity.ImageID {
			return "", errConfigurationStale
		}
		identity.ImageVersion = dependencies.image.Version
		if strings.TrimSpace(identity.ImageVersion) != identity.ImageVersion || len(identity.ImageVersion) > 128 || strings.ContainsAny(identity.ImageVersion, "\x00\r\n") {
			return "", errInvalidResponse
		}
		if want.ImageSkip != nil && !*want.ImageSkip && identity.ImageVersion == "" {
			return "", errConfigurationBusy
		}
	} else if dependencies.image != nil {
		return "", errConfigurationStale
	}
	if identity.TemplateID != "" {
		content := dependencies.template
		if content == nil || content.base.ID != identity.TemplateID || content.base.ProjectID != content.project.ID || !validAPIID(content.project.ID) || !validAPIID(content.version.ID) || content.version.ID == content.base.ID || content.version.Number < 1 {
			return "", errConfigurationStale
		}
		identity.TemplateProjectID = content.project.ID
		identity.TemplateVersionID = content.version.ID
		identity.TemplateVersion = content.version.Number
	} else if dependencies.template != nil {
		return "", errConfigurationStale
	}
	if want.RFProfile != "" {
		if dependencies.rf == nil || !validAPIID(dependencies.rf.id) {
			return "", errConfigurationStale
		}
		identity.RFProfileID = dependencies.rf.id
	} else if dependencies.rf != nil {
		return "", errConfigurationStale
	}
	body, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pnp-claim:%x", sha256.Sum256(body)), nil
}

// The pre-claim target is a safe projection only. Native credentials, CLI
// output and configuration are discarded by pnpDevices before reaching here.
// Its hash freezes workflow/state and all known physical members across a
// Prepared restart without storing the native response in the journal.
func pnpClaimPreparedResource(want pnpDeviceIntent, dependencies *pnpClaimDependencies, target *pnpClaimTarget) (string, error) {
	dependency, err := pnpClaimResource(want, dependencies)
	if err != nil {
		return "", err
	}
	serials, err := validatePnPDeviceIntent(want)
	if err != nil || target == nil || target.device.Info == nil || target.device.ID != dependencies.refs.deviceID {
		return "", errConfigurationStale
	}
	sort.Strings(serials)
	if len(serials) != len(target.serials) {
		return "", errConfigurationStale
	}
	for i := range serials {
		if serials[i] != target.serials[i] {
			return "", errConfigurationStale
		}
	}
	body, err := json.Marshal(struct {
		Device  pnpDevice
		Serials []string
	}{target.device, target.serials})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pnp-claim:v2:%s:%x", strings.TrimPrefix(dependency, "pnp-claim:"), sha256.Sum256(body)), nil
}

// Post-acknowledgement observation checks the dependency portion only: native
// workflows are expected to change after a claim. This is not completion proof.
// Exact legacy acknowledged keys retain their prior observation semantics;
// new or resumed dispatch always requires the complete v2 baseline.
func pnpClaimResourceMatchesDependencies(resource, dependency string) bool {
	if resource == dependency {
		return true
	}
	suffix, ok := strings.CutPrefix(resource, "pnp-claim:v2:")
	parts := strings.Split(suffix, ":")
	if !ok || len(parts) != 2 || "pnp-claim:"+parts[0] != dependency {
		return false
	}
	for _, part := range parts {
		if len(part) != 64 || strings.ToLower(part) != part {
			return false
		}
		if _, err := hex.DecodeString(part); err != nil {
			return false
		}
	}
	return true
}
