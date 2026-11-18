package helm_test

import (
	"fmt"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "embed"

	"github.com/user-Xlll/go-ci/cmd/helm"
)

//go:embed testdata/values.yml
var testData []byte

func TestQueryValuesPath(t *testing.T) {
	var cfg map[string]any

	require.NoError(t, yaml.Unmarshal(testData, &cfg))

	mp := cfg["a"].(map[string]any)["map"].(map[string]any)
	pairs := make([]any, 0, len(mp))
	keys := make([]any, 0, len(mp))
	vals := make([]any, 0, len(mp))
	valsX := make([]any, 0, len(mp))

	for k, v := range mp {
		pairs = append(pairs, map[string]any{"key": k, "value": v})
		keys = append(keys, k)
		vals = append(vals, v)

		if mv, ok := v.(map[string]any); ok {
			valsX = append(valsX, mv["x"])
		}
	}

	arr := cfg["a"].(map[string]any)["array"].([]any)
	arrKeys := make([]any, len(arr))

	for i, v := range arr {
		arrKeys[i] = (v).(map[string]any)["key"]
	}

	testCases := map[string]any{
		"a":                cfg["a"],
		"a.array":          cfg["a"].(map[string]any)["array"],
		"a.array[*]":       cfg["a"].(map[string]any)["array"],
		"a.array[1]":       cfg["a"].(map[string]any)["array"].([]any)[1],
		"a.array[*].key":   arrKeys,
		"a.map[*]":         pairs,
		"a.map[*:key]":     keys,
		"a.map[*:value]":   vals,
		"a.map[*].key":     keys,
		"a.map[*].value":   vals,
		"a.map[*:value].x": valsX,
	}

	for path, value := range testCases {
		t.Run(path, func(t1 *testing.T) {
			if _, ok := value.([]any); ok {
				assert.ElementsMatch(t1, value, helm.QueryValuesPath(cfg, path))
			} else {
				assert.Equal(t1, value, helm.QueryValuesPath(cfg, path))
			}

		})
	}
}

func ExampleIndexToPlace() {
	data := `1
12
123
1234
12345`

	for i := range len(data) + 1 {
		fmt.Println(helm.IndexToPlace(i, data))
	}
	// Output: {0 1 1}
	// {1 1 2}
	// {2 2 1}
	// {3 2 2}
	// {4 2 3}
	// {5 3 1}
	// {6 3 2}
	// {7 3 3}
	// {8 3 4}
	// {9 4 1}
	// {10 4 2}
	// {11 4 3}
	// {12 4 4}
	// {13 4 5}
	// {14 5 1}
	// {15 5 2}
	// {16 5 3}
	// {17 5 4}
	// {18 5 5}
	// {19 5 6}
}
