package util_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/user-Xlll/go-ci/pkg/util"
)

func TestRenderTemplate(t *testing.T) {

	resolver := func(s string) (string, bool) {
		return fmt.Sprintf("!%s!", s), true
	}

	for from, to := range map[string]string{
		"$A":       "!A!",
		"123$B123": "123!B123!",
		"$C":       "!C!",
		"123$ 456": "123$ 456",
		"123$4A":   "123$4A"} {

		actual, err := util.RenderTemplate(from, resolver)

		if assert.NoError(t, err, from) {
			assert.Equal(t, to, actual)
		}
	}

	_, err := util.RenderTemplate("$A", func(s string) (string, bool) { return "", false })

	assert.ErrorIs(t, err, util.ErrVariableNotFound)
}
