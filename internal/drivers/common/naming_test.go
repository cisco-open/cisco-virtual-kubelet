package common

import (
	"testing"
)

func TestK8sToAppHostingName(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		podName   string
		want      string
	}{
		{
			name:      "simple name with hyphen in default namespace",
			namespace: "default",
			podName:   "nginx-on-switch",
			want:      "nginx_on_switch",
		},
		{
			name:      "simple name with hyphen in empty namespace",
			namespace: "",
			podName:   "nginx-on-switch",
			want:      "nginx_on_switch",
		},
		{
			name:      "name without hyphen",
			namespace: "default",
			podName:   "nginx",
			want:      "nginx",
		},
		{
			name:      "name with multiple hyphens",
			namespace: "default",
			podName:   "my-cool-app-v2",
			want:      "my_cool_app_v2",
		},
		{
			name:      "non-default namespace adds hash suffix",
			namespace: "production",
			podName:   "nginx",
			want:      "nginx_ab8e18", // hash of "production"
		},
		{
			name:      "non-default namespace with hyphen in pod name",
			namespace: "prod",
			podName:   "nginx-app",
			want:      "nginx_app_6754af", // hash of "prod"
		},
		{
			name:      "long name gets truncated in default namespace",
			namespace: "default",
			podName:   "this-is-a-very-long-pod-name-that-exceeds-forty-characters",
			want:      "this_is_a_very_long_pod_name_that_exceed", // truncated to 40
		},
		{
			name:      "long name with non-default namespace",
			namespace: "production",
			podName:   "this-is-a-very-long-pod-name-that-exceeds-forty",
			want:      "this_is_a_very_long_pod_name_that_ab8e18", // truncated + hash
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := K8sToAppHostingName(tt.namespace, tt.podName)
			if got != tt.want {
				t.Errorf("K8sToAppHostingName(%q, %q) = %q, want %q", tt.namespace, tt.podName, got, tt.want)
			}
			// Verify length constraint
			if len(got) > MaxAppHostingNameLength {
				t.Errorf("K8sToAppHostingName(%q, %q) length = %d, exceeds max %d", tt.namespace, tt.podName, len(got), MaxAppHostingNameLength)
			}
		})
	}
}

func TestAppHostingToK8sName(t *testing.T) {
	tests := []struct {
		name    string
		appName string
		want    string
	}{
		{
			name:    "simple name with underscore",
			appName: "nginx_on_switch",
			want:    "nginx-on-switch",
		},
		{
			name:    "name without underscore",
			appName: "nginx",
			want:    "nginx",
		},
		{
			name:    "name with hash suffix removed",
			appName: "nginx_app_6754af",
			want:    "nginx-app",
		},
		{
			name:    "name with multiple underscores",
			appName: "my_cool_app_v2",
			want:    "my-cool-app-v2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AppHostingToK8sName(tt.appName)
			if got != tt.want {
				t.Errorf("AppHostingToK8sName(%q) = %q, want %q", tt.appName, got, tt.want)
			}
		})
	}
}

func TestK8sToAppHostingNameValidCharacters(t *testing.T) {
	// Test that output only contains valid AppHosting characters: [0-9a-zA-Z_]
	testCases := []struct {
		namespace string
		podName   string
	}{
		{"default", "nginx-on-switch"},
		{"prod", "my-app-v2"},
		{"kube-system", "coredns-abc123"},
		{"", "simple"},
	}

	for _, tc := range testCases {
		result := K8sToAppHostingName(tc.namespace, tc.podName)
		for _, c := range result {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_') {
				t.Errorf("K8sToAppHostingName(%q, %q) = %q contains invalid character %q", tc.namespace, tc.podName, result, string(c))
			}
		}
	}
}
