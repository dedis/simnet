package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	status "go.dedis.ch/cothority/v3/status/service"
	"go.dedis.ch/onet/v3/app"
	"go.dedis.ch/onet/v3/network"
	"go.dedis.ch/simnet"
)

// Key is the type of key for the context.
type Key string

func homeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("USERPROFILE") // windows
}

type testRound struct{}

func (r testRound) Execute(ctx context.Context, tun simnet.Tunnel) {
	fmt.Println("Execute !")

	files := ctx.Value(Key("files")).(map[string]string)
	for ip, content := range files {
		fmt.Printf("Found file for Pod %s\n", ip)

		tun.Create(ip, func(addr string) {
			fmt.Printf("Tunnel to %s\n", addr)
			fmt.Println("Contacting...")

			hc := &app.CothorityConfig{}
			_, err := toml.Decode(content, hc)
			if err != nil {
				fmt.Println(err)
				return
			}

			si, err := hc.GetServerIdentity()
			if err != nil {
				fmt.Println(err)
				return
			}

			si.Address = network.Address("tls://" + addr)

			cl := status.NewClient()
			rep, err := cl.Request(si)
			if err != nil {
				fmt.Println(err)
				return
			}

			fmt.Printf("%#v\n", rep)
		})
	}
}

func main() {
	fmt.Println("Simulating on Kubernetes...")

	var kubeconfig *string
	if home := homeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()

	sim, err := simnet.NewSimulation(*kubeconfig, testRound{})
	if err != nil {
		panic(err)
	}

	err = sim.Deploy()
	if err != nil {
		panic(err)
	}

	files, err := sim.Before("/root/.config/conode/private.toml")

	ctx := context.WithValue(context.Background(), Key("files"), files)

	err = sim.Run(ctx)
	if err != nil {
		panic(err)
	}

	// err = sim.Clean()
	// if err != nil {
	// 	panic(err)
	// }
}
