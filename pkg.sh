#!/bin/sh

set -e

rm -rf redist
mkdir -p redist/windows-amd64
mkdir -p redist/windows-386
mkdir -p redist/linux-amd64
mkdir -p redist/darwin-amd64

export CGO_ENABLED=0

pushd client/cmd/dexc
GOOS=linux GOARCH=amd64 go build -v -trimpath -o ../../../redist/linux-amd64
GOOS=windows GOARCH=amd64 go build -v -trimpath -o ../../../redist/windows-amd64
GOOS=windows GOARCH=386 go build -v -trimpath -o ../../../redist/windows-386
GOOS=darwin GOARCH=amd64 go build -v -trimpath -o ../../../redist/darwin-amd64
popd

pushd client/cmd/dexcctl
GOOS=linux GOARCH=amd64 go build -v -trimpath -o ../../../redist/linux-amd64
GOOS=windows GOARCH=amd64 go build -v -trimpath -o ../../../redist/windows-amd64
GOOS=windows GOARCH=386 go build -v -trimpath -o ../../../redist/windows-386
GOOS=darwin GOARCH=amd64 go build -v -trimpath -o ../../../redist/darwin-amd64
popd

pushd client/webserver/site
npm ci
npm run build
popd

# 7za a -mx=9 site.zip site/dist site/src/font site/src/html site/src/img

rm -rf redist/site
mkdir -p redist/site/src
pushd client/webserver/site
cp -R dist ../../../redist/site
cp -R src/font src/html src/img ../../../redist/site/src
popd

pushd redist
cp -R site darwin-amd64
cp -R site linux-amd64
cp -R site windows-386
cp -R site windows-amd64
pushd windows-amd64
zip -q dexc-windows-amd64.zip *
popd
pushd windows-386
zip -q dexc-windows-386.zip *
popd
pushd linux-amd64
tar -I 'gzip -9' -cf dexc-linux-amd64.tar.gz ./*
popd
pushd darwin-amd64
tar -I 'gzip -9' -cf dexc-darwin-amd64.tar.gz ./*
popd
popd
