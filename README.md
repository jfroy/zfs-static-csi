# zfs-static-csi

A small Kubernetes CSI driver that exposes existing ZFS datasets as static
`PersistentVolume`s. It is a more ergonomic, fail-closed replacement for
`local`/`hostPath` PVs in clusters where ZFS already manages the underlying
storage.

The driver does **not** create or delete datasets. The only operations it
performs are reading dataset properties and bind-mounting a dataset's existing
mountpoint into the kubelet pod directory.

## Model

* An administrator opts a dataset into being exposed by setting a single ZFS
  user property:

  ```sh
  zfs set com.github.jfroy.zfs-static-csi:share=on tank/data/myapp
  ```

* The administrator authors a `PersistentVolume` whose `csi.volumeHandle` is
  the dataset name (e.g. `tank/data/myapp`).

* When kubelet calls `NodePublishVolume`, the driver:
  1. shells out to `zfs get` (running chrooted into the host root so it uses
     the host's `zfs(8)` — guaranteed ABI-matched with the host kernel ZFS
     module) to read `type`, `mountpoint`, `mounted`, and the
     `com.github.jfroy.zfs-static-csi:share` property of the dataset;
  2. fails with `NotFound` if the dataset is not on this node;
  3. fails with `FailedPrecondition` if the property is unset, the dataset
     isn't a filesystem, or it isn't currently mounted;
  4. otherwise bind-mounts the dataset's mountpoint into the pod's kubelet
     target path.

The runtime container image is `gcr.io/distroless/static-debian13` plus the
static Go binary — no glibc, no zfs userspace, no shell. The host must have
`zfsutils-linux` installed (which it does already, since the host is the one
running ZFS).

## Why not local PVs?

* No mountpoint paths in PV specs — the driver looks them up from ZFS.
* Datasets must be **explicitly** opted in; system datasets cannot leak into
  pods by accident.
* Fails closed if the dataset is missing or unmounted. Local PVs don't
  verify the path is a mount point — if the dataset isn't mounted, they
  happily bind whatever is at the path (usually the node's root filesystem)
  into the pod.

## What this driver is **not**

* It is not a provisioner. There is no `CreateVolume`/`DeleteVolume`. Use
  `zfs create` / `zfs destroy` out of band.
* It is not a multi-node sharer. Each PV is bound to the node that holds the
  dataset.
* It does not support zvols / block volumes (filesystem datasets only).
* It does not support snapshots, clones, or volume expansion.
* It does not enforce the `capacity.storage` declared on the PV — that field
  is purely metadata for the scheduler and quota system.

## Layout

```
cmd/zfs-static-csi/         entrypoint
pkg/driver/                 Identity + Node CSI services
pkg/zfs/                    thin wrapper around the `zfs` binary
charts/zfs-static-csi/      Helm chart (single source of truth for manifests)
examples/                   example StorageClass + PV + PVC
```

By default the DaemonSet schedules on every node and fails closed on nodes
without ZFS. Operators typically constrain it to storage nodes via
`nodeSelector` or `affinity` — see the chart's `values.yaml` for examples
(custom label, or the Talos `extensions.talos.dev/zfs` Exists pattern). Each
pod contains the CSI driver and `csi-node-driver-registrar`; no controller
pod is deployed because there is no Controller service.

## Build

```sh
make test     # run unit tests
make build    # build ./bin/zfs-static-csi
make image    # build the container image
```

## Deploy

### Helm (recommended)

The chart is published as an OCI artifact to GHCR and signed with cosign on
every tagged release.

```sh
helm install zfs-static-csi \
  oci://ghcr.io/jfroy/charts/zfs-static-csi \
  --version <X.Y.Z> \
  --namespace kube-system
```

To constrain placement, pass `--set-json` or a values file with a
`nodeSelector` or `affinity` (see the chart's `values.yaml`).

### Without Helm at install time

If you don't want Helm in the install path, render the chart locally and
apply the result. The chart is the only source of truth for manifests:

```sh
helm template zfs-static-csi charts/zfs-static-csi \
  --namespace kube-system \
  | kubectl apply -n kube-system -f -
```

### Verifying signatures

Both the container image and the Helm chart are signed via cosign keyless
signing using GitHub Actions OIDC. Verify before installing:

```sh
# Container image
cosign verify ghcr.io/jfroy/zfs-static-csi:<X.Y.Z> \
  --certificate-identity-regexp '^https://github.com/jfroy/zfs-static-csi/\.github/workflows/image\.yaml@.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com

# Helm chart
cosign verify ghcr.io/jfroy/charts/zfs-static-csi:<X.Y.Z> \
  --certificate-identity-regexp '^https://github.com/jfroy/zfs-static-csi/\.github/workflows/chart\.yaml@.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## Use a dataset

```sh
# 3. Opt the dataset in.
zfs set com.github.jfroy.zfs-static-csi:share=on tank/data/myapp

# 4. Apply the example PV/PVC (edit the dataset name, capacity and
#    nodeAffinity hostname first).
kubectl apply -f examples/pv-pvc.yaml
```

## Troubleshooting

* `NotFound` from the driver: the dataset doesn't exist on the node where
  the pod is scheduled, or the pool isn't imported. Check the pod's node and
  run `zfs list <dataset>` there.
* `FailedPrecondition: dataset ... is not opted in`: set the share property
  with `zfs set com.github.jfroy.zfs-static-csi:share=on <dataset>`.
* `FailedPrecondition: dataset ... is not mounted`: ZFS hasn't mounted the
  dataset on this node. Check `mountpoint` is not `none`/`legacy` and run
  `zfs mount <dataset>`.
* Pod stuck `ContainerCreating`: kubelet logs and the
  `zfs-static-csi-node` DaemonSet pod logs (`-c csi-driver`) include the
  full CSI request/response trace at `--v=2`.

## License

Apache 2.0. See [LICENSE](LICENSE).
