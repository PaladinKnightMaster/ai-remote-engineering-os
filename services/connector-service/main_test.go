package main

import (
	"context"
	"strings"
	"testing"
)

// bind-pending validates its flags BEFORE touching the database, so a nil pool is the proof it
// never got that far (a call that reached the pool would panic).
func TestBindPendingRequiresLabelAndResourceID(t *testing.T) {
	for name, args := range map[string][]string{
		"no flags":          {},
		"label only":        {"--label", "loopback-demo"},
		"resource id only":  {"--resource-id", "r1"},
		"empty label":       {"--label", "", "--resource-id", "r1"},
		"empty resource id": {"--label", "loopback-demo", "--resource-id", ""},
	} {
		err := bindPending(context.Background(), nil, args)
		if err == nil || !strings.Contains(err.Error(), "requires --label and --resource-id") {
			t.Errorf("%s: err = %v, want the requires-both-flags refusal", name, err)
		}
	}
}
