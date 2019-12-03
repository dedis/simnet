package kubernetes

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

const (
	// BaseLocalPort is the first port to use for port forwarding.
	BaseLocalPort = 5000
)

type portTuple struct {
	pod    int32
	router int32
}

type routerMapping map[string][]portTuple

// Forwarder is an implementation that will open a tunnel between the local
// machine to a distant host.
type Forwarder interface {
	ForwardPorts() error
}

func makeForwarder(dialer httpstream.Dialer, ports []string, stopChan, readyChan chan struct{}) (Forwarder, error) {
	out, errOut := new(bytes.Buffer), new(bytes.Buffer)
	addrs := []string{"0.0.0.0"}
	return portforward.NewOnAddresses(dialer, addrs, ports, stopChan, readyChan, out, errOut)
}

// Tunnel provides the primitive to open tunnels between the host
// and simulation nodes running inside a cluster.
type Tunnel struct {
	engine              *kubeEngine
	roundTripperFactory func(*rest.Config) (http.RoundTripper, spdy.Upgrader, error)
	forwarderFactory    func(httpstream.Dialer, []string, chan struct{}, chan struct{}) (Forwarder, error)
}

func newTunnel(e *kubeEngine) Tunnel {
	return Tunnel{
		engine:              e,
		roundTripperFactory: spdy.RoundTripperFor,
		forwarderFactory:    makeForwarder,
	}
}

// Create opens a tunnel between the simulation IP and the host so that the
// simulation node can be contacted with the address given in parameter.
func (t Tunnel) Create(base int, ip string, exec func(addr string)) error {
	for _, pod := range t.engine.pods {
		if pod.Status.PodIP == ip {
			stop := make(chan struct{}, 1)
			ready := make(chan struct{}, 1)
			err := make(chan error, 1)

			ports := []string{}
			for i, tu := range t.engine.mapping[ip] {
				ports = append(ports, fmt.Sprintf("%d:%d", base+i, tu.router))
			}

			go t.forwardPort(t.engine.router.Name, ports, ready, stop, err)
			<-ready

			// Execute the arbitrary code. The ip can be contacted by using the
			// address provided in parameter.
			exec(fmt.Sprintf("127.0.0.1:%d", base))

			// The port forwarding is stopped after the execution of the requests.
			close(stop)
			return <-err
		}
	}

	return errors.New("pod not found")
}

func (t Tunnel) forwardPort(podName string, ports []string, readyChan, stopChan chan struct{}, errChan chan error) {
	roundTripper, upgrader, err := t.roundTripperFactory(t.engine.config)
	if err != nil {
		close(readyChan)
		errChan <- err
		return
	}

	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", t.engine.namespace, podName)
	hostIP := strings.TrimLeft(t.engine.config.Host, "htps:/")
	serverURL := url.URL{Scheme: "https", Path: path, Host: hostIP}
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: roundTripper}, http.MethodPost, &serverURL)

	forwarder, err := t.forwarderFactory(dialer, ports, stopChan, readyChan)
	if err != nil {
		close(readyChan)
		errChan <- err
		return
	}

	err = forwarder.ForwardPorts()
	if err != nil {
		close(readyChan)
		errChan <- err
	}

	close(errChan)
}
