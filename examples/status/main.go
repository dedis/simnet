package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/buger/goterm"
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
func (r StatusSimulationRound) Execute(ctx context.Context) error {
	files := ctx.Value(kubernetes.FilesKey("private.toml")).(map[string]interface{})
	idents := make([]*network.ServerIdentity, 0, len(files))

	for ip, value := range files {
		si := value.(*network.ServerIdentity)
		si.Address = network.NewAddress(network.TLS, ip+":7770")
		idents = append(idents, si)
	}

	fmt.Print("Checking connectivity...")
	client := status.NewClient()

	for i := range idents {
		ro := make([]*network.ServerIdentity, 1, len(idents))
		ro[0] = idents[i]
		ro = append(ro, idents[:i]...)
		ro = append(ro, idents[i+1:]...)

		fmt.Printf(goterm.ResetLine("Checking connectivity... [%d/%d]"), i+1, len(idents))
		_, err := client.CheckConnectivity(ro[0].GetPrivate(), ro, 5*time.Second, true)
		if err != nil {
			return err
		}

		time.Sleep(1 * time.Second)
	}

	fmt.Println(goterm.ResetLine("Checking connectivity... ok"))
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

	sim := simnet.NewSimulation(StatusSimulationRound{}, engine)

	err = sim.Run(os.Args)
	if err != nil {
		panic(err)
	}
}
