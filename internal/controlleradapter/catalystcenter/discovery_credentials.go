// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

// Credential input/projection contract. Public creation uses the separate
// Retain-only executor and common journal; projection itself grants no permit.
// The shared Secret transport remains {version:N,value:"..."}. Set-valued
// properties carry a JSON string array in value, so version and the complete
// unordered set arrive atomically without per-position credential counters.
type discoveryCredentialIntent struct {
	base        discoveryIntent
	metadata    map[string]any
	credentials []configurationCredentialIntent
}

type discoveryCredentialField struct {
	model, api string
	set        bool
}

var discoveryCredentialFields = []discoveryCredentialField{
	{"enable_password_list", "enablePasswordList", true},
	{"password_list", "passwordList", true},
	{"http_read_credential", "httpReadCredential", false},
	{"http_write_credential", "httpWriteCredential", false},
	{"snmp_auth_passphrase", "snmpAuthPassphrase", false},
	{"snmp_priv_passphrase", "snmpPrivPassphrase", false},
	{"snmp_ro_community", "snmpRoCommunity", false},
	{"snmp_rw_community", "snmpRwCommunity", false},
}

func parseDiscoveryCredentialIntents(raw []byte) ([]discoveryCredentialIntent, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, errConfigurationSecret
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return nil, errConfigurationSecret
	}
	inventory, ok := root["inventory"].(map[string]any)
	if !ok || len(inventory) != 1 {
		return nil, errConfigurationSecret
	}
	rows, ok := inventory["discovery"].([]any)
	if !ok || len(rows) > 100 {
		return nil, errConfigurationSecret
	}
	var out []discoveryCredentialIntent
	seen := map[string]bool{}
	for index, row := range rows {
		entry, ok := row.(map[string]any)
		if !ok {
			return nil, errConfigurationSecret
		}
		common := map[string]any{}
		for key, value := range entry {
			common[key] = value
		}
		want := discoveryCredentialIntent{metadata: map[string]any{}}
		for _, field := range discoveryCredentialFields {
			// Inline material is never hashed or retained as model/status data.
			if entry[field.model] != nil {
				return nil, errConfigurationSecret
			}
			delete(common, field.model)
			versionKey := field.model + "_version"
			if value, present := entry[versionKey]; present {
				raw, err := json.Marshal(value)
				var version int64
				if err != nil || json.Unmarshal(raw, &version) != nil || version < 1 {
					return nil, errConfigurationSecret
				}
				want.credentials = append(want.credentials, configurationCredentialIntent{path: fmt.Sprintf("/discovery/%d/%s", index, field.model), field: field.api, version: version})
			}
			delete(common, versionKey)
		}
		for model, api := range map[string]string{"snmp_auth_protocol": "snmpAuthProtocol", "snmp_mode": "snmpMode", "snmp_priv_protocol": "snmpPrivProtocol", "snmp_ro_community_desc": "snmpRoCommunityDesc", "snmp_rw_community_desc": "snmpRwCommunityDesc", "snmp_user_name": "snmpUserName", "snmp_version": "snmpVersion", "user_name_list": "userNameList"} {
			value, present := entry[model]
			if !present {
				continue
			}
			delete(common, model)
			if value == nil {
				continue
			}
			if model == "user_name_list" {
				values, ok := value.([]any)
				if !ok || len(values) == 0 || len(values) > 64 {
					return nil, errConfigurationSecret
				}
				usernames := []string{}
				for _, item := range values {
					s, err := globalCredentialText(item)
					if err != nil {
						return nil, errConfigurationSecret
					}
					usernames = append(usernames, s)
				}
				sort.Strings(usernames)
				if len(slices.Compact(slices.Clone(usernames))) != len(usernames) {
					return nil, errConfigurationSecret
				}
				want.metadata[api] = usernames
				continue
			}
			text, err := globalCredentialText(value)
			if err != nil {
				return nil, errConfigurationSecret
			}
			enums := map[string][]string{"snmp_auth_protocol": {"SHA", "MD5"}, "snmp_priv_protocol": {"DES", "AES128"}, "snmp_mode": {"AuthPriv", "AuthNoPriv", "NoAuthNoPriv"}, "snmp_version": {"v2", "v3"}}
			if choices, restricted := enums[model]; restricted && !slices.Contains(choices, text) {
				return nil, errConfigurationSecret
			}
			want.metadata[api] = text
		}
		// Reuse the ordinary discovery validators without inventing a real
		// credential reference when inline-only input is being checked.
		global, hasGlobal := common["global_credential_list"]
		hasGlobal = hasGlobal && global != nil
		if !hasGlobal {
			common["global_credential_list"] = []any{"CVK_PRIVATE_VALIDATION_ONLY"}
		}
		base, err := parseDiscoveryModels([]any{common})
		if err != nil || len(base) != 1 {
			return nil, errConfigurationSecret
		}
		want.base = base[0]
		if !hasGlobal {
			want.base.credentials = nil
		}
		if seen[want.base.name] {
			return nil, errConfigurationSecret
		}
		seen[want.base.name] = true
		if err := validateDiscoveryCredentialGroups(want); err != nil {
			return nil, err
		}
		out = append(out, want)
	}
	slices.SortFunc(out, func(a, b discoveryCredentialIntent) int { return strings.Compare(a.base.name, b.base.name) })
	return out, nil
}

func validateDiscoveryCredentialGroups(w discoveryCredentialIntent) error {
	selected := map[string]bool{}
	for _, leaf := range w.credentials {
		selected[leaf.field] = true
	}
	version, _ := w.metadata["snmpVersion"].(string)
	mode, _ := w.metadata["snmpMode"].(string)
	auth := w.metadata["snmpAuthProtocol"] != nil || selected["snmpAuthPassphrase"]
	privacy := w.metadata["snmpPrivProtocol"] != nil || selected["snmpPrivPassphrase"]
	community := selected["snmpRoCommunity"] || selected["snmpRwCommunity"]
	if w.metadata["snmpRoCommunityDesc"] != nil && !selected["snmpRoCommunity"] || w.metadata["snmpRwCommunityDesc"] != nil && !selected["snmpRwCommunity"] {
		return errConfigurationSecret
	}
	if community || auth || privacy || mode != "" || w.metadata["snmpUserName"] != nil || version != "" {
		switch version {
		case "v2":
			if !community || auth || privacy || mode != "" || w.metadata["snmpUserName"] != nil {
				return errConfigurationSecret
			}
		case "v3":
			if community || w.metadata["snmpUserName"] == nil {
				return errConfigurationSecret
			}
			switch mode {
			case "AuthPriv":
				if w.metadata["snmpAuthProtocol"] == nil || !selected["snmpAuthPassphrase"] || w.metadata["snmpPrivProtocol"] == nil || !selected["snmpPrivPassphrase"] {
					return errConfigurationSecret
				}
			case "AuthNoPriv":
				if w.metadata["snmpAuthProtocol"] == nil || !selected["snmpAuthPassphrase"] || privacy {
					return errConfigurationSecret
				}
			case "NoAuthNoPriv":
				if auth || privacy {
					return errConfigurationSecret
				}
			default:
				return errConfigurationSecret
			}
		default:
			return errConfigurationSecret
		}
	}
	if len(w.base.credentials) == 0 && len(w.credentials) == 0 && !(version == "v3" && mode == "NoAuthNoPriv") {
		return errConfigurationSecret
	}
	return nil
}

type discoveryCredentialProjection struct {
	intents                 []discoveryCredentialIntent
	secrets                 *configurationSecretSnapshot
	specDigest, modelDigest string
	subjectDigest           string
	// Only a recorded versioned creator selects this route. Full lineage,
	// custody and current-source authorization remain executor checks.
	metadataLifecycle bool
}

func (p *discoveryCredentialProjection) tracks(w discoveryCredentialIntent) bool {
	return len(w.credentials) > 0 || p.metadataLifecycle
}

// Global names stay explicit and unresolved; they must pass through the existing
// native credential resolver before a caller can construct a complete request.
type discoveryCredentialRequest struct {
	fields      map[string]any
	public      map[string]any
	globalNames []string
	versions    []cvk.NetworkControllerConfigurationCredentialVersion
}

func (*discoveryCredentialRequest) String() string { return "<redacted discovery credential request>" }
func (*discoveryCredentialRequest) GoString() string {
	return "<redacted discovery credential request>"
}
func (*discoveryCredentialRequest) MarshalJSON() ([]byte, error) {
	return []byte(`"<redacted discovery credential request>"`), nil
}

func (*discoveryCredentialProjection) String() string {
	return "<redacted discovery credential projection>"
}
func (*discoveryCredentialProjection) GoString() string {
	return "<redacted discovery credential projection>"
}
func (*discoveryCredentialProjection) MarshalJSON() ([]byte, error) {
	return []byte(`"<redacted discovery credential projection>"`), nil
}

