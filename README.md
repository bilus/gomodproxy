# gomodproxy

[![Build Status](https://travis-ci.org/sixt/gomodproxy.svg?branch=master)](https://travis-ci.org/sixt/gomodproxy)
[![Go Report Card](https://goreportcard.com/badge/github.com/sixt/gomodproxy)](https://goreportcard.com/report/github.com/sixt/gomodproxy)
[![Docker Hub](https://images.microbadger.com/badges/image/sixtlabs/gomodproxy.svg)](https://hub.docker.com/r/sixtlabs/gomodproxy/)

gomodproxy is a caching proxy for [Go modules].

Go 1.11 has introduced optional proxy support via GOPROXY environment variable.  It is essential for use cases where you want to have better control over your dependencies and handle scenarios when GitHub is down or some open-source dependency has been removed.

## Releasing

See https://goreleaser.com/quick-start/

## Getting started

gomodproxy requires Go 1.11 or newer. There are no plans to support `vgo` or Go 1.11 beta versions.

```
# Download and install from sources
git clone https://github.com/sixt/gomodproxy.git
cd gomodproxy
go build ./cmd/gomodproxy

# Run it
./gomodproxy -addr :8000

# Build some project using the proxy, dependencies will be downloaded to $HOME/.gomodproxy
...
GOPROXY=http://127.0.0.1:8000 go build
```

To let gomodproxy access the private Git repositories you may provide SSH keys or username/password for HTTPS access:

```
./gomodproxy \
  -git bitbucket.org/mycompany:/path/to/id_rsa \
  -git example.com/:/path/to/id_rsa_for_example_com \
  -git github.com/mycompany:username:password
```

## Features

* Small, pragmatic and easy to use.
* Self-contained, does not require Go, Git or any other software to be installed. Just run the binary.
* Supports Git out of the box. Other VCS are going to be supported via the plugins.
* Caches dependencies in memory or to the local disk. S3 support is planned in the nearest future. Other store types are likely to be supported via plugins.

## How it works

The entry point is cmd/gomodproxy/main.go. According to the given command-line flags it initializes:
* the `api` package implementing for Go proxy API
* the `vcs` package implementing the Git client
* the `store` package implementing in-memory and disk-based stores

### API

API package implements the HTTP proxy API as described in the [Go documentation].

**GET /:module/@v/list**

Queries the VCS to retrieve either a list of version tags, or the latest commit hash if the package does not use semantic versioning. This is the only request that is not cached and always contains the recent VCS hosting information.

**GET /:module/@v/:version.info**

Returns a JSON specifying the module version and the timestamps of the corresponding commit.

**GET /:module/@v/:version.mod**

If a `go.mod` file is present in the sources of the requested module - it is returned unmodified. Otherwise a minimal synthetic `go.mod` with no required module dependencies is generated.

**GET /:module/@v/:version.zip**

Returns ZIP archive contents with the snapshot of the requested module version. To keep the checksums unchanged, we follow the same (sometimes weird) refinements as does the Go tool - stripping off vendor directories, setting file timestamps back to 1980 etc.

On every request API tries to look for a module in the caches, and if it's not there - it fetches the requested revision using the `vcs` package and fulfils the caches.

### VCS

VCS package defines an interface for a typical VCS client and implements a Git client using `go-git` library:

It closely follows the logic of how `go get` fetches the modules, and implements all the quirks, such as go-imports meta tag resolution, or removing vendor directories from the repos.

The plugins are planned to be implemented as external command-line utilities written in any programming language. The protocol is to be defined yet.

### Store

Store package defines an interface for a caching store and provides the following store implementations:

* In-memory LRU cache of given capacity
* Disk-based directory cache
* S3 store

Other store implementations are planned to be supported similarly to VCS plugins, as external utilities following a defined command-line protocol.

## Contributing

The code in this project is licensed under Apache 2.0 license.

Please, use the search tool before opening a new issue. If you have found a bug please provide as many details as you can. Also, if you are interested in helping this project you may review the open issues and leave some feedback or react to them.

If you make a pull request, please consider the following:

* Open your pull request against `master` branch.
* Your pull request should have no more than two commits, otherwise please squash them.
* All tests should pass.
* You should add or modify tests to cover your proposed code changes.
* If your pull request contains a new feature, please document it in the README.

[Go modules]: https://github.com/golang/go/wiki/Modules
[Go documentation]: https://golang.org/cmd/go/#hdr-Module_proxy_protocol
