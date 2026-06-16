// SKIPPED: schema blob too large (954154 bytes; limit 524288 bytes)
package cfgval_system_1791

import "github.com/openconfig/goyang/pkg/yang"

// FamilyRoot is a placeholder; schema generation was skipped.
type FamilyRoot struct{}

// UnzipSchema returns an empty map so ValidateBody skips validation.
func UnzipSchema() (map[string]*yang.Entry, error) {
	return map[string]*yang.Entry{}, nil
}
