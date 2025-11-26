# Build the manager binary
ARG repository
FROM --platform=$BUILDPLATFORM ${repository}golang:1.17 as builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /workspace
ENV GO111MODULE on
ENV GOPROXY https://goproxy.cn,direct
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY cmd cmd/
COPY pkg pkg/

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH}  go build -a -o main ./cmd/main.go

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM --platform=${TARGETARCH} ${repository}centos:7.9.2009
ARG TARGETOS
ARG TARGETARCH
WORKDIR /
COPY --from=builder /workspace/main .
RUN mkdir -p /etc/log-controller/template
RUN mkdir -p /etc/log-controller/global
RUN chmod +x main
ENV FILEBEAT_VERSION=8.15.2
COPY filebeat-${FILEBEAT_VERSION}-linux-${TARGETARCH}.tar.gz /tmp/
RUN mkdir -p /etc/filebeat /var/lib/filebeat /var/log/filebeat && \
    tar zxf /tmp/filebeat-${FILEBEAT_VERSION}-linux-${TARGETARCH}.tar.gz -C /tmp/ && \
    cp -rf /tmp/filebeat-${FILEBEAT_VERSION}-linux-${TARGETARCH}/filebeat /usr/bin/ && \
    cp -rf /tmp/filebeat-${FILEBEAT_VERSION}-linux-${TARGETARCH}/fields.yml /etc/filebeat/ && \
    cp -rf /tmp/filebeat-${FILEBEAT_VERSION}-linux-${TARGETARCH}/kibana /etc/filebeat/ && \
    cp -rf /tmp/filebeat-${FILEBEAT_VERSION}-linux-${TARGETARCH}/module /etc/filebeat/ && \
    cp -rf /tmp/filebeat-${FILEBEAT_VERSION}-linux-${TARGETARCH}/modules.d /etc/filebeat/ && \
    rm -rf /var/cache/apk/* /tmp/filebeat-${FILEBEAT_VERSION}-linux-${TARGETARCH}.tar.gz /tmp/filebeat-${FILEBEAT_VERSION}-linux-${TARGETARCH}


