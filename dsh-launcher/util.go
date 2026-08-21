package main

import "regexp"

var versionRe = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)

// parseVersion reports whether s looks like a semver (x.y.z with optional
// prerelease). Used to filter the registry "time" map to real versions.
func parseVersion(s string) (string, error) {
	if !versionRe.MatchString(s) {
		return "", errNotVersion
	}
	return s, nil
}

type versionError struct{}

func (versionError) Error() string { return "not a version" }

var errNotVersion = versionError{}
