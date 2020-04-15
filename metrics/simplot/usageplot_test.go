package main

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.dedis.ch/simnet/metrics"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
)

func TestUsagePlot_Process(t *testing.T) {
	n := 5
	up := newUsagePlot(false, false, false, false)

	p, err := up.Process(makeStats(n))
	require.NoError(t, err)
	require.NotNil(t, p)

	up = newUsagePlot(true, true, true, true)

	var values []interface{}
	up.processor = func(p *plot.Plot, vv ...interface{}) error {
		values = vv
		return nil
	}

	p, err = up.Process(makeStats(n))
	require.NoError(t, err)
	require.NotNil(t, p)
	require.Equal(t, 24, len(values))
	require.Equal(t, n, values[1].(plotter.XYs).Len())

	ticks := p.X.Tick.Marker.Ticks(0, 5)
	require.Len(t, ticks, 6)
	require.Equal(t, "A, B", ticks[0].Label)
}

func TestUsagePlot_ProcessFailure(t *testing.T) {
	n := 5
	e := errors.New("processor error")
	up := newUsagePlot(false, false, false, false)
	up.processor = func(*plot.Plot, ...interface{}) error {
		return e
	}

	_, err := up.Process(makeStats(n))
	require.Error(t, err)
	require.Equal(t, e, err)

	e = errors.New("factory error")
	up.factory = func() (*plot.Plot, error) {
		return nil, e
	}
	_, err = up.Process(makeStats(n))
	require.Error(t, err)
	require.Equal(t, e, err)
}

func makeValues(n int) []uint64 {
	return make([]uint64, n)
}

func makeNodeStats(n int) metrics.NodeStats {
	return metrics.NodeStats{
		Timestamps: make([]int64, n),
		TxBytes:    makeValues(n),
		RxBytes:    makeValues(n),
		CPU:        makeValues(n),
		Memory:     makeValues(n),
	}
}

func makeStats(n int) *metrics.Stats {
	return &metrics.Stats{
		Timestamp: time.Now().Unix(),
		Tags: map[int64]string{
			0:             "A",
			1:             "B",
			9999999999999: "C",
		},
		Nodes: map[string]metrics.NodeStats{
			"node0": makeNodeStats(n),
			"node1": makeNodeStats(n),
			"node2": makeNodeStats(n),
		},
	}
}
