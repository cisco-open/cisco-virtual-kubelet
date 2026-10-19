// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const templateComparisonScope = "/devices"
const templateComparisonTag = "private-v1"

type templateInputBinding struct{ keyID, input string }

// Keys are read through the existing approved projection, never through the
// Secret API. Only domain-scoped key identities and keyed input MACs persist.
type templateInputComparison struct {
	key   []byte
	keyID string
	check func(context.Context) error
}

func (*templateInputComparison) String() string   { return "<private template input comparison>" }
func (*templateInputComparison) GoString() string { return "<private template input comparison>" }

func templateComparisonConfig(config *configapi.NetworkControllerConfig) bool {
	return config != nil && len(config.Spec.ManagedSections) == 1 && config.Spec.ManagedSections[0] == "inventory" && len(config.Spec.SecretRefs) == 1 && config.Spec.SecretRefs[0].Use == "PrivateComparisonKey" && config.Spec.SecretRefs[0].Section == "inventory" && config.Spec.SecretRefs[0].Path == templateComparisonScope
}

func (a *adapter) compilePrivateTemplateInventoryApply(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot) (configurationApplyPlan, error) {
	var zero configurationApplyPlan
	if !templateComparisonConfig(config) || source == nil {
		return zero, errConfigurationSecret
	}
	// Consume the complete model before reading any native prerequisites. Other
	// inventory actions cannot acquire this capability by borrowing its key.
	if _, _, _, err := parseTemplateInventory(source.raw, source.raw, true); err != nil {
		return zero, err
	}
	comparison, err := a.resolveTemplateComparison(nc, config, source, "inventory", templateComparisonScope, "cvk-template-key-v1")
	if err != nil {
		return zero, err
	}
	source.templateComparison = comparison
	// The operational reference was explicitly consumed above. Existing native
	// inventory stages still receive no value-injection references; checks against
	// the live config continue to validate the original projection via source.
	working := config.DeepCopy()
	working.Spec.SecretRefs = nil
	return a.compileCompositeInventoryApply(working, source)
}

// Match provider target-change semantics: ordered targets/params/resourceParams
// and committed identities, not global copy/force options or source formatting.
func (c *templateInputComparison) bind(resource string, payload []byte) (string, error) {
	if c == nil {
		return resource, nil
	}
	if len(c.key) != 32 || !validTemplateComparisonDigest(c.keyID) {
		return "", errConfigurationSecret
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(payload, &body) != nil || body["targetInfo"] == nil {
		return "", errConfigurationTransition
	}
	decode := func(raw json.RawMessage) (any, error) {
		var value any
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.UseNumber()
		if decoder.Decode(&value) != nil {
			return nil, errConfigurationTransition
		}
		return value, nil
	}
	targets, err := decode(body["targetInfo"])
	if err != nil {
		return "", err
	}
	template, err := decode(body["templateId"])
	if err != nil {
		return "", err
	}
	input := map[string]any{"targetInfo": targets, "templateId": template}
	if raw := body["memberTemplateDeploymentInfo"]; raw != nil {
		var members []map[string]json.RawMessage
		if json.Unmarshal(raw, &members) != nil {
			return "", errConfigurationTransition
		}
		selected := []any{}
		for _, member := range members {
			row := map[string]any{}
			for _, field := range []string{"templateId", "mainTemplateId", "targetInfo"} {
				value, err := decode(member[field])
				if err != nil {
					return "", err
				}
				row[field] = value
			}
			selected = append(selected, row)
		}
		input["members"] = selected
	}
	canonical, err := json.Marshal(input)
	if err != nil {
		return "", errConfigurationTransition
	}
	mac := hmac.New(sha256.New, c.key)
	mac.Write([]byte("cvk-template-input-v1\x00"))
	mac.Write(canonical)
	separator := "/"
	if strings.HasPrefix(resource, "template-composite-deploy:") {
		separator = ":"
	}
	out := resource + separator + templateComparisonTag + separator + c.keyID + separator + hex.EncodeToString(mac.Sum(nil))
	if len(out) > 512 {
		return "", errConfigurationTransition
	}
	return out, nil
}

func validTemplateComparisonDigest(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32 && hex.EncodeToString(raw) == value
}

func splitTemplateComparisonResource(resource string) (string, *templateInputBinding, error) {
	separator := "/"
	baseCount := 4
	if strings.HasPrefix(resource, "template-composite-deploy:") {
		separator = ":"
		baseCount = 2
	} else if !strings.HasPrefix(resource, "template-deploy:") {
		return "", nil, errConfigurationTransition
	}
	parts := strings.Split(resource, separator)
	if len(resource) > 512 {
		return "", nil, errConfigurationTransition
	}
	if len(parts) == baseCount {
		return resource, nil, nil
	}
	if len(parts) != baseCount+3 || parts[baseCount] != templateComparisonTag || !validTemplateComparisonDigest(parts[baseCount+1]) || !validTemplateComparisonDigest(parts[baseCount+2]) {
		return "", nil, errConfigurationTransition
	}
	return strings.Join(parts[:baseCount], separator), &templateInputBinding{keyID: parts[baseCount+1], input: parts[baseCount+2]}, nil
}

var _ fmt.Stringer = (*templateInputComparison)(nil)

// Resolve operational comparison material through the same approved projection
// and unchanged-source checks; callers own their parser, destination and domain.
func (a *adapter) resolveTemplateComparison(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, section, scope, domain string) (*templateInputComparison, error) {
	snapshot, err := resolveConfigurationSecretsForUse(nc, config, a.intentSecretPath, []configurationSecretDestination{{section, scope}}, "PrivateComparisonKey")
	if err != nil {
		return nil, err
	}
	if len(snapshot.values) != 1 {
		return nil, errConfigurationSecret
	}
	text := strings.TrimSpace(string(snapshot.values[0].value))
	if len(text) != 64 {
		return nil, errConfigurationSecret
	}
	key, err := hex.DecodeString(text)
	if err != nil || len(key) != 32 {
		return nil, errConfigurationSecret
	}
	source.secrets = snapshot
	keyID := hmac.New(sha256.New, key)
	keyID.Write([]byte(domain + "\x00" + string(nc.UID) + "\x00" + string(config.UID) + "\x00" + nc.Spec.Endpoint + "\x00"))
	comparison := &templateInputComparison{key: key, keyID: hex.EncodeToString(keyID.Sum(nil))}
	comparison.check = func(ctx context.Context) error { return a.checkConfigurationSnapshot(ctx, config, source) }
	return comparison, nil
}
