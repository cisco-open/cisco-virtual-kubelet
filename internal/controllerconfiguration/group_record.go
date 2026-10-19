// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"io"
)

const MaxGroupIdentityRecordBytes = 1 << 20

type groupIdentityRecord = cvk.NetworkControllerConfigurationGroupIdentity

// EncodeGroupIdentityRecord creates bounded, non-secret restart evidence. It
// does not persist anything. A future journal writer must atomically bind this
// record to its operation and gate incompatible workers before publishing it.
func EncodeGroupIdentityRecord(op *cvk.NetworkControllerConfigurationOperation, targets []GroupTargetIdentity) ([]byte, error) {
	canonical, err := CanonicalGroupTargets(targets)
	if err != nil {
		return nil, err
	}
	digest, err := GroupOperationDigest(op, canonical)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(groupIdentityRecord{Version: GroupIdentityVersion, Digest: digest, Targets: canonical})
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxGroupIdentityRecordBytes {
		return nil, errors.New("group identity record exceeds bound")
	}
	return raw, nil
}

// DecodeGroupIdentityRecord requires the actual journal operation and its
// separately bound expected digest, not only a self-consistent record checksum. Truncated/pruned/replaced targets cannot
// be accepted as a smaller group after restart.
func DecodeGroupIdentityRecord(op *cvk.NetworkControllerConfigurationOperation, expectedDigest string, raw []byte) ([]GroupTargetIdentity, error) {
	reject := errors.New("invalid group identity record")
	if len(raw) == 0 || len(raw) > MaxGroupIdentityRecordBytes {
		return nil, reject
	}
	decodedDigest, err := hex.DecodeString(expectedDigest)
	if err != nil || len(decodedDigest) != 32 || hex.EncodeToString(decodedDigest) != expectedDigest {
		return nil, reject
	}
	var record groupIdentityRecord
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&record) != nil {
		return nil, reject
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF || record.Version != GroupIdentityVersion || record.Digest != expectedDigest {
		return nil, reject
	}
	canonical, err := CanonicalGroupTargets(record.Targets)
	if err != nil {
		return nil, reject
	}
	digest, err := GroupOperationDigest(op, canonical)
	if err != nil || digest != record.Digest {
		return nil, reject
	}
	return canonical, nil
}
