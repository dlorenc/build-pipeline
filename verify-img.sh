#!/bin/bash
set -ex
img=$1

tmp=$(mktemp -d)
function exit() {
    rm -rf $tmp
}
# trap exit EXIT

dgst=$(echo $img | cut -d':' -f2)
repo=$(echo $img | cut -d'@' -f1)
sigimg=$repo:$dgst.sig
pushd $tmp
crane pull $sigimg img.tar
layer=$(tar -tf img.tar | grep tar.gz)
tar -xOf img.tar $layer | tar -x
gpg --verify signature body.json
popd
mv $tmp/body.json .