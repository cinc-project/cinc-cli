package resolver

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Version is a parsed semantic version. It mirrors Semverse::Version (the gem
// chef uses), so comparison and constraint satisfaction match chef's resolver
// bit for bit. Only the major/minor/patch and an optional pre-release are
// modeled; build metadata is parsed but, like chef, ignored for ordering.
type Version struct {
	Major, Minor, Patch int
	PreRelease          string
	raw                 string
}

// versionRE matches a bare version string (no constraint operator):
// MAJOR[.MINOR[.PATCH]][-PRERELEASE][+BUILD], mirroring Semverse's REGEX.
var versionRE = regexp.MustCompile(`^(\d+)(?:\.(\d+))?(?:\.(\d+))?(?:-([0-9A-Za-z\-.]+))?(?:\+([0-9A-Za-z\-.]+))?$`)

// ParseVersion parses a version string the way Semverse::Version does: a
// missing minor or patch defaults to 0.
func ParseVersion(s string) (Version, error) {
	m := versionRE.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return Version{}, fmt.Errorf("invalid version %q", s)
	}
	v := Version{raw: s, PreRelease: m[4]}
	v.Major, _ = strconv.Atoi(m[1])
	if m[2] != "" {
		v.Minor, _ = strconv.Atoi(m[2])
	}
	if m[3] != "" {
		v.Patch, _ = strconv.Atoi(m[3])
	}
	return v, nil
}

// String renders the version as MAJOR.MINOR.PATCH (with an optional
// -PRERELEASE), which is the form chef writes into a lock's cookbook version.
func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.PreRelease != "" {
		s += "-" + v.PreRelease
	}
	return s
}

// IsZero reports whether the version is 0.0.0 with no pre-release, matching
// Semverse::Version#zero?, which the constraint's greedy-match guard checks.
func (v Version) IsZero() bool {
	return v.Major == 0 && v.Minor == 0 && v.Patch == 0 && v.PreRelease == ""
}

// Compare returns -1, 0, or +1 as v sorts before, equal to, or after other,
// following Semverse's ordering: numeric major/minor/patch, then a present
// pre-release sorts BEFORE the same version without one.
func (v Version) Compare(other Version) int {
	if c := cmpInt(v.Major, other.Major); c != 0 {
		return c
	}
	if c := cmpInt(v.Minor, other.Minor); c != 0 {
		return c
	}
	if c := cmpInt(v.Patch, other.Patch); c != 0 {
		return c
	}
	return comparePreRelease(v.PreRelease, other.PreRelease)
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// comparePreRelease orders pre-release identifiers per SemVer / Semverse: no
// pre-release outranks any pre-release; otherwise compare dot-separated
// identifiers, numeric ones numerically and below alphanumeric ones.
func comparePreRelease(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return 1 // release > pre-release
	}
	if b == "" {
		return -1
	}
	ai, bi := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(ai) && i < len(bi); i++ {
		an, aNum := strconv.Atoi(ai[i])
		bn, bNum := strconv.Atoi(bi[i])
		switch {
		case aNum == nil && bNum == nil:
			if c := cmpInt(an, bn); c != 0 {
				return c
			}
		case aNum == nil: // numeric < alphanumeric
			return -1
		case bNum == nil:
			return 1
		default:
			if c := strings.Compare(ai[i], bi[i]); c != 0 {
				return c
			}
		}
	}
	return cmpInt(len(ai), len(bi))
}

// Constraint is a parsed Semverse::Constraint: an operator and a target
// version. Satisfaction semantics match the gem, including the "~>" pessimistic
// operator and the greedy-match guard that keeps pre-releases from satisfying a
// non-pre-release lower bound.
type Constraint struct {
	Operator string
	Version  Version
	// patchGiven records whether the constraint string included a patch
	// component, which "~>" needs to decide its upper bound.
	minorGiven bool
	patchGiven bool
	raw        string
}

var constraintRE = regexp.MustCompile(`^\s*(>=|<=|~>|~|!=|=|>|<)?\s*(\d+)(?:\.(\d+))?(?:\.(\d+))?(?:-([0-9A-Za-z\-.]+))?(?:\+([0-9A-Za-z\-.]+))?\s*$`)

// ParseConstraint parses a single version constraint string (e.g. ">= 1.0.0",
// "~> 2.0", "= 1.2.3", or a bare "1.2.3" which means "= 1.2.3"), matching
// Semverse::Constraint. For operators other than "~>"/"~", a missing minor or
// patch defaults to 0.
func ParseConstraint(s string) (Constraint, error) {
	m := constraintRE.FindStringSubmatch(s)
	if m == nil {
		return Constraint{}, fmt.Errorf("invalid version constraint %q", s)
	}
	op := m[1]
	if op == "" {
		op = "="
	}
	c := Constraint{Operator: op, raw: s, minorGiven: m[3] != "", patchGiven: m[4] != ""}
	c.Version.Major, _ = strconv.Atoi(m[2])
	if m[3] != "" {
		c.Version.Minor, _ = strconv.Atoi(m[3])
	}
	if m[4] != "" {
		c.Version.Patch, _ = strconv.Atoi(m[4])
	}
	c.Version.PreRelease = m[5]
	return c, nil
}

// normalizeConstraint renders a constraint the way Semverse::Constraint#to_s
// does, which is how chef stores constraints in a lock. For "~>"/"~" only the
// components the author wrote are shown ("~> 2.0" stays "~> 2.0"); for every
// other operator a missing minor/patch defaults to 0 (">= 1" -> ">= 1.0.0").
func normalizeConstraint(c Constraint) string {
	approx := c.Operator == "~>" || c.Operator == "~"
	out := fmt.Sprintf("%s %d", c.Operator, c.Version.Major)
	if !approx || c.minorGiven {
		out += fmt.Sprintf(".%d", c.Version.Minor)
	}
	if !approx || c.patchGiven {
		out += fmt.Sprintf(".%d", c.Version.Patch)
	}
	if c.Version.PreRelease != "" {
		out += "-" + c.Version.PreRelease
	}
	return out
}

// Satisfies reports whether target meets the constraint, following
// Semverse::Constraint#satisfies?.
func (c Constraint) Satisfies(target Version) bool {
	// Greedy-match guard: a pre-release target never satisfies a
	// non-pre-release constraint unless the operator is < or <= (or the
	// constraint is 0.0.0).
	if !c.Version.IsZero() && c.greedyMatch(target) {
		return false
	}
	switch c.Operator {
	case "=":
		return target.Compare(c.Version) == 0
	case "!=":
		return target.Compare(c.Version) != 0
	case ">":
		return target.Compare(c.Version) > 0
	case "<":
		return target.Compare(c.Version) < 0
	case ">=":
		return target.Compare(c.Version) >= 0
	case "<=":
		return target.Compare(c.Version) <= 0
	case "~>", "~":
		return c.approx(target)
	default:
		return false
	}
}

func (c Constraint) greedyMatch(target Version) bool {
	if c.Operator == "<" || c.Operator == "<=" {
		return false
	}
	return target.PreRelease != "" && c.Version.PreRelease == ""
}

// approx implements the "~>" pessimistic operator: min <= target < max, where
// max bumps the last specified component. "~> 2.0" allows [2.0.0, 3.0.0);
// "~> 2.1.3" allows [2.1.3, 2.2.0).
func (c Constraint) approx(target Version) bool {
	min := c.Version
	var max Version
	if !c.patchGiven {
		max = Version{Major: min.Major + 1}
	} else {
		max = Version{Major: min.Major, Minor: min.Minor + 1}
	}
	return min.Compare(target) <= 0 && target.Compare(max) < 0
}
