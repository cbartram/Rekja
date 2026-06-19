package thunderstore

import (
	"fmt"
	"strconv"
	"strings"
)

// CompareVersions compares Thunderstore's three-part semantic version strings.
func CompareVersions(left string, right string) (int, error) {
	leftParts, err := parseVersion(left)
	if err != nil {
		return 0, err
	}
	rightParts, err := parseVersion(right)
	if err != nil {
		return 0, err
	}
	for i := 0; i < len(leftParts); i++ {
		if leftParts[i] > rightParts[i] {
			return 1, nil
		}
		if leftParts[i] < rightParts[i] {
			return -1, nil
		}
	}
	return 0, nil
}

func parseVersion(version string) ([3]int, error) {
	var parsed [3]int
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		return parsed, fmt.Errorf("version must have three parts: %s", version)
	}
	for index, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return parsed, fmt.Errorf("invalid version part %q in %s", part, version)
		}
		parsed[index] = value
	}
	return parsed, nil
}

// ParseDependency parses Namespace-Name-Version. Package names may contain
// underscores but not dashes in Thunderstore manifests.
func ParseDependency(value string) (DependencyRef, error) {
	parts := strings.Split(value, "-")
	if len(parts) < 3 {
		return DependencyRef{}, fmt.Errorf("invalid dependency reference: %s", value)
	}
	version := parts[len(parts)-1]
	name := strings.Join(parts[1:len(parts)-1], "-")
	ref := DependencyRef{
		Namespace: parts[0],
		Name:      name,
		Version:   version,
	}
	if _, err := parseVersion(version); err != nil {
		return DependencyRef{}, fmt.Errorf("invalid dependency version in %s: %w", value, err)
	}
	return ref, nil
}
