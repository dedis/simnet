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

func (r skipchainSimulationRound) Execute(ctx context.Context) error {
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
		return err
	}

	fmt.Printf("Genesis block %x created with roster %v.\n", genesis.Hash, genesis.Roster.List)

	data := make([]byte, 8)
	n := 50

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
	kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")

	options := []kubernetes.Option{
		kubernetes.WithFileMapper(
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
		),
		kubernetes.WithTopology(
			net.NewAreaTopology(
				&net.Area{N: 1},
				&net.Area{N: 5, X: 100, Y: 0, Latency: net.Delay{Value: 25 * time.Millisecond}},
			),
		),
		kubernetes.WithImage(
			"dedis/conode:latest",
			[]string{"bash", "-c"},
			[]string{"/root/conode setup --non-interactive --port 7770 && /root/conode -d 2 server"},
			kubernetes.NewTCP(7770),
			kubernetes.NewTCP(7771),
		),
	}

	engine, err := kubernetes.NewStrategy(kubeconfig, options...)
	if err != nil {
		panic(err)
	}

	sim := simnet.NewSimulation(skipchainSimulationRound{}, engine)

	err = sim.Run()
	if err != nil {
		panic(err)
	}
}
