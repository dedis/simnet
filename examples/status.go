package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
	status "go.dedis.ch/cothority/v3/status/service"
	"go.dedis.ch/onet/v3/app"
	"go.dedis.ch/onet/v3/network"
	"go.dedis.ch/simnet"
	"go.dedis.ch/simnet/sim/kubernetes"
)

// StatusSimulationRound contacts each node of the simulation network and asks
// them for their status.
type StatusSimulationRound struct{}

// Execute will contact each known node and ask for its status.
func (r StatusSimulationRound) Execute(ctx context.Context) {
	files := ctx.Value(kubernetes.FilesKey("private.toml")).(map[string]interface{})
	idents := make([]*network.ServerIdentity, 0, len(files))

	for ip, value := range files {
		si := value.(*network.ServerIdentity)
		si.Address = network.NewAddress(network.TLS, ip+":7770")
		idents = append(idents, si)
	}

	fmt.Println("Checking connectivity...")
	client := status.NewClient()
	_, err := client.CheckConnectivity(idents[0].GetPrivate(), idents, 5*time.Second, true)
	if err != nil {
		fmt.Printf("Error: %+v\n", err)
	}

	if err != nil {
		fmt.Printf("Error: %v\n", err)
	}
}

func main() {
	kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")

	opt := kubernetes.WithFileMapper(
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

	engine, err := kubernetes.NewStrategy(kubeconfig, opt)
	if err != nil {
		panic(err)
	}

	sim := simnet.NewSimulation(StatusSimulationRound{}, engine)

	err = sim.Run()
	if err != nil {
		panic(err)
	}
}
