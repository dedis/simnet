package metrics

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const testLines = `
0,0,0,0,0
 1 ,2 ,3, 4,5 
1,2,

`

func TestStats_New(t *testing.T) {
	stats := NewStats()
	require.NotNil(t, stats.Tags)
}

func TestStats_NodeStats(t *testing.T) {
	lines := []byte(testLines)
	reader := bytes.NewReader(lines)

	ns := NewNodeStats(reader, time.Unix(0, 0), time.Unix(1, 0))
	require.NotNil(t, ns)
	require.Equal(t, 2, len(ns.Timestamps))
}
