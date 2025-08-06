package testutil

import "strings"

// Contains checks if a string contains a substring - helper for tests
func Contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || 
		(len(substr) <= len(s) && s[:len(substr)] == substr) ||
		(len(substr) <= len(s) && s[len(s)-len(substr):] == substr) ||
		strings.Contains(s, substr))
}

// ContainsAny checks if any of the given strings are contained in the target string
func ContainsAny(target string, items ...string) bool {
	for _, item := range items {
		if strings.Contains(target, item) {
			return true
		}
	}
	return false
}
