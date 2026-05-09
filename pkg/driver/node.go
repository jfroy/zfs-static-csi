package driver

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
	mount "k8s.io/mount-utils"

	"github.com/jfroy/zfs-static-csi/pkg/zfs"
)

type nodeServer struct {
	csi.UnimplementedNodeServer
	driver *Driver
}

func (s *nodeServer) NodeGetInfo(ctx context.Context, req *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	return &csi.NodeGetInfoResponse{
		NodeId: s.driver.cfg.NodeID,
	}, nil
}

func (s *nodeServer) NodeGetCapabilities(ctx context.Context, req *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	return &csi.NodeGetCapabilitiesResponse{}, nil
}

func (s *nodeServer) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	targetPath := req.GetTargetPath()
	cap := req.GetVolumeCapability()

	if volumeID == "" {
		return nil, status.Error(codes.InvalidArgument, "VolumeId is required")
	}
	if targetPath == "" {
		return nil, status.Error(codes.InvalidArgument, "TargetPath is required")
	}
	if cap == nil {
		return nil, status.Error(codes.InvalidArgument, "VolumeCapability is required")
	}
	if cap.GetBlock() != nil {
		return nil, status.Error(codes.InvalidArgument, "block volume mode is not supported by this driver")
	}
	if cap.GetMount() == nil {
		return nil, status.Error(codes.InvalidArgument, "VolumeCapability.Mount is required")
	}

	ds, err := s.driver.zfs.GetDataset(ctx, volumeID)
	if err != nil {
		var nfe *zfs.NotFoundError
		if errors.As(err, &nfe) {
			return nil, status.Errorf(codes.NotFound, "%v", err)
		}
		return nil, status.Errorf(codes.Internal, "look up dataset %q: %v", volumeID, err)
	}
	if ds.Type != "filesystem" {
		return nil, status.Errorf(codes.FailedPrecondition,
			"dataset %q has type %q; only filesystem datasets are supported", volumeID, ds.Type)
	}
	if !ds.IsShared() {
		return nil, status.Errorf(codes.FailedPrecondition,
			"dataset %q is not opted in (set %s=%s to expose)", volumeID, zfs.ShareProperty, zfs.ShareValueOn)
	}
	if !ds.Mounted {
		return nil, status.Errorf(codes.FailedPrecondition,
			"dataset %q is not mounted on this node", volumeID)
	}
	if ds.Mountpoint == "" || ds.Mountpoint == "none" || ds.Mountpoint == "legacy" || ds.Mountpoint == "-" {
		return nil, status.Errorf(codes.FailedPrecondition,
			"dataset %q has no usable mountpoint (%q)", volumeID, ds.Mountpoint)
	}

	source := filepath.Join(s.driver.cfg.HostPrefix, ds.Mountpoint)
	if fi, err := os.Stat(source); err != nil {
		return nil, status.Errorf(codes.FailedPrecondition,
			"dataset %q mountpoint %q is not visible to the driver: %v", volumeID, source, err)
	} else if !fi.IsDir() {
		return nil, status.Errorf(codes.FailedPrecondition,
			"dataset %q mountpoint %q is not a directory", volumeID, source)
	}

	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		return nil, status.Errorf(codes.Internal, "create target %q: %v", targetPath, err)
	}

	// Idempotent retry: a pre-existing mount at target is treated as success.
	notMnt, err := s.driver.mounter.IsLikelyNotMountPoint(targetPath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "check mount point %q: %v", targetPath, err)
	}
	if !notMnt {
		klog.V(2).InfoS("target already mounted, treating as no-op",
			"volumeID", volumeID, "target", targetPath)
		return &csi.NodePublishVolumeResponse{}, nil
	}

	options := []string{"bind"}
	// CSI spec: *_READER_ONLY access modes MUST be mounted read-only,
	// regardless of whether kubelet also sets req.Readonly.
	mode := cap.GetAccessMode().GetMode()
	if req.GetReadonly() ||
		mode == csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY ||
		mode == csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY {
		options = append(options, "ro")
	}
	options = append(options, cap.GetMount().GetMountFlags()...)

	klog.InfoS("bind mounting dataset",
		"volumeID", volumeID, "source", source, "target", targetPath, "options", options)

	if err := s.driver.mounter.Mount(source, targetPath, "", options); err != nil {
		_ = os.Remove(targetPath)
		return nil, status.Errorf(codes.Internal,
			"bind mount %q -> %q: %v", source, targetPath, err)
	}
	return &csi.NodePublishVolumeResponse{}, nil
}

func (s *nodeServer) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	volumeID := req.GetVolumeId()
	targetPath := req.GetTargetPath()
	if volumeID == "" {
		return nil, status.Error(codes.InvalidArgument, "VolumeId is required")
	}
	if targetPath == "" {
		return nil, status.Error(codes.InvalidArgument, "TargetPath is required")
	}

	klog.InfoS("unpublishing volume", "volumeID", volumeID, "target", targetPath)
	// extensiveMountPointCheck=true consults /proc/mounts to avoid the
	// IsLikelyNotMountPoint false-negative for bind mounts onto the same fs.
	if err := mount.CleanupMountPoint(targetPath, s.driver.mounter, true); err != nil {
		return nil, status.Errorf(codes.Internal,
			"cleanup mount point %q: %v", targetPath, err)
	}
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

var _ csi.NodeServer = (*nodeServer)(nil)
