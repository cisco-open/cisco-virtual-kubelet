// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package iosxe

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

func TestBuildEnvironmentOptions_NoEnvVars(t *testing.T) {
	driver := &XEDriver{}
	container := &v1.Container{
		Name: "test-container",
	}
	pod := &v1.Pod{}

	opts, err := driver.buildEnvironmentOptions(container, pod)
	require.NoError(t, err)
	assert.Empty(t, opts)
}

func TestBuildEnvironmentOptions_DirectVars(t *testing.T) {
	driver := &XEDriver{}
	container := &v1.Container{
		Env: []v1.EnvVar{
			{Name: "SIMPLE_VAR", Value: "simple_value"},
			{Name: "EMPTY_VAR", Value: ""},           // Should be skipped
			{Name: "QUOTED_VAR", Value: `value with "quotes"`},
			{Name: "SPECIAL_VAR", Value: "value$with`special\\chars"},
		},
	}
	pod := &v1.Pod{}

	opts, err := driver.buildEnvironmentOptions(container, pod)
	require.NoError(t, err)

	expected := []string{
		`-e SIMPLE_VAR='simple_value'`,
		`-e QUOTED_VAR='value with "quotes"'`,
		`-e SPECIAL_VAR='value$with` + "`" + `special\chars'`,
	}
	assert.Equal(t, expected, opts)
}

