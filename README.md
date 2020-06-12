# Simnet

[![Build Status](https://travis-ci.org/dedis/simnet.svg?branch=master)](https://travis-ci.org/dedis/simnet)
[![Coverage Status](https://coveralls.io/repos/github/dedis/simnet/badge.svg?branch=master)](https://coveralls.io/github/dedis/simnet?branch=master)
![GitHub tag (latest SemVer)](https://img.shields.io/github/v/tag/dedis/simnet?label=version)

SimNet is a tool to simulate a decentralized application using the cloud to
host the simulation nodes. It provides multiple configurations to affect the
topology so that specific link between two nodes can have a delay or a loss
of packets for instance.

The tool is designed to work with three phases:
 - Deployment: everything the simulation needs is deployed to the cloud.
 - Execution: the actual simulation is performed and statistics are written
 to a JSON file.
 - Cleaning: it wipes everything in scope to the simulation.

Thoses phases are designed to be independant so that they can be run separatly
which means that simulations can be executed multiple times without deploying.

## Strategies

The environment the simulation is running on depends on the chosen strategy. Two
are currently available: Kubernetes and Docker.

It is completly interchangeable so that you can test your simulation locally with
Docker and a small number of nodes. Then the simulation can be deployed on
Kubernetes with a bigger amount of nodes.

### Kubernetes

When a simulation is deployed to a Kubernetes cluster, each instance of the
application uses a POD that will contain the application Docker container
alongside with a monitor container that will gather data for the statistics.

One POD will also be used to deploy a router that will simply run OpenVPN so
that the simulation can open a tunnel to the cluster network and thus make
requests to the nodes.

### Docker

Simulations with the Docker strategy will interact with the local Docker setup
to create one container per Node. Each of those containers will need another
temporary container to configure the network emulation (latencies, etc...) but
they will terminate before the simulation begins and they are booted one after
the other.

## Daemons

Some strategies like Kubernetes might need additionnal containers to work. These
are built independently using the automated build of DockerHub.

| Router | Router Init | Monitor |
| ------ | ----------- | ------- |
| [![Docker Cloud Build Status](https://img.shields.io/docker/cloud/build/dedis/simnet-router?style=flat-square)](https://hub.docker.com/repository/docker/dedis/simnet-router/timeline) | [![Docker Cloud Build Status](https://img.shields.io/docker/cloud/build/dedis/simnet-router-init?style=flat-square)](https://hub.docker.com/repository/docker/dedis/simnet-router-init/timeline) | [![Docker Cloud Build Status](https://img.shields.io/docker/cloud/build/dedis/simnet-monitor?style=flat-square)](https://hub.docker.com/repository/docker/dedis/simnet-monitor/timeline) |

Docker images for the daemons have their own version that will trigger a build
for each new version. The master branch will also build its own tag for each
new commit.

Dockerhub is configured to automatically create the following tags:
- `latest` for the master branch
- `x.y.z` for each `daemon-vx.y.z` tag on the repository

# Getting started

## Setup

To run simulations with Kubernetees, **OpenVPN** needs to be installed.

By default, simnet looks for OpenVPN in `/usr/local/opt/openvpn/sbin/openvpn` (that should be the case for example if you used Homebrew on MacOS to install OpenVPN), but you can specify a different path with the `-vpn` CLI option.

For **MacOS** users, OpenVPN is also required to run Docker simulation (this is due to MacOS runing docker via a VM).

## Writing the simulation

A basic template with the Docker strategy is shown below:

```go
type simRound struct {}

func (s simRound) Execute(ctx context.Context) error {
    return nil
}

func main() {
    engine, err := docker.NewStrategy(options...)
	if err != nil {
		panic(err)
	}

	sim := simnet.NewSimulation(simRound{}, engine)

	err = sim.Run(os.Args)
	if err != nil {
		panic(err)
	}
}
```

A few number of options is necessary to run an application.

```go
options := []sim.Option{
    sim.WithTopology(
        net.NewSimpleTopology(3, 25),
    ),
    sim.WithImage(
        "nginx", // DockerHub image
        nil, // CMD if needed
        nil, // ARGS if needed
        sim.NewTCP(8080),
    ),
}
```

Finally, the simulation tool provides a bunch of command-line arguments so that
it is easier to run the different phases. Assuming a `main.go`, use the
following:

```bash
# Runs the simulation from A to Z.
go run main.go

# ... or do it step by step
go run main.go -do-deploy

# This can be done multiple times but be aware that statistics will be
# overwritten.
go run main.go -do-execute

# Important to reduce the cost
go run main.go -do-clean
```

### Plots

First you need to install the plot tool
```bash
go get -u go.dedis.ch/simnet/metrics/simplot
```

Then to draw a plot
```bash
simplot -tx -output plot-tx.png
simplot -rx -output plot-rx.png
simplot -cpu -output plot-cpu.png
simplot -mem -output plot-mem.png
```

## Context

The context passed to the round execution contains some pieces of information
that can be useful when running a simulation.

### NodeInfo
```go
nodes := ctx.Value(sim.NodeInfoKey{}).([]sim.NodeInfo)
addr := nodes[0].Address
```

### Files
A file mapper can be specifiec in the strategy options. This file will be read
on each application node and stored in the execution context.

```go
files := ctx.Value(sim.FilesKey("my.conf")).(sim.Files)
for ident, file := range files {
    ...
}
```

See the skipchain example for more details.

# How to

## How to simulate with Kubernetes locally ?

This section describes a setup using Minikube to run a local Kubernetes cluster
where the simulation can be deployed.

Caveat: Minikube VM does not have the _sch_netem_ module which means Chaos
testing does not work out of the box and requires a few steps more.

### Install Minikube

Follow the instructions [here](https://minikube.sigs.k8s.io/docs/start/) then

```bash
# Start the cluster.
minikube start --docker-opt bip=172.18.0.1/16

# In case of error, you can first try to delete the previous cluster.
minikube delete

# After the cluster has started, you can monitor with the dashboard.
minikube dashboard
```

### Build the docker images

A local change to the daemon images can be used by building the images inside
minikube and forcing it to use the local ones. You will need to add "Never" for
the image pull policy in the kubernetes deployments.

```bash
# Set the docker environment variables for this session.
eval $(minikube docker-env)

cd $SIMNET_DIR
make build_monitor
make build_router
```

Mininet will now have the required images in its local images store. The
simulation image still need to be deployed on DockerHub.

### Run the simulation

The simulation is now ready to be run.

```bash
cd $SIMNET_DIR
make EXAMPLE="skipchain" run # be patient, time for a coffee !
# or make run to run the default simulation.

# In case the simulation fails and cannot clean the cluster correctly, you
# can force the deletion.
make clean
```

### Chaos testing in Minikube

This section describes the steps to get a local cluster that supports Chaos
by enabling the right kernel module in the Minikube ISO.

```bash
cd $HOME/workspace # ... or a different folder
git clone git@github.com:kubernetes/minikube.git

cd minikube
make buildroot-image
make out/minikube.iso
make linux-menuconfig

# Inside the Kconfig menu, you will to navigate to
#   --> Networking support
#   --> Networking options
#   --> QoS and/or fair queuing
#   --> Network Emulator (NETEM) + HTB
# Press space until you have <*> for NETEM
# Finally, save and close the menu

make out/minikube.iso
./out/minikube delete
./out/minikube start --iso-url=file://$(pwd)/out/minikube.iso
```

You can check that you are good to go by checking that the Minikube VM has
the module enabled.

```bash
./out/minikube ssh
$ modprobe sch_netem # should not display an error
```
