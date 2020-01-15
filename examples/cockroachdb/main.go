package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.dedis.ch/simnet"
	"go.dedis.ch/simnet/network"
	"go.dedis.ch/simnet/sim"
	"go.dedis.ch/simnet/sim/kubernetes"
)

type simRound struct{}

func (s simRound) Configure(sio sim.IO) error {
	// Initialise the cluster.
	out, err := sio.Exec("node0", []string{"./cockroach", "init", "--insecure"})
	if err != nil {
		return err
	}

	fmt.Println(string(out))

	fmt.Println("Sleeping 5s")
	time.Sleep(5 * time.Second)

	return nil
}

func (s simRound) Execute(ctx context.Context) error {
	nodes := ctx.Value(sim.NodeInfoKey{}).([]sim.NodeInfo)
	fmt.Printf("Nodes: %v\n", nodes)

	return nil
}

func main() {
	options := []sim.Option{
		sim.WithTopology(
			network.NewSimpleTopology(3, 25),
		),
		sim.WithImage(
			"cockroachdb/cockroach:v19.2.2",
			nil,
			[]string{"start", "--insecure", "--join=node0,node1,node2"},
			sim.NewTCP(26257),
			sim.NewTCP(8080),
		),
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