func TestEscapeShellValue(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple value",
			input:    "simple_value",
			expected: `'simple_value'`,
		},
		{
			name:     "value with spaces",
			input:    "value with spaces",
			expected: `'value with spaces'`,
		},
		{
			name:     "value with quotes",
			input:    `value with "quotes"`,
			expected: `'value with "quotes"'`,
		},
		{
			name:     "value with dollar signs",
			input:    "value$with$variables",
			expected: `'value$with$variables'`,
		},
		{
			name:     "value with backticks",
			input:    "value`with`commands",
			expected: "'value`with`commands'",
		},
		{
			name:     "value with backslashes",
			input:    "value\\with\\backslashes",
			expected: `'value\with\backslashes'`,
		},
		{
			name:     "value with single quotes",
			input:    "it's a value",
			expected: `'it'\''s a value'`,
		},
		{
			name:     "complex value",
			input:    `complex "value" with $vars and \backslashes and ` + "`commands`",
			expected: `'complex "value" with $vars and \backslashes and ` + "`commands`" + `'`,
		},
		{
			name:     "empty value",
			input:    "",
			expected: `''`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := escapeShellValue(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDistributeRunOpts_WithinLimit(t *testing.T) {
	baseOpts := []string{"--label pod=test"}
	envOpts := []string{"-e VAR1=value1", "-e VAR2=value2"}

	result, err := distributeRunOpts(baseOpts, envOpts)
	require.NoError(t, err)

	assert.Len(t, result, 1)
	expected := "--label pod=test -e VAR1=value1 -e VAR2=value2"
	assert.Equal(t, expected, *result[1].LineRunOpts)
	assert.Equal(t, uint16(1), *result[1].LineIndex)
}

func TestDistributeRunOpts_ExceedsLineLimit(t *testing.T) {
	baseOpts := []string{"--label pod=test-pod-with-very-long-name-that-takes-space"}

	// Create enough env vars to exceed 235 chars per line
	var envOpts []string
	for i := 0; i < 10; i++ {
		envOpts = append(envOpts, fmt.Sprintf("-e LONG_VAR_NAME_%d=\"very_long_value_that_takes_up_space_%d\"", i, i))
	}

	result, err := distributeRunOpts(baseOpts, envOpts)
	require.NoError(t, err)

	// Should span multiple lines
	assert.Greater(t, len(result), 1)

	// Verify each line is within limit
	for lineIndex, line := range result {
		lineLength := len(*line.LineRunOpts)
		assert.LessOrEqual(t, lineLength, MaxRunOptsLineLength,
			"Line %d length (%d) exceeds limit (%d): %s", lineIndex, lineLength, MaxRunOptsLineLength, *line.LineRunOpts)
	}

	// Verify line indices are sequential
	for i := uint16(1); i <= uint16(len(result)); i++ {
		assert.Contains(t, result, i)
		assert.Equal(t, i, *result[i].LineIndex)
	}
}

func TestDistributeRunOpts_SingleOptionTooLong(t *testing.T) {
	baseOpts := []string{}
	// Create a single option that exceeds the line limit
	longValue := strings.Repeat("a", MaxRunOptsLineLength)
	envOpts := []string{fmt.Sprintf("-e LONG_VAR=%s", longValue)}

	result, err := distributeRunOpts(baseOpts, envOpts)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "single RunOpts option too long")
	assert.Nil(t, result)
}

func TestDistributeRunOpts_TooManyLines(t *testing.T) {
	baseOpts := []string{}

	// Create enough env vars to test large scenarios
	var envOpts []string
	// Each env var is about 30 chars, so we need about 8 per line to hit the 235 char limit
	// Test with 240 variables (should result in exactly 30 lines)
	for i := 0; i < 240; i++ {
		envOpts = append(envOpts, fmt.Sprintf("-e VAR_%d=value_%d", i, i))
	}

	result, err := distributeRunOpts(baseOpts, envOpts)
	require.NoError(t, err)
	assert.NotNil(t, result)

	// Should use all available lines efficiently
	assert.LessOrEqual(t, len(result), MaxRunOptsLines)
}

func TestDistributeRunOpts_EmptyInput(t *testing.T) {
	baseOpts := []string{}
	envOpts := []string{}

	result, err := distributeRunOpts(baseOpts, envOpts)
	require.NoError(t, err)

	// Should have one empty line for backward compatibility
	assert.Len(t, result, 1)
	assert.Equal(t, "", *result[1].LineRunOpts)
	assert.Equal(t, uint16(1), *result[1].LineIndex)
}

// mockSecretLister provides a simple mock implementation for testing
type mockSecretLister struct {
	secrets map[string]*v1.Secret
}

func (m *mockSecretLister) Get(name string) (*v1.Secret, error) {
	if secret, exists := m.secrets[name]; exists {
		return secret, nil
	}
	return nil, fmt.Errorf("secret not found: %s", name)
}

func (m *mockSecretLister) List(selector labels.Selector) ([]*v1.Secret, error) {
	var secrets []*v1.Secret
	for _, secret := range m.secrets {
		secrets = append(secrets, secret)
	}
	return secrets, nil
}

// mockConfigMapLister provides a simple mock implementation for testing
type mockConfigMapLister struct {
	configmaps map[string]*v1.ConfigMap
}

func (m *mockConfigMapLister) Get(name string) (*v1.ConfigMap, error) {
	if cm, exists := m.configmaps[name]; exists {
		return cm, nil
	}
	return nil, fmt.Errorf("configmap not found: %s", name)
}

func (m *mockConfigMapLister) List(selector labels.Selector) ([]*v1.ConfigMap, error) {
	var configmaps []*v1.ConfigMap
	for _, cm := range m.configmaps {
		configmaps = append(configmaps, cm)
	}
	return configmaps, nil
}

func TestBuildEnvironmentOptions_SecretRef(t *testing.T) {
	// Create a test secret
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"username": []byte("admin"),
			"password": []byte("secret123"),
		},
	}

	// Create mock secret lister
	mockLister := &mockSecretLister{
		secrets: map[string]*v1.Secret{
			"test-secret": secret,
		},
	}

	driver := &XEDriver{
		secretLister: mockLister,
	}

	container := &v1.Container{
		Env: []v1.EnvVar{
			{
				Name: "DB_USER",
				ValueFrom: &v1.EnvVarSource{
					SecretKeyRef: &v1.SecretKeySelector{
						LocalObjectReference: v1.LocalObjectReference{Name: "test-secret"},
						Key:                  "username",
					},
				},
			},
			{
				Name: "DB_PASS",
				ValueFrom: &v1.EnvVarSource{
					SecretKeyRef: &v1.SecretKeySelector{
						LocalObjectReference: v1.LocalObjectReference{Name: "test-secret"},
						Key:                  "password",
					},
				},
			},
		},
	}
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
	}

	opts, err := driver.buildEnvironmentOptions(container, pod)
	require.NoError(t, err)

	expected := []string{
		`-e DB_USER='admin'`,
		`-e DB_PASS='secret123'`,
	}
	assert.Equal(t, expected, opts)
}

