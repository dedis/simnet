package kubernetes

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.dedis.ch/simnet/network"
	"go.dedis.ch/simnet/sim"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	fakecorev1 "k8s.io/client-go/kubernetes/typed/core/v1/fake"
	"k8s.io/client-go/rest"
	fakerest "k8s.io/client-go/rest/fake"
	testcore "k8s.io/client-go/testing"
)

const testTimeout = 500 * time.Millisecond

func TestEngine_NewFailures(t *testing.T) {
	engine, err := newKubeEngine(&rest.Config{Host: ":"}, "", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "couldn't parse the host")
	require.Nil(t, engine)

	setMockBadClient()
	defer setMockClient()

	engine, err = newKubeEngine(nil, "", &sim.Options{})
	require.Error(t, err)
	require.Equal(t, "client: client error", err.Error())
	require.Nil(t, engine)
}

func TestEngine_CreateDeployments(t *testing.T) {
	n := 3
	engine, client := makeEngine(n)
	engine.options.Ports = []sim.Port{
		sim.NewTCP(2000),
		sim.NewUDP(20001),
	}

	w, err := engine.CreateDeployment()
	require.NoError(t, err)
	defer w.Stop()

	for i := 0; i < n; i++ {
		select {
		case evt := <-w.ResultChan():
			dpl, ok := evt.Object.(*appsv1.Deployment)
			require.True(t, ok)
			require.Equal(t, 2, len(dpl.Spec.Template.Spec.Containers))
		case <-time.After(testTimeout):
			t.Fatal("timeout")
		}
	}

	for _, act := range client.Actions() {
		if wa, ok := act.(testcore.WatchActionImpl); ok {
			value, has := wa.WatchRestrictions.Labels.RequiresExactMatch(LabelID)
			if has {
				require.Equal(t, AppID, value)
				return
			}
		}
	}

	t.Fatal("watch action not found")
}

func TestEngine_CreateDeploymentFailure(t *testing.T) {
	n := 3
	engine, client := makeEngine(n)

	// First test that the error on creation is handled.
	e := errors.New("create error")
	client.PrependReactor("*", "*", func(action testcore.Action) (bool, runtime.Object, error) {
		return true, nil, e
	})
	_, err := engine.CreateDeployment()
	require.Error(t, err)
	require.Equal(t, e, err)

	// Then test that the watcher error is handled aswell.
	fw := watch.NewFake()
	e = errors.New("watcher error")
	client.PrependWatchReactor("*", testcore.DefaultWatchReactor(fw, e))

	_, err = engine.CreateDeployment()
	require.Error(t, err)
	require.Equal(t, e, err)
}

func TestEngine_WaitDeployment(t *testing.T) {
	n := 3
	engine, _ := makeEngine(n)

	w := watch.NewFakeWithChanSize(n, false)

	for i := 0; i < n; i++ {
		w.Modify(&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name: fmt.Sprintf("node%d", i),
			},
			Status: appsv1.DeploymentStatus{
				AvailableReplicas: 1,
			},
		})
	}

	require.NoError(t, engine.WaitDeployment(w))
}

func TestEngine_WaitDeploymentFailure(t *testing.T) {
	engine, _ := makeEngine(3)

	reason := "oops"

	w := watch.NewFakeWithChanSize(1, false)
	w.Modify(&appsv1.Deployment{
		Status: appsv1.DeploymentStatus{
			Conditions: []appsv1.DeploymentCondition{
				{
					Type:   appsv1.DeploymentAvailable,
					Status: apiv1.ConditionFalse,
				},
				{
					Type:   appsv1.DeploymentProgressing,
					Status: apiv1.ConditionFalse,
					Reason: reason,
				},
			},
		},
	})

	err := engine.WaitDeployment(w)
	require.Error(t, err)
	require.Equal(t, reason, err.Error())
}

func TestEngine_FetchPods(t *testing.T) {
	list := &apiv1.PodList{
		Items: []apiv1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node0",
					Labels: map[string]string{LabelID: AppID},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: "node1"},
			},
		},
	}
	engine := newKubeEngineTest(fake.NewSimpleClientset(list), "", 1)

	pods, err := engine.FetchPods()
	require.NoError(t, err)
	require.Equal(t, 1, len(pods))
	require.Equal(t, 1, len(engine.pods))
}

