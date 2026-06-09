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

package iosxr

import (
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
)

func TestParseSourceTable(t *testing.T) {
	out := `Sno Name              File                 Installed By
--- ----------------- -------------------- --------------------
1   hello-app         hello-app.tar.gz     APP_MANAGER`
	got := parseSourceTable(out)
	if !got["hello-app"] {
		t.Fatalf("expected hello-app source, got %#v", got)
	}
}

func TestParseApplicationDetail(t *testing.T) {
	out := `Application: cvk0000_1234567890abcdef1234567890abcdef
  Type: Docker
  Source: hello-app
  Config State: Activated
  Workflow: Config
  Docker Information:
    Container ID: 8ad655704425
    Container name: cvk0000_1234567890abcdef1234567890abcdef
    Labels: io.kubernetes.pod.name=hello-xr,io.kubernetes.pod.namespace=default,io.kubernetes.pod.uid=1234,io.kubernetes.container.name=hello,type=tpa
    Image: hello-app:latest
    Status: Up About a minute
    Networks: host`

	app := parseApplicationDetail(out)
	if app.ID == "" || app.Source != "hello-app" || app.State() != "RUNNING" || app.Image != "hello-app:latest" {
		t.Fatalf("unexpected app detail: %#v", app)
	}
	ns, name, uid, container := common.PodIdentityFromRunOpts(app.RunOpts)
	if ns != "default" || name != "hello-xr" || uid != "1234" || container != "hello" {
		t.Fatalf("unexpected labels: ns=%q name=%q uid=%q container=%q runOpts=%v", ns, name, uid, container, app.RunOpts)
	}
}

func TestImageRefRPMSourceInference(t *testing.T) {
	d := &IOSXRDriver{}
	ref, err := d.resolveImageRef("/harddisk:/hello-app-0.1.0-ThinXR_26.1.1.x86_64.rpm")
	if err != nil {
		t.Fatal(err)
	}
	if ref.PackagePath != "/harddisk:/hello-app-0.1.0-ThinXR_26.1.1.x86_64.rpm" || ref.Source != "hello-app" {
		t.Fatalf("unexpected ref: %#v", ref)
	}
}

func TestParseXRInterfaceIPs(t *testing.T) {
	out := `Interface                      IP-Address      Status          Protocol Vrf-Name
MgmtEth0/RP0/CPU0/0             169.254.0.2     Up              Up       default
HundredGigE0/0/0/0              unassigned      Shutdown        Down     default`
	got := parseXRInterfaceIPs(out)
	if len(got) != 2 {
		t.Fatalf("len=%d got=%#v", len(got), got)
	}
	if got[0].Interface != "MgmtEth0/RP0/CPU0/0" || got[0].IPv4 != "169.254.0.2" || got[0].Status != "up/up" {
		t.Fatalf("unexpected first iface: %#v", got[0])
	}
	if got[1].IPv4 != "" || got[1].Status != "shutdown/down" {
		t.Fatalf("unexpected second iface: %#v", got[1])
	}
}