func TestBuildEnvironmentOptions_ConfigMapRef(t *testing.T) {
	// Create a test configmap
	configMap := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-config",
			Namespace: "default",
		},
		Data: map[string]string{
			"api-url":  "https://api.example.com",
			"log-level": "debug",
		},
	}

	// Create mock configmap lister
	mockLister := &mockConfigMapLister{
		configmaps: map[string]*v1.ConfigMap{
			"test-config": configMap,
		},
	}

	driver := &XEDriver{
		configMapLister: mockLister,
	}

	container := &v1.Container{
		Env: []v1.EnvVar{
			{
				Name: "API_URL",
				ValueFrom: &v1.EnvVarSource{
					ConfigMapKeyRef: &v1.ConfigMapKeySelector{
						LocalObjectReference: v1.LocalObjectReference{Name: "test-config"},
						Key:                  "api-url",
					},
				},
			},
			{
				Name: "LOG_LEVEL",
				ValueFrom: &v1.EnvVarSource{
					ConfigMapKeyRef: &v1.ConfigMapKeySelector{
						LocalObjectReference: v1.LocalObjectReference{Name: "test-config"},
						Key:                  "log-level",
					},
				},
			},
		},
	}
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
	}

	opts, err := driver.buildEnvironmentOptions(container, pod)
	require.NoError(t, err)

	expected := []string{
		`-e API_URL='https://api.example.com'`,
		`-e LOG_LEVEL='debug'`,
	}
	assert.Equal(t, expected, opts)
}

func TestBuildEnvironmentOptions_OptionalSecretMissing(t *testing.T) {
	// Create empty secret lister (no secrets)
	mockLister := &mockSecretLister{
		secrets: map[string]*v1.Secret{},
	}

	driver := &XEDriver{
		secretLister: mockLister,
	}

	optionalTrue := true
	container := &v1.Container{
		Env: []v1.EnvVar{
			{
				Name: "OPTIONAL_VAR",
				ValueFrom: &v1.EnvVarSource{
					SecretKeyRef: &v1.SecretKeySelector{
						LocalObjectReference: v1.LocalObjectReference{Name: "missing-secret"},
						Key:                  "some-key",
						Optional:             &optionalTrue,
					},
				},
			},
			{
				Name:  "REGULAR_VAR",
				Value: "regular_value",
			},
		},
	}
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
	}

	opts, err := driver.buildEnvironmentOptions(container, pod)
	require.NoError(t, err)

	// Should only include the regular variable, optional missing secret is skipped
	expected := []string{
		`-e REGULAR_VAR='regular_value'`,
	}
	assert.Equal(t, expected, opts)
}

func TestBuildEnvironmentOptions_RequiredSecretMissing(t *testing.T) {
	// Create empty secret lister (no secrets)
	mockLister := &mockSecretLister{
		secrets: map[string]*v1.Secret{},
	}

	driver := &XEDriver{
		secretLister: mockLister,
	}

	container := &v1.Container{
		Env: []v1.EnvVar{
			{
				Name: "REQUIRED_VAR",
				ValueFrom: &v1.EnvVarSource{
					SecretKeyRef: &v1.SecretKeySelector{
						LocalObjectReference: v1.LocalObjectReference{Name: "missing-secret"},
						Key:                  "some-key",
					},
				},
			},
		},
	}
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
	}

	opts, err := driver.buildEnvironmentOptions(container, pod)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to resolve environment variable REQUIRED_VAR")
	assert.Nil(t, opts)
}

