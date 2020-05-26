package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/urfave/cli/v2"
	"go.dedis.ch/simnet/metrics"
	"golang.org/x/xerrors"
	"gonum.org/v1/plot/vg"
)

var (
	// HumanSuffixes is a list of human-readable suffixes for bytes.
	HumanSuffixes = []string{"B", "KiB", "MiB", "GiB"}
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

	errNoInput        = "couldn't open the input file"
	errInputMalformed = "couldn't read the statistics"
	errMakePlot       = "couldn't create the plot"
	errMakeImage      = "couldn't save the plot image"
)

func main() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		panic(xerrors.Errorf("couldn't find home directory: %v", err))
	}

	defaultInput := filepath.Join(homeDir, ".config", "simnet", DefaultInputFilePath)

	app := &cli.App{
		Name:  "Simplot",
		Usage: "Parse SimNet results into meaningful visualization",
		Flags: []cli.Flag{
			&cli.PathFlag{
				Name:  "input",
				Value: defaultInput,
			},
		},
		Commands: []*cli.Command{
			{
				Name:  "graph",
				Usage: "generate a graph",
				Flags: []cli.Flag{
					&cli.PathFlag{
						Name:  "output",
						Value: "example.png",
					},
				},
				Subcommands: []*cli.Command{
					{
						Name:  "cpu",
						Usage: "generate a graph of the CPU utilization",
						Action: func(c *cli.Context) error {
							input := c.Path("input")
							output := c.Path("output")
							return generateGraph(input, output, false, false, true, false)
						},
					},
					{
						Name:  "mem",
						Usage: "generate a graph of the memory utilization",
						Action: func(c *cli.Context) error {
							input := c.Path("input")
							output := c.Path("output")
							return generateGraph(input, output, false, false, false, true)
						},
					},
					{
						Name:  "tx",
						Usage: "generate a graph of the transmission bandwidth",
						Action: func(c *cli.Context) error {
							input := c.Path("input")
							output := c.Path("output")
							return generateGraph(input, output, true, false, false, false)
						},
					},
					{
						Name:  "rx",
						Usage: "generate a graph of the reception bandwidth",
						Action: func(c *cli.Context) error {
							input := c.Path("input")
							output := c.Path("output")
							return generateGraph(input, output, false, true, false, false)
						},
					},
				},
			},
			{
				Name:  "max",
				Usage: "retrieve the maximum values of each node",
				Action: func(c *cli.Context) error {
					input := c.Path("input")

					return printMax(input)
				},
			},
			{
				Name:  "average",
				Usage: "retrieve the average values of each node",
				Action: func(c *cli.Context) error {
					input := c.Path("input")

					return printAverage(input)
				},
			},
		},
	}

	err = app.Run(os.Args)
	if err != nil {
		panic(err)
	}
}

func generateGraph(input, output string, withTx, withRx, withCPU, withMem bool) error {
	stats, err := readStats(input)
	if err != nil {
		return err
	}

	// Create the plot with the requested data.
	up := usagePlotFactory(withTx, withRx, withCPU, withMem)
	plot, err := up.Process(stats)
	if err != nil {
		return xerrors.New(errMakePlot)
	}

	// Save an image of the plot.
	err = plot.Save(DefaultImageWidth, DefaultImageHeight, output)
	if err != nil {
		// The lib creates the file anyway even if the format is not supported
		// so it needs to be removed on failure.
		os.Remove(output)

		return xerrors.New(errMakeImage)
	}

	return nil
}

func printMax(input string) error {
	stats, err := readStats(input)
	if err != nil {
		return err
	}

	fmt.Println("Maximum of [CPU Memory Rx Tx]:")

	forEachOrdered(stats, func(node string, ns metrics.NodeStats) {
		cpu, mem, tx, rx := ns.Max()

		fmt.Printf("Node <%s>\t: %8.2f%%\t%s\t%s\t%s\n", node, float64(cpu)/100.0,
			int2human(float64(mem)), int2human(float64(rx)), int2human(float64(tx)))
	})

	return nil
}

func printAverage(input string) error {
	stats, err := readStats(input)
	if err != nil {
		return err
	}

	fmt.Println("Average of [CPU Memory Rx Tx] (Standard deviation):")

	forEachOrdered(stats, func(node string, ns metrics.NodeStats) {
		cpu, mem, tx, rx := ns.Average()
		stddevcpu, stddevmem, stddevtx, stddevrx := ns.StdDev()

		fmt.Printf("Node <%s>\t: %8.2f%% (%.2f%%)\t%s (%s)\t%s (%s)\t%s (%s)\n",
			node,
			cpu/100, stddevcpu/100,
			int2human(mem), int2human(stddevmem),
			int2human(rx), int2human(stddevrx),
			int2human(tx), int2human(stddevtx))
	})

	return nil
}

func forEachOrdered(stats *metrics.Stats, fn func(string, metrics.NodeStats)) {
	keys := make(sort.StringSlice, 0, len(stats.Nodes))
	for node := range stats.Nodes {
		keys = append(keys, node)
	}

	sort.Sort(keys)

	for _, node := range keys {
		fn(node, stats.Nodes[node])
	}
}

func int2human(value float64) string {
	suffix := 0
	for value > 1024 || suffix >= len(HumanSuffixes) {
		value /= 1024
		suffix++
	}

	return fmt.Sprintf("%6.2f %s", value, HumanSuffixes[suffix])
}

func readStats(input string) (*metrics.Stats, error) {
	// Read the statistics from the file and decode the JSON data out of it.
	f, err := os.Open(input)
	if err != nil {
		return nil, xerrors.New(errNoInput)
	}

	defer f.Close()

	dec := json.NewDecoder(bufio.NewReader(f))

	stats := &metrics.Stats{}
	err = dec.Decode(stats)
	if err != nil {
		return nil, xerrors.New(errInputMalformed)
	}

	return stats, nil
}
