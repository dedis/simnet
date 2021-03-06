package main

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.dedis.ch/simnet/metrics"
	"gonum.org/v1/plot"
)

func TestPlotter_Main(t *testing.T) {
	input, err := ioutil.TempFile(os.TempDir(), "plotter")
	defer input.Close()
	require.NoError(t, err)

	enc := json.NewEncoder(input)
	require.NoError(t, enc.Encode(makeStats(5)))

	dir, err := ioutil.TempDir(os.TempDir(), "plotter")
	require.NoError(t, err)

	output := filepath.Join(dir, "example.png")

	os.Args = []string{os.Args[0], "-input", input.Name(), "graph", "-output", output, "tx"}

	defer os.Remove(input.Name())
	defer os.RemoveAll(dir)

	main()

	os.Args = []string{os.Args[0], "-input", input.Name(), "max"}
	main()

	os.Args = []string{os.Args[0], "-input", input.Name(), "average"}
	main()
}

func TestPlotter_MainMissingInput(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			require.EqualError(t, r.(error), errNoInput)
		} else {
			t.Fatal("expect a panic")
		}
	}()

	os.Args = []string{os.Args[0], "-input", "invalid_name", "graph", "cpu"}
	main()
}

func TestPlotter_MainBadInput(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			require.EqualError(t, r.(error), errInputMalformed)
		} else {
			t.Fatal("expect a panic")
		}
	}()

	f, err := ioutil.TempFile(os.TempDir(), "plotter")
	require.NoError(t, err)
	defer f.Close()
	defer os.Remove(f.Name())

	os.Args = []string{os.Args[0], "-input", f.Name(), "graph", "rx"}
	main()
}

func TestPlotter_MainBadPlot(t *testing.T) {
	usagePlotFactory = testNewUsagePlot
	defer func() {
		usagePlotFactory = newUsagePlot
	}()

	defer func() {
		if r := recover(); r != nil {
			require.EqualError(t, r.(error), errMakePlot)
		} else {
			t.Fatal("expect a panic")
		}
	}()

	f, err := ioutil.TempFile(os.TempDir(), "plotter")
	require.NoError(t, err)
	defer f.Close()
	defer os.Remove(f.Name())

	enc := json.NewEncoder(f)
	err = enc.Encode(&metrics.Stats{
		Nodes: map[string]metrics.NodeStats{
			"node0": {
				Timestamps: []int64{0},
				TxBytes:    []uint64{uint64(math.NaN())},
			},
		},
	})
	require.NoError(t, err)

	os.Args = []string{os.Args[0], "-input", f.Name(), "graph", "mem"}
	main()
}

func TestPlotter_MainBadOutput(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			require.EqualError(t, r.(error), errMakeImage)
		} else {
			t.Fatal("expect a panic")
		}
	}()

	dir, err := ioutil.TempDir(os.TempDir(), "plotter")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	input := filepath.Join(dir, "input.json")
	ioutil.WriteFile(input, []byte("{}"), 0644)

	// Trigger a unsupported format.
	os.Args = []string{os.Args[0], "-input", input, "graph", "-output", "noname", "tx"}
	main()
}

func testFactory() (*plot.Plot, error) {
	return nil, errors.New("factory error")
}

func testNewUsagePlot(bool, bool, bool, bool) usagePlot {
	up := newUsagePlot(false, false, false, false)
	up.factory = testFactory
	return up
}
