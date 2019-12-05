package metrics

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

const testLines = `
0,0,0,0,0
 1 ,2 ,3, 4,5 
1,2,

`

func TestStats_NodeStats(t *testing.T) {
	lines := []byte(testLines)
	reader := bytes.NewReader(lines)

	ns := NewNodeStats(reader)
	require.NotNil(t, ns)
	require.Equal(t, 2, len(ns.Timestamps))
}
