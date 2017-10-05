## Development Docker Image

This image can be used to get started with the vnf-agent Go code. It contains:

- The development environment with all libs & dependencies required to build VPP / Go code,
- A pre-built vpp ready to be used,
- A pre-built Go agent.

### Getting the Image
You can either download a pre-built image or build the image
yourself on your local machine.
For getting latest image, type:
docker pull dockerhub.cisco.com/vnf-docker-dev/dev_vpp_agent:latest
Or you can browse through images and pick one you want here:
https://engci-maven-master.cisco.com/artifactory/webapp/#/artifacts/browse/tree/General/vnf-docker-dev-local/dev_vpp_agent


#### Building Locally
To build the image on your local machine,  type:
```
sudo docker build -t dev_vpp_agent .
```

#### Verifying a Created or Downloaded Image
You can verify the newly built or downloaded image as follows:

```
docker images
``` 

You should see something this:

```
REPOSITORY                       TAG                 IMAGE ID            CREATED             SIZE
dev_vpp_agent                    latest              0692f574f21a        11 minutes ago      3.58 GB
...
```
Get the details of the newly built image:

```
docker image inspect dev_vpp_agent
docker image history dev_vpp_agent
```
---

### Starting the Image
To start the image, type:
```
sudo docker run -it --name vpp_agent --privileged --rm dev_vpp_agent bash
```
To open another terminal:
```
sudo docker exec -it vpp_agent bash
```

### Running VPP and the Agent

**NOTE: The Agent will terminate if it cannot connect to VPP and
to an Etcd server. If the Agent can not connect to a Kafka broker, 
Kafka-related APIs will report an error.** 

Start VPP in one of two modes: 
 - If you don't need (or don't have) DPDK, use "vpp lite":

```
vpp unix { interactive } plugins { plugin dpdk_plugin.so { disable } }
```
Note: you most likely do not have DPDK support if you're doing development on your local laptop.

- If you want DPDK (with no PCI devices), use:

