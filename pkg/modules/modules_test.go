package modules_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/user-Xlll/go-ci/pkg/modules"
)

func TestMakeShortNames(t *testing.T) {
	paths := []string{
		"1/2/3",
		"1/3",
		"4",
		"5",
		"4/4",
		"6",
		"7/8/9",
		"6/8/9",
		"8/9"}

	assert.Equal(t, map[string]string{
		"1/2/3": "3_2",
		"1/3":   "3_1",
		"4":     "4",
		"5":     "5",
		"4/4":   "4_4",
		"6":     "6",
		"7/8/9": "9_7",
		"6/8/9": "9_6",
		"8/9":   "9"},
		modules.MakeShortNames(paths))
}
