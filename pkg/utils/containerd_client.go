package utils

import (
	"context"
	"fmt"
	"github.com/containerd/containerd"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1alpha2"
	"k8s.io/klog/v2"
	"net"
	"net/url"
	"time"
)

const (
	// unixProtocol is the network protocol of unix socket.
	unixProtocol = "unix"
)

// NewClient new containerd client by factory
func NewClient(endpoint string, timeout time.Duration) (runtimeapi.RuntimeServiceClient, *grpc.ClientConn, error) {
	var (
		// ctnrdClient   *containerd.Client
		runtimeClient runtimeapi.RuntimeServiceClient
		// grpcConn      *grpc.ClientConn
	)
	ctx := context.Background()
	// NOTE: we're using runtime client api rather than cri-api
	// due to the cri-api is lack of critical information, e.g. container envs.
	runtimeClient, conn, err := getRuntimeClient(ctx, endpoint, timeout)
	if err != nil {
		return runtimeClient, conn, err
	}
	_, err = containerd.New(endpoint, containerd.WithTimeout(timeout))
	if err != nil {
		return runtimeClient, conn, err
	}
	return runtimeClient, conn, nil
}
func getRuntimeClient(ctx context.Context, endpoint string, timeout time.Duration) (runtimeapi.RuntimeServiceClient, *grpc.ClientConn, error) {
	// Set up a connection to the server.
	conn, err := getRuntimeClientConnection(ctx, endpoint, timeout)
	if err != nil {
		return nil, nil, errors.Wrap(err, "connect")
	}
	runtimeClient := runtimeapi.NewRuntimeServiceClient(conn)
	return runtimeClient, conn, nil
}
func getRuntimeClientConnection(ctx context.Context, endpoint string, timeout time.Duration) (*grpc.ClientConn, error) {
	klog.Info("get runtime connection")
	return getConnection([]string{endpoint}, timeout)
}

func getConnection(endPoints []string, timeout time.Duration) (*grpc.ClientConn, error) {
	if endPoints == nil || len(endPoints) == 0 {
		return nil, fmt.Errorf("endpoint is not set")
	}
	endPointsLen := len(endPoints)
	var conn *grpc.ClientConn
	for indx, endPoint := range endPoints {
		klog.Infof("connect using endpoint '%s' with '%s' timeout", endPoint, timeout)
		addr, dialer, err := GetAddressAndDialer(endPoint)
		if err != nil {
			if indx == endPointsLen-1 {
				return nil, err
			}
			klog.Error(err)
			continue
		}
		conn, err = grpc.Dial(addr, grpc.WithInsecure(), grpc.WithBlock(), grpc.WithTimeout(timeout), grpc.WithContextDialer(dialer))
		if err != nil {
			errMsg := errors.Wrapf(err, "connect endpoint '%s', make sure you are running as root and the endpoint has been started", endPoint)
			if indx == endPointsLen-1 {
				return nil, errMsg
			}
			klog.Error(errMsg)
		} else {
			klog.Infof("connected successfully using endpoint: %s", endPoint)
			break
		}
	}
	return conn, nil
}

// GetAddressAndDialer returns the address parsed from the given endpoint and a context dialer.
func GetAddressAndDialer(endpoint string) (string, func(ctx context.Context, addr string) (net.Conn, error), error) {
	protocol, addr, err := parseEndpointWithFallbackProtocol(endpoint, unixProtocol)
	if err != nil {
		return "", nil, err
	}
	if protocol != unixProtocol {
		return "", nil, fmt.Errorf("only support unix socket endpoint")
	}

	return addr, dial, nil
}

func dial(ctx context.Context, addr string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, unixProtocol, addr)
}

func parseEndpointWithFallbackProtocol(endpoint string, fallbackProtocol string) (protocol string, addr string, err error) {
	if protocol, addr, err = parseEndpoint(endpoint); err != nil && protocol == "" {
		fallbackEndpoint := fallbackProtocol + "://" + endpoint
		protocol, addr, err = parseEndpoint(fallbackEndpoint)
	}
	return
}

func parseEndpoint(endpoint string) (string, string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", "", err
	}

	switch u.Scheme {
	case "tcp":
		return "tcp", u.Host, nil

	case "unix":
		return "unix", u.Path, nil

	case "":
		return "", "", fmt.Errorf("using %q as endpoint is deprecated, please consider using full url format", endpoint)

	default:
		return u.Scheme, "", fmt.Errorf("protocol %q not supported", u.Scheme)
	}
}
