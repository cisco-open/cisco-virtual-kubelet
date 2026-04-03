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

package common

import (
	"strings"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestGetAppHostingName(t *testing.T) {
	tests := []struct {
		name string
		pod  *v1.Pod
		want string
	}{
		{
			name: "generates name from UID",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					UID: types.UID("a24a730b-8b13-4fd0-96ee-900f99d87670"),
				},
			},
			want: "cvk0000_a24a730b8b134fd096ee900f99d87670",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetAppHostingName(tt.pod, 0)
			if got != tt.want {
				t.Errorf("GetAppHostingName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetAppHostingNameLength(t *testing.T) {
	// Name format is cvkNNNN_<UID32> where UID is UID36 with _ stripped
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID: types.UID("a24a730b-8b13-4fd0-96ee-900f99d87670"),
		},
	}
	got := GetAppHostingName(pod, 1)
	if len(got) > 40 {
		t.Errorf("GetAppHostingName() length = %d, exceeds max 40", len(got))
	}
	if len(got) != 40 {
		t.Errorf("GetAppHostingName() length = %d, expected 40.  Name should be padded to maxlen", len(got))
	}
}

func TestGetAppHostingNameValidCharacters(t *testing.T) {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID: types.UID("a24a730b-8b13-4fd0-96ee-900f99d87670"),
		},
	}
	result := GetAppHostingName(pod, 1)
	for _, c := range result {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c == '_')) {
			t.Errorf("GetAppHostingName() = %q contains invalid character %q (expected hex only)", result, string(c))
		}
	}
}

func TestGenerateContainerAppIDs(t *testing.T) {
	tests := []struct {
		name           string
		pod            *v1.Pod
		wantNumEntries int
		checkKeys      []string
	}{
		{
			name: "single container",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					UID:       types.UID("a24a730b-8b13-4fd0-96ee-900f99d87670"),
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{Name: "nginx"},
					},
				},
			},
			wantNumEntries: 1,
			checkKeys:      []string{"nginx"},
		},
		{
			name: "multiple containers",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "default",
					UID:       types.UID("b35b841c-9c24-5ge1-a7ff-a11g00e98781"),
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{Name: "nginx"},
						{Name: "sidecar"},
						{Name: "logging"},
					},
				},
			},
			wantNumEntries: 3,
			checkKeys:      []string{"nginx", "sidecar", "logging"},
		},
		{
			name: "empty pod",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "empty-pod",
					Namespace: "default",
					UID:       types.UID("c46c952d-ad35-6hf2-b8gg-b22h11f09892"),
				},
				Spec: v1.PodSpec{
					Containers: []v1.Container{},
				},
			},
			wantNumEntries: 0,
			checkKeys:      []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateContainerAppIDs(tt.pod)

			if len(got) != tt.wantNumEntries {
				t.Errorf("GenerateContainerAppIDs() returned %d entries, want %d", len(got), tt.wantNumEntries)
			}

			for _, key := range tt.checkKeys {
				appID, exists := got[key]
				if !exists {
					t.Errorf("GenerateContainerAppIDs() missing key %q", key)
				}
				if len(appID) != 40 {
					t.Errorf("GenerateContainerAppIDs() appID for %q has length %d, want 40", key, len(appID))
				}
				if !strings.HasPrefix(appID, "cvk000") {
					t.Errorf("GenerateContainerAppIDs() appID for %q = %q, doesn't have expected prefix", key, appID)
				}
			}
		})
	}
}

func TestExtractContainerNameFromLabels(t *testing.T) {
	tests := []struct {
		name        string
		runOptsLine string
		want        string
	}{
		{
			name:        "container name in middle",
			runOptsLine: "--label io.kubernetes.pod.name=nginx --label io.kubernetes.container.name=nginx --label io.kubernetes.pod.namespace=default",
			want:        "nginx",
		},
		{
			name:        "container name at end",
			runOptsLine: "--label io.kubernetes.pod.name=test-pod --label io.kubernetes.container.name=sidecar",
			want:        "sidecar",
		},
		{
			name:        "no container name label",
			runOptsLine: "--label io.kubernetes.pod.name=test-pod --label io.kubernetes.pod.namespace=default",
			want:        "",
		},
		{
			name:        "container name with hyphens",
			runOptsLine: "--label io.kubernetes.container.name=my-app-container --label foo=bar",
			want:        "my-app-container",
		},
		{
			name:        "empty string",
			runOptsLine: "",
			want:        "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractContainerNameFromLabels(tt.runOptsLine)
			if got != tt.want {
				t.Errorf("ExtractContainerNameFromLabels() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseCVKAppName(t *testing.T) {
	tests := []struct {
		name      string
		appName   string
		wantIdx   int8
		wantUID   string
		wantMatch bool
	}{
		{
			name:      "valid CVK app name index 0",
			appName:   "cvk0000_a24a730b8b134fd096ee900f99d87670",
			wantIdx:   0,
			wantUID:   "a24a730b8b134fd096ee900f99d87670",
			wantMatch: true,
		},
		{
			name:      "valid CVK app name index 1",
			appName:   "cvk0001_a24a730b8b134fd096ee900f99d87670",
			wantIdx:   1,
			wantUID:   "a24a730b8b134fd096ee900f99d87670",
			wantMatch: true,
		},
		{
			name:      "non-CVK app name",
			appName:   "my-custom-app",
			wantMatch: false,
		},
		{
			name:      "wrong prefix",
			appName:   "xyz0000_a24a730b8b134fd096ee900f99d87670",
			wantMatch: false,
		},
		{
			name:      "too short",
			appName:   "cvk0000_abc",
			wantMatch: false,
		},
		{
			name:      "too long",
			appName:   "cvk0000_a24a730b8b134fd096ee900f99d87670extra",
			wantMatch: false,
		},
		{
			name:      "missing underscore",
			appName:   "cvk00000a24a730b8b134fd096ee900f99d87670",
			wantMatch: false,
		},
		{
			name:      "empty string",
			appName:   "",
			wantMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx, uid, ok := ParseCVKAppName(tt.appName)
			if ok != tt.wantMatch {
				t.Errorf("ParseCVKAppName(%q) match = %v, want %v", tt.appName, ok, tt.wantMatch)
			}
			if ok {
				if idx != tt.wantIdx {
					t.Errorf("ParseCVKAppName(%q) index = %d, want %d", tt.appName, idx, tt.wantIdx)
				}
				if uid != tt.wantUID {
					t.Errorf("ParseCVKAppName(%q) uid = %q, want %q", tt.appName, uid, tt.wantUID)
				}
			}
		})
	}
}

func TestIsCVKManagedApp(t *testing.T) {
	if !IsCVKManagedApp("cvk0000_a24a730b8b134fd096ee900f99d87670") {
		t.Error("IsCVKManagedApp should return true for valid CVK app name")
	}
	if IsCVKManagedApp("my-custom-app") {
		t.Error("IsCVKManagedApp should return false for non-CVK app name")
	}
}
