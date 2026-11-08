// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package iosxe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	"github.com/google/uuid"
	"github.com/virtual-kubelet/virtual-kubelet/log"
)

const standardFilesystemPath = "/restconf/data/Cisco-IOS-XE-platform-software-oper:cisco-platform-software/q-filesystem"

var standardStageName = regexp.MustCompile(`^cvk-stage-[a-f0-9]{32}\.tar$`)

// Receipts are minted only after a fresh, random destination is observed absent.
// They are not reconstructed from filenames. An old file or a replacement worker
// without its receipt must preserve the file, even when its name looks managed.
// All file mutations run inside the existing complete device mutation guard.
type standardStageReceipt struct {
	appID, path, statPath               string
	copied, safe, deleting, quarantined bool
}

type standardFilesystemInventory struct {
	Locations []struct {
		FRU        string `json:"fru"`
		Slot       *int   `json:"slot"`
		Bay        *int   `json:"bay"`
		Chassis    *int   `json:"chassis"`
		Partitions []struct {
			Name     string `json:"name"`
			Writable *bool  `json:"is-writable"`
			Primary  bool   `json:"is-primary"`
		} `json:"partitions"`
	} `json:"Cisco-IOS-XE-platform-software-oper:q-filesystem"`
}
type standardFileInfo struct {
	Entries []struct {
		Name string `json:"full-path"`
		Type string `json:"type"`
	} `json:"Cisco-IOS-XE-platform-software-oper:partition-content"`
}

func (d *XEDriver) standardFlashPartition(ctx context.Context) (string, error) {
	var inventory standardFilesystemInventory
	// Do not retrieve the recursive filesystem inventory: it can exceed the
	// response bound. The exact file key is queried separately below.
	query := url.Values{"fields": {"fru;slot;bay;chassis;partitions(name;is-writable;is-primary)"}}
	if err := d.client.Get(ctx, standardFilesystemPath+"?"+query.Encode(), &inventory, json.Unmarshal); err != nil {
		return "", fmt.Errorf("read Standard staging filesystem: %w", err)
	}
	var candidates, primary []string
	for _, loc := range inventory.Locations {
		if loc.FRU == "" || loc.Slot == nil || loc.Bay == nil || loc.Chassis == nil {
			continue
		}
		for _, part := range loc.Partitions {
			if part.Name != "flash:" || part.Writable == nil || !*part.Writable {
				continue
			}
			key := url.PathEscape(loc.FRU) + "," + strconv.Itoa(*loc.Slot) + "," + strconv.Itoa(*loc.Bay) + "," + strconv.Itoa(*loc.Chassis)
			candidate := standardFilesystemPath + "=" + key + "/partitions=flash%3A"
			candidates = append(candidates, candidate)
			if part.Primary {
				primary = append(primary, candidate)
			}
		}
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	if len(primary) == 1 {
		return primary[0], nil
	}
	return "", fmt.Errorf("cannot identify one writable Standard staging flash partition")
}

func (d *XEDriver) standardStageFile(ctx context.Context, statPath, name string) (exists bool, regular bool, err error) {
	var info standardFileInfo
	if err := d.client.Get(ctx, statPath, &info, json.Unmarshal); err != nil {
		// Qualified exact-key response after validating the parent flash partition.
		// A missing root/parent or a malformed 2xx reply is never absence evidence.
		if common.IsRESTCONFStatus(err, http.StatusNotFound) && (common.HasRESTCONFErrorTag(err, "invalid-value") || common.HasRESTCONFErrorTag(err, "data-missing")) {
			return false, false, nil
		}
		return false, false, fmt.Errorf("observe Standard staging file: %w", err)
	}
	if len(info.Entries) != 1 || info.Entries[0].Name != name || info.Entries[0].Type == "" {
		return false, false, fmt.Errorf("unrecognized Standard staging file observation")
	}
	return true, info.Entries[0].Type == "file", nil
}

func (d *XEDriver) allocateStandardStage(ctx context.Context, appID string) (string, error) {
	partition, err := d.standardFlashPartition(ctx)
	if err != nil {
		// Cleanup capability is optional. Firmware without this operational model
		// keeps the existing copy-first flow, using an unowned unique path.
		// No file may be deleted without the qualified absence observation.
		return d.unownedStandardStage(ctx, err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		id, err := uuid.NewRandom()
		if err != nil {
			return "", fmt.Errorf("allocate Standard staging identity: %w", err)
		}
		name := "cvk-stage-" + strings.ReplaceAll(id.String(), "-", "") + ".tar"
		statPath := partition + "/partition-content=" + url.PathEscape(name) + "?" + url.Values{"fields": {"full-path;type"}}.Encode()
		exists, _, err := d.standardStageFile(ctx, statPath, name)
		if err != nil {
			return d.unownedStandardStage(ctx, err)
		}
		if exists {
			continue
		}
		receipt := &standardStageReceipt{appID: appID, path: "flash:/" + name, statPath: statPath}
		d.standardStagingMu.Lock()
		if d.standardStaging == nil {
			d.standardStaging = make(map[string]*standardStageReceipt)
		}
		d.standardStaging[receipt.path] = receipt
		d.standardStagingMu.Unlock()
		return receipt.path, nil
	}
	return "", fmt.Errorf("could not allocate an unused Standard staging file")
}

func (d *XEDriver) unownedStandardStage(ctx context.Context, cause error) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return "", err
	}
	file := "flash:/cvk-stage-" + strings.ReplaceAll(id.String(), "-", "") + ".tar"
	log.G(ctx).WithError(cause).Warnf("Standard staging ownership could not be established; retaining %s after use", file)
	return file, nil
}

