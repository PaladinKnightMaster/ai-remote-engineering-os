// Package resource holds the services/api resource-registry domain types: the
// stored Resource and the create/update Input DTO. They mirror the Resource
// schema in packages/shared-schemas/ (T-003) / the OpenAPI fragment.
package resource

import "fmt"

// Types is the closed set of resource types (matches the Resource.type enum in
// the contract). The DB enforces the same set via a CHECK.
var Types = map[string]struct{}{
	"rdp":             {},
	"vnc":             {},
	"ssh":             {},
	"meshcentral":     {},
	"workspace":       {},
	"agentic_desktop": {},
}

// Resource is the stored, server-completed resource.
type Resource struct {
	ID     string            `json:"id"`
	Type   string            `json:"type"`
	Name   string            `json:"name"`
	SiteID string            `json:"siteId,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
}

// Input is the create/update request body. `id` is server-assigned, so it is
// not part of the input.
type Input struct {
	Type   string            `json:"type"`
	Name   string            `json:"name"`
	SiteID string            `json:"siteId,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
}

// Validate checks required fields and the type enum at the API boundary so a
// malformed request fails with 400 rather than as a DB error.
func (in Input) Validate() error {
	if in.Name == "" {
		return fmt.Errorf("name is required")
	}
	if _, ok := Types[in.Type]; !ok {
		return fmt.Errorf("type %q is not a valid resource type", in.Type)
	}
	return nil
}
