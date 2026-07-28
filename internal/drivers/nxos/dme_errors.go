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

package nxos

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	configtransport "github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
)

const (
	maxDMEMethodLength  = 16
	maxDMECodeLength    = 64
	maxDMEDNLength      = 256
	maxDMEContextLength = 384
	maxNXAPIErrorLength = 1024
)

// DMEErrorCategory is the stable, operator-actionable classification for an
// NX-OS DME failure.
type DMEErrorCategory string

const (
	DMEErrorAuth       DMEErrorCategory = "auth"
	DMEErrorRetryable  DMEErrorCategory = "retryable"
	DMEErrorValidation DMEErrorCategory = "validation"
	DMEErrorPermanent  DMEErrorCategory = "permanent"
)

// DMEError reports bounded, redacted request metadata while retaining the
// original error for errors.Is/errors.As. Context never includes the request
// body; a device error-MO text is included only after credential redaction and
// length/control-character sanitisation.
type DMEError struct {
	Category   DMEErrorCategory
	Code       string
	StatusCode int
	Method     string
	DN         string
	Context    string

	cause error
}

func (e *DMEError) Error() string {
	if e == nil {
		return "<nil>"
	}
	parts := []string{"nxapi dme request failed", "category=" + string(e.Category)}
	if e.Method != "" {
		parts = append(parts, "method="+e.Method)
	}
	if e.DN != "" {
		parts = append(parts, fmt.Sprintf("dn=%q", e.DN))
	}
	if e.StatusCode > 0 {
		parts = append(parts, "status="+strconv.Itoa(e.StatusCode))
	}
	if e.Code != "" {
		parts = append(parts, "code="+e.Code)
	}
	if e.Context != "" {
		parts = append(parts, fmt.Sprintf("context=%q", e.Context))
	}
	return strings.Join(parts, " ")
}

func (e *DMEError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// Retryable lets the shared idempotent retry helper classify DME HTTP 429/5xx
// responses without parsing formatted strings.
func (e *DMEError) Retryable() bool {
	return e != nil && e.Category == DMEErrorRetryable
}

func (e *DMEError) AuthFailure() bool {
	return e != nil && e.Category == DMEErrorAuth
}

func (e *DMEError) ValidationFailure() bool {
	return e != nil && e.Category == DMEErrorValidation
}

func (e *DMEError) PermanentFailure() bool {
	return e != nil && e.Category == DMEErrorPermanent
}

type dmeErrorDetail struct {
	code string
	text string
}

func dmeResponseError(raw []byte) error {
	return dmeResponseErrorFor("", "", raw)
}

func dmeResponseErrorFor(method, dn string, raw []byte) error {
	details := dmeErrorDetails(raw)
	if len(details) == 0 {
		return nil
	}
	return buildDMEError(method, dn, 0, details, nil)
}

func wrapDMERequestError(method, dn string, cause error) error {
	if cause == nil {
		return nil
	}
	var (
		status  int
		details []dmeErrorDetail
	)
	var restErr *configtransport.RESTError
	if errors.As(cause, &restErr) && restErr != nil {
		status = restErr.StatusCode
		details = dmeErrorDetails([]byte(restErr.Body))
	}
	return buildDMEError(method, dn, status, details, cause)
}

func buildDMEError(method, dn string, status int, details []dmeErrorDetail, cause error) *DMEError {
	category := classifyDMEError(status, "", "", cause)
	codes := make([]string, 0, len(details))
	contexts := make([]string, 0, len(details))
	for _, detail := range details {
		detailCategory := classifyDMEError(status, detail.code, detail.text, cause)
		if dmeCategoryPriority(detailCategory) > dmeCategoryPriority(category) {
			category = detailCategory
		}
		if code := safeDMECode(detail.code); code != "" && !containsString(codes, code) {
			codes = append(codes, code)
		}
		if text := safeDMEValue(detail.text, maxDMEContextLength); text != "" {
			contexts = append(contexts, text)
		}
	}
	return &DMEError{
		Category:   category,
		Code:       safeDMEValue(strings.Join(codes, ","), maxDMECodeLength),
		StatusCode: safeHTTPStatus(status),
		Method:     safeDMEMethod(method),
		DN:         safeDMEValue(dn, maxDMEDNLength),
		Context:    safeDMEValue(strings.Join(contexts, "; "), maxDMEContextLength),
		cause:      cause,
	}
}

func classifyDMEError(status int, code, text string, cause error) DMEErrorCategory {
	numericCode, _ := strconv.Atoi(strings.TrimSpace(code))
	lowerText := strings.ToLower(text)

	if status == http.StatusUnauthorized || status == http.StatusForbidden ||
		numericCode == http.StatusUnauthorized || numericCode == http.StatusForbidden ||
		containsAny(lowerText, "unauthorized", "forbidden", "authentication", "authorization", "bad token", "invalid token") {
		return DMEErrorAuth
	}
	if status == http.StatusTooManyRequests || (status >= 500 && status <= 599) ||
		numericCode == http.StatusTooManyRequests || (numericCode >= 500 && numericCode <= 599) {
		return DMEErrorRetryable
	}
	if cause != nil && configtransport.IsTransient(cause) {
		return DMEErrorRetryable
	}
	if (status >= 400 && status < 500) || (numericCode >= 400 && numericCode < 500) ||
		containsAny(lowerText, "unknown class", "invalid", "malformed", "validation", "missing", "not supported", "unsupported", "cannot parse") {
		return DMEErrorValidation
	}
	return DMEErrorPermanent
}

func dmeCategoryPriority(category DMEErrorCategory) int {
	switch category {
	case DMEErrorAuth:
		return 4
	case DMEErrorRetryable:
		return 3
	case DMEErrorValidation:
		return 2
	default:
		return 1
	}
}

func dmeErrorDetails(raw []byte) []dmeErrorDetail {
	var env dmeEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil
	}
	var out []dmeErrorDetail
	for _, item := range env.IMData {
		out = append(out, collectDMEErrorDetails(item)...)
	}
	return out
}

