package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/buger/goterm"
	"go.dedis.ch/cothority/v3/skipchain"
	"go.dedis.ch/onet/v3"
	"go.dedis.ch/onet/v3/app"
	"go.dedis.ch/onet/v3/network"
	"go.dedis.ch/simnet"
	net "go.dedis.ch/simnet/network"
	"go.dedis.ch/simnet/sim/kubernetes"
)

type skipchainSimulationRound struct{}

func (r skipchainSimulationRound) Execute(ctx context.Context) {
	files := ctx.Value(kubernetes.FilesKey("private.toml")).(map[string]interface{})
	idents := make([]*network.ServerIdentity, 0, len(files))

	for ip, value := range files {
		si := value.(*network.ServerIdentity)
		si.Address = network.NewAddress(network.TLS, ip+":7770")
		idents = append(idents, si)
	}

	ro := onet.NewRoster(idents)

	client := skipchain.NewClient()
	genesis, err := client.CreateGenesis(ro, 4, 32, skipchain.VerificationStandard, []byte("deadbeef"))
	if err != nil {
		panic(err)
	}

	fmt.Printf("Genesis block %x created with roster %v.\n", genesis.Hash, genesis.Roster.List)

	data := make([]byte, 8)
	n := 50

	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint64(data, uint64(i))
		_, err := client.StoreSkipBlock(genesis, ro, data)
		if err != nil {
			panic(err)
		}

		fmt.Printf(goterm.ResetLine("Block [%d/%d] created"), i+1, n)
	}

	fmt.Println("")
}

func main() {
	kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")

	files := kubernetes.WithFileMapper(
		kubernetes.FilesKey("private.toml"),
		kubernetes.FileMapper{
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
	)

	topo := kubernetes.WithTopology(net.NewSimpleTopology(10, 50*time.Millisecond))

	engine, err := kubernetes.NewStrategy(kubeconfig, files, topo)
	if err != nil {
		panic(err)
	}

	sim := simnet.NewSimulation(skipchainSimulationRound{}, engine)

	err = sim.Run()
	if err != nil {
		panic(err)
	}
}
