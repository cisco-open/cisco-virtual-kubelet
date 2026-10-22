// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
)

func (c *Client) customApplicationID(ctx context.Context, name string) (string, error) {
	var list struct {
		Data *[]struct {
			ID   string `json:"appId"`
			Name string `json:"appName"`
		} `json:"data"`
	}
	if err := c.Request(ctx, "GET", "/dataservice/template/policy/customapp/", nil, &list); err != nil {
		return "", err
	}
	if list.Data == nil {
		return "", ErrResponse
	}
	id := ""
	for _, item := range *list.Data {
		if item.Name == name {
			if id != "" || !validID.MatchString(item.ID) {
				return "", errors.New("SD-WAN custom application identity conflict")
			}
			id = item.ID
		}
	}
	return id, nil
}

func (c *Client) observeCustomApplication(ctx context.Context, r Resource) (*Observation, error) {
	id, err := c.customApplicationID(ctx, r.Name)
	if err != nil || id == "" {
		return nil, err
	}
	var body map[string]any
	if err := c.Request(ctx, "GET", r.Collection+url.PathEscape(id), nil, &body); err != nil {
		return nil, err
	}
	data, ok := body["data"].(map[string]any)
	if !ok || data["appName"] != r.Name {
		return nil, ErrResponse
	}
	return &Observation{ID: id, Body: body}, nil
}

func (c *Client) waitCustomApplicationTask(ctx context.Context, taskID string) error {
	if !validID.MatchString(taskID) {
		return ErrResponse
	}
	deadline := time.NewTimer(2 * time.Minute)
	defer deadline.Stop()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		var status struct {
			Summary struct {
				Status string `json:"status"`
			} `json:"summary"`
			Validation struct {
				Status string `json:"status"`
			} `json:"validation"`
			Data []struct {
				StatusID string `json:"statusId"`
			} `json:"data"`
		}
		if err := c.Request(ctx, "GET", "/dataservice/device/action/status/"+url.PathEscape(taskID), nil, &status); err != nil {
			return err
		}
		if status.Summary.Status == "done" {
			if strings.Contains(strings.ToLower(status.Validation.Status), "fail") {
				return errors.New("SD-WAN custom application task validation failed")
			}
			for _, item := range status.Data {
				if strings.Contains(strings.ToLower(item.StatusID), "fail") {
					return errors.New("SD-WAN custom application task failed")
				}
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("SD-WAN custom application task timed out")
		case <-ticker.C:
		}
	}
}

func (c *Client) createCustomApplication(ctx context.Context, r Resource) (string, error) {
	var result struct {
		ID     string `json:"appId"`
		TaskID string `json:"taskId"`
	}
	if err := c.Request(ctx, "POST", r.Collection, r.Body, &result); err != nil {
		return "", err
	}
	if result.ID != "" {
		if !validID.MatchString(result.ID) {
			return "", ErrResponse
		}
		return result.ID, nil
	}
	if err := c.waitCustomApplicationTask(ctx, result.TaskID); err != nil {
		return "", err
	}
	for i := 0; i < 15; i++ {
		id, err := c.customApplicationID(ctx, r.Name)
		if err != nil || id != "" {
			return id, err
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return "", errors.New("SD-WAN custom application ID not visible after task completion")
}

func (c *Client) updateCustomApplication(ctx context.Context, r Resource, o *Observation) error {
	var result struct {
		TaskID string `json:"taskId"`
	}
	if err := c.Request(ctx, "PUT", r.Collection+url.PathEscape(o.ID), r.Body, &result); err != nil {
		return err
	}
	if result.TaskID != "" {
		return c.waitCustomApplicationTask(ctx, result.TaskID)
	}
	return nil
}

func (c *Client) deleteCustomApplication(ctx context.Context, r Resource, id string) error {
	var result struct {
		TaskID string `json:"taskId"`
	}
	if err := c.Request(ctx, "DELETE", r.Collection+url.PathEscape(id), nil, &result); err != nil {
		return err
	}
	if result.TaskID != "" {
		return c.waitCustomApplicationTask(ctx, result.TaskID)
	}
	return nil
}
