package main

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// physicalCoreCount uses sysfs topology when available, then legacy procfs.
// Package/core IDs are paired because core IDs repeat between CPU packages.
func physicalCoreCount() int {
	if n := countSysfsPhysicalCores("/sys/devices/system/cpu"); n > 0 {
		return n
	}
	return procPhysicalCoreCount()
}

func countSysfsPhysicalCores(root string) int {
	dirs, err := filepath.Glob(filepath.Join(root, "cpu[0-9]*"))
	if err != nil {
		return 0
	}
	unique := make(map[string]struct{})
	for _, dir := range dirs {
		name := filepath.Base(dir)
		if _, err := strconv.Atoi(strings.TrimPrefix(name, "cpu")); err != nil {
			continue
		}
		// Offline CPUs may have no readable topology: ignore them.
		core, err := os.ReadFile(filepath.Join(dir, "topology/core_id"))
		if err != nil {
			continue
		}
		packageID, err := os.ReadFile(filepath.Join(dir, "topology/physical_package_id"))
		if err != nil {
			continue
		}
		c, p := strings.TrimSpace(string(core)), strings.TrimSpace(string(packageID))
		if _, err := strconv.Atoi(c); err != nil {
			continue
		}
		if _, err := strconv.Atoi(p); err != nil {
			continue
		}
		unique[p+":"+c] = struct{}{}
	}
	return len(unique)
}

// readDefaultRouteInterface uses /proc/net/route; absence is harmless.
// Lowest metric wins when multiple IPv4 default routes are present.
func readDefaultRouteInterface() string {
	// Query the kernel's effective route first (including policy-routing rules).
	// The destination is only looked up; no packets are sent.
	if iface := effectiveRouteInterface(); iface != "" {
		return iface
	}
	// Fall back to procfs when iproute2 is unavailable or route lookup fails.
	if data, err := os.ReadFile("/proc/net/route"); err == nil {
		if iface := parseDefaultRouteInterface(string(data)); iface != "" {
			return iface
		}
	}
	if data, err := os.ReadFile("/proc/net/ipv6_route"); err == nil {
		return parseIPv6DefaultRouteInterface(string(data))
	}
	return ""
}

func parseDefaultRouteInterface(data string) string {
	scanner := bufio.NewScanner(strings.NewReader(data))
	bestName := ""
	bestMetric := uint64(^uint64(0))
	for scanner.Scan() {
		f := strings.Fields(scanner.Text())
		if len(f) < 8 || f[1] != "00000000" || f[7] != "00000000" {
			continue
		}
		flags, err := strconv.ParseUint(f[3], 16, 32)
		if err != nil || flags&1 == 0 {
			continue
		}
		metric, err := strconv.ParseUint(f[6], 10, 64)
		if err != nil {
			continue
		}
		if metric < bestMetric {
			bestName = f[0]
			bestMetric = metric
		}
	}
	return bestName
}

func chooseNetworkInterface(interfaces []net.Interface, preferred string) (net.Interface, []net.Addr) {
	var fallback net.Interface
	var fallbackAddrs []net.Addr
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		hasGlobal := false
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err == nil && !ip.IsUnspecified() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
				hasGlobal = true
				break
			}
		}
		if !hasGlobal {
			continue
		}
		if iface.Name == preferred {
			return iface, addrs
		}
		if fallback.Name == "" {
			fallback, fallbackAddrs = iface, addrs
		}
	}
	return fallback, fallbackAddrs
}
