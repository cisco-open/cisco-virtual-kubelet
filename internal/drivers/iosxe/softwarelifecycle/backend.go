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

// Package softwarelifecycle implements IOS XE's optional, path-based software
// inventory capability. It deliberately stops once an image is registered;
// the provider continues to use standard gNOI OS for activation and verify.
package softwarelifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
	lifecycle "github.com/cisco/virtual-kubelet-cisco/internal/softwarelifecycle"
)

const (
	installInventoryPath = "/Cisco-IOS-XE-install-oper:install-oper-data/install-location-information"
	installOperDataPath  = "/Cisco-IOS-XE-install-oper:install-oper-data"
	installRPCPath       = "/operations/Cisco-IOS-XE-install-rpc:install"
)

type rpcInvoker interface {
	InvokeRPC(ctx context.Context, path string, payload []byte) ([]byte, error)
}

// Adapter maps IOS XE install-oper and install-rpc YANG surfaces into the
// neutral software lifecycle capability.
type Adapter struct {
	transport transport.Interface
}

var _ lifecycle.Backend = (*Adapter)(nil)

// New constructs an IOS XE lifecycle adapter. Only RESTCONF is supported: the
// path-based install action is an IOS XE RESTCONF RPC, not a generic gNOI RPC.
func New(t transport.Interface) (*Adapter, error) {
	if t == nil {
		return nil, fmt.Errorf("iosxe software lifecycle: nil transport: %w", lifecycle.ErrUnsupported)
	}
	if t.Capabilities().Kind != transport.KindRESTCONF {
		return nil, fmt.Errorf("iosxe software lifecycle requires RESTCONF, got %q: %w", t.Capabilities().Kind, lifecycle.ErrUnsupported)
	}
	return &Adapter{transport: t}, nil
}

// Inspect resolves targetVersion to one exact, unique IOS XE inventory image.
// A shortened dotted version is accepted only when it identifies one image.
func (a *Adapter) Inspect(ctx context.Context, targetVersion string) (lifecycle.InventoryImage, error) {
	if err := lifecycle.ValidateTargetVersion(targetVersion); err != nil {
		return lifecycle.InventoryImage{}, err
	}
	raw, err := a.transport.Fetch(ctx, installInventoryPath)
	if err != nil {
		return lifecycle.InventoryImage{}, fmt.Errorf("fetch IOS XE install inventory: %w", err)
	}
	return inventoryImageFromJSON(raw, targetVersion)
}

// RegisterDeviceFile submits IOS XE's install-by-path RPC. The RPC is always
// install-only (one-shot=false); activation remains under the generic gNOI OS
// state machine. The caller owns durable operation-ID allocation and replay
// prevention.
func (a *Adapter) RegisterDeviceFile(ctx context.Context, request lifecycle.DeviceFileRequest) (lifecycle.DeviceFileRegistration, error) {
	if err := ValidateDevicePath(request.Path); err != nil {
		return lifecycle.DeviceFileRegistration{}, err
	}
	if err := ValidateOperationID(request.OperationID); err != nil {
		return lifecycle.DeviceFileRegistration{}, err
	}
	invoker, ok := a.transport.(rpcInvoker)
	if !ok {
		return lifecycle.DeviceFileRegistration{}, fmt.Errorf("IOS XE RESTCONF transport does not expose install RPC invocation: %w", lifecycle.ErrUnsupported)
	}
	payload, err := json.Marshal(installRPCEnvelope{
		Input: installRPCInput{
			UUID:    request.OperationID,
			OneShot: false,
			Path:    request.Path,
		},
	})
	if err != nil {
		return lifecycle.DeviceFileRegistration{}, fmt.Errorf("marshal IOS XE install request: %w", err)
	}
	if _, err := invoker.InvokeRPC(ctx, installRPCPath, payload); err != nil {
		return lifecycle.DeviceFileRegistration{}, fmt.Errorf("invoke IOS XE install-by-path RPC: %w", err)
	}
	return lifecycle.DeviceFileRegistration{OperationID: request.OperationID}, nil
}

// ValidateDeviceFilePath applies the same IOS XE path policy used by
// RegisterDeviceFile without touching the device.
func (a *Adapter) ValidateDeviceFilePath(path string) error {
	return ValidateDevicePath(path)
}

