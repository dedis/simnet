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

func TestNodeStats_Max(t *testing.T) {
	ns := NodeStats{
		Timestamps: []int64{0, 0, 0, 0},
		CPU:        []uint64{1, 2, 3, 4},
		Memory:     []uint64{7, 3, 2, 1},
		TxBytes:    []uint64{1, 2, 6, 3},
		RxBytes:    []uint64{1, 5, 2, 3},
	}

	cpu, mem, tx, rx := ns.Max()
	require.Equal(t, uint64(4), cpu)
	require.Equal(t, uint64(7), mem)
	require.Equal(t, uint64(6), tx)
	require.Equal(t, uint64(5), rx)
}

func TestNodeStats_Average(t *testing.T) {
	ns := NodeStats{
		Timestamps: []int64{0, 0, 0, 0},
		CPU:        []uint64{1, 2, 3, 4},
		Memory:     []uint64{4, 3, 2, 1},
		TxBytes:    []uint64{1, 2, 4, 3},
		RxBytes:    []uint64{1, 4, 2, 3},
	}

	cpu, mem, tx, rx := ns.Average()
	require.Equal(t, float64(2.5), cpu)
	require.Equal(t, float64(2.5), mem)
	require.Equal(t, float64(2.5), tx)
	require.Equal(t, float64(2.5), rx)
}
