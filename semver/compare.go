package semver

import (
	"github.com/Masterminds/semver/v3"
	"github.com/maruel/natural"
)

const (
	cmpLt = -1
	cmpEq = 0
	cmpGt = 1
)

// Compare orders two versions, using natural sort for prerelease tags
// so e.g. "rc2" < "rc10". Nil sorts before non-nil; two nils are equal.
func Compare(a, b *semver.Version) int {
	switch {
	case a == nil && b == nil:
		return cmpEq
	case a == nil:
		return cmpLt
	case b == nil:
		return cmpGt
	}

	if a.Major() != b.Major() {
		if a.Major() > b.Major() {
			return cmpGt
		}
		return cmpLt
	}
	if a.Minor() != b.Minor() {
		if a.Minor() > b.Minor() {
			return cmpGt
		}
		return cmpLt
	}
	if a.Patch() != b.Patch() {
		if a.Patch() > b.Patch() {
			return cmpGt
		}
		return cmpLt
	}

	aPre, bPre := a.Prerelease(), b.Prerelease()
	switch {
	case aPre == "" && bPre == "":
		return cmpEq
	case aPre == "":
		// per semver: a release outranks any prerelease of the same X.Y.Z
		return cmpGt
	case bPre == "":
		return cmpLt
	}

	switch {
	case natural.Less(aPre, bPre):
		return cmpLt
	case natural.Less(bPre, aPre):
		return cmpGt
	default:
		return cmpEq
	}
}

// Equal reports whether a and b are the same version.
func Equal(a, b *semver.Version) bool { return Compare(a, b) == cmpEq }

// GreaterThan reports whether a > b.
func GreaterThan(a, b *semver.Version) bool { return Compare(a, b) == cmpGt }

// LessThan reports whether a < b.
func LessThan(a, b *semver.Version) bool { return Compare(a, b) == cmpLt }
