package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.dedis.ch/simnet"
	"go.dedis.ch/simnet/network"
	"go.dedis.ch/simnet/sim"
	"go.dedis.ch/simnet/sim/kubernetes"
)

var (
	commandInit         = []string{"./cockroach", "init", "--insecure"}
	commandWorkloadInit = []string{"./cockroach", "workload", "init", "movr", "postgresql://root@node0:26257?sslmode=disable"}
	commandWorkloadRun  = []string{"./cockroach", "workload", "run", "movr", "--duration=10s", "postgresql://root@node0:26257?sslmode=disable"}
)

type simRound struct{}

func (s simRound) Before(simio sim.IO, nodes []sim.NodeInfo) error {
	reader, writer := io.Pipe()

	go io.Copy(os.Stdout, reader)

	// Initialise the cluster.
	err := simio.Exec("node0", commandInit, sim.ExecOptions{
		Stdout: writer,
		Stderr: writer,
	})
	if err != nil {
		return err
	}

	return nil
}

func (s simRound) Execute(simio sim.IO, nodes []sim.NodeInfo) error {
	fmt.Printf("Nodes: %v\n", nodes)

	reader, writer := io.Pipe()

	go io.Copy(os.Stdout, reader)

	err := simio.Exec("node0", commandWorkloadInit, sim.ExecOptions{
		Stdout: writer,
		Stderr: writer,
	})
	if err != nil {
		return err
	}

	err = simio.Exec("node0", commandWorkloadRun, sim.ExecOptions{
		Stdout: writer,
		Stderr: writer,
	})
	if err != nil {
		return err
	}

	// In order to clean the Go routine, it's important to close one side of
	// the pipe.
	writer.Close()

	return nil
}

func (s simRound) After(simio sim.IO, nodes []sim.NodeInfo) error {
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
	//engine, err := docker.NewStrategy(options...)
	if err != nil {
		panic(err)
	}

	sim := simnet.NewSimulation(simRound{}, engine)

	err = sim.Run(os.Args)
	if err != nil {
		panic(err)
	}
}
