package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"time"

	"go.dedis.ch/simnet"
	"go.dedis.ch/simnet/network"
	"go.dedis.ch/simnet/sim"
	"go.dedis.ch/simnet/sim/docker"
	"golang.org/x/xerrors"
)

type dkgSimple struct{}

func (s dkgSimple) Before(simio sim.IO, nodes []sim.NodeInfo) error {
	return nil
}

func (s dkgSimple) Execute(simio sim.IO, nodes []sim.NodeInfo) error {
	fmt.Printf("Nodes: %v\n", nodes)

	out := &bytes.Buffer{}
	opts := sim.ExecOptions{
		Stdout: out,
		Stderr: out,
	}

	// 1. Exchange certificates
	args := []string{"dkgcli", "--config", "/config", "minogrpc", "token"}

	err := simio.Exec(nodes[0].Name, args, opts)
	if err != nil {
		return xerrors.Errorf("failed to exec cmd: %v", err)
	}

	connStr := strings.Trim(out.String(), " \n\r")

	fmt.Printf("1[%s] - Token: %q\n", nodes[0].Name, out.String())

	args = append([]string{"dkgcli", "--config", "/config", "minogrpc", "join",
		"--address", "//" + nodes[0].Address + ":2000"}, strings.Split(connStr, " ")...)

	for i := 1; i < len(nodes); i++ {
		out.Reset()
		err = simio.Exec(nodes[i].Name, args, opts)
		if err != nil {
			return xerrors.Errorf("failed to join: %v", err)
		}

		fmt.Printf("2[%s] - Join: %q\n", nodes[i].Name, out.String())
	}

	// 2. DKG listen
	args = []string{"dkgcli", "--config", "/config", "dkg", "listen"}

	for _, node := range nodes {
		out.Reset()
		err = simio.Exec(node.Name, args, opts)
		if err != nil {
			return xerrors.Errorf("failed to listen: %v", err)
		}

		fmt.Printf("3[%s] - Listen: %q\n", node.Name, out.String())
	}

	// 3. DKG setup
	authorities := make([]string, len(nodes)*2)
	for i, node := range nodes {
		rc, err := simio.Read(node.Name, "/config/dkgauthority")
		if err != nil {
			return xerrors.Errorf("failed to read dkgauthority file: %v", err)
		}

		authority, err := ioutil.ReadAll(rc)
		if err != nil {
			return xerrors.Errorf("failed to read authority: %v", err)
		}

		authorities[i*2] = "--authority"
		authorities[i*2+1] = string(authority)

		fmt.Printf("4[%s] - Read authority: %q\n", node.Name, string(authority))
	}

	args = append([]string{"dkgcli", "--config", "/config", "dkg", "setup"}, authorities...)

	out.Reset()
	err = simio.Exec(nodes[0].Name, args, opts)
	if err != nil {
		return xerrors.Errorf("failed to setup: %v", err)
	}

	fmt.Printf("5[%s] - Setup: %q\n", nodes[0].Name, out.String())

	return nil
}

func (s dkgSimple) After(simio sim.IO, nodes []sim.NodeInfo) error {
	return nil
}

func main() {
	startArgs := []string{"--config", "/config", "start",
		"--routing", "tree", "--listen", "tcp://0.0.0.0:2000"}

	options := []sim.Option{
		sim.WithTopology(
			network.NewSimpleTopology(20, time.Millisecond*10),
		),
		sim.WithImage("dedis/ddkg:0.0.3", []string{}, []string{}, sim.NewTCP(2000)),
		sim.WithUpdate(func(opts *sim.Options, _, IP string) {
			opts.Args = append(startArgs, "--public", fmt.Sprintf("//%s:2000", IP))
		}),
	}

	// kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")

	// engine, err := kubernetes.NewStrategy(kubeconfig, options...)
	engine, err := docker.NewStrategy(options...)
	if err != nil {
		panic(err)
	}

	sim := simnet.NewSimulation(dkgSimple{}, engine)

	err = sim.Run(os.Args)
	if err != nil {
		panic(err)
	}
}
