package model

import (
	"sort"
	"strings"
	"sync"
)

// UpstreamModelProfile carries the per-model metadata the Grok Build
// /v1/models catalog advertises. grok-build (xai-grok-shell remote/client.rs)
// drives its reasoning-effort menu, default effort, and context budget from
// exactly these fields instead of a compiled-in table, so the gateway mirrors
// the same source of truth once an account has synchronized its catalog.
type UpstreamModelProfile struct {
	// ReasoningEfforts is the ordered menu from `reasoning_efforts[].value`,
	// filtered to the grok-build wire vocabulary. Empty means the catalog did
	// not publish a menu and static capabilities apply.
	ReasoningEfforts []string
	// DefaultReasoningEffort is the menu entry flagged `default: true`, or the
	// top-level `reasoning_effort` when the menu carries no flag.
	DefaultReasoningEffort string
	// SupportsReasoningEffort mirrors `supports_reasoning_effort`.
	SupportsReasoningEffort bool
	// ContextWindow mirrors `context_window` (tokens); zero when absent.
	ContextWindow int
	// MaxCompletionTokens mirrors `max_completion_tokens`; zero when absent.
	MaxCompletionTokens int
	// SupportsBackendSearch mirrors `supports_backend_search`.
	SupportsBackendSearch bool
}

var (
	upstreamProfilesMu sync.RWMutex
	upstreamProfiles   = map[string]UpstreamModelProfile{}
)

