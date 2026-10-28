package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultRoute(t *testing.T) {
	data := "Iface Destination Gateway Flags RefCnt Use Metric Mask MTU Window IRTT\n" +
		"eth0 00000000 01020304 0003 0 0 100 00000000 0 0 0\n" +
		"wlan0 00000000 01020304 0003 0 0 20 00000000 0 0 0\n"
	if got := parseDefaultRouteInterface(data); got != "wlan0" {
		t.Fatalf("got %q", got)
	}
}
func TestNoDefaultRoute(t *testing.T) {
	if got := parseDefaultRouteInterface("Iface Destination\n"); got != "" {
		t.Fatalf("got %q", got)
	}
}
func TestSysfsCoreTopology(t *testing.T) {
	root := t.TempDir()
	for _, entry := range []struct{ cpu, pkg, core string }{{"cpu0", "0", "0"}, {"cpu1", "0", "0"}, {"cpu2", "0", "1"}, {"cpu3", "1", "0"}} {
		dir := filepath.Join(root, entry.cpu, "topology")
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "physical_package_id"), []byte(entry.pkg), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "core_id"), []byte(entry.core), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if got := countSysfsPhysicalCores(root); got != 3 {
		t.Fatalf("got %d, want 3", got)
	}
}
