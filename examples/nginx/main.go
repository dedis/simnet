package main

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.dedis.ch/simnet"
	"go.dedis.ch/simnet/network"
	"go.dedis.ch/simnet/sim"
	"go.dedis.ch/simnet/sim/kubernetes"
	"golang.org/x/xerrors"
)

var world = []string{
	"per-au tul-us 273.000000",
	"tul-us per-au 50.000000",
	"per-au sql-us 268.500000",
	"per-au ind-us 261.000000",
	"per-au san-us 255.000000",
	"per-au vie-at 246.000000",
	"per-au ham-de 334.500000",
	"per-au bhd-uk 330.000000",
	"per-au eug-us 362.250000",
	"per-au gva-ch 342.000000",
	"per-au cgh-br 306.750000",
	"per-au dac-bd 90.000000",
	"per-au arn-se 292.500000",
	"per-au cdg-fr 321.000000",
	"per-au krt-sd 418.500000",
	"per-au mdw-us 254.250000",
	"per-au lax3-us 216.000000",
}

type simRound struct{}

func (s simRound) Before(simio sim.IO, nodes []sim.NodeInfo) error {
	// Example how to disconnect a one-way link so that node0 cannot contact
	// node1 anymore.
	err := simio.Disconnect("per-au", "tul-us", "sql-us", "ind-us")
	if err != nil {
		return xerrors.Errorf("couldn't disconnect: %v", err)
	}

	// ... and how to revert back.
	err = simio.Reconnect("per-au")
	if err != nil {
		return xerrors.Errorf("couldn't reconnect: %v", err)
	}

	return nil
}

func (s simRound) Execute(simio sim.IO, nodes []sim.NodeInfo) error {
	fmt.Printf("Nodes: %v\n", nodes)

	for _, node := range nodes {
		simio.Tag(node.Name)

		resp, err := http.Get(fmt.Sprintf("http://%s:80", node.Address))
		if err != nil {
			return err
		}

		defer resp.Body.Close()

		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return err
		}

		fmt.Printf("Found page of length %d bytes for %s\n", len(body), node.Name)
		time.Sleep(2 * time.Second)
	}

	file := filepath.Join(os.TempDir(), "simnet-nginx")

	err := simio.FetchStats(time.Now().Add(-5*time.Second), time.Now(), file)
	if err != nil {
		return err
	}

	return nil
}

func (s simRound) After(simio sim.IO, nodes []sim.NodeInfo) error {
	return nil
}

func main() {
	options := []sim.Option{
		sim.WithTopology(
			network.NewFullTopology(parseInputs(world)...),
		),
		sim.WithImage("nginx", nil, nil, sim.NewTCP(80)),
		// Example of a mount of type tmpfs.
		sim.WithTmpFS("/storage", 256*sim.MB),
		// Example of requesting a minimum amount of resources.
		kubernetes.WithResources("20m", "64Mi"),
	}

	kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")

	engine, err := kubernetes.NewStrategy(kubeconfig, options...)
	// engine, err := docker.NewStrategy(options...)
	if err != nil {
		panic(err)
	}

	sim := simnet.NewSimulation(simRound{}, engine)

	err = sim.Run(os.Args)
	if err != nil {
		panic(err)
	}
}

func parseInputs(inputs []string) []network.FullInput {
	out := make([]network.FullInput, len(inputs))
	for i, input := range inputs {
		parts := strings.Split(input, " ")
		latency, err := strconv.ParseFloat(parts[2], 64)
		if err != nil {
			panic(err)
		}

		out[i] = network.FullInput{
			From:    parts[0],
			To:      parts[1],
			Latency: time.Duration(latency) * time.Millisecond,
		}
	}
	return out
}
