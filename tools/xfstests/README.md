# Local xfstests environment

This directory provides an isolated Linux environment for running xfstests
against FlexRAID. Host development packages are not required; only Docker
Engine with the Compose plugin is required.

The container uses the host Linux kernel for FUSE, so run this on a Linux host.
Docker Desktop on macOS/Windows does not provide an equivalent FUSE setup.

## Quick start

From the repository root:

```bash
./tools/xfstests/xfstests build
./tools/xfstests/xfstests run generic/089
```

Open an interactive container shell:

```bash
./tools/xfstests/xfstests shell
```

Inside the container, the xfstests source is at `/opt/xfstests`. For example:

```bash
cd /opt/xfstests
./check generic/089
```

List the generic tests marked `auto` in xfstests' group list:

```bash
./tools/xfstests/xfstests list
```

Run the complete generic `auto` group:

```bash
./tools/xfstests/xfstests run-generic
```

## Data and results

Virtual disk directories, mountpoint, generated FlexRAID configuration, logs,
and xfstests results are stored under `tools/xfstests/data/` on the host.
They survive container removal and image rebuilds.

`clean` removes Compose containers only; it does not delete test data.

## Rebuild xfstests

The image builds xfstests from `kdave/xfstests`. The default ref is `master`.
To use another branch, tag, or commit:

```bash
XFSTESTS_REF=<git-ref> ./tools/xfstests/xfstests build
```

Rebuild the image after changing the ref.

## FUSE permissions

The initial Compose configuration uses `privileged: true` and passes
`/dev/fuse` into the container to make FUSE mount operations work reliably
during initial setup. This is intentionally broad; once the environment is
verified on the target host, the required capabilities can be narrowed.

The container runs as root because xfstests requires privileged filesystem
operations. It uses the host kernel and does not load a separate kernel or
mount a virtual block device for the FUSE test filesystem.