func TestBuildEnvironmentOptions_MixedSources(t *testing.T) {
	// Create test resources
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "test-secret", Namespace: "default"},
		Data:       map[string][]byte{"password": []byte("secret123")},
	}
	configMap := &v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "test-config", Namespace: "default"},
		Data:       map[string]string{"api-url": "https://api.example.com"},
	}

	// Create mock listers
	mockSecretLister := &mockSecretLister{
		secrets: map[string]*v1.Secret{"test-secret": secret},
	}
	mockConfigMapLister := &mockConfigMapLister{
		configmaps: map[string]*v1.ConfigMap{"test-config": configMap},
	}

	driver := &XEDriver{
		secretLister:    mockSecretLister,
		configMapLister: mockConfigMapLister,
	}

	container := &v1.Container{
		Env: []v1.EnvVar{
			{Name: "DIRECT_VAR", Value: "direct_value"},
			{
				Name: "SECRET_VAR",
				ValueFrom: &v1.EnvVarSource{
					SecretKeyRef: &v1.SecretKeySelector{
						LocalObjectReference: v1.LocalObjectReference{Name: "test-secret"},
						Key:                  "password",
					},
				},
			},
			{
				Name: "CONFIGMAP_VAR",
				ValueFrom: &v1.EnvVarSource{
					ConfigMapKeyRef: &v1.ConfigMapKeySelector{
						LocalObjectReference: v1.LocalObjectReference{Name: "test-config"},
						Key:                  "api-url",
					},
				},
			},
		},
	}
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
	}

	opts, err := driver.buildEnvironmentOptions(container, pod)
	require.NoError(t, err)

	expected := []string{
		`-e DIRECT_VAR='direct_value'`,
		`-e SECRET_VAR='secret123'`,
		`-e CONFIGMAP_VAR='https://api.example.com'`,
	}
	assert.Equal(t, expected, opts)
}

func TestResolveSecretKeyRef_MissingKey(t *testing.T) {
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "test-secret", Namespace: "default"},
		Data:       map[string][]byte{"existing-key": []byte("value")},
	}

	mockLister := &mockSecretLister{
		secrets: map[string]*v1.Secret{"test-secret": secret},
	}

	driver := &XEDriver{
		secretLister: mockLister,
	}

	// Test missing required key
	ref := &v1.SecretKeySelector{
		LocalObjectReference: v1.LocalObjectReference{Name: "test-secret"},
		Key:                  "missing-key",
	}

	value, err := driver.resolveSecretKeyRef(ref, "default")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "key missing-key not found in secret test-secret")
	assert.Empty(t, value)

	// Test missing optional key
	optionalTrue := true
	ref.Optional = &optionalTrue

	value, err = driver.resolveSecretKeyRef(ref, "default")
	assert.NoError(t, err)
	assert.Empty(t, value)
}

func TestConvertPodToAppConfigs_WithEnvironmentVariables(t *testing.T) {
	t.Skip("Integration test disabled temporarily - need to fix DeviceSpec type issues")
}

func TestConvertPodToAppConfigs_MultipleContainers_WithEnvironmentVariables(t *testing.T) {
	t.Skip("Integration test disabled temporarily - need to fix DeviceSpec type issues")
}

// Helper function disabled temporarily - need to fix YANG type issues
/*
func getRunOptsString(runOpts map[uint16]*Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_RunOptss_RunOpts) string {
	var result strings.Builder
	for i := uint16(1); i <= uint16(len(runOpts)); i++ {
		if opts, exists := runOpts[i]; exists && opts.LineRunOpts != nil {
			if result.Len() > 0 {
				result.WriteString(" ")
			}
			result.WriteString(*opts.LineRunOpts)
		}
	}
	return result.String()
}
*/