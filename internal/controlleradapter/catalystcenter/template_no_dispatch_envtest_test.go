// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import "testing"

// Real API metadata/CRD persistence; native operations remain local HTTPS
// fixtures. Does not read kubeconfig or repair the installed cluster.
func TestEnvtestTemplateNoDispatchReplan(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, stage := range templateNoDispatchStages {
		for _, mode := range []string{"field-edit", "read-error", "lost-ack"} {
			t.Run(stage+"/"+mode, func(t *testing.T) {
				exerciseTemplateNoDispatchReplan(t, stage, mode, installWirelessFeatureQualificationAPI(t, api, "template-refusal-"+stage+"-"+mode))
			})
		}
	}
}
