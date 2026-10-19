// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"os"
	"time"
)

// A private write-ahead file fences the standalone qualification cleanup helper.
// It is not a controller reservation or an authorization for product deletion.
type qualificationCleanup struct {
	Name            string    `json:"name"`
	ID              string    `json:"id"`
	UpdateTaskID    string    `json:"updateTaskId"`
	DeleteTaskID    string    `json:"deleteTaskId,omitempty"`
	SubmittedAt     time.Time `json:"submittedAt"`
	DeleteAttempted bool      `json:"deleteAttempted"`
	Deleted         bool      `json:"deleted"`
}

func claimQualificationCleanup(path string, cleanup qualificationCleanup) error {
	return claimQualificationRecord(path, cleanup)
}

func claimQualificationRecord(path string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err := file.Write(raw); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}
