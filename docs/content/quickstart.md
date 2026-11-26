# Quickstart & Installation

`afc` (`agent-fault-check`) ships as a single static binary for **Linux (`amd64`, `arm64`)** and **macOS (`arm64`, `amd64`)**, plus multi-arch Docker images.

## Install OSS Core (Apache-2.0)

```bash
VERSION="{{VERSION}}"
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
TARBALL="afc_${VERSION#v}_${OS}_${ARCH}.tar.gz"

curl -fsSLO "https://github.com/evhu-dev/agent-fault-check/releases/download/${VERSION}/${TARBALL}"
curl -fsSLO "https://github.com/evhu-dev/agent-fault-check/releases/download/${VERSION}/checksums.txt"
grep " ${TARBALL}$" checksums.txt | shasum -a 256 -c -
tar -xzf "${TARBALL}" afc
sudo install -m 0755 afc /usr/local/bin/afc
afc version
```

## Install Pro / Team Binary

Commercial binaries (`afc-pro`) are hosted publicly at [`evhu-dev/agent-fault-check-releases`](https://github.com/evhu-dev/agent-fault-check-releases/releases/latest) and unlock Pro/Team features when `AFC_LICENSE_KEY` is set.

```bash
VERSION="{{VERSION}}"
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
TARBALL="afc-pro_${VERSION#v}_${OS}_${ARCH}.tar.gz"

curl -fsSLO "https://github.com/evhu-dev/agent-fault-check-releases/releases/download/${VERSION}/${TARBALL}"
curl -fsSLO "https://github.com/evhu-dev/agent-fault-check-releases/releases/download/${VERSION}/checksums.txt"
grep " ${TARBALL}$" checksums.txt | shasum -a 256 -c -
tar -xzf "${TARBALL}" afc
sudo install -m 0755 afc /usr/local/bin/afc
afc version
```

## Docker (GHCR)

```bash
docker pull ghcr.io/evhu-dev/agent-fault-check:{{VERSION_NUM}}
docker run --rm -v "$PWD:/work" -w /work \
  ghcr.io/evhu-dev/agent-fault-check:{{VERSION_NUM}} \
  validate --spec chaos-spec.yaml
```

## Validate & Run Your First Suite

```bash
afc validate --spec chaos-spec.yaml
afc run --spec chaos-spec.yaml --verbose
```
