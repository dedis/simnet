package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"go.dedis.ch/simnet/metrics"
	"gonum.org/v1/plot/vg"
)

var usagePlotFactory = newUsagePlot

const (
	// DefaultImageWidth is the default width for the image of the plot
	DefaultImageWidth = 12 * vg.Inch
	// DefaultImageHeight is the default height for the image of the plot
	DefaultImageHeight = 8 * vg.Inch
	// DefaultInputFilePath is the default file path that will be read to find
	// the statistics.
	DefaultInputFilePath = "result.json"

	errNoInput        = "coudln't open the input file"
	errInputMalformed = "couldn't read the statistics"
	errMakePlot       = "couldn't create the plot"
	errMakeImage      = "couldn't save the plot image"
)

func main() {
	flagset := flag.NewFlagSet(os.Args[0], flag.ExitOnError)

	input := flagset.String("input", DefaultInputFilePath, "input file")
	output := flagset.String("output", "example.png", "file path of the output")
	withTx := flagset.Bool("tx", false, "add tx to the plot")
	withRx := flagset.Bool("rx", false, "add rx to the plot")
	withCPU := flagset.Bool("cpu", false, "add cpu to the plot")
	withMem := flagset.Bool("mem", false, "add memory to the plot")
	flagset.Parse(os.Args[1:])

	// Read the statistics from the file and decode the JSON data out of it.
	f, err := os.Open(*input)
	defer f.Close()
	checkErr(err, errNoInput)

	dec := json.NewDecoder(bufio.NewReader(f))

	stats := &metrics.Stats{}
	err = dec.Decode(stats)
	checkErr(err, errInputMalformed)

	// Create the plot with the requested data.
	up := usagePlotFactory(*withTx, *withRx, *withCPU, *withMem)
	plot, err := up.Process(stats)
	checkErr(err, errMakePlot)

	// Save an image of the plot.
	err = plot.Save(DefaultImageWidth, DefaultImageHeight, *output)
	if err != nil {
		// The lib creates the file anyway even if the format is not supported
		// so it needs to be removed on failure.
		os.Remove(*output)
	}
	checkErr(err, errMakeImage)
}

func checkErr(err error, msg string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "Plotter: %+v\n", err)
		panic(msg)
	}
}