func collectDMEErrorDetails(item map[string]json.RawMessage) []dmeErrorDetail {
	var out []dmeErrorDetail
	for class, raw := range item {
		if class == "error" {
			var errMO dmeErrorMO
			if err := json.Unmarshal(raw, &errMO); err == nil {
				code := strings.TrimSpace(errMO.Attributes.Code)
				text := strings.TrimSpace(errMO.Attributes.Text)
				if code != "" || text != "" {
					out = append(out, dmeErrorDetail{code: code, text: text})
				}
			}
			continue
		}
		var mo dmeMO
		if err := json.Unmarshal(raw, &mo); err != nil {
			continue
		}
		for _, child := range mo.Children {
			out = append(out, collectDMEErrorDetails(child)...)
		}
	}
	return out
}

// dmeErrors retains the original internal helper shape for callers that only
// need safe human-readable entries.
func dmeErrors(raw []byte) []string {
	details := dmeErrorDetails(raw)
	out := make([]string, 0, len(details))
	for _, detail := range details {
		msg := safeDMEValue(detail.text, maxDMEContextLength)
		if code := safeDMECode(detail.code); code != "" {
			msg = strings.TrimSpace("code=" + code + " " + msg)
		}
		if msg != "" {
			out = append(out, msg)
		}
	}
	return out
}

func safeDMEMethod(method string) string {
	method = strings.ToUpper(strings.TrimSpace(method))
	if len(method) > maxDMEMethodLength {
		return ""
	}
	for _, r := range method {
		if r < 'A' || r > 'Z' {
			return ""
		}
	}
	return method
}

func safeDMECode(code string) string {
	code = strings.TrimSpace(code)
	if len(code) > maxDMECodeLength {
		return ""
	}
	for _, r := range code {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("._:-", r) {
			return ""
		}
	}
	return code
}

func safeDMEValue(value string, maxRunes int) string {
	value = configtransport.RedactCredentials(value)
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if maxRunes > 0 && len(runes) > maxRunes {
		value = string(runes[:maxRunes-1]) + "…"
	}
	return value
}

func safeHTTPStatus(status int) int {
	if status < 100 || status > 599 {
		return 0
	}
	return status
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func containsAny(value string, markers ...string) bool {
	for _, marker := range markers {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}
