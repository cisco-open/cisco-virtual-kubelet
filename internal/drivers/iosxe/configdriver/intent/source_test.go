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

package intent

import (
	"context"
	"reflect"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

func sourceScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme(core): %v", err)
	}
	return s
}

func newConfigMap(name, ns, key, body string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Data:       map[string]string{key: body},
	}
}

func TestLoadSourceInlineFragment(t *testing.T) {
	t.Parallel()
	src := configv1alpha1.ConfigurationSource{
		Inline: &runtime.RawExtension{Raw: []byte(`{"system":{"hostname":"edge-01"}}`)},
	}
	got, err := LoadSource(context.Background(), nil, "network", "edge-01", src)
	if err != nil {
		t.Fatalf("LoadSource: %v", err)
	}
	want := map[string]any{"system": map[string]any{"hostname": "edge-01"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v\nwant %#v", got, want)
	}
}

func TestLoadSourceConfigMapEnvelope(t *testing.T) {
	t.Parallel()
	body := `
iosxe:
  devices:
    - name: edge-01
      host: 10.0.0.1
      configuration:
        vlan:
          vlans:
            - id: 10
              name: users
`
	cm := newConfigMap("edge-01-data", "network", "data.nac.yaml", body)
	c := fake.NewClientBuilder().WithScheme(sourceScheme(t)).WithObjects(cm).Build()

	src := configv1alpha1.ConfigurationSource{
		ConfigMapRef: &configv1alpha1.ConfigMapKeyRef{Name: "edge-01-data", Key: "data.nac.yaml"},
	}
	got, err := LoadSource(context.Background(), c, "network", "edge-01", src)
	if err != nil {
		t.Fatalf("LoadSource: %v", err)
	}
	if _, ok := got["vlan"]; !ok {
		t.Fatalf("configuration block missing vlan; got %#v", got)
	}
}

func TestLoadSourceEnvelopeMissingDevice(t *testing.T) {
	t.Parallel()
	body := `
iosxe:
  devices:
    - name: other-device
      configuration:
        system: {}
`
	cm := newConfigMap("data", "network", "k", body)
	c := fake.NewClientBuilder().WithScheme(sourceScheme(t)).WithObjects(cm).Build()

	src := configv1alpha1.ConfigurationSource{
		ConfigMapRef: &configv1alpha1.ConfigMapKeyRef{Name: "data", Key: "k"},
	}
	_, err := LoadSource(context.Background(), c, "network", "edge-01", src)
	if err == nil || !strings.Contains(err.Error(), "edge-01") {
		t.Fatalf("expected device-missing error, got %v", err)
	}
}

func TestLoadSourceConfigMapMissing(t *testing.T) {
	t.Parallel()
	c := fake.NewClientBuilder().WithScheme(sourceScheme(t)).Build()
	src := configv1alpha1.ConfigurationSource{
		ConfigMapRef: &configv1alpha1.ConfigMapKeyRef{Name: "missing", Key: "k"},
	}
	if _, err := LoadSource(context.Background(), c, "network", "edge-01", src); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestLoadSourceRejectsBothOrNeither(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		src  configv1alpha1.ConfigurationSource
	}{
		{"neither", configv1alpha1.ConfigurationSource{}},
		{"both", configv1alpha1.ConfigurationSource{
			Inline:       &runtime.RawExtension{Raw: []byte(`{}`)},
			ConfigMapRef: &configv1alpha1.ConfigMapKeyRef{Name: "x", Key: "k"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadSource(context.Background(), nil, "network", "edge-01", tc.src)
			if err == nil || !strings.Contains(err.Error(), "exactly one") {
				t.Fatalf("expected 'exactly one' error, got %v", err)
			}
		})
	}
}

func TestLoadSourceBinaryKeyRejected(t *testing.T) {
	t.Parallel()
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "data", Namespace: "network"},
		BinaryData: map[string][]byte{"k": {0xde, 0xad}},
	}
	c := fake.NewClientBuilder().WithScheme(sourceScheme(t)).WithObjects(cm).Build()
	src := configv1alpha1.ConfigurationSource{
		ConfigMapRef: &configv1alpha1.ConfigMapKeyRef{Name: "data", Key: "k"},
	}
	_, err := LoadSource(context.Background(), c, "network", "edge-01", src)
	if err == nil || !strings.Contains(err.Error(), "binary") {
		t.Fatalf("expected binary-data rejection, got %v", err)
	}
}
