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
)

// StatusSimulationRound contacts each node of the simulation network and asks
// them for their status.
type StatusSimulationRound struct{}

// Execute will contact each known node and ask for its status.
func (r StatusSimulationRound) Execute(ctx context.Context, tun engine.Tunnel) {
	files := ctx.Value(engine.FilesKey("private.toml")).(map[string]interface{})
	wg := sync.WaitGroup{}

	base := 5000
	for ip, value := range files {
		firstPort := base
		base += 2
		wg.Add(1)
		go func(ip string, value interface{}) {
			defer wg.Done()
			tun.Create(firstPort, ip, func(addr string) {
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
		}(ip, value)
	}

	wg.Wait()
}

func main() {
	kubeconfig := filepath.Join(os.Getenv("HOME"), ".kube", "config")

	opt := engine.WithFileMapper(
		engine.FilesKey("private.toml"),
		engine.FileMapper{
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

	sim, err := simnet.NewSimulation(kubeconfig, StatusSimulationRound{}, opt)
	if err != nil {
		panic(err)
	}

	err = sim.Run()
	if err != nil {
		panic(err)
	}
}
