package engine

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	apiv1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

const (
	// BaseLocalPort is the first port to use for port forwarding.
	BaseLocalPort = 5000
)

// KubernetesTunnel provides the primitive to open tunnels between the host
// and simulation nodes running inside a cluster.
type KubernetesTunnel struct {
	config    *rest.Config
	namespace string
	router    apiv1.Pod
	mapping   map[string][]portTuple
	pods      []apiv1.Pod
}

// Create opens a tunnel between the simulation IP and the host so that the
// simulation node can be contacted with the address given in parameter.
func (t KubernetesTunnel) Create(base int, ip string, exec func(addr string)) error {
	for _, pod := range t.pods {
		if pod.Status.PodIP == ip {
			stop := make(chan struct{}, 1)
			ready := make(chan struct{}, 1)

			ports := []string{}
			for i, tu := range t.mapping[ip] {
				ports = append(ports, fmt.Sprintf("%d:%d", base+i, tu.router))
			}

			go t.forwardPort(t.router.Name, ports, ready, stop)
			<-ready

			// Execute the arbitrary code. The ip can be contacted by using the
			// address provided in parameter.
			exec(fmt.Sprintf("127.0.0.1:%d", base))

			// The port forwarding is stopped after the execution of the requests.
			close(stop)
			return nil
		}
	}

	return errors.New("pod not found")
}

func (t KubernetesTunnel) forwardPort(podName string, ports []string, readyChan, stopChan chan struct{}) {
	roundTripper, upgrader, err := spdy.RoundTripperFor(t.config)
	if err != nil {
		fmt.Printf("Tunnel error: %v\n", err)
		return
	}

	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", t.namespace, podName)
	hostIP := strings.TrimLeft(t.config.Host, "htps:/")
	serverURL := url.URL{Scheme: "https", Path: path, Host: hostIP}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: roundTripper}, http.MethodPost, &serverURL)
	out, errOut := new(bytes.Buffer), new(bytes.Buffer)

	addrs := []string{"0.0.0.0"}
	forwarder, err := portforward.NewOnAddresses(dialer, addrs, ports, stopChan, readyChan, out, errOut)
	if err != nil {
		fmt.Printf("Tunnel error: %v\n", err)
		close(readyChan)
		return
	}

	err = forwarder.ForwardPorts()
	if err != nil {
		fmt.Printf("Tunnel error: %v\n", err)
		close(readyChan)
	}
}
