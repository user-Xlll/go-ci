package npm_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user-Xlll/go-ci/pkg/modules/npm"
)

func TestNpm(t *testing.T) {
	modules, err := npm.FindNPMModules("testdata")
	require.NoError(t, err, "FindNPMModules()")

	slices.SortFunc(modules, func(a, b *npm.ModuleInfo) int {
		return strings.Compare(a.Name, b.Name)
	})

	require.Len(t, modules, 2)

	assert.Equal(t, "@example-org/root", modules[0].Name)
	assert.Len(t, modules[0].Scripts, 1)
	assert.Len(t, modules[0].Scripts["build"], 2)

	assert.Equal(t, "infra-cloud", modules[1].Name)
	assert.Len(t, modules[1].Scripts, 2)
	assert.Len(t, modules[1].Scripts["build"], 1)
	assert.Len(t, modules[1].Scripts["build:storybook"], 1)
}
