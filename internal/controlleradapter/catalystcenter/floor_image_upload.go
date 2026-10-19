// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strings"
	"unicode"
)

var errFloorImage = errors.New("invalid or unsupported floor image upload")

const maxFloorImageBytes = 8 << 20

// This helper consumes an already-bound byte snapshot. It does not open local
// files or fetch model-supplied URLs. Manifest source binding and a durable
// completion receipt are required before this primitive can be activated.
func validateFloorImage(filename string, image []byte) (string, error) {
	if filename == "" || len(filename) > 1024 || strings.ContainsAny(filename, "\\") || strings.ContainsFunc(filename, unicode.IsControl) || len(image) == 0 || len(image) > maxFloorImageBytes {
		return "", errFloorImage
	}
	name := path.Base(filename)
	if name == "." || name == "/" || name == ".." {
		return "", errFloorImage
	}
	typeName := http.DetectContentType(image)
	ext := strings.ToLower(path.Ext(name))
	if !((typeName == "image/png" && ext == ".png") || (typeName == "image/jpeg" && (ext == ".jpg" || ext == ".jpeg")) || (typeName == "application/pdf" && ext == ".pdf")) {
		return "", errFloorImage
	}
	return name, nil
}

func floorImageMultipart(filename string, image []byte) ([]byte, string, error) {
	name, err := validateFloorImage(filename, image)
	if err != nil {
		return nil, "", err
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("image", name)
	if err != nil {
		return nil, "", errFloorImage
	}
	if _, err = part.Write(image); err != nil {
		return nil, "", errFloorImage
	}
	if err = w.Close(); err != nil {
		return nil, "", errFloorImage
	}
	return body.Bytes(), w.FormDataContentType(), nil
}

func (c *client) uploadFloorImage(ctx context.Context, floorID, filename string, image []byte) (Task, error) {
	if !validAPIID(floorID) {
		return Task{}, errFloorImage
	}
	body, contentType, err := floorImageMultipart(filename, image)
	if err != nil {
		return Task{}, err
	}
	raw, err := c.mutateContentType(ctx, http.MethodPost, "/dna/intent/api/v2/floors/"+url.PathEscape(floorID)+"/uploadImage", nil, body, 0, contentType)
	if err != nil {
		return Task{}, err
	}
	var receipt struct {
		Response Task `json:"response"`
	}
	if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
		return Task{}, errInvalidResponse
	}
	return Task{ID: receipt.Response.ID}, nil
}

// The pinned upload API also returns a synchronous HTTP 201 floor DTO. Require
// the requested native group identity and nonempty image metadata; a task
// envelope, empty response or unrelated floor cannot confirm this response mode.
func (c *client) uploadFloorImageCompleted(ctx context.Context, floorID, floorPath, filename string, image []byte) error {
	if !validAPIID(floorID) || !validSitePath(floorPath) {
		return errFloorImage
	}
	body, contentType, err := floorImageMultipart(filename, image)
	if err != nil {
		return err
	}
	raw, err := c.mutateContentType(ctx, http.MethodPost, "/dna/intent/api/v2/floors/"+url.PathEscape(floorID)+"/uploadImage", nil, body, http.StatusCreated, contentType)
	if err != nil {
		return err
	}
	var receipt struct {
		GroupUUID string                     `json:"groupInstanceUuid"`
		ImageInfo map[string]json.RawMessage `json:"imageInfo"`
	}
	if json.Unmarshal(raw, &receipt) != nil || receipt.GroupUUID != floorID || len(receipt.ImageInfo) == 0 {
		return errInvalidResponse
	}
	return nil
}
