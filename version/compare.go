package version

import (
	goversion "github.com/hashicorp/go-version"
	"github.com/maruel/natural"
)

const (
	cmpLt = -1
	cmpEq = 0
	cmpGt = 1
)

// Compare orders two versions, using natural sort for prerelease tags so e.g.
// "rc2" < "rc10". Nil sorts before non-nil; two nils are equal.
func Compare(a, b *goversion.Version) int {
	switch {
	case a == nil && b == nil:
		return cmpEq
	case a == nil:
		return cmpLt
	case b == nil:
		return cmpGt
	}

	if c := compareSegments(a, b); c != 0 {
		return c
	}
	return comparePrerelease(a.Prerelease(), b.Prerelease())
}

// compareSegments compares the numeric components, treating an absent trailing
// segment as zero so 1.2 and 1.2.0 rank equal.
func compareSegments(a, b *goversion.Version) int {
	as, bs := a.Segments64(), b.Segments64()
	for i := range max(len(as), len(bs)) {
		switch av, bv := segment(as, i), segment(bs, i); {
		case av < bv:
			return cmpLt
		case av > bv:
			return cmpGt
		}
	}
	return cmpEq
}

// segment returns the ith component, or zero past the end.
func segment(s []int64, i int) int64 {
	if i < len(s) {
		return s[i]
	}
	return 0
}

// comparePrerelease ranks two prerelease strings naturally, with an empty
// prerelease (a final release) outranking any prerelease per semver.
func comparePrerelease(a, b string) int {
	switch {
	case a == "" && b == "":
		return cmpEq
	case a == "":
		return cmpGt
	case b == "":
		return cmpLt
	case natural.Less(a, b):
		return cmpLt
	case natural.Less(b, a):
		return cmpGt
	default:
		return cmpEq
	}
}

// Equal reports whether a and b are the same version.
func Equal(a, b *goversion.Version) bool { return Compare(a, b) == cmpEq }

// EqualString reports whether two version strings denote the same version,
// tolerating a "v" prefix and missing trailing segments (v1.2 == 1.2.0). A
// string that does not parse falls back to prefix-stripped exact match, so a
// non-semver ref still compares.
func EqualString(a, b string) bool {
	pa, errA := Parse(a)
	pb, errB := Parse(b)
	if errA != nil || errB != nil {
		return RemovePrefix(a) == RemovePrefix(b)
	}
	return Equal(pa, pb)
}

// GreaterThan reports whether a > b.
func GreaterThan(a, b *goversion.Version) bool { return Compare(a, b) == cmpGt }

// LessThan reports whether a < b.
func LessThan(a, b *goversion.Version) bool { return Compare(a, b) == cmpLt }
