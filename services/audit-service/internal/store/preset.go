package store

// PresetName is the versioned, named identifier of the sole read-time
// lifecycle-noise preset (ADR-0067 §D3, criteria AUD-28) — reported verbatim
// in the response envelope when the preset is applied. This file is the ONE
// place `audit-service` defines preset membership: both the SQL predicate (in
// List, via presetExcludedOutcomes) and the name a caller reports are derived
// from here. Widening the excluded set is a criteria change and therefore
// requires a new ADR (AUD-28) — it is not a config knob.
//
// The excluded outcomes are content-free charter-lifecycle facts emitted by
// the adaptive plan-request cycle (services/api/internal/workstream/store.go):
// plan.requested / plan.expired. Nothing else is excluded. This is a read-time
// lens, not a write-side classification — every event remains chained,
// stored, and retrievable via the unfiltered path (EVD-9, EVD-11).
const PresetName = "governed-actions@1"

// governedActionsV1ExcludedOutcomes is the exact excluded-outcome set for the
// governed-actions@1 preset. Order is irrelevant; membership is exhaustive —
// exactly {plan.requested, plan.expired}, nothing else.
var governedActionsV1ExcludedOutcomes = []string{
	"plan.requested",
	"plan.expired",
}

// presetExcludedOutcomes returns the excluded-outcome set for the given
// preset flag, or nil when the preset is not applied. Returning nil (rather
// than an empty, non-nil slice) matters: List binds it as a nullable text[]
// parameter, and `$n::text[] IS NULL` is how the query skips the exclusion
// entirely when no preset is requested.
func presetExcludedOutcomes(preset bool) []string {
	if !preset {
		return nil
	}
	return governedActionsV1ExcludedOutcomes
}
