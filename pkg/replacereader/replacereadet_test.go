package replacereader_test

import (
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user-Xlll/go-ci/pkg/replacereader"
)

func TestReplaceAll(t *testing.T) {

	src := `a = 5
	b = 4
	c = 3`

	rules := []replacereader.Rule{{Re: regexp.MustCompile("(?<key>a)\\s*=\\s*(?<value>\\d+)"), Template: "$key=10"}}

	var (
		glineNum  int
		goldValue string
		gnewValue string
	)
	data, err := io.ReadAll(replacereader.NewReplaceReader(strings.NewReader(src), rules, func(lineNum int, oldValue, newValue string, rule *replacereader.Rule) {
		assert.Equal(t, &rules[0], rule)
		glineNum, goldValue, gnewValue = lineNum, oldValue, newValue
	}))
	require.NoError(t, err)
	require.Equal(t, `a=10
	b = 4
	c = 3`, string(data))

	assert.Equal(t, 1, glineNum)
	assert.Equal(t, `a = 5`, goldValue)
	assert.Equal(t, `a=10`, gnewValue)
}
