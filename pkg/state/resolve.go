package state

import (
	"fmt"

	"github.com/fentas/b/pkg/envmatch"
)

// ResolveProfileIncludes flattens a profile by recursively merging included profiles.
// Returns a new EnvEntry with all included fields merged. The profile's own fields
// override included ones. Returns error on circular includes or missing profiles.
func ResolveProfileIncludes(profile *EnvEntry, allProfiles EnvList) (*EnvEntry, error) {
	if len(profile.Includes) == 0 {
		return profile, nil
	}

	// Collect profiles in dependency order (post-order DFS)
	visited := make(map[string]bool) // fully processed nodes
	stack := make(map[string]bool)   // current recursion stack (cycle detection)
	var order []*EnvEntry
	if err := collectIncludes(profile.Key, allProfiles, visited, stack, &order); err != nil {
		return nil, err
	}

	// Merge in order: earlier profiles are base, later override
	merged := &EnvEntry{
		Key:         profile.Key,
		Description: profile.Description, // never inherited
		Files:       make(map[string]envmatch.GlobConfig),
	}

	for _, p := range order {
		// Merge files (later wins for same glob)
		for glob, gc := range p.Files {
			merged.Files[glob] = gc
		}

		// Concatenate ignores
		merged.Ignore = appendUnique(merged.Ignore, p.Ignore)

		// Last non-empty wins for scalar fields
		if p.Version != "" {
			merged.Version = p.Version
		}
		if p.Strategy != "" {
			merged.Strategy = p.Strategy
		}
		if p.Group != "" {
			merged.Group = p.Group
		}
		if p.OnPreSync != "" {
			merged.OnPreSync = p.OnPreSync
		}
		if p.OnPostSync != "" {
			merged.OnPostSync = p.OnPostSync
		}
	}

	// Includes are fully resolved
	merged.Includes = nil
	return merged, nil
}

// collectIncludes performs a post-order DFS with proper cycle detection.
// visited = fully processed nodes (skip), stack = current recursion path (cycle).
func collectIncludes(key string, profiles EnvList, visited, stack map[string]bool, order *[]*EnvEntry) error {
	if stack[key] {
		return fmt.Errorf("circular include detected: %s", key)
	}
	if visited[key] {
		return nil // already processed via another path
	}

	profile := profiles.Get(key)
	if profile == nil {
		return fmt.Errorf("included profile %q not found", key)
	}

	stack[key] = true
	for _, inc := range profile.Includes {
		if err := collectIncludes(inc, profiles, visited, stack, order); err != nil {
			return err
		}
	}
	stack[key] = false

	visited[key] = true
	*order = append(*order, profile)
	return nil
}

// appendUnique appends items to a slice, skipping duplicates.
func appendUnique(slice []string, items []string) []string {
	seen := make(map[string]bool, len(slice))
	for _, s := range slice {
		seen[s] = true
	}
	for _, item := range items {
		if !seen[item] {
			slice = append(slice, item)
			seen[item] = true
		}
	}
	return slice
}
