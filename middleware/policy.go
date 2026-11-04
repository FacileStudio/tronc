package middleware

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

var tokenName = regexp.MustCompile(`^[a-z][a-z-]*$`)

type cspDirective struct {
	name    string
	sources []string
}

// defaultCSP is the policy an app gets before it extends anything, in the
// order the header lists it. style-src carries 'unsafe-inline' on purpose:
// Svelte transitions and SvelteKit's own markup write inline styles, and a
// style cannot run code, so the trade is worth it. script-src does not.
//
// It is a function, not a variable, so no importing package can weaken the
// suite's policy by mutating a shared slice.
func defaultCSP() []cspDirective {
	return []cspDirective{
		{"default-src", []string{"'self'"}},
		{"base-uri", []string{"'self'"}},
		{"object-src", []string{"'none'"}},
		{"frame-ancestors", []string{"'none'"}},
		{"form-action", []string{"'self'"}},
		{"img-src", []string{"'self'", "data:", "blob:", "https:"}},
		{"connect-src", []string{"'self'"}},
		{"script-src", []string{"'self'"}},
		{"style-src", []string{"'self'", "'unsafe-inline'"}},
	}
}

// BuildCSP renders the default policy extended by extra, plus report-uri when
// set. Sources extend a directive, replace a 'none' default, and a directive
// the default lacks is appended in name order. It refuses a directive name or
// source that would inject a directive or split the header.
func BuildCSP(extra map[string][]string, reportURI string) (string, error) {
	if err := validateCSP(extra, reportURI); err != nil {
		return "", err
	}

	defaults := defaultCSP()
	parts := make([]string, 0, len(defaults)+len(extra)+1)
	known := make(map[string]bool, len(defaults))
	for _, directive := range defaults {
		known[directive.name] = true
		parts = append(parts, renderDirective(directive.name, mergeSources(directive.sources, extra[directive.name])))
	}
	for _, name := range slices.Sorted(maps.Keys(extra)) {
		if !known[name] {
			parts = append(parts, renderDirective(name, mergeSources(nil, extra[name])))
		}
	}
	if reportURI != "" {
		parts = append(parts, "report-uri "+reportURI)
	}
	return strings.Join(parts, "; "), nil
}

func validateCSP(extra map[string][]string, reportURI string) error {
	for name, sources := range extra {
		if !tokenName.MatchString(name) {
			return fmt.Errorf("tronc/middleware: CSP directive %q is not a directive name", name)
		}
		for _, source := range sources {
			if !validSource(source) {
				return fmt.Errorf("tronc/middleware: CSP source %q would corrupt the policy", source)
			}
		}
	}
	if reportURI != "" && !validSource(reportURI) {
		return fmt.Errorf("tronc/middleware: CSP report-uri %q would corrupt the policy", reportURI)
	}
	return nil
}

func validSource(source string) bool {
	return source != "" && !strings.ContainsAny(source, ";, \t\r\n") && !strings.ContainsFunc(source, unicode.IsControl)
}

func mergeSources(defaults, extra []string) []string {
	if len(extra) == 0 {
		return defaults
	}
	if len(defaults) == 1 && defaults[0] == "'none'" {
		defaults = nil
	}
	merged := slices.Clone(defaults)
	for _, source := range extra {
		if !slices.Contains(merged, source) {
			merged = append(merged, source)
		}
	}
	return merged
}

func renderDirective(name string, sources []string) string {
	if len(sources) == 0 {
		return name
	}
	return name + " " + strings.Join(sources, " ")
}

func openerPolicy(value string) (string, error) {
	if value == "" {
		return "same-origin", nil
	}
	allowed := []string{"same-origin", "same-origin-allow-popups", "noopener-allow-popups", "unsafe-none"}
	if !slices.Contains(allowed, value) {
		return "", fmt.Errorf("tronc/middleware: Cross-Origin-Opener-Policy %q is not one of %v", value, allowed)
	}
	return value, nil
}

func permissionsPolicy(allow []string) (string, error) {
	features := []string{"camera", "microphone", "geolocation"}
	for _, feature := range allow {
		if !tokenName.MatchString(feature) {
			return "", fmt.Errorf("tronc/middleware: Permissions-Policy feature %q is not a feature name", feature)
		}
		if !slices.Contains(features, feature) {
			features = append(features, feature)
		}
	}
	parts := make([]string, 0, len(features))
	for _, feature := range features {
		value := "()"
		if slices.Contains(allow, feature) {
			value = "(self)"
		}
		parts = append(parts, feature+"="+value)
	}
	return strings.Join(parts, ", "), nil
}
