# Crusoe Container Storage Interface (CSI) Driver

This repository defines the official Container Storage Interface (CSI) Driver for use with [Crusoe Cloud](https://crusoecloud.com/), the world's first carbon-reducing, low-cost GPU cloud platform.

## Getting Started

Please follow the [Helm installation instructions](https://github.com/crusoecloud/crusoe-csi-driver-helm-charts) to install the CSI Driver.

## Shared Filesystem Support

The shared filesystem driver (`fs.csi.crusoe.ai`) advertises the topology label `fs.csi.crusoe.ai/supports-shared-disks=true` only on instance shapes where the underlying NFS data path is available. Dynamic provisioning of shared volumes will fail with `ResourceExhausted` (`could not find topology constraint with fs.csi.crusoe.ai/location and fs.csi.crusoe.ai/supports-shared-disks segments`) if no node in the PVC's topology requirements advertises this capability.

The currently supported shapes are:

| Family             | Required slice size | Notes                                  |
|--------------------|---------------------|----------------------------------------|
| `c1a`, `s1a`       | any                 | All CPU shapes supported               |
| `c2a`, `s2a`       | any                 | All CPU shapes supported               |
| `l40s-48gb`        | `.10x` (full only)  | 10 slices per host                     |
| `gb200-186gb*`     | `.4x` (full only)   | 4 slices per host                      |
| All other GPU      | `.8x` (full only)   | e.g. `h100-80gb.8x`, `b200-180gb.8x`   |

The authoritative source is `supportsFS()` in [`internal/node/fs/util.go`](./internal/node/fs/util.go). New CPU shapes should be added to the `cpuInstanceFamilies` slice in the same file.