func TestEngine_FetchPodsFailure(t *testing.T) {
	engine, client := makeEngine(1)

	_, err := engine.FetchPods()
	require.Error(t, err)

	e := errors.New("list error")
	client.PrependReactor("*", "*", func(action testcore.Action) (bool, runtime.Object, error) {
		return true, nil, e
	})

	_, err = engine.FetchPods()
	require.Error(t, err)
	require.Equal(t, e, err)
}

func TestEngine_UploadConfig(t *testing.T) {
	engine, _ := makeEngine(3)
	engine.pods = []apiv1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					LabelNode: "node1",
				},
			},
		},
	}

	wg := sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		dec := json.NewDecoder(engine.fs.(*testFS).reader)

		var rules []network.Rule
		err := dec.Decode(&rules)
		require.NoError(t, err)
		require.Equal(t, 1, len(rules))
	}()

	err := engine.UploadConfig()
	require.NoError(t, err)

	wg.Wait()
}

func TestEngine_UploadConfigFailures(t *testing.T) {
	engine, _ := makeEngine(1)
	engine.pods = []apiv1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					LabelNode: "node1",
				},
			},
		},
	}

	e := errors.New("writing error")
	engine.fs = &testFS{err: e}

	err := engine.UploadConfig()
	require.Error(t, err)
	require.Equal(t, e, err)

	_, writer := io.Pipe()
	writer.Close()
	engine.fs = &testFS{writer: writer}
	err = engine.UploadConfig()
	require.Error(t, err)
	require.Contains(t, err.Error(), "io: read/write on closed pipe")
}

func TestEngine_DeployRouter(t *testing.T) {
	engine, _ := makeEngine(3)

	pods := []apiv1.Pod{
		{
			Spec: apiv1.PodSpec{
				Containers: []apiv1.Container{
					{
						Ports: []apiv1.ContainerPort{
							{
								ContainerPort: 2000,
							},
						},
					},
				},
			},
		},
	}

	w, err := engine.DeployRouter(pods)
	require.NoError(t, err)
	w.Stop()

	select {
	case <-time.After(testTimeout):
		t.Fatal("timeout")
	case <-w.ResultChan():
	}
}

func TestEngine_DeployRouterFailure(t *testing.T) {
	engine, client := makeEngine(0)

	e := errors.New("create error")
	client.PrependReactor("*", "*", func(action testcore.Action) (bool, runtime.Object, error) {
		return true, nil, e
	})

	_, err := engine.DeployRouter([]apiv1.Pod{})
	require.Error(t, err)
	require.Equal(t, e, err)

	fw := watch.NewFake()
	e = errors.New("watcher error")
	client.PrependWatchReactor("*", testcore.DefaultWatchReactor(fw, e))

	_, err = engine.DeployRouter([]apiv1.Pod{})
	require.Error(t, err)
	require.Equal(t, e, err)
}

func TestEngine_WaitRouter(t *testing.T) {
	client := fake.NewSimpleClientset()
	engine := newKubeEngineTest(client, "", 0)

	w := watch.NewFakeWithChanSize(1, false)
	w.Modify(&appsv1.Deployment{
		Status: appsv1.DeploymentStatus{
			AvailableReplicas: 1,
		},
	})

	fw := watch.NewFakeWithChanSize(1, false)
	fw.Modify(&apiv1.Service{Spec: apiv1.ServiceSpec{Ports: []apiv1.ServicePort{{NodePort: 31000}}}})
	client.PrependWatchReactor("services", testcore.DefaultWatchReactor(fw, nil))

	_, _, err := engine.WaitRouter(w)
	require.NoError(t, err)
}

func TestEngine_WaitRouterFailure(t *testing.T) {
	engine := newKubeEngineTest(fake.NewSimpleClientset(), "", 0)

	reason := "oops"

	w := watch.NewFakeWithChanSize(1, false)
	w.Modify(&appsv1.Deployment{
		Status: appsv1.DeploymentStatus{
			Conditions: []appsv1.DeploymentCondition{
				{
					Type:   appsv1.DeploymentAvailable,
					Status: apiv1.ConditionFalse,
				},
				{
					Type:   appsv1.DeploymentProgressing,
					Status: apiv1.ConditionFalse,
					Reason: reason,
				},
			},
		},
	})

	_, _, err := engine.WaitRouter(w)
	require.Error(t, err)
	require.Equal(t, reason, err.Error())
}

