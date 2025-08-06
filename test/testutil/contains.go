package testutil

import "strings"

// ContainsAny checks if any of the given strings are contained in the target string
func ContainsAny(target string, items ...string) bool {
	for _, item := range items {
		if strings.Contains(target, item) {
			return true
		}
	}
	return false
}
