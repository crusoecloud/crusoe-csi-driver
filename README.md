# Crusoe Container Storage Interface (CSI) Driver

This repository defines the official Container Storage Interface (CSI) Driver for use with [Crusoe Cloud](https://crusoecloud.com/), the world's first carbon-reducing, low-cost GPU cloud platform.

## Getting Started

### Prerequisites

This guide assumes that the user has already set up a Container Orchestrator (CO) on Crusoe Cloud compute. 
If you have not, we would recommend beginning by deploying one of our existing solutions – 
the [Crusoe Cloud RKE2 solution](https://github.com/crusoecloud/crusoe-ml-rke2) is a great place to start.

### Setting up credentials

As the CSI Driver will communicate with the Crusoe Cloud API to orchestrate storage operations, you will have to set up
credentials in your Kubernetes cluster which the driver can then use to communicate with the API. Here is a `.yaml` file 
which can be modified with your credentials and applied to your cluster (using `kubectl apply -f credentials.yaml -n $CRUSOE_CSI_NAMESPACE`).

By default, the driver will use the `crusoe-api-keys` secret.

```yaml
apiVersion: v1
data:
  CRUSOE_CSI_ACCESS_KEY: <base-64 encoded Crusoe Access Key>
  CRUSOE_CSI_SECRET_KEY: <base-64 encoded Crusoe Secret Key>
kind: Secret
metadata:
  name: crusoe-api-keys
  namespace: crusoe-csi-driver

```

### Installing the Driver

We recommend using Helm to install the CSI driver.