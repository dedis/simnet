package main

import (
	"fmt"
	"regexp"
	"sort"

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
	keys := make([]string, 0, len(stats.Nodes))
	for key := range stats.Nodes {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	lines := make([]interface{}, 0)
	for _, node := range keys {
		ns := stats.Nodes[node]
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

	plot.X.Tick.Marker = tagTicks{
		tags: processTags(stats.Tags),
	}

	err = p.processor(plot, lines...)
	if err != nil {
		return nil, err
	}

	return plot, nil
}

func processTags(raw map[int64]string) map[int]string {
	tags := make(map[int]string)
	for ts, tag := range raw {
		key := int(ts / (1000 * 1000 * 1000)) // ns to sec
		if _, ok := tags[key]; ok {
			tags[key] += ", " + tag
		} else {
			tags[key] = tag
		}
	}
	return tags
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

type tagTicks struct {
	tags map[int]string
}

func (t tagTicks) Ticks(min, max float64) []plot.Tick {
	tks := make([]plot.Tick, int(max-min+1))

	for i := range tks {
		value := float64(int(min) + i)

		tks[i] = plot.Tick{
			Value: value,
			Label: fmt.Sprintf("+%ds", i),
		}
	}

	for key, tag := range t.tags {
		index := key - int(min)
		if index >= 0 && index < len(tks) {
			tks[index].Label = tag
		}
	}

	return tks
}
