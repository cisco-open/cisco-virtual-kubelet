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

package semconv

const (
	CvkEntityType   string = "cvk.entity.type"
	CvkEntityID     string = "cvk.entity.id"
	CvkEvidenceType string = "cvk.evidence.type"

	EntityTypeDevice       string = "device"
	EntityTypeInterface    string = "interface"
	EntityTypePod          string = "pod"
	EntityTypeApp          string = "app"
	EntityTypeConfig       string = "config"
	EntityTypeOperation    string = "operation"
	EntityTypeSubscription string = "subscription"
	EntityTypeTopologyLink string = "topology_link"

	EvidenceTypeMetricAnomaly   string = "metric_anomaly"
	EvidenceTypeStateTransition string = "state_transition"
	EvidenceTypeConfigChange    string = "config_change"
	EvidenceTypeTransportError  string = "transport_error"
	EvidenceTypeOperatorAction  string = "operator_action"
	EvidenceTypeAIAgentAction   string = "ai_agent_action"

	CvkWorkflowName  string = "cvk.workflow.name"
	CvkWorkflowRunID string = "cvk.workflow.run_id"
	CvkTaskName      string = "cvk.task.name"
	CvkTaskID        string = "cvk.task.id"
	CvkToolName      string = "cvk.tool.name"
	CvkToolCallID    string = "cvk.tool.call_id"
)
