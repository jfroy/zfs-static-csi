// Package driver implements the CSI gRPC services for zfs-static-csi:
// Identity + Node only. There is no Controller service; volumes are static.
package driver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
	mount "k8s.io/mount-utils"

	"github.com/jfroy/zfs-static-csi/pkg/zfs"
)

type Config struct {
	Name     string
	Version  string
	NodeID   string
	Endpoint string
	// HostPrefix, when non-empty, is both the path inside the container at
	// which the host root is bind-mounted (used to translate ZFS-reported
	// mountpoints) and the chroot dir for the zfs(8) child process.
	HostPrefix string
	// ZfsBinary, when set, overrides the default search list for the zfs(8)
	// binary. Path is interpreted relative to HostPrefix when chrooting.
	ZfsBinary string
}

type Driver struct {
	cfg     Config
	zfs     *zfs.Client
	mounter mount.Interface
	server  *grpc.Server
}

func New(cfg Config) (*Driver, error) {
	zfsClient, err := zfs.NewClient(zfs.Options{
		ChrootDir:  cfg.HostPrefix,
		BinaryPath: cfg.ZfsBinary,
	})
	if err != nil {
		return nil, fmt.Errorf("init zfs client: %w", err)
	}
	return &Driver{
		cfg:     cfg,
		zfs:     zfsClient,
		mounter: newMounter(),
	}, nil
}

func (d *Driver) Run() error {
	network, addr, err := parseEndpoint(d.cfg.Endpoint)
	if err != nil {
		return err
	}
	if network == "unix" {
		if err := os.Remove(addr); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale socket %s: %w", addr, err)
		}
	}

	listener, err := net.Listen(network, addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", d.cfg.Endpoint, err)
	}

	d.server = grpc.NewServer(grpc.UnaryInterceptor(logInterceptor))
	csi.RegisterIdentityServer(d.server, &identityServer{driver: d})
	csi.RegisterNodeServer(d.server, &nodeServer{driver: d})

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	go func() {
		<-ctx.Done()
		klog.InfoS("shutdown signal received, stopping gRPC server")
		d.server.GracefulStop()
	}()

	klog.InfoS("starting gRPC server",
		"endpoint", d.cfg.Endpoint,
		"driver", d.cfg.Name,
		"version", d.cfg.Version,
		"nodeID", d.cfg.NodeID,
		"hostPrefix", d.cfg.HostPrefix)
	if err := d.server.Serve(listener); err != nil {
		return fmt.Errorf("gRPC serve: %w", err)
	}
	return nil
}

func parseEndpoint(ep string) (network, addr string, err error) {
	switch {
	case strings.HasPrefix(ep, "unix://"):
		u, err := url.Parse(ep)
		if err != nil {
			return "", "", fmt.Errorf("parse endpoint: %w", err)
		}
		return "unix", u.Path, nil
	case strings.HasPrefix(ep, "tcp://"):
		u, err := url.Parse(ep)
		if err != nil {
			return "", "", fmt.Errorf("parse endpoint: %w", err)
		}
		return "tcp", u.Host, nil
	default:
		return "", "", fmt.Errorf("invalid endpoint %q (want unix:// or tcp://)", ep)
	}
}

func logInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	klog.V(4).InfoS("CSI request", "method", info.FullMethod, "request", req)
	resp, err := handler(ctx, req)
	if err != nil {
		// NotFound / FailedPrecondition / AlreadyExists are normal flow
		// control for kubelet retries — keep them out of error logs.
		st, _ := status.FromError(err)
		switch st.Code() {
		case codes.OK, codes.NotFound, codes.FailedPrecondition, codes.AlreadyExists:
			klog.V(2).InfoS("CSI response", "method", info.FullMethod, "code", st.Code().String(), "message", st.Message())
		default:
			klog.ErrorS(err, "CSI error", "method", info.FullMethod, "code", st.Code().String())
		}
		return resp, err
	}
	klog.V(4).InfoS("CSI response", "method", info.FullMethod, "response", resp)
	return resp, nil
}
