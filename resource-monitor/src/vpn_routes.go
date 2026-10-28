package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// effectiveRouteInterface asks the kernel which interface it would use to
// reach a public destination. `ip route get` does not transmit traffic.
// This handles ordinary route metrics and most policy routing, including
// VPNs with an IPv4 or IPv6 default route in a separate routing table.
// It cannot identify every split-tunnel or application-specific VPN policy.
func effectiveRouteInterface() string {
	for _, request := range [][]string{
		{"-j", "-4", "route", "get", "1.1.1.1"},
		{"-j", "-6", "route", "get", "2606:4700:4700::1111"},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
		out, err := exec.CommandContext(ctx, "ip", request...).Output()
		cancel()
		if err != nil {
			continue
		}
		if iface := parseRouteGetJSON(out); iface != "" {
			return iface
		}
	}
	return ""
}

// parseRouteGetJSON handles the JSON output of `ip -j route get`.
// Reject non-forwarding results such as local/blackhole routes.
func parseRouteGetJSON(data []byte) string {
	var routes []struct {
		Dev  string `json:"dev"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &routes); err != nil {
		return ""
	}
	for _, route := range routes {
		if route.Dev == "" || route.Dev == "lo" {
			continue
		}
		switch route.Type {
		case "blackhole", "unreachable", "prohibit", "local", "throw":
			continue
		}
		return route.Dev
	}
	return ""
}

// parseIPv6DefaultRouteInterface reads Linux /proc/net/ipv6_route.
// The first 32 hex digits represent the destination; the next 2 hex
// digits represent the prefix length. Metric is hexadecimal, field 6.
func parseIPv6DefaultRouteInterface(data string) string {
	best, bestMetric := "", uint64(^uint64(0))
	for _, line := range strings.Split(data, "\n") {
		f := strings.Fields(line)
		if len(f) < 10 || f[0] != strings.Repeat("0", 32) || f[1] != "00" {
			continue
		}
		metric, err := strconv.ParseUint(f[5], 16, 64)
		if err != nil {
			continue
		}
		iface := f[9]
		if iface == "" || iface == "lo" {
			continue
		}
		if metric < bestMetric {
			best, bestMetric = iface, metric
		}
	}
	return best
}
