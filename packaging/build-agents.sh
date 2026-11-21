#!/bin/sh
# build-agents.sh OUTDIR VERSION: builds the agent the application runs on remote hosts,
# cmd/ttt-agent, for each platform it supports, into OUTDIR as ttt-agent-GOOS-GOARCH.gz.
# VERSION must be the version the application is built with, as the -X of version.Version:
# the application only talks to an agent of its own version. The agents are static, with
# no cgo, so they run whatever the host's C library, and are gzipped without names or
# times, so builds are reproducible.
set -eu
out=$1
version=$2
root=$(cd "$(dirname "$0")/.." && pwd)
mkdir -p "$out"
for platform in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do
	goos=${platform%/*}
	goarch=${platform#*/}
	bin="$out/ttt-agent-$goos-$goarch"
	(cd "$root" && CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -trimpath \
		-ldflags "-s -w -buildid= -X github.com/wrouesnel/tmux-tabbed-terminal/version.Version=$version" \
		-o "$bin" ./cmd/ttt-agent)
	gzip -9nf "$bin"
done
