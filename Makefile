ARCH=amd64
VERSION ?= 1.0.0-amd64
NODEIMG = log-controller:$(VERSION)
REGISTRY ?= 10.120.1.233:8443/k8s-deploy
generate-crd:
	./hack/code-generator/update-codegen-crd.sh harmonycloud.cn/log-collector/pkg/apis/logcollector/v1alpha1 harmonycloud.cn/log-collector/pkg
	controller-gen rbac:roleName=logcollector  crd:crdVersions=v1 webhook paths="./pkg/apis/..." output:crd:artifacts:config=kube/crd/bases

build-node:
	CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) go build -o main ./cmd/main.go
	docker build -f build/Dockerfile-$(ARCH) -t $(NODEIMG) .

push-node:
	docker tag $(NODEIMG) $(REGISTRY)/$(NODEIMG)
	docker push $(REGISTRY)/$(NODEIMG)