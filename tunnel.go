package simnet

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

// Tunnel is an interface that will create tunnels to node of the simulation so
// that simulation running on a private network can be accessible on a per
// needed basis.
type Tunnel interface {
	Create(ipaddr string, exec func(addr string)) error
}

// KubernetesTunnel provides the primitive to open tunnels between the host
// and simulation nodes running inside a cluster.
type KubernetesTunnel struct {
	config    *rest.Config
	namespace string
	pods      []apiv1.Pod
}

// Create opens a tunnel between the simulation IP and the host so that the
// simulation node can be contacted with the address given in parameter.
func (t KubernetesTunnel) Create(ip string, exec func(addr string)) error {
	for _, pod := range t.pods {
		if pod.Status.PodIP == ip {
			stop := make(chan struct{}, 1)

			go t.forwardPort(pod.Name, []string{"5000:7770", "5001:7771"}, stop)

			// Execute the arbitrary code. The ip can be contacted by using the
			// address provided in parameter.
			exec("127.0.0.1:5000")

			// The port forwarding is stopped after the execution of the requests.
			close(stop)
			return nil
		}
	}

	return errors.New("opd not found")
}

func (t KubernetesTunnel) forwardPort(podName string, ports []string, stopChan chan struct{}) {
	roundTripper, upgrader, err := spdy.RoundTripperFor(t.config)
	if err != nil {
		fmt.Printf("Tunnel error: %v\n", err)
	}

	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", t.namespace, podName)
	hostIP := strings.TrimLeft(t.config.Host, "htps:/")
	serverURL := url.URL{Scheme: "https", Path: path, Host: hostIP}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: roundTripper}, http.MethodPost, &serverURL)
	readyChan := make(chan struct{}, 1)
	out, errOut := new(bytes.Buffer), new(bytes.Buffer)

	forwarder, err := portforward.New(dialer, ports, stopChan, readyChan, out, errOut)
	if err != nil {
		fmt.Printf("Tunnel error: %v\n", err)
	}

	go func() {
		for range readyChan {
			fmt.Printf("Tunnel: %s%s", errOut.String(), out.String())
		}
	}()

	err = forwarder.ForwardPorts()
	if err != nil {
		fmt.Printf("Tunnel error: %v\n", err)
	}
}
