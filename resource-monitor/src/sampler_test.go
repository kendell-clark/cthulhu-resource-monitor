package main

import (
	"strings"
	"testing"
	"time"
)

func TestNetworkRateUnits(t *testing.T) {
	tests := []struct {
		rate float64
		want string
	}{
		{0, "0 bytes per second"},
		{512, "512 bytes per second"},
		{1024, "1.0 kilobytes per second"},
		{1024 * 1024, "1.00 megabytes per second"},
	}
	for _, tt := range tests {
		if got := formatNetworkRate(tt.rate); got != tt.want {
			t.Errorf("rate %v: got %q, want %q", tt.rate, got, tt.want)
		}
	}
}

func TestCPUCacheRead(t *testing.T) {
	snapshots.Lock()
	old := snapshots.cpu
	snapshots.cpu = cachedCPU{text: "cached CPU test value", at: time.Now()}
	snapshots.Unlock()
	defer func() { snapshots.Lock(); snapshots.cpu = old; snapshots.Unlock() }()
	if got := detailedCPUReport(); got != "cached CPU test value" {
		t.Errorf("cache was not used: %q", got)
	}
}

func TestNetworkCacheRead(t *testing.T) {
	snapshots.Lock()
	old := snapshots.network
	snapshots.network = cachedNetwork{text: "cached network test value", at: time.Now()}
	snapshots.Unlock()
	defer func() { snapshots.Lock(); snapshots.network = old; snapshots.Unlock() }()
	got, err := networkReport()
	if err != nil || !strings.Contains(got, "cached network test value") {
		t.Errorf("cache was not used: %q, %v", got, err)
	}
}
