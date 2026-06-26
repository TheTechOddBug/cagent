package tuitest

import (
	"fmt"
	"regexp"
	"strings"
)

// Matcher tests a captured frame. Matchers are the vocabulary WaitFor and
// Assert speak; they replace Selenium's brittle selectors with simple,
// composable predicates over the rendered text.
type Matcher interface {
	match(frame string) bool
	describe() string
}

type matcherFunc struct {
	fn   func(string) bool
	desc string
}

func (m matcherFunc) match(frame string) bool { return m.fn(frame) }
func (m matcherFunc) describe() string        { return m.desc }

// Contains matches when the frame contains substr.
func Contains(substr string) Matcher {
	return matcherFunc{
		fn:   func(frame string) bool { return strings.Contains(frame, substr) },
		desc: fmt.Sprintf("frame to contain %q", substr),
	}
}

// ContainsAll matches when the frame contains every substring.
func ContainsAll(substrs ...string) Matcher {
	return matcherFunc{
		fn: func(frame string) bool {
			for _, s := range substrs {
				if !strings.Contains(frame, s) {
					return false
				}
			}
			return true
		},
		desc: fmt.Sprintf("frame to contain all of %q", substrs),
	}
}

// Absent matches when the frame does NOT contain substr. Pair it with a prior
// WaitFor on something positive, since "absent" is trivially true before the
// UI has rendered anything.
func Absent(substr string) Matcher {
	return matcherFunc{
		fn:   func(frame string) bool { return !strings.Contains(frame, substr) },
		desc: fmt.Sprintf("frame to NOT contain %q", substr),
	}
}

// Matches matches when the frame matches the regular expression pattern.
// It panics if pattern does not compile, surfacing the mistake at test
// authoring time.
func Matches(pattern string) Matcher {
	re := regexp.MustCompile(pattern)
	return matcherFunc{
		fn:   func(frame string) bool { return re.MatchString(frame) },
		desc: fmt.Sprintf("frame to match /%s/", pattern),
	}
}

// Not inverts a matcher.
func Not(m Matcher) Matcher {
	return matcherFunc{
		fn:   func(frame string) bool { return !m.match(frame) },
		desc: "NOT " + m.describe(),
	}
}
