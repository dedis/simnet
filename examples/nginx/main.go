package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"

	"go.dedis.ch/simnet"
	"go.dedis.ch/simnet/network"
	"go.dedis.ch/simnet/sim"
	"go.dedis.ch/simnet/sim/kubernetes"
)

type simRound struct{}

func (s simRound) Configure(simio sim.IO) error {
	return nil
}

func (s simRound) Execute(ctx context.Context, simio sim.IO) error {
	nodes := ctx.Value(sim.NodeInfoKey{}).([]sim.NodeInfo)
	fmt.Printf("Nodes: %v\n", nodes)

	for _, node := range nodes {
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
	}

	return nil
}

func main() {
	options := []sim.Option{
		sim.WithTopology(
			network.NewSimpleTopology(3, 25),
		),
		sim.WithImage("nginx", nil, nil, sim.NewTCP(80)),
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
