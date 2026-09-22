# Changelog

## [Unreleased]

### Changed

- **The `fs` driver (`fs.csi.crusoe.ai`) now stages shared volumes once per node.**
  It mounts each shared NFS export a single time per node (`NodeStageVolume`) and
  bind-mounts that into each pod, instead of mounting the export separately for every
  pod. This removes the per-pod mount/umount churn on the shared NFS superblock that
  drove the mount stalls in INC-648. The `ssd` driver is unchanged.
- Pod-visible mount paths are unchanged. A pod still sees its volume at the same
  target path; only the source becomes a bind of the per-node mount.

### Upgrade notes

- An in-place `helm upgrade` from a pre-staging driver is safe and needs no node
  drain. Pods that are already running keep their mounts across the driver
  DaemonSet restart (the mount lives in the node kernel, not the driver pod).
- A pod that reschedules onto a node after the upgrade is staged on demand by
  `NodePublishVolume`, so it gets a real mount, not an empty one. (kubelet does not
  re-issue `NodeStageVolume` for a volume it already recorded as device-mounted
  before the upgrade; the driver handles that case itself.)

### Downgrade / rollback caveat

- Rolling the `fs` driver **back** to a pre-staging (no-stage) version is safe for
  running workloads (existing mounts survive), but it leaves one orphaned per-node
  NFS mount behind for each shared volume that was in use. The no-stage driver does
  not implement `NodeUnstageVolume`, so kubelet cannot reclaim the staged mount.
  **There is no data loss.**
- The orphaned mount is silent and persists until it is cleaned up manually or the
  node is rebooted/recreated. It holds the NFS export (and the disk attach) open, so
  clean it up before detaching that disk or draining the node.
- Cleanup, per affected node: `umount` the orphaned mount at
  `/var/lib/kubelet/plugins/kubernetes.io/csi/fs.csi.crusoe.ai/<hash>/globalmount`
  (run it in the node's mount namespace, e.g. `nsenter -t 1 -m -- umount <path>`).
- Validated on CMK (kubelet 1.33.4): upgrade path via the automated functest
  (CRUSOE-103809), downgrade path manually.
