package skills

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidName_Accepts(t *testing.T) {
	for _, name := range []string{
		"a",
		"abc",
		"my-skill",
		"skill-with-many-dashes",
		"a1b2c3",
		"writing-clearly-and-concisely",
		// 64 chars exactly
		"abcdefghij-abcdefghij-abcdefghij-abcdefghij-abcdefghij-abcdefghi",
	} {
		assert.True(t, ValidName(name), "expected %q to validate", name)
	}
}

func TestValidName_Rejects(t *testing.T) {
	for _, name := range []string{
		"",                  // empty
		"-leading-dash",     // can't start with dash
		"UPPER",             // no uppercase
		"snake_case",        // no underscores
		"with space",        // no spaces
		"path/traversal",    // no slashes
		"..",                // no dots
		"my.skill",          // no dots
		"__reserved",        // reserved prefix
		// 65 chars (one over)
		"abcdefghij-abcdefghij-abcdefghij-abcdefghij-abcdefghij-abcdefghij",
	} {
		assert.False(t, ValidName(name), "expected %q to be rejected", name)
	}
}
