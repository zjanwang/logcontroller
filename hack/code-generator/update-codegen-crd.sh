#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

REPO_ROOT=$(git rev-parse --show-toplevel)
cd "${REPO_ROOT}"

INPUTDIR=$1
OUTPUTPKG=$2

echo $INPUTDIR
echo $OUTPUTPKG

echo "Generating with deepcopy-gen"
GO111MODULE=on go install k8s.io/code-generator/cmd/deepcopy-gen
export GOPATH=$(go env GOPATH | awk -F ':' '{print $1}')
export PATH=$PATH:$GOPATH/bin

deepcopy-gen \
  --go-header-file $(dirname "${BASH_SOURCE[0]}")/boilerplate.go.txt \
  --input-dirs=$INPUTDIR \
  --output-package=$INPUTDIR \
  --output-file-base=zz_generated.deepcopy

echo "Generating with register-gen"
GO111MODULE=on go install k8s.io/code-generator/cmd/register-gen
register-gen \
  --go-header-file $(dirname "${BASH_SOURCE[0]}")/boilerplate.go.txt \
  --input-dirs=$INPUTDIR \
  --output-package=$INPUTDIR \
  --output-file-base=zz_generated.register

echo "Generating with client-gen"
GO111MODULE=on go install k8s.io/code-generator/cmd/client-gen
client-gen \
  --go-header-file $(dirname "${BASH_SOURCE[0]}")/boilerplate.go.txt \
  --input-base="" \
  --input=$INPUTDIR \
  --output-package=$OUTPUTPKG/client/clientset \
  --clientset-name=versioned


echo "Generating with lister-gen"
GO111MODULE=on go install k8s.io/code-generator/cmd/lister-gen
lister-gen \
  --go-header-file $(dirname "${BASH_SOURCE[0]}")/boilerplate.go.txt \
  --input-dirs=$INPUTDIR \
  --output-package=$OUTPUTPKG/client/listers

echo "Generating with informer-gen"
GO111MODULE=on go install k8s.io/code-generator/cmd/informer-gen
informer-gen \
  --go-header-file $(dirname "${BASH_SOURCE[0]}")/boilerplate.go.txt \
  --input-dirs=$INPUTDIR \
  --versioned-clientset-package=$OUTPUTPKG/client/clientset/versioned \
  --listers-package=$OUTPUTPKG/client/listers \
  --output-package=$OUTPUTPKG/client/informers
