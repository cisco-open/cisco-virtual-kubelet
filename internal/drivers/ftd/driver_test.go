// Copyright (c) 2026 Cisco Systems Inc.
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

package ftd

import (
	"context"
	"strings"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const sampleShowVersion = `
--------------[ ftdv-01.localdomain ]---------------
Model                     : Cisco Secure Firewall Threat Defense for KVM (75) Version 7.6.4 (Build 69)
UUID                      : 937334c4-6359-11f1-96bf-b789d20e4b89
VDB version               : 392
----------------------------------------------------
`

const sampleShowNetwork = `
===============[ System Information ]===============
Hostname                  : ftdv-01.localdomain
Management port           : 8305

===============[ br1 ]===============
State                     : Enabled
Link State                : Up
Channels                  : Management & Events
IPv4 Address              : 10.0.2.2
IPv4 Netmask              : 255.255.255.0
`

type fakeFTDClient struct {
	outputs map[string]string
	errs    map[string]error
}

func (f fakeFTDClient) Run(_ context.Context, command string) (string, error) {
	if err := f.errs[command]; err != nil {
		return "", err
	}
	return f.outputs[command], nil
}

func TestParseShowVersion(t *testing.T) {
	info := parseShowVersion(sampleShowVersion)
	if info.Hostname != "ftdv-01.localdomain" {
		t.Fatalf("hostname=%q", info.Hostname)
	}
	if info.ProductID != "Cisco Secure Firewall Threat Defense for KVM (75)" {
		t.Fatalf("product=%q", info.ProductID)
	}
	if info.SoftwareVersion != "7.6.4-69" {
		t.Fatalf("version=%q", info.SoftwareVersion)
	}
	if info.SerialNumber != "937334c4-6359-11f1-96bf-b789d20e4b89" {
		t.Fatalf("serial=%q", info.SerialNumber)
	}
}

func TestParseShowNetwork(t *testing.T) {
	network := parseShowNetwork(sampleShowNetwork)
	if network.Hostname != "ftdv-01.localdomain" || network.ManagementPort != "8305" {
		t.Fatalf("unexpected network metadata: %#v", network)
	}
	if len(network.Interfaces) != 1 {
		t.Fatalf("interfaces=%d", len(network.Interfaces))
	}
	if got := network.Interfaces[0]; got.Name != "br1" || got.IPv4 != "10.0.2.2" || ftdInterfaceStatus(got) != "up" {
		t.Fatalf("unexpected interface: %#v status=%s", got, ftdInterfaceStatus(got))
	}
}

func TestFTDDriverAdvertisesHealthNodeWithoutPods(t *testing.T) {
	driver := &FTDDriver{
		config: &v1alpha1.DeviceSpec{
			FTD: &v1alpha1.FTDConfig{
				Resources: &v1alpha1.FTDResourceConfig{CPUCores: 6, MemoryMB: 12288, StorageMB: 4096},
			},
		},
		client: fakeFTDClient{outputs: map[string]string{
			"show managers": "Managed locally.\n",
		}},
	}
	resources, err := driver.GetDeviceResources(context.Background())
	if err != nil {
		t.Fatalf("GetDeviceResources: %v", err)
	}
	if got := resources.Pods().Value(); got != 0 {
		t.Fatalf("pods=%d", got)
	}
	oper, err := driver.GetGlobalOperationalData(context.Background())
	if err != nil {
		t.Fatalf("GetGlobalOperationalData: %v", err)
	}
	if !oper.AppHostingUnsupported || oper.IoxEnabled {
		t.Fatalf("unexpected app-hosting state: %#v", oper)
	}
	if oper.SystemCPU.Quota != 6 || oper.Memory.Quota != 12288 || oper.Storage.Quota != 4096 {
		t.Fatalf("unexpected resources: %#v", oper)
	}
}

func TestFTDDriverRejectsAppHosting(t *testing.T) {
	driver := &FTDDriver{}
	err := driver.DeployPod(context.Background(), &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "hello"}}, nil, nil)
	if !apierrors.IsForbidden(err) {
		t.Fatalf("DeployPod err=%v, want forbidden", err)
	}
	if !strings.Contains(err.Error(), ErrAppHostingUnsupported.Error()) {
		t.Fatalf("DeployPod err=%v, want ErrAppHostingUnsupported", err)
	}
	pods, err := driver.ListPods(context.Background())
	if err != nil || len(pods) != 0 {
		t.Fatalf("ListPods pods=%v err=%v", pods, err)
	}
}

func TestFTDDriverTopologyFromShowNetwork(t *testing.T) {
	driver := &FTDDriver{
		client: fakeFTDClient{outputs: map[string]string{
			"show network": sampleShowNetwork,
		}},
	}
	ips, err := driver.GetInterfaceIPs(context.Background())
	if err != nil {
		t.Fatalf("GetInterfaceIPs: %v", err)
	}
	if len(ips) != 1 || ips[0].Interface != "br1" || ips[0].IPv4 != "10.0.2.2" || ips[0].Status != "up" {
		t.Fatalf("unexpected interface IPs: %#v", ips)
	}
	stats, err := driver.GetInterfaceStats(context.Background())
	if err != nil {
		t.Fatalf("GetInterfaceStats: %v", err)
	}
	if len(stats) != 1 || stats[0].Name != "br1" || stats[0].OperStatus != "up" {
		t.Fatalf("unexpected interface stats: %#v", stats)
	}
}