// ObserveDeviceFile correlates the durable UUID against IOS XE's active and
// historical install operations and cross-checks the target image inventory.
// Inventory is authoritative for activation: an operation is never reported as
// usable without an exact InventoryImage whose state is activatable.
func (a *Adapter) ObserveDeviceFile(ctx context.Context, operationID, targetVersion string) (lifecycle.DeviceFileObservation, error) {
	if err := ValidateOperationID(operationID); err != nil {
		return lifecycle.DeviceFileObservation{}, err
	}
	if err := lifecycle.ValidateTargetVersion(targetVersion); err != nil {
		return lifecycle.DeviceFileObservation{}, err
	}
	raw, err := a.transport.Fetch(ctx, installOperDataPath)
	if err != nil {
		return lifecycle.DeviceFileObservation{}, fmt.Errorf("fetch IOS XE install operation state: %w", err)
	}

	root, err := decodeObject(raw)
	if err != nil {
		return lifecycle.DeviceFileObservation{}, fmt.Errorf("decode IOS XE install operation state: %w", err)
	}
	op, found, err := findOperation(root, operationID)
	if err != nil {
		return lifecycle.DeviceFileObservation{}, err
	}
	if !found {
		return lifecycle.DeviceFileObservation{}, fmt.Errorf("IOS XE install operation %s: %w", operationID, lifecycle.ErrOperationNotFound)
	}
	image, imageErr := inventoryImageFromNode(root, targetVersion)
	if imageErr != nil && !errors.Is(imageErr, lifecycle.ErrTargetNotFound) {
		return lifecycle.DeviceFileObservation{}, imageErr
	}

	observation := lifecycle.DeviceFileObservation{
		OperationID: operationID,
		State:       normalizeOperationState(op.Status, op.Done),
	}
	if imageErr == nil {
		observation.Image = &image
	}
	if observation.State == lifecycle.OperationStateFailed ||
		(imageErr == nil && image.State == lifecycle.InventoryStateInvalid) {
		observation.State = lifecycle.OperationStateFailed
		return observation, nil
	}
	if observation.State != lifecycle.OperationStateSucceeded {
		// Inventory cannot complete an operation whose correlated UUID has not
		// completed. This avoids mistaking a pre-existing same-version image for
		// the result of the current request.
		return observation, nil
	}
	if observation.Image != nil && observation.Image.State.Activatable() {
		return observation, nil
	}

	// IOS XE can mark the RPC complete slightly before install-version-info is
	// updated. Keep polling instead of allowing activation without an exact,
	// activatable inventory identity.
	observation.State = lifecycle.OperationStatePending
	return observation, nil
}

type installRPCEnvelope struct {
	Input installRPCInput `json:"Cisco-IOS-XE-install-rpc:input"`
}

type installRPCInput struct {
	UUID    string `json:"uuid"`
	OneShot bool   `json:"one-shot"`
	Path    string `json:"path"`
}

// ValidateDevicePath accepts IOS XE-local image paths and rejects strings that
// could be interpreted as URLs, credentials, traversal, or multiple resources.
func ValidateDevicePath(path string) error {
	if path == "" || len(path) > 128 {
		return lifecycle.ErrInvalidDevicePath
	}
	prefix := ""
	for _, candidate := range []string{"flash:", "bootflash:", "harddisk:"} {
		if strings.HasPrefix(path, candidate) {
			prefix = candidate
			break
		}
	}
	if prefix == "" {
		return lifecycle.ErrInvalidDevicePath
	}
	remainder := strings.TrimPrefix(path, prefix)
	if strings.HasPrefix(remainder, "/") {
		remainder = strings.TrimPrefix(remainder, "/")
	}
	if remainder == "" || strings.HasSuffix(remainder, "/") || strings.Contains(remainder, "//") {
		return lifecycle.ErrInvalidDevicePath
	}
	for _, segment := range strings.Split(remainder, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return lifecycle.ErrInvalidDevicePath
		}
		for _, c := range []byte(segment) {
			if !isSafePathByte(c) {
				return lifecycle.ErrInvalidDevicePath
			}
		}
	}
	return nil
}

func isSafePathByte(c byte) bool {
	return c >= 'a' && c <= 'z' ||
		c >= 'A' && c <= 'Z' ||
		c >= '0' && c <= '9' ||
		c == '.' || c == '_' || c == '-' || c == '+'
}

