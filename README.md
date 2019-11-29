# Simnet

Simnet is a tool to simulate a decentralized application using the cloud to
host the simulation nodes. It provides multiple configurations to affect the
topology so that specific link between two nodes can have a delay or a loss
of packets.

TODO: extend the doc

Important: not all Docker images are deployed so it cannot currently be run
on a live cluster.

## Engines

The simulation uses engines to manage the deployment and running of the rounds.
A kubernetes engine is provided and used by default.
TODO: Docker engine for local simulations ?

# How to

## How to simulate with Kubernetes locally ?

This section describes a setup using Minikube to run a local Kubernetes cluster
where the simulation can be deployed.

Caveat: Minikube VM does not have the _netem_ module which means Chaos testing
does not work out of the box and requires a few steps more. TODO: show how.

### Install Minikube

Follow the instructions [here](https://minikube.sigs.k8s.io/docs/start/) then

```bash
# Start the cluster.
minikube start

# In case of error, you can first try to delete the previous cluster.
minikube delete

# After the cluster has started, you can monitor with the dashboard.
minikube dashboard
```

### Build the docker images

TODO: this step won't be necessary anymore after the images are deployed!

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
make run # be patient, time for a coffee !

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
