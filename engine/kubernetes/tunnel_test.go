package kubernetes

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/transport/spdy"
)

func TestTunnel_Create(t *testing.T) {
	tun := Tunnel{
		engine: &kubeDeployer{
			config:  &rest.Config{},
			mapping: routerMapping{},
			pods:    []apiv1.Pod{{Status: apiv1.PodStatus{PodIP: "a.b.c.d"}}},
		},
		forwarderFactory:    testPortforwarderFactory,
		roundTripperFactory: testRoundTripperFactory,
	}

	err := tun.Create(2000, "a.b.c.d", func(string) {})
	require.NoError(t, err)
}

func TestTunnel_CreateWithNoPod(t *testing.T) {
	tun := newTunnel(&kubeDeployer{})

	err := tun.Create(2000, "a.b.c.d", func(string) {})
	require.Error(t, err)
	require.Equal(t, "pod not found", err.Error())
}

func TestTunnel_BadRoundTripper(t *testing.T) {
	tun := Tunnel{
		engine: &kubeDeployer{
			mapping: routerMapping{},
			pods:    []apiv1.Pod{{Status: apiv1.PodStatus{PodIP: "a.b.c.d"}}},
		},
		forwarderFactory:    testPortforwarderFactory,
		roundTripperFactory: testRoundTripperFactory,
	}

	err := tun.Create(2000, "a.b.c.d", func(string) {})
	require.Error(t, err)
	require.Equal(t, "no config", err.Error())
}

func TestTunnel_CreateBadMapping(t *testing.T) {
	tun := newTunnel(&kubeDeployer{
		config:  &rest.Config{},
		mapping: routerMapping{},
		pods:    []apiv1.Pod{{Status: apiv1.PodStatus{PodIP: "a.b.c.d"}}},
	})

	err := tun.Create(2000, "a.b.c.d", func(string) {})
	require.Error(t, err)
	require.Contains(t, err.Error(), "at least 1 port")
}

func TestTunnel_CreateNoConnection(t *testing.T) {
	tun := newTunnel(&kubeDeployer{
		config: &rest.Config{},
		mapping: routerMapping{
			"a.b.c.d": []portTuple{{3000, 4000}},
		},
		pods: []apiv1.Pod{{Status: apiv1.PodStatus{PodIP: "a.b.c.d"}}},
	})

	err := tun.Create(2000, "a.b.c.d", func(string) {})
	require.Error(t, err)
	require.Contains(t, err.Error(), "error upgrading connection:")
}

type testForwarder struct{}

func (tf testForwarder) ForwardPorts() error {
	return nil
}

func testPortforwarderFactory(d httpstream.Dialer, p []string, c, r chan struct{}) (Forwarder, error) {
	close(r)

	return testForwarder{}, nil
}

func testRoundTripperFactory(cfg *rest.Config) (http.RoundTripper, spdy.Upgrader, error) {
	if cfg == nil {
		return nil, nil, errors.New("no config")
	}

	return nil, nil, nil
}
