package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/buger/goterm"
	status "go.dedis.ch/cothority/v3/status/service"
	"go.dedis.ch/onet/v3"
	"go.dedis.ch/onet/v3/app"
	"go.dedis.ch/onet/v3/network"
	"go.dedis.ch/simnet"
	"go.dedis.ch/simnet/sim"
	"go.dedis.ch/simnet/sim/kubernetes"
)

// StatusSimulationRound contacts each node of the simulation network and asks
// them for their status.
type statusSimulationRound struct{}

func (r statusSimulationRound) Before(simio sim.IO, nodes []sim.NodeInfo) error {
	return nil
}

// Execute will contact each known node and ask for its status.
func (r statusSimulationRound) Execute(simio sim.IO, nodes []sim.NodeInfo) error {
	roster, err := readRoster(simio, nodes)
	if err != nil {
		return err
	}

	fmt.Print("Checking connectivity...")
	client := status.NewClient()

	for i, node := range roster.List {
		ro := make([]*network.ServerIdentity, 1, len(roster.List))
		ro[0] = node
		ro = append(ro, roster.List[:i]...)
		ro = append(ro, roster.List[i+1:]...)

		fmt.Printf(goterm.ResetLine("Checking connectivity... [%d/%d]"), i+1, len(roster.List))
		_, err := client.CheckConnectivity(ro[0].GetPrivate(), ro, 5*time.Second, true)
		if err != nil {
			return err
		}

		time.Sleep(1 * time.Second)
	}

	fmt.Println(goterm.ResetLine("Checking connectivity... ok"))
	return nil
}

func (r statusSimulationRound) After(simio sim.IO, nodes []sim.NodeInfo) error {
	return nil
}

func main() {
	kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")

	options := []sim.Option{
		sim.WithImage(
			"dedis/conode:latest",
			[]string{"bash", "-c"},
			[]string{"/root/conode setup --non-interactive --port 7770 && /root/conode -d 2 server"},
			// The port needs to be published so that we can send a message to a node to start
			// the simulation.
			sim.NewTCP(7771),
		),
	}

	engine, err := kubernetes.NewStrategy(kubeconfig, options...)
	if err != nil {
		panic(err)
	}

	sim := simnet.NewSimulation(statusSimulationRound{}, engine)

	err = sim.Run(os.Args)
	if err != nil {
		panic(err)
	}
}

func makeServerIdentity(cfg *app.CothorityConfig) (*network.ServerIdentity, error) {
	si, err := cfg.GetServerIdentity()
	if err != nil {
		return nil, err
	}

	return si, nil
}

func readRoster(simio sim.IO, nodes []sim.NodeInfo) (*onet.Roster, error) {
	identities := make([]*network.ServerIdentity, len(nodes))

	for i, node := range nodes {
		reader, err := simio.Read(node.Name, "/root/.config/conode/private.toml")
		if err != nil {
			return nil, err
		}

		hc := &app.CothorityConfig{}
		_, err = toml.DecodeReader(reader, hc)
		reader.Close()
		if err != nil {
			return nil, err
		}

		si, err := makeServerIdentity(hc)
		if err != nil {
			return nil, err
		}

		si.Address = network.NewAddress(network.TLS, node.Address+":7770")

		identities[i] = si
	}

	roster := onet.NewRoster(identities)

	return roster, nil
}
