package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func powerFixture(t *testing.T, root, name string, attrs map[string]string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for key, value := range attrs {
		if err := os.WriteFile(filepath.Join(dir, key), []byte(value+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBatteryReportLaptopAndPeripheral(t *testing.T) {
	root := t.TempDir()
	powerFixture(t, root, "AC", map[string]string{"type": "Mains", "online": "1"})
	powerFixture(t, root, "BAT0", map[string]string{"type": "Battery", "model_name": "Primary", "capacity": "99", "status": "Not charging", "charge_now": "3385000", "current_now": "0"})
	powerFixture(t, root, "hidpp_battery_0", map[string]string{"type": "Battery", "scope": "Device", "model_name": "Wireless Keyboard", "capacity_level": "Normal", "status": "Discharging"})
	got, err := batteryReportFrom(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"99 percent", "not charging", "External power: AC: connected", "Wireless Keyboard, battery level normal, discharging"} {
		if !strings.Contains(got, want) {
			t.Errorf("report %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "estimated time remaining") {
		t.Errorf("unexpected runtime estimate: %q", got)
	}
}

func TestBatteryRuntime(t *testing.T) {
	root := t.TempDir()
	powerFixture(t, root, "BAT0", map[string]string{"type": "Battery", "status": "Discharging", "capacity": "50", "charge_now": "3000000", "current_now": "1000000"})
	got, err := batteryReportFrom(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "estimated time remaining 3 hours") {
		t.Fatal(got)
	}
}

func TestBatteryEnergyRuntime(t *testing.T) {
	root := t.TempDir()
	powerFixture(t, root, "BAT0", map[string]string{"type": "Battery", "status": "Discharging", "energy_now": "40000000", "power_now": "20000000"})
	got, err := batteryReportFrom(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "estimated time remaining 2 hours") {
		t.Fatal(got)
	}
}

func TestNoBattery(t *testing.T) {
	got, err := batteryReportFrom(t.TempDir())
	if err != nil || got != "No battery information available." {
		t.Fatalf("%q %v", got, err)
	}
}

func TestMissingCapacityAndAbsentBattery(t *testing.T) {
	root := t.TempDir()
	powerFixture(t, root, "BAT0", map[string]string{"type": "Battery", "present": "0", "capacity": "80"})
	powerFixture(t, root, "mouse", map[string]string{"type": "Battery", "scope": "Device", "model_name": "Mouse", "status": "Discharging"})
	got, err := batteryReportFrom(root)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "80 percent") || !strings.Contains(got, "Mouse, discharging") {
		t.Fatal(got)
	}
}
