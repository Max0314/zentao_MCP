// Package models defines domain types used across the application.
package models

import (
	"regexp"
	"slices"
)

// CompiledRule is a precompiled method+path matching rule.
type CompiledRule struct {
	Methods []string
	Regex   *regexp.Regexp
}

// OperationMatcher evaluates allow/block rules for API operations.
type OperationMatcher struct {
	Allow []CompiledRule
	Block []CompiledRule
}

// NewOperationMatcher creates a matcher with the given allow and block rules.
func NewOperationMatcher(allow, block []CompiledRule) *OperationMatcher {
	return &OperationMatcher{
		Allow: allow,
		Block: block,
	}
}

// IsAllowed returns true if the method+path is allowed and not blocked.
func (m *OperationMatcher) IsAllowed(method, path string) bool {
	var allowed bool

	for _, rule := range m.Allow {
		if !slices.Contains(rule.Methods, method) {
			continue
		}

		if rule.Regex.MatchString(path) {
			allowed = true

			break
		}
	}

	for _, rule := range m.Block {
		if !slices.Contains(rule.Methods, method) {
			continue
		}

		if rule.Regex.MatchString(path) {
			return false
		}
	}

	return allowed
}