func TestEngine_WaitRouterVPNFailure(t *testing.T) {
	engine, client := makeEngine(1)

	e := errors.New("create error")
	client.PrependReactor("*", "*", func(action testcore.Action) (bool, runtime.Object, error) {
		return true, nil, e
	})

	_, err := engine.createVPNService()
	require.Error(t, err)
	require.Equal(t, e, err)

	w := watch.NewFakeWithChanSize(1, false)
	w.Modify(&appsv1.Deployment{
		Status: appsv1.DeploymentStatus{
			AvailableReplicas: 1,
		},
	})

	engine.config.Host = ":"
	_, _, err = engine.WaitRouter(w)
	require.Error(t, err)
}

func TestEngine_CreateVPNFailures(t *testing.T) {
	engine, client := makeEngine(0)
	fw := watch.NewFake()

	e := errors.New("watch error")
	client.PrependWatchReactor("services", testcore.DefaultWatchReactor(fw, e))

	_, err := engine.createVPNService()
	require.Error(t, err)
	require.True(t, errors.Is(err, e))
}

func TestEngine_FetchCertificates(t *testing.T) {
	client := fake.NewSimpleClientset(
		makeRouterPod(),
		&apiv1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "node0"},
		},
	)

	dir, err := ioutil.TempDir(os.TempDir(), "simnet-kubernetes-test")
	require.NoError(t, err)

	engine := newKubeEngineTest(client, "", 0)
	engine.options.OutputDir = dir

	certs, err := engine.FetchCertificates()
	require.NoError(t, err)

	stat, err := os.Stat(certs.CA)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), stat.Mode())

	stat, err = os.Stat(certs.Cert)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), stat.Mode())

	stat, err = os.Stat(certs.Key)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), stat.Mode())
}

func TestEngine_FetchCertificatesFailures(t *testing.T) {
	engine, client := makeEngine(0)

	e := errors.New("list error")
	client.PrependReactor("*", "*", func(action testcore.Action) (bool, runtime.Object, error) {
		return true, nil, e
	})

	_, err := engine.FetchCertificates()
	require.Error(t, err)
	require.Equal(t, e, err)

	client.PrependReactor("*", "*", func(action testcore.Action) (bool, runtime.Object, error) {
		return true, &apiv1.PodList{}, nil
	})

	_, err = engine.FetchCertificates()
	require.Error(t, err)
	require.Equal(t, "missing router pod", err.Error())

	client.PrependReactor("*", "*", func(action testcore.Action) (bool, runtime.Object, error) {
		return true, &apiv1.PodList{Items: []apiv1.Pod{*makeRouterPod()}}, nil
	})

	engine.fs = &testFS{err: errors.New("oops")}
	_, err = engine.FetchCertificates()
	require.Error(t, err)
}

func TestEngine_WriteCertificatesFailures(t *testing.T) {
	engine, _ := makeEngine(0)

	err := engine.writeCertificates("", sim.Certificates{CA: "/", Key: "/", Cert: "/"})
	require.Error(t, err)
	require.IsType(t, (*os.PathError)(nil), err)

	dir, err := ioutil.TempDir(os.TempDir(), "simnet-kubernetes-test")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	certs := sim.Certificates{
		CA:   filepath.Join(dir, "ca"),
		Key:  filepath.Join(dir, "key"),
		Cert: filepath.Join(dir, "cert"),
	}

	engine.fs = &testFS{errRead: errors.New("oops")}
	err = engine.writeCertificates("", certs)
	require.Error(t, err)
}

func TestEngine_Delete(t *testing.T) {
	srvice := &apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "simnet-router"},
	}

	client := fake.NewSimpleClientset(srvice)

	actions := make(chan testcore.Action, 1)
	// As the fake client does not implement the delete collection action, we
	// test differently by listening for the action.
	client.AddReactor("*", "*", func(action testcore.Action) (bool, runtime.Object, error) {
		actions <- action
		return true, nil, nil
	})

	engine := newKubeEngineTest(client, "", 0)

	w, err := engine.DeleteAll()
	require.NoError(t, err)
	defer w.Stop()

	act := <-actions
	require.NotNil(t, act)

	labels := act.(testcore.DeleteCollectionActionImpl).ListRestrictions.Labels
	value, has := labels.RequiresExactMatch(LabelApp)
	require.True(t, has)
	require.Equal(t, AppName, value)
}

