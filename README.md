# Simnet

[![Build Status](https://travis-ci.org/dedis/simnet.svg?branch=master)](https://travis-ci.org/dedis/simnet)
[![Coverage Status](https://coveralls.io/repos/github/dedis/simnet/badge.svg?branch=master)](https://coveralls.io/github/dedis/simnet?branch=master)

Simnet is a tool to simulate a decentralized application using the cloud to
host the simulation nodes. It provides multiple configurations to affect the
topology so that specific link between two nodes can have a delay or a loss
of packets.

TODO: extend the doc

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

## Strategies

The simulation uses strategies to manage the deployment and running of the rounds.
A kubernetes strategy is provided and used by default.

TODO: Docker strategy for local simulations ?

### Kubernetes

When a simulation is deployed to a Kubernetes cluster, each instance of the
application uses a POD that will contain the application Docker container
alongside with a monitor container that will gather data for the statistics.

One POD will also be used to deploy a router that will simply run OpenVPN so
that the simulation can open a tunnel to the cluster network and thus make
requests to the nodes.

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

# After the simulation has successfully completed, it will generate a file
# with the results. A tool is provided to easily generate plots.
make ARGS="-cpu" plot
ls example.png

make ARGS="-mem" plot
make ARGS="-tx" plot
make ARGS="-rx" plot

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
