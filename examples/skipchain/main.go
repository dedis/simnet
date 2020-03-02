package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/buger/goterm"
	"go.dedis.ch/cothority/v3/skipchain"
	"go.dedis.ch/onet/v3"
	"go.dedis.ch/onet/v3/app"
	"go.dedis.ch/onet/v3/network"
	"go.dedis.ch/simnet"
	net "go.dedis.ch/simnet/network"
	"go.dedis.ch/simnet/sim"
	"go.dedis.ch/simnet/sim/docker"
)

type skipchainSimulationRound struct{}

func (r skipchainSimulationRound) Before(simio sim.IO, nodes []sim.NodeInfo) error {
	return nil
}

func (r skipchainSimulationRound) Execute(simio sim.IO, nodes []sim.NodeInfo) error {
	roster, err := readRoster(simio, nodes)

	client := skipchain.NewClient()
	genesis, err := client.CreateGenesis(roster, 4, 32, skipchain.VerificationStandard, []byte("deadbeef"))
	if err != nil {
		return err
	}

	fmt.Printf("Genesis block %x created.\n", genesis.Hash)

	data := make([]byte, 8)
	n := 10

	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint64(data, uint64(i))
		_, err := client.StoreSkipBlock(genesis, roster, data)
		if err != nil {
			return err
		}

		fmt.Printf(goterm.ResetLine("Block [%d/%d] created"), i+1, n)
	}

	fmt.Println("")
	return nil
}

func (r skipchainSimulationRound) After(simio sim.IO, nodes []sim.NodeInfo) error {
	return nil
}

func main() {
	options := []sim.Option{
		sim.WithTopology(
			net.NewAreaTopology(
				&net.Area{N: 3, Latency: net.Delay{Value: 25 * time.Millisecond}},
				&net.Area{N: 4, X: 50, Latency: net.Delay{Value: 25 * time.Millisecond}},
				&net.Area{N: 4, Y: 50},
			),
		),
		sim.WithImage(
			"dedis/conode:latest",
			[]string{"bash", "-c"},
			[]string{"/root/conode setup --non-interactive --port 7770 && /root/conode -d 2 server"},
			sim.NewTCP(7770),
			sim.NewTCP(7771),
		),
	}

	engine, err := docker.NewStrategy(options...)
	if err != nil {
		panic(err)
	}

	sim := simnet.NewSimulation(skipchainSimulationRound{}, engine)

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
