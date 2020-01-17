package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
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

func (r skipchainSimulationRound) Configure(simio sim.IO) error {
	return nil
}

func (r skipchainSimulationRound) Execute(ctx context.Context, simio sim.IO) error {
	files := ctx.Value(sim.FilesKey("private.toml")).(sim.Files)
	idents := make([]*network.ServerIdentity, len(files))

	fmt.Println("\nRoster:")
	for id, value := range files {
		si := value.(*network.ServerIdentity)
		si.Address = network.NewAddress(network.TLS, id.IP+":7770")
		idents[id.Index] = si

		fmt.Printf("%v\n", id)
	}
	fmt.Println("")

	ro := onet.NewRoster(idents)

	client := skipchain.NewClient()
	genesis, err := client.CreateGenesis(ro, 4, 32, skipchain.VerificationStandard, []byte("deadbeef"))
	if err != nil {
		return err
	}

	fmt.Printf("Genesis block %x created.\n", genesis.Hash)

	data := make([]byte, 8)
	n := 10

	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint64(data, uint64(i))
		_, err := client.StoreSkipBlock(genesis, ro, data)
		if err != nil {
			return err
		}

		fmt.Printf(goterm.ResetLine("Block [%d/%d] created"), i+1, n)
	}

	fmt.Println("")
	return nil
}

func main() {
	options := []sim.Option{
		sim.WithFileMapper(
			sim.FilesKey("private.toml"),
			sim.FileMapper{
				Path: "/root/.config/conode/private.toml",
				Mapper: func(r io.Reader) (interface{}, error) {
					hc := &app.CothorityConfig{}
					_, err := toml.DecodeReader(r, hc)
					if err != nil {
						return nil, err
					}

					si, err := hc.GetServerIdentity()
					if err != nil {
						return nil, err
					}

					return si, nil
				},
			},
		),
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
