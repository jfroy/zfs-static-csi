// zfs-static-csi exposes existing ZFS datasets to Kubernetes as static PVs.
package main

import (
	"flag"
	"fmt"
	"os"

	"k8s.io/klog/v2"

	"github.com/jfroy/zfs-static-csi/pkg/driver"
)

const driverName = "zfs-static-csi.jfroy.github.com"

// Overridden via -ldflags at build time.
var version = "dev"

func main() {
	klog.InitFlags(nil)

	var (
		endpoint   = flag.String("endpoint", "unix:///csi/csi.sock", "CSI endpoint (unix:// or tcp://)")
		nodeID     = flag.String("node-id", "", "Node identifier; typically the Kubernetes node name")
		hostPrefix = flag.String("host-prefix", "", "Path prefix prepended to ZFS-reported mountpoints to translate them into the driver container's view (e.g. /host)")
		showVer    = flag.Bool("version", false, "Print version and exit")
	)
	flag.Parse()

	if *showVer {
		fmt.Printf("zfs-static-csi %s\n", version)
		return
	}
	if *nodeID == "" {
		klog.ErrorS(nil, "--node-id is required")
		os.Exit(2)
	}

	d := driver.New(driver.Config{
		Name:       driverName,
		Version:    version,
		NodeID:     *nodeID,
		Endpoint:   *endpoint,
		HostPrefix: *hostPrefix,
	})

	if err := d.Run(); err != nil {
		klog.ErrorS(err, "driver exited with error")
		os.Exit(1)
	}
}
