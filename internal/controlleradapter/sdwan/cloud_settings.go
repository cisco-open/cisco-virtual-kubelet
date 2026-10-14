// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"errors"
	"regexp"
)

type SDWANSettings struct {
	CloudProviderSettings *CloudProviderSettings `json:"cloud_provider_settings,omitempty"`
}
type CloudProviderSettings struct {
	UmbrellaOrgID          *string `json:"umbrella_org_id,omitempty"`
	UmbrellaAuthKeyV2      *string `json:"umbrella_auth_key_v2,omitempty"`
	UmbrellaAuthSecretV2   *string `json:"umbrella_auth_secret_v2,omitempty"`
	UmbrellaSIGAuthKey     *string `json:"umbrella_sig_auth_key,omitempty"`
	UmbrellaSIGAuthSecret  *string `json:"umbrella_sig_auth_secret,omitempty"`
	UmbrellaDNSAuthKey     *string `json:"umbrella_dns_auth_key,omitempty"`
	UmbrellaDNSAuthSecret  *string `json:"umbrella_dns_auth_secret,omitempty"`
	ZscalerOrganization    *string `json:"zscaler_organization,omitempty"`
	ZscalerPartnerBaseURI  *string `json:"zscaler_partner_base_uri,omitempty"`
	ZscalerPartnerKey      *string `json:"zscaler_partner_key,omitempty"`
	ZscalerUsername        *string `json:"zscaler_username,omitempty"`
	ZscalerPassword        *string `json:"zscaler_password,omitempty"`
	CiscoSSEOrgID          *string `json:"cisco_sse_org_id,omitempty"`
	CiscoSSEAuthKey        *string `json:"cisco_sse_auth_key,omitempty"`
	CiscoSSEAuthSecret     *string `json:"cisco_sse_auth_secret,omitempty"`
	CiscoSSEContextSharing *bool   `json:"cisco_sse_context_sharing,omitempty"`
}

const cloudSettingsPath = "/dataservice/settings/configuration/cloudProviderSetting"
const cloudSettingsID = "cloud_provider_settings"

var numericOrgID = regexp.MustCompile(`^[0-9]+$`)

func compileSDWANSettings(settings *SDWANSettings) ([]Resource, error) {
	if settings == nil || settings.CloudProviderSettings == nil {
		return nil, nil
	}
	s := settings.CloudProviderSettings
	if s.UmbrellaOrgID != nil && !numericOrgID.MatchString(*s.UmbrellaOrgID) || s.CiscoSSEOrgID != nil && !numericOrgID.MatchString(*s.CiscoSSEOrgID) {
		return nil, errors.New("cloud provider organization IDs must be numeric strings")
	}
	data := map[string]any{"ciscoSSEContextSharing": false}
	for _, item := range []struct {
		key   string
		value *string
	}{
		{"umbrellaOrgId", s.UmbrellaOrgID}, {"umbrellaAuthKeyV2", s.UmbrellaAuthKeyV2}, {"umbrellaAuthSecretV2", s.UmbrellaAuthSecretV2},
		{"umbrellaSIGAuthKey", s.UmbrellaSIGAuthKey}, {"umbrellaSIGAuthSecret", s.UmbrellaSIGAuthSecret}, {"umbrellaDNSAuthKey", s.UmbrellaDNSAuthKey}, {"umbrellaDNSAuthSecret", s.UmbrellaDNSAuthSecret},
		{"zscalerOrganization", s.ZscalerOrganization}, {"zscalerPartnerBaseUri", s.ZscalerPartnerBaseURI}, {"zscalerPartnerKey", s.ZscalerPartnerKey}, {"zscalerUsername", s.ZscalerUsername}, {"zscalerPassword", s.ZscalerPassword},
		{"ciscoSSEOrgId", s.CiscoSSEOrgID}, {"ciscoSSEAuthKey", s.CiscoSSEAuthKey}, {"ciscoSSEAuthSecret", s.CiscoSSEAuthSecret},
	} {
		if item.value != nil {
			data[item.key] = *item.value
		}
	}
	if s.CiscoSSEContextSharing != nil {
		data["ciscoSSEContextSharing"] = *s.CiscoSSEContextSharing
	}
	return []Resource{{Key: "settings/cloud_provider_settings", Name: cloudSettingsID, Section: "settings", Type: cloudSettingsID, Kind: "cloud-setting", Body: data}}, nil
}

func (c *Client) observeCloudSettings(ctx context.Context) (*Observation, error) {
	var body map[string]any
	if err := c.Request(ctx, "GET", cloudSettingsPath, nil, &body); err != nil {
		return nil, err
	}
	rows, ok := body["data"].([]any)
	if !ok {
		return nil, ErrResponse
	}
	if len(rows) == 0 {
		return nil, nil
	}
	if len(rows) != 1 {
		return nil, ErrResponse
	}
	row, ok := rows[0].(map[string]any)
	if !ok {
		return nil, ErrResponse
	}
	return &Observation{ID: cloudSettingsID, Body: row}, nil
}
func (c *Client) createCloudSettings(ctx context.Context, r Resource) (string, error) {
	if err := c.Request(ctx, "POST", cloudSettingsPath, r.Body, nil); err != nil {
		return "", err
	}
	return cloudSettingsID, nil
}
func (c *Client) updateCloudSettings(ctx context.Context, r Resource) error {
	return c.Request(ctx, "PUT", cloudSettingsPath, r.Body, nil)
}
func equalCloudSettings(r Resource, observed map[string]any) (bool, error) {
	for key, value := range r.Body {
		if observed[key] != value {
			return false, nil
		}
	}
	return true, nil
}