// ValidateOperationID enforces the canonical RFC 4122 textual layout accepted
// by IOS XE. Requiring a UUID prevents accidental reuse of human-readable CR
// names as device mutation identifiers.
func ValidateOperationID(id string) error {
	if len(id) != 36 {
		return lifecycle.ErrInvalidOperationID
	}
	for i := range id {
		switch i {
		case 8, 13, 18, 23:
			if id[i] != '-' {
				return lifecycle.ErrInvalidOperationID
			}
		default:
			if !isHex(id[i]) {
				return lifecycle.ErrInvalidOperationID
			}
		}
	}
	version := id[14]
	variant := id[19]
	if version < '1' || version > '8' || !strings.ContainsRune("89aAbB", rune(variant)) {
		return lifecycle.ErrInvalidOperationID
	}
	return nil
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

type inventoryVersion struct {
	Version          string
	VersionExtension string
	State            lifecycle.InventoryState
	SourcePath       string
	Location         int
}

func (v inventoryVersion) identity() string {
	if v.VersionExtension == "" || strings.HasSuffix(v.Version, "."+v.VersionExtension) {
		return v.Version
	}
	return v.Version + "." + v.VersionExtension
}

func inventoryImageFromJSON(raw []byte, target string) (lifecycle.InventoryImage, error) {
	root, err := decodeObject(raw)
	if err != nil {
		return lifecycle.InventoryImage{}, fmt.Errorf("decode IOS XE install inventory: %w", err)
	}
	return inventoryImageFromNode(root, target)
}

func inventoryImageFromNode(root map[string]any, target string) (lifecycle.InventoryImage, error) {
	locations, found, err := collectNamedList(root, "install-location-information")
	if err != nil {
		return lifecycle.InventoryImage{}, fmt.Errorf("decode IOS XE install inventory: %w", err)
	}
	if !found {
		return lifecycle.InventoryImage{}, fmt.Errorf("decode IOS XE install inventory: install-location-information missing")
	}

	var candidates []inventoryVersion
	for locationIndex, location := range locations {
		versions, _, err := directNamedList(location, "install-version-info")
		if err != nil {
			return lifecycle.InventoryImage{}, fmt.Errorf("decode IOS XE install inventory: %w", err)
		}
		for _, version := range versions {
			base := stringField(version, "version")
			entry := inventoryVersion{
				Version:          base,
				VersionExtension: stringField(version, "version-extension"),
				State:            normalizeInventoryState(stringField(version, "current")),
				SourcePath:       stringField(version, "src-filename"),
				Location:         locationIndex,
			}
			if versionMatches(entry.identity(), base, target) {
				candidates = append(candidates, entry)
			}
		}
	}
	if len(candidates) == 0 {
		return lifecycle.InventoryImage{}, fmt.Errorf("target version %s: %w", target, lifecycle.ErrTargetNotFound)
	}

	// An explicitly supplied full inventory identity wins over other prefix
	// candidates. Otherwise every matching exact identity must be the same.
	hasExactIdentity := false
	for _, candidate := range candidates {
		if candidate.identity() == target {
			hasExactIdentity = true
			break
		}
	}
	grouped := map[string][]inventoryVersion{}
	for _, candidate := range candidates {
		if hasExactIdentity && candidate.identity() != target {
			continue
		}
		grouped[candidate.identity()] = append(grouped[candidate.identity()], candidate)
	}
	if len(grouped) != 1 {
		return lifecycle.InventoryImage{}, fmt.Errorf("target version %s matched %d inventory identities: %w", target, len(grouped), lifecycle.ErrAmbiguousTarget)
	}
	for identity, entries := range grouped {
		state := entries[0].State
		sourcePath := entries[0].SourcePath
		selectedLocations := map[int]struct{}{entries[0].Location: {}}
		for _, entry := range entries[1:] {
			state = conservativeInventoryState(state, entry.State)
			selectedLocations[entry.Location] = struct{}{}
			if entry.SourcePath != sourcePath {
				sourcePath = ""
			}
		}
		// A stack or dual-supervisor response is ready only when every reported
		// install location contains the selected identity. A partial rollout is
		// observable as in-progress and must never authorize activation.
		if len(selectedLocations) != len(locations) && state.Activatable() {
			state = lifecycle.InventoryStateInProgress
		}
		return lifecycle.InventoryImage{Version: identity, State: state, SourcePath: sourcePath}, nil
	}
	panic("unreachable")
}

func versionMatches(identity, base, target string) bool {
	return identity == target || base == target || strings.HasPrefix(identity, target+".")
}

func normalizeInventoryState(state string) lifecycle.InventoryState {
	switch state {
	case "install-version-state-present":
		return lifecycle.InventoryStatePresent
	case "install-version-state-installed":
		return lifecycle.InventoryStateInstalled
	case "install-version-state-provisioned-uncommitted":
		return lifecycle.InventoryStateProvisionedUncommitted
	case "install-version-state-provisioned-committed":
		return lifecycle.InventoryStateProvisionedCommitted
	case "install-version-state-in-progress":
		return lifecycle.InventoryStateInProgress
	case "install-version-state-invalid":
		return lifecycle.InventoryStateInvalid
	default:
		return lifecycle.InventoryStateUnknown
	}
}

func conservativeInventoryState(left, right lifecycle.InventoryState) lifecycle.InventoryState {
	if left == right {
		return left
	}
	if left == lifecycle.InventoryStateInvalid || right == lifecycle.InventoryStateInvalid {
		return lifecycle.InventoryStateInvalid
	}
	if left == lifecycle.InventoryStateInProgress || right == lifecycle.InventoryStateInProgress {
		return lifecycle.InventoryStateInProgress
	}
	if left.Activatable() && right.Activatable() {
		return lifecycle.InventoryStateInstalled
	}
	return lifecycle.InventoryStateUnknown
}

type installOperation struct {
	Status string
	Done   string
}

func findOperation(root map[string]any, operationID string) (installOperation, bool, error) {
	active, _, err := collectNamedList(root, "install-oper")
	if err != nil {
		return installOperation{}, false, fmt.Errorf("decode IOS XE active install operations: %w", err)
	}
	history, _, err := collectNamedList(root, "install-oper-hist")
	if err != nil {
		return installOperation{}, false, fmt.Errorf("decode IOS XE install operation history: %w", err)
	}
	var matches []map[string]any
	for _, operations := range [][]map[string]any{active, history} {
		for _, operation := range operations {
			if stringField(operation, "op-uuid") == operationID {
				matches = append(matches, operation)
			}
		}
	}
	if len(matches) > 1 {
		return installOperation{}, false, fmt.Errorf("IOS XE install operation UUID %s matched %d entries: %w", operationID, len(matches), lifecycle.ErrAmbiguousOperation)
	}
	if len(matches) == 0 {
		return installOperation{}, false, nil
	}
	return installOperation{
		Status: stringField(matches[0], "op-status"),
		Done:   stringField(matches[0], "op-done"),
	}, true, nil
}

func normalizeOperationState(status, done string) lifecycle.OperationState {
	switch done {
	case "op-reverted":
		return lifecycle.OperationStateFailed
	case "op-not-complete":
		switch status {
		case "install-op-not-started", "install-op-pend-usr-cnfrm":
			return lifecycle.OperationStatePending
		default:
			return lifecycle.OperationStateInProgress
		}
	case "op-complete":
		switch status {
		case "install-op-succ", "install-op-marked-succ":
			return lifecycle.OperationStateSucceeded
		case "install-op-fail", "install-op-fail-revert", "install-op-dep-fail", "install-op-timeout", "install-op-sts-cancel":
			return lifecycle.OperationStateFailed
		default:
			return lifecycle.OperationStateUnknown
		}
	default:
		// op-done is the device's authoritative finality signal. Missing or
		// unknown values must never authorize activation.
		return lifecycle.OperationStateUnknown
	}
}

func decodeObject(raw []byte) (map[string]any, error) {
	var root map[string]any
	if len(raw) == 0 {
		return nil, errors.New("empty JSON response")
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, err
	}
	if root == nil {
		return nil, errors.New("JSON response is not an object")
	}
	return root, nil
}

func collectNamedList(node any, name string) ([]map[string]any, bool, error) {
	var result []map[string]any
	found := false
	var walk func(any) error
	walk = func(current any) error {
		switch value := current.(type) {
		case map[string]any:
			for key, child := range value {
				if localName(key) == name {
					items, err := objectList(child)
					if err != nil {
						return fmt.Errorf("%s: %w", name, err)
					}
					found = true
					result = append(result, items...)
					continue
				}
				if err := walk(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range value {
				if err := walk(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(node); err != nil {
		return nil, found, err
	}
	return result, found, nil
}

func directNamedList(object map[string]any, name string) ([]map[string]any, bool, error) {
	for key, value := range object {
		if localName(key) == name {
			items, err := objectList(value)
			return items, true, err
		}
	}
	return nil, false, nil
}

func objectList(value any) ([]map[string]any, error) {
	list, ok := value.([]any)
	if !ok {
		return nil, errors.New("expected JSON array")
	}
	result := make([]map[string]any, 0, len(list))
	for _, item := range list {
		object, ok := item.(map[string]any)
		if !ok {
			return nil, errors.New("array item is not a JSON object")
		}
		result = append(result, object)
	}
	return result, nil
}

func stringField(object map[string]any, name string) string {
	for key, value := range object {
		if localName(key) != name {
			continue
		}
		text, _ := value.(string)
		return text
	}
	return ""
}

func localName(name string) string {
	if index := strings.LastIndexByte(name, ':'); index >= 0 {
		return name[index+1:]
	}
	return name
}