func TestEngine_DeleteFailure(t *testing.T) {
	engine, client := makeEngine(0)

	client.PrependReactor("*", "services", func(action testcore.Action) (bool, runtime.Object, error) {
		return true, nil, nil
	})

	e := errors.New("delete error")
	client.PrependReactor("*", "deployments", func(action testcore.Action) (bool, runtime.Object, error) {
		return true, nil, e
	})

	_, err := engine.DeleteAll()
	require.Error(t, err)
	require.Equal(t, e, err)

	fw := watch.NewFake()
	e = errors.New("watcher error")
	client.PrependWatchReactor("deployments", testcore.DefaultWatchReactor(fw, e))

	_, err = engine.DeleteAll()
	require.Error(t, err)
	require.Equal(t, e, err)

	engine.client = fake.NewSimpleClientset()
	_, err = engine.DeleteAll()
	require.Error(t, err)
	require.IsType(t, (*apierrors.StatusError)(nil), err)

}

func TestEngine_WaitDeletion(t *testing.T) {
	engine, _ := makeEngine(0)

	w := watch.NewFakeWithChanSize(1, false)

	w.Delete(&appsv1.Deployment{
		Status: appsv1.DeploymentStatus{
			AvailableReplicas:   0,
			UnavailableReplicas: 0,
		},
	})

	err := engine.WaitDeletion(w, testTimeout)
	require.NoError(t, err)
}

func TestEngine_WaitDeletionFailure(t *testing.T) {
	engine, _ := makeEngine(0)

	fw := watch.NewFake()
	err := engine.WaitDeletion(fw, time.Millisecond)
	require.Error(t, err)
	require.Equal(t, "timeout", err.Error())
}

func TestEngine_StreamLogs(t *testing.T) {
	cli := newFakeClientset()

	engine := newKubeEngineTest(cli, "", 0)
	ch := make(chan struct{})
	defer close(ch)

	dir, err := ioutil.TempDir(os.TempDir(), "simnet-kubernetes-test")
	require.NoError(t, err)
	defer os.RemoveAll(dir)
	engine.options.OutputDir = dir

	engine.pods = []apiv1.Pod{{ObjectMeta: metav1.ObjectMeta{Name: "pod"}}}

	err = engine.StreamLogs(ch)
	require.NoError(t, err)

	logline := "this is a log line"
	_, err = cli.writer.Write([]byte(logline))
	require.NoError(t, err)
	cli.writer.Close()

	engine.wgLogs.Wait()

	content, err := ioutil.ReadFile(filepath.Join(dir, "logs", "pod.log"))
	require.Equal(t, logline, string(content))
}

func TestEngine_StreamLogsFailures(t *testing.T) {
	cli := newFakeClientset()

	engine := newKubeEngineTest(cli, "", 0)
	engine.options.OutputDir = "\000"
	ch := make(chan struct{})
	defer close(ch)

	// Error if the log folder cannot be cleaned.
	err := engine.StreamLogs(ch)
	require.Error(t, err)
	require.Contains(t, err.Error(), "couldn't clean")

	// Error if the log folder cannot be created.
	engine.options.OutputDir = "/"
	err = engine.StreamLogs(ch)
	require.Error(t, err)
	require.Contains(t, err.Error(), "couldn't create log folder")

	dir, err := ioutil.TempDir(os.TempDir(), "simnet-kubernetes-test")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	// Error if a container log file cannot be created.
	engine.options.OutputDir = dir
	engine.pods = []apiv1.Pod{{ObjectMeta: metav1.ObjectMeta{Name: "\000"}}}
	err = engine.StreamLogs(ch)
	require.Error(t, err)
	require.Contains(t, err.Error(), "couldn't create log file")

	// Error if the stream cannot be opened
	engine.pods = []apiv1.Pod{{ObjectMeta: metav1.ObjectMeta{Name: "pod"}}}
	cli.err = errors.New("stream error")
	err = engine.StreamLogs(ch)
	require.Error(t, err)
	require.True(t, errors.Is(err, cli.err))
}