// A caller-specified destination or local source is persistent caller storage.
// Explicit adoption also relinquishes any still-live receipt for that basename;
// basename matching conservatively protects aliases of the device filesystem.
func (d *XEDriver) preserveStandardPath(source string) error {
	u, err := url.Parse(source)
	if err != nil {
		return err
	}
	name := path.Base(u.Path)
	if u.Opaque != "" {
		name = path.Base(u.Opaque)
	}
	if !standardStageName.MatchString(name) {
		return nil
	}
	d.standardStagingMu.Lock()
	defer d.standardStagingMu.Unlock()
	for file, r := range d.standardStaging {
		if path.Base(file) != name {
			continue
		}
		if r.deleting {
			return fmt.Errorf("Standard staging file cleanup is in progress; retry local adoption")
		}
		delete(d.standardStaging, file)
	}
	return nil
}

func (d *XEDriver) standardStageCopyResult(file string, err error) {
	d.standardStagingMu.Lock()
	defer d.standardStagingMu.Unlock()
	r := d.standardStaging[file]
	if r == nil {
		return
	}
	if err == nil {
		r.copied = true
		r.safe = true
		return
	}
	// The qualified synchronous copy RPC has returned a definitive 4xx error.
	// Timeouts, lost replies, 5xx and unknown output retain the file for inspection.
	var response *common.RESTCONFError
	if !common.IsRESTCONFMutationAmbiguous(err) && errors.As(err, &response) && response.StatusCode >= 400 && response.StatusCode < 500 {
		r.safe = true
		return
	}
	r.quarantined = true
}
func (d *XEDriver) standardStageInstallPending(file string) {
	d.standardStagingMu.Lock()
	defer d.standardStagingMu.Unlock()
	if r := d.standardStaging[file]; r != nil {
		r.safe = false
	}
}
func (d *XEDriver) standardStagesConsumed(appID string) {
	d.standardStagingMu.Lock()
	defer d.standardStagingMu.Unlock()
	for _, r := range d.standardStaging {
		if r.appID == appID && r.copied && !r.quarantined {
			r.safe = true
		}
	}
}
func (d *XEDriver) hasStandardStageCleanup(appID string) bool {
	d.standardStagingMu.Lock()
	defer d.standardStagingMu.Unlock()
	for _, r := range d.standardStaging {
		if r.appID == appID && !r.quarantined {
			return true
		}
	}
	return false
}

func (d *XEDriver) cleanupStandardStages(ctx context.Context, appID string) error {
	if d.verifiedMode() {
		return nil
	}
	d.standardStagingMu.Lock()
	var pending []*standardStageReceipt
	for _, r := range d.standardStaging {
		if r.appID == appID && r.safe && !r.deleting && !r.quarantined {
			r.deleting = true
			pending = append(pending, r)
		}
	}
	d.standardStagingMu.Unlock()
	var result error
	for _, r := range pending {
		err := d.removeStandardStage(ctx, r)
		d.standardStagingMu.Lock()
		r.deleting = false
		if err == nil {
			delete(d.standardStaging, r.path)
		} else if common.IsRESTCONFMutationAmbiguous(err) {
			r.quarantined = true
		}
		d.standardStagingMu.Unlock()
		if err != nil {
			result = errors.Join(result, err)
			log.G(ctx).WithError(err).Warnf("Standard staging cleanup retained %s", r.path)
		} else {
			log.G(ctx).Infof("Standard staging cleanup confirmed absent: %s (app %s)", r.path, r.appID)
		}
	}
	return result
}

func (d *XEDriver) removeStandardStage(ctx context.Context, r *standardStageReceipt) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	name := path.Base(r.path)
	if !standardStageName.MatchString(name) || r.path != "flash:/"+name {
		return fmt.Errorf("refusing non-owned Standard staging path")
	}
	exists, regular, err := d.standardStageFile(ctx, r.statPath, name)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if !regular {
		return fmt.Errorf("refusing Standard staging cleanup of non-regular file")
	}
	client, ok := d.client.(postWithResultClient)
	if !ok {
		return fmt.Errorf("Standard cleanup requires RPC response decoding")
	}
	var response appHostingRPCOutput
	payload := map[string]any{"Cisco-IOS-XE-rpc:delete": map[string]string{"filename-drop-node-name": r.path}}
	if err = client.PostWithResult(ctx, "/restconf/operations/Cisco-IOS-XE-rpc:delete", payload, &response, json.Marshal, decodeAppHostingRPCOutput); err != nil {
		return fmt.Errorf("delete Standard staging file: %w", err)
	}
	if !response.Present || strings.TrimSpace(response.Result) != "RPC request successful" {
		return common.NewRESTCONFMutationAmbiguousError(fmt.Errorf("Standard file delete completion unconfirmed"))
	}
	exists, _, err = d.standardStageFile(ctx, r.statPath, name)
	if err != nil {
		return common.NewRESTCONFMutationAmbiguousError(err)
	}
	if exists {
		return common.NewRESTCONFMutationAmbiguousError(fmt.Errorf("Standard staging file still present after delete"))
	}
	return nil
}