// No Secret API calls or product I/O. Resolve only the exact destinations from
// the fully validated product model through the existing authorized projection.
func (a *adapter) prepareDiscoveryCredentialProjection(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, source *configSourceSnapshot) (*discoveryCredentialProjection, error) {
	if a == nil || nc == nil || source == nil || c == nil || c.Spec.Mode != configapi.NetworkControllerApplyModeApply || nc.Spec.Paused || nc.DeletionTimestamp != nil || c.DeletionTimestamp != nil || len(c.Spec.ManagedSections) != 1 || c.Spec.ManagedSections[0] != "inventory" || controlleradapter.ValidateConfigContract(nc, c, a.intentSecretPath) != nil {
		return nil, errConfigurationSecret
	}
	if nc.Namespace != a.key.Namespace || nc.Name != a.key.Name || nc.UID != a.uid || nc.Generation != a.generation {
		return nil, errConfigurationSecret
	}
	intents, err := parseDiscoveryCredentialIntents(source.raw)
	if err != nil {
		return nil, err
	}
	if c.Spec.Source.Inline != nil && (!bytes.Equal(c.Spec.Source.Inline.Raw, source.raw) || source.uid != "" || source.version != "" || source.key.Name != "" || source.key.Namespace != "") {
		return nil, errConfigurationSecret
	}
	if ref := c.Spec.Source.ConfigMapRef; ref != nil && (source.key.Namespace != c.Namespace || source.key.Name != ref.Name || source.uid == "" || source.version == "") {
		return nil, errConfigurationSecret
	}
	destinations := []configurationSecretDestination{}
	for _, want := range intents {
		for _, leaf := range want.credentials {
			destinations = append(destinations, configurationSecretDestination{"inventory", leaf.path})
		}
	}
	if len(destinations) != len(c.Spec.SecretRefs) {
		return nil, errConfigurationSecret
	}
	var snapshot *configurationSecretSnapshot
	if len(destinations) > 0 {
		snapshot, err = resolveConfigurationSecrets(nc, c, a.intentSecretPath, destinations)
		if err != nil {
			return nil, err
		}
	}
	// Existing ordinary rows keep their family even after a typed versioned
	// creator appears in the same owner. Metadata support is not migration.
	projection := &discoveryCredentialProjection{intents: intents, secrets: snapshot, specDigest: featureIdentityDigest(c.Spec), modelDigest: featureIdentityDigest(source.raw), subjectDigest: discoveryCredentialSubject(nc, c, source), metadataLifecycle: discoveryMetadataLifecycleShape(c) && !discoveryOrdinaryHistoryOwner(nc, c) && (discoveryCredentialVersionedCreatorRecorded(nc, c) || discoveryCredentialMetadataCreatorRecorded(nc, c) || discoveryNoAuthInitialIntents(nc, c, intents))}
	// Validate versioned scalar/set values before exposing any payload builder.
	for i := range intents {
		if _, err := projection.requestFields(nc, c, source, i); err != nil {
			return nil, err
		}
	}
	source.secrets = snapshot
	return projection, nil
}

func (p *discoveryCredentialProjection) requestFields(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, source *configSourceSnapshot, index int) (*discoveryCredentialRequest, error) {
	if p == nil || nc == nil || c == nil || source == nil || nc.Spec.Paused || nc.DeletionTimestamp != nil || c.DeletionTimestamp != nil || index < 0 || index >= len(p.intents) || featureIdentityDigest(c.Spec) != p.specDigest || featureIdentityDigest(source.raw) != p.modelDigest || discoveryCredentialSubject(nc, c, source) != p.subjectDigest {
		return nil, errConfigurationSecret
	}
	if p.secrets != nil {
		if err := p.secrets.unchanged(nc, c); err != nil {
			return nil, err
		}
	}
	w := p.intents[index]
	resolved := map[string]string{}
	var versions []cvk.NetworkControllerConfigurationCredentialVersion
	var err error
	if len(w.credentials) > 0 {
		resolved, versions, err = resolveVersionedConfigurationCredentials(p.secrets, string(c.UID), "inventory", w.credentials)
		if err != nil {
			return nil, err
		}
	}
	// Clone request data: caller changes cannot rewrite the next frozen payload.
	raw, _ := json.Marshal(w.base.fields)
	body := map[string]any{}
	if json.Unmarshal(raw, &body) != nil {
		return nil, errConfigurationSecret
	}
	for key, value := range w.metadata {
		if list, ok := value.([]string); ok {
			body[key] = slices.Clone(list)
		} else {
			body[key] = value
		}
	}
	for _, field := range discoveryCredentialFields {
		value, present := resolved[field.api]
		if !present {
			continue
		}
		if !field.set {
			body[field.api] = value
			continue
		}
		decoder := json.NewDecoder(bytes.NewBufferString(value))
		var values []string
		if decoder.Decode(&values) != nil || len(values) == 0 || len(values) > 64 {
			return nil, errConfigurationSecret
		}
		if _, err := decoder.Token(); err != io.EOF {
			return nil, errConfigurationSecret
		}
		for _, item := range values {
			if len(item) == 0 || strings.ContainsRune(item, 0) {
				return nil, errConfigurationSecret
			}
		}
		sort.Strings(values)
		if len(slices.Compact(slices.Clone(values))) != len(values) {
			return nil, errConfigurationSecret
		}
		body[field.api] = values
	}
	return &discoveryCredentialRequest{fields: body, globalNames: slices.Clone(w.base.credentials), versions: versions}, nil
}

func discoveryCredentialSubject(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, source *configSourceSnapshot) string {
	return featureIdentityDigest([]any{nc.UID, nc.Generation, nc.Name, nc.Namespace, c.UID, c.Generation, c.ResourceVersion, c.Name, c.Namespace, source.uid, source.version, source.key})
}

var _ fmt.Stringer = (*discoveryCredentialProjection)(nil)