func TestEngine_ReadStats(t *testing.T) {
	engine := &kubeEngine{
		fs: &testFS{},
	}

	stats, err := engine.ReadStats("pod-name", time.Now(), time.Now())
	require.NoError(t, err)
	require.NotNil(t, stats)

	e := errors.New("oops")
	engine.fs = &testFS{err: e}
	_, err = engine.ReadStats("pod-name", time.Now(), time.Now())
	require.Error(t, err)
	require.Equal(t, e, err)
}

func TestEngine_ReadFile(t *testing.T) {
	engine := &kubeEngine{
		fs: &testFS{},
	}

	reader, err := engine.ReadFile("pod-name", "file-path")
	require.NoError(t, err)
	require.NotNil(t, reader)
}

func TestEngine_String(t *testing.T) {
	engine := &kubeEngine{
		namespace: "default",
		config:    &rest.Config{Host: "1.2.3.4"},
	}

	require.Equal(t, "Kubernetes[default] @ 1.2.3.4", engine.String())
}

func newKubeEngineTest(client kubernetes.Interface, ns string, n int) *kubeEngine {
	r, w := io.Pipe()

	return &kubeEngine{
		config: &rest.Config{
			Host: "https://127.0.0.1:333",
		},
		writer:    bytes.NewBuffer(nil),
		client:    client,
		namespace: ns,
		options: &sim.Options{
			Topology: network.NewSimpleTopology(n, 50*time.Millisecond),
		},
		fs: &testFS{reader: r, writer: w},
	}
}

func makeEngine(n int) (*kubeEngine, *fake.Clientset) {
	client := fake.NewSimpleClientset()
	engine := newKubeEngineTest(client, "", n)

	return engine, client
}

func makeRouterPod() *apiv1.Pod {
	return &apiv1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "simnet-router",
			Labels: map[string]string{LabelID: RouterID},
		},
	}
}

type testFS struct {
	err     error
	errRead error
	reader  io.ReadCloser
	writer  io.WriteCloser
}

func (fs *testFS) Read(pod, container, path string) (io.ReadCloser, error) {
	r, w := io.Pipe()
	if fs.errRead != nil {
		w.CloseWithError(fs.errRead)
	} else {
		w.Close()
	}
	return r, fs.err
}

func (fs *testFS) Write(pod, container string, cmd []string) (io.WriteCloser, <-chan error, error) {
	return fs.writer, nil, fs.err
}

func setMockClient() {
	newClient = kubernetes.NewForConfig
}

func setMockBadClient() {
	newClient = func(*rest.Config) (*kubernetes.Clientset, error) {
		return nil, errors.New("client error")
	}
}

// Some methods are not faked correctly and thus provoking crashes during the
// tests. This client has the purpose of implementing missing features.
type fakeClientset struct {
	*fake.Clientset

	err    error
	reader io.ReadCloser
	writer io.WriteCloser
}

func newFakeClientset() *fakeClientset {
	r, w := io.Pipe()

	return &fakeClientset{
		Clientset: fake.NewSimpleClientset(),
		reader:    r,
		writer:    w,
	}
}

func (cli *fakeClientset) CoreV1() corev1.CoreV1Interface {
	return fakeCoreV1{
		FakeCoreV1: &fakecorev1.FakeCoreV1{},
		cli:        cli,
	}
}

type fakeCoreV1 struct {
	*fakecorev1.FakeCoreV1
	cli *fakeClientset
}

func (core fakeCoreV1) Pods(string) corev1.PodInterface {
	return fakePodInterface{
		cli: core.cli,
	}
}

type fakePodInterface struct {
	*fakecorev1.FakePods
	cli *fakeClientset
}

func (pi fakePodInterface) GetLogs(string, *apiv1.PodLogOptions) *rest.Request {
	cl := &fakerest.RESTClient{
		Err: pi.cli.err,
		Resp: &http.Response{
			StatusCode: 200,
			Header:     http.Header{},
			Body:       pi.cli.reader,
		},
	}

	return cl.Request()
}
