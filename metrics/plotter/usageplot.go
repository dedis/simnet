package main

import (
	"regexp"

	"go.dedis.ch/simnet/metrics"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/plotutil"
)

// mapper is a mapping function that must return a specific unit from the
// statistics.
type mapper func(index int, ns metrics.NodeStats) float64

// usagePlot is a plot factory that can create plot from usage statistics.
type usagePlot struct {
	regex   *regexp.Regexp
	mappers map[string]mapper

	factory   func() (*plot.Plot, error)
	processor func(*plot.Plot, ...interface{}) error
}

func newUsagePlot(tx, rx, cpu, mem bool) usagePlot {
	mappers := make(map[string]mapper)
	if tx {
		mappers["-tx"] = func(index int, ns metrics.NodeStats) float64 {
			return float64(ns.TxBytes[index])
		}
	}
	if rx {
		mappers["-rx"] = func(index int, ns metrics.NodeStats) float64 {
			return float64(ns.RxBytes[index])
		}
	}
	if cpu {
		mappers["-cpu"] = func(index int, ns metrics.NodeStats) float64 {
			return float64(ns.CPU[index])
		}
	}
	if mem {
		mappers["-mem"] = func(index int, ns metrics.NodeStats) float64 {
			return float64(ns.Memory[index])
		}
	}

	return usagePlot{
		regex:     regexp.MustCompile("node[0-9]+"),
		mappers:   mappers,
		factory:   plot.New,
		processor: plotutil.AddLinePoints,
	}
}

// Process takes the statistics and produce the plot that will contain only the
// lines defined by the list of mappers.
func (p usagePlot) Process(stats *metrics.Stats) (*plot.Plot, error) {
	lines := make([]interface{}, 0)
	for node, ns := range stats.Nodes {
		label := p.regex.FindString(node)

		for prefix, m := range p.mappers {
			points := makePoints(ns, m)
			lines = append(lines, label+prefix, points)
		}
	}

	plot, err := p.factory()
	if err != nil {
		return nil, err
	}

	err = p.processor(plot, lines...)
	if err != nil {
		return nil, err
	}

	return plot, nil
}

// makePoints is a helper function to create an array of points using a mapper.
func makePoints(ns metrics.NodeStats, m mapper) plotter.XYs {
	points := make(plotter.XYs, len(ns.Timestamps))
	for i, ts := range ns.Timestamps {
		points[i].X = float64(ts)
		points[i].Y = m(i, ns)
	}

	return points
}
