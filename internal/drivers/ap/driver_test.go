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

package ap

import (
	"context"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

func TestGetDeviceResourcesPodCapacity(t *testing.T) {
	tests := []struct {
		name    string
		maxPods int32
		want    int64
	}{
		{name: "AP default", want: 2},
		{name: "configured lower capacity", maxPods: 1, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &Driver{config: &v1alpha1.DeviceSpec{MaxPods: tt.maxPods}}
			resources, err := d.GetDeviceResources(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			podCapacity := (*resources)[corev1.ResourcePods]
			if got := podCapacity.Value(); got != tt.want {
				t.Fatalf("pod capacity = %d, want %d", got, tt.want)
			}
		})
	}
}