```
vpp unix { interactive } dpdk { no-pci }
```
Note that for DPDK, you would need to run the container in 
privileged mode (add `--privileged` option to `docker run`). 
For more options, please refer to the [VPP documentation](https://wiki.fd.io/view/VPP/Command-line_Arguments).

To run the Agent, do the following:
- Edit `/opt/vnf-agent/dev/etcd.conf` to point the agent to an ETCD 
  server that runs outside of the VPP/Agent container. The
  default configuration is:

```
insecure-transport: true
dial-timeout: 1000000000
endpoints: 
 - "172.17.0.1:2379"
```
*Note that if you start Etcd in its own container on the same
host as the VPP/Agent container (as described below), you can
use the default endpoint configuration as is. ETCD is by default
mapped onto the host at port 2379; the host's IP address 
will typically be 172.17.0.1, unless you change your Docker 
networking settings.*

- Edit `/opt/vnf-agent/dev/kafka.conf` to point the agent to a Kafka broker.
 The default configuration is:

```
addrs:
 - "172.17.0.1:9092"
```

*Note that if you start Kafka in its own container on the same
host as the VPP/Agent container (as described below), you can
use the default broker address configuration as is. Kafka will
be mapped  onto the host at port 9092; the host's IP address 
will typically be 172.17.0.1, unless you change your Docker 
networking settings.*

- Start the Agent:
```
vpp-agent --etcdv3-config=/opt/vnf-agent/dev/etcd.conf --kafka-config=/opt/vnf-agent/dev/kafka.conf
```

### Running Etcd Server on Local Host
You can run an ETCD server in a separate container on your local
host as follows:
```
sudo docker run -p 2379:2379 --name etcd --rm \
    quay.io/coreos/etcd:v3.0.16 /usr/local/bin/etcd \
    -advertise-client-urls http://0.0.0.0:2379 \
    -listen-client-urls http://0.0.0.0:2379
```
(ETCD server will be available on your host OS IP (most likely `172.17.0.1` 
in the default docker environment) on port `2379`)

Call the agent via ETCD using the testing client:
```
cd $GOPATH/src/gitlab.cisco.com/ctao/vnf-agent/agent_plugins/ifplugin/examples/
go run crud.go /opt/vnf-agent/dev/etcd.conf -ct
go run crud.go /opt/vnf-agent/dev/etcd.conf -mt
go run crud.go /opt/vnf-agent/dev/etcd.conf -dt
```

### Running Kafka on Local Host
You can start Kafka in a separate container:
```
sudo docker run -p 2181:2181 -p 9092:9092 --name kafka --rm \
 --env ADVERTISED_HOST=172.17.0.1 --env ADVERTISED_PORT=9092 spotify/kafka
```

### Rebuilding the Agent
```
cd $GOPATH/src/gitlab.cisco.com/ctao/vnf-agent/
git pull      # if needed
make
make test     # optional
make install
```

This should update the `agent` binary in yor `$GOPATH/bin` directory.

### Rebuilding of the container image:
Use the `--no-cache` flag for `docker build`:
```
sudo docker build --no-cache -t dev_vpp_agent .
```

### Mounting of a host directory into the Docker container:
Use `-v` option of the docker command:
```
sudo docker run -v /host/folder:/container/folder -it --name vpp_agent --privileged --rm dev_vpp_agent bash
```

E.g. if you have the vnf-agent code in `~/go/src/gitlab.cisco.com/ctao/vnf-agent/` 
on your host OS, you can mount it as `/root/go/src/gitlab.cisco.com/ctao/vnf-agent/` 
in the container as follows:
```
sudo docker run -v ~/go/src/gitlab.cisco.com/ctao/vnf-agent/:/root/go/src/gitlab.cisco.com/ctao/vnf-agent/ -it --name vpp_agent --rm dev_vpp_agent bash
```
Then you can modify the code on you host OS and us the container for building and testing it.

---

## Example: Using the Development Environment on a MacBook with Gogland
This section describes the setup of a lightweight, portable development environment on your local notebook (MacBook or MacBook Pro in this example). The MacBook will be the host for the Development Environment container and the folder containing the agent sources will be shared between the host and the container. Then, you can run the IDE, git, and other tools on the host and the compilations/testing in the container.

#### Prerequisites

1. Get [Docker for Mac](https://docs.docker.com/docker-for-mac/). If you don't have it already installed, follow the [install instructions](https://docs.docker.com/docker-for-mac/install/).

2. Install Go and the [Gogland](https://www.jetbrains.com/go/) IDE. Go can be downloaded from this [repository](https://golang.org/dl/), and its install instructions are [here](https://golang.org/doc/install). Gogland download and install instructions are [here](https://www.jetbrains.com/go/download/).   

2. Once you have Docker up & running on your Mac, build and verify the Development Environment container [as described above](#getting-the-image).

#### Building and Running the Agent
For the mixed host-container environment, the folder holding the 
Agent source code must be setup properly on both the host and in
 the container. You must also set GOPATH and GOROOT appropriately
in Gogland and in the development container. We will now walk you 
through these steps.

**On your Mac**:
- Create the Go home folder, for example `~/go/`

- Create the Go source folder in your Go home folder. The Go source
  folder must be called `src`. So you will have: ` ~/go/src`
  
- In the Go source folder, create the folder that will hold the 
  local clone of the vnf-agent repository. The path for the folder
  must reflect the import path in Go source code. So, the vnf-agent
  repository folder will be created as follows:
```
   cd  ~/go/src
   mkdir gitlab.cisco.com
   mkdir gitlab.cisco.com/ctao
```
  
- The agent repository is located at `http://gitlab.cisco.com/ctao/vnf-agent`. 
  Go into your local vnf-agent repo folder and checkout the Agent code:
```
   cd gitlab.cisco.com/ctao
   git clone http://gitlab.cisco.com/ctao/vnf-agent.git 
```

- In Gogland, set `GOROOT` and `GOPATH` (`Preferences->Go->GOROOT`, `Preferences->Go->GOPATH`). Set `GOROOT` to where Go is installed (default: `/usr/local/go`) and the global `GOPATH` to the location of your Go home folder (`/Users/jmedved/Documents/Git/go-home/` in our example).

- Create a new project in Gogland (`File->New->Project`, ) will popup the project creation window. Enter your newly created Go home folder (`/Users/jmedved/Documents/Git/go-home/` in our example) as the location and accept the default value for the SDK. Click `Create`, and you should now be able to browse the Agent source code in Gogland.

- Start the Development Environment container with the -v option, mounting the Go home folder in the container. With our example, and assuming that we want to mount our Go home folder into the `root/go-home` folder, type:

```
   sudo docker run -v ~/go/src/gitlab.cisco.com/ctao/vnf-agent/:/root/go/src/gitlab.cisco.com/ctao/vnf-agent/ -it --name vpp_agent --rm dev_vpp_agent bash
```
The above command will put you into the Development Environment container console. 

**In the container console**:

- Setup the GOPATH and PATH variables in the Development Environment container:

```
   export GOPATH=/root/go
   export PATH=/$GOPATH/bin:$PATH
```
- Change directory into the vnf-agent folder and build & install the Agent:
```
   cd /root/go-home/src/gitlab.cisco.com/ctao/vnf-agent
   make
   make install
```
 
- Use the newly built agent as described in Section
  '[Running VPP and the Agent](#running-vpp-and-the-agent)'.