// NormalizeUpstreamProfile validates and canonicalizes a catalog profile:
// effort values are lower-cased, de-duplicated, restricted to known tiers,
// and the default is dropped when the menu does not contain it.
func NormalizeUpstreamProfile(profile UpstreamModelProfile) UpstreamModelProfile {
	efforts := make([]string, 0, len(profile.ReasoningEfforts))
	seen := make(map[string]struct{}, len(profile.ReasoningEfforts))
	for _, raw := range profile.ReasoningEfforts {
		value := strings.ToLower(strings.TrimSpace(raw))
		if !IsKnownReasoningEffort(value) {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		efforts = append(efforts, value)
	}
	profile.ReasoningEfforts = efforts
	profile.DefaultReasoningEffort = strings.ToLower(strings.TrimSpace(profile.DefaultReasoningEffort))
	if _, exists := seen[profile.DefaultReasoningEffort]; !exists {
		profile.DefaultReasoningEffort = ""
	}
	if len(efforts) > 0 {
		profile.SupportsReasoningEffort = true
	}
	if profile.ContextWindow < 0 {
		profile.ContextWindow = 0
	}
	if profile.MaxCompletionTokens < 0 {
		profile.MaxCompletionTokens = 0
	}
	return profile
}

// RegisterUpstreamModelProfile stores the catalog profile for an upstream model
// slug (without provider prefix). Accounts share this map. A later sync may
// expand a real effort menu or raise the context window. It does not replace
// either with an empty menu, a none-only menu, a narrower menu, or a smaller
// context window. An empty or none-only menu leaves the static table in place
// when that table already lists a real effort.
func RegisterUpstreamModelProfile(upstreamModel string, profile UpstreamModelProfile) {
	slug := strings.ToLower(externalModelSlug(upstreamModel))
	if slug == "" {
		return
	}
	normalized := NormalizeUpstreamProfile(profile)
	upstreamProfilesMu.Lock()
	existing, ok := upstreamProfiles[slug]
	upstreamProfiles[slug] = mergeUpstreamProfile(slug, existing, ok, normalized)
	upstreamProfilesMu.Unlock()
}

func mergeUpstreamProfile(slug string, existing UpstreamModelProfile, hasExisting bool, incoming UpstreamModelProfile) UpstreamModelProfile {
	merged := incoming
	switch {
	case !menuHasRealEffort(incoming.ReasoningEfforts):
		if hasExisting && menuHasRealEffort(existing.ReasoningEfforts) {
			merged.ReasoningEfforts = append([]string(nil), existing.ReasoningEfforts...)
			merged.DefaultReasoningEffort = existing.DefaultReasoningEffort
			merged.SupportsReasoningEffort = existing.SupportsReasoningEffort
		} else if staticMenuHasRealEffort(slug) {
			merged.ReasoningEfforts = nil
			merged.DefaultReasoningEffort = ""
			merged.SupportsReasoningEffort = false
		}
	case hasExisting && menuHasRealEffort(existing.ReasoningEfforts) && !menuCovers(existing.ReasoningEfforts, incoming.ReasoningEfforts):
		merged.ReasoningEfforts = unionReasoningMenu(existing.ReasoningEfforts, incoming.ReasoningEfforts)
		merged.DefaultReasoningEffort = existing.DefaultReasoningEffort
		merged.SupportsReasoningEffort = true
	}
	if menuHasRealEffort(merged.ReasoningEfforts) && staticMenuHasRealEffort(slug) {
		merged.ReasoningEfforts = unionReasoningMenu(merged.ReasoningEfforts, grokReasoningCapabilities[slug])
		merged.SupportsReasoningEffort = true
		if !menuContains(merged.ReasoningEfforts, merged.DefaultReasoningEffort) {
			merged.DefaultReasoningEffort = ""
		}
	}
	if hasExisting && existing.ContextWindow > merged.ContextWindow {
		merged.ContextWindow = existing.ContextWindow
	}
	return merged
}

func menuHasRealEffort(efforts []string) bool {
	for _, effort := range efforts {
		if effort != "" && effort != ReasoningEffortNone {
			return true
		}
	}
	return false
}

func staticMenuHasRealEffort(slug string) bool {
	return menuHasRealEffort(grokReasoningCapabilities[slug])
}

func menuContains(efforts []string, effort string) bool {
	if effort == "" {
		return false
	}
	for _, value := range efforts {
		if value == effort {
			return true
		}
	}
	return false
}

func unionReasoningMenu(base, extra []string) []string {
	out := make([]string, 0, len(base)+len(extra))
	seen := make(map[string]struct{}, len(base)+len(extra))
	for _, list := range [][]string{base, extra} {
		for _, effort := range list {
			if _, ok := seen[effort]; ok {
				continue
			}
			seen[effort] = struct{}{}
			out = append(out, effort)
		}
	}
	return out
}

func menuCovers(existing, incoming []string) bool {
	have := make(map[string]struct{}, len(incoming))
	for _, effort := range incoming {
		have[effort] = struct{}{}
	}
	for _, effort := range existing {
		if _, ok := have[effort]; !ok {
			return false
		}
	}
	return true
}

// UpstreamProfile returns the registered catalog profile for a model slug.
func UpstreamProfile(upstreamModel string) (UpstreamModelProfile, bool) {
	slug := strings.ToLower(externalModelSlug(upstreamModel))
	if slug == "" {
		return UpstreamModelProfile{}, false
	}
	upstreamProfilesMu.RLock()
	profile, ok := upstreamProfiles[slug]
	upstreamProfilesMu.RUnlock()
	if !ok {
		return UpstreamModelProfile{}, false
	}
	profile.ReasoningEfforts = append([]string(nil), profile.ReasoningEfforts...)
	return profile, true
}

// UpstreamProfileSlugs lists registered slugs in stable order (for admin/debug views).
func UpstreamProfileSlugs() []string {
	upstreamProfilesMu.RLock()
	slugs := make([]string, 0, len(upstreamProfiles))
	for slug := range upstreamProfiles {
		slugs = append(slugs, slug)
	}
	upstreamProfilesMu.RUnlock()
	sort.Strings(slugs)
	return slugs
}

// ResetUpstreamProfiles clears every registered profile. Tests use it to
// isolate catalog state between cases.
func ResetUpstreamProfiles() {
	upstreamProfilesMu.Lock()
	upstreamProfiles = map[string]UpstreamModelProfile{}
	upstreamProfilesMu.Unlock()
}
