package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"

	"go.dedis.ch/simnet/engine"
	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/plotutil"
	"gonum.org/v1/plot/vg"
)

var re = regexp.MustCompile("node[0-9]+")

func checkErr(err error) {
	if err != nil {
		fmt.Printf("Error: %+v\n", err)
		os.Exit(1)
	}
}

func extractLabel(name string) string {
	return re.FindString(name)
}

func main() {
	withTx := flag.Bool("tx", false, "add tx to the plot")
	withRx := flag.Bool("rx", false, "add rx to the plot")
	withCPU := flag.Bool("cpu", false, "add cpu to the plot")
	withMem := flag.Bool("mem", false, "add memory to the plot")
	flag.Parse()

	f, err := os.Open("result.json")
	checkErr(err)

	stats := &engine.Stats{}
	dec := json.NewDecoder(bufio.NewReader(f))
	err = dec.Decode(stats)
	f.Close()
	checkErr(err)

	plot, err := plot.New()
	checkErr(err)

	lines := make([]interface{}, 0)
	for node, ns := range stats.Nodes {
		label := extractLabel(node)

		if *withTx {
			tx := addTxBytes(plot, ns)
			lines = append(lines, label+"-tx", tx)
		}

		if *withRx {
			rx := addRxBytes(plot, ns)
			lines = append(lines, label+"-rx", rx)
		}

		if *withCPU {
			cpu := addCPU(plot, ns)
			lines = append(lines, label+"-cpu", cpu)
		}

		if *withMem {
			mem := addMemory(plot, ns)
			lines = append(lines, label+"-mem", mem)
		}
	}

	err = plotutil.AddLinePoints(plot, lines...)
	checkErr(err)

	err = plot.Save(12*vg.Inch, 8*vg.Inch, "example.png")
	checkErr(err)
}

func addTxBytes(p *plot.Plot, ns engine.NodeStats) plotter.XYs {
	points := make(plotter.XYs, len(ns.Timestamps))
	for i := range points {
		points[i].X = float64(ns.Timestamps[i])
		points[i].Y = float64(ns.TxBytes[i])
	}

	return points
}

func addRxBytes(p *plot.Plot, ns engine.NodeStats) plotter.XYs {
	points := make(plotter.XYs, len(ns.Timestamps))
	for i := range points {
		points[i].X = float64(ns.Timestamps[i])
		points[i].Y = float64(ns.RxBytes[i])
	}

	return points
}

func addCPU(p *plot.Plot, ns engine.NodeStats) plotter.XYs {
	points := make(plotter.XYs, len(ns.CPU))
	for i := range points {
		points[i].X = float64(ns.Timestamps[i])
		points[i].Y = float64(ns.CPU[i])
	}

	return points
}

func addMemory(p *plot.Plot, ns engine.NodeStats) plotter.XYs {
	points := make(plotter.XYs, len(ns.Memory))
	for i := range points {
		points[i].X = float64(ns.Timestamps[i])
		points[i].Y = float64(ns.Memory[i])
	}

	return points
}
