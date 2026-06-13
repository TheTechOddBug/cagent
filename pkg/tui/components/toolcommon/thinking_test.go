package toolcommon

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestThinkingDescription(t *testing.T) {
	t.Parallel()

	cases := []struct {
		label string
		want  string
	}{
		{"", ""},
		{"off", "off"},
		{"adaptive", "adaptive"},
		{"high", "high"},
		{"minimal", "minimal"},
		{"8192", "8.2K tokens"},
		{"500", "500 tokens"},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, ThinkingDescription(c.label), "label %q", c.label)
	}
}
