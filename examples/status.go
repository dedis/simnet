package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	status "go.dedis.ch/cothority/v3/status/service"
	"go.dedis.ch/onet/v3/app"
	"go.dedis.ch/onet/v3/network"
	"go.dedis.ch/simnet"
	"go.dedis.ch/simnet/engine"
	"go.dedis.ch/simnet/engine/kubernetes"
)

// StatusSimulationRound contacts each node of the simulation network and asks
// them for their status.
type StatusSimulationRound struct{}

// Execute will contact each known node and ask for its status.
func (r StatusSimulationRound) Execute(ctx context.Context, tun engine.Tunnel) {
	files := ctx.Value(kubernetes.FilesKey("private.toml")).(map[string]interface{})
	wg := sync.WaitGroup{}

	base := 5000
	for ip, value := range files {
		firstPort := base
		base += 2
		wg.Add(1)
		go func(ip string, value interface{}) {
			defer wg.Done()
			err := tun.Create(firstPort, ip, func(addr string) {
				si := value.(*network.ServerIdentity)
				si.Address = network.Address("tls://" + addr)

				cl := status.NewClient()
				for i := 0; i < 100; i++ {
					_, err := cl.Request(si)
					if err != nil {
						fmt.Println(err)
						return
					}

					time.Sleep(100 * time.Millisecond)
				}

				fmt.Printf("[%s] Status done.\n", ip)
			})

			if err != nil {
				fmt.Printf("Error: %v\n", err)
			}
		}(ip, value)
	}

	wg.Wait()
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

	engine, err := kubernetes.NewEngine(kubeconfig, opt)
	if err != nil {
		panic(err)
	}

	sim := simnet.NewSimulation(StatusSimulationRound{}, engine)

	err = sim.Run()
	if err != nil {
		panic(err)
	}
}
