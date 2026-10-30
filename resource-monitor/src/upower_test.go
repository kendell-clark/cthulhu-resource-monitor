package main

import (
	"strings"
	"testing"
)

func TestUPowerHeadsetAndDuplicateSuppression(t *testing.T) {
	devices := []upowerBattery{
		{NativePath: "BAT0", Model: "Primary", Kind: 2, HasPercentage: true, Percentage: 100, PowerSupply: true},
		{NativePath: "hidpp_battery_0", Model: "Logitech Keyboard", Kind: 6, BatteryLevel: 4, HasPercentage: true, Percentage: 55},
		{NativePath: "/org/bluez/hci0/dev_68_59_32_7D_81_36", Model: "JBL LIVE670NC", Kind: 17, HasPercentage: true, Percentage: 10},
	}
	got := filterUPowerBatteries(devices, map[string]bool{"BAT0": true, "hidpp_battery_0": true})
	if len(got) != 1 || !strings.Contains(got[0], "JBL LIVE670NC, 10 percent") {
		t.Fatalf("unexpected batteries: %v", got)
	}
}

func TestUPowerCoarseBatteryLevel(t *testing.T) {
	got := upowerBatteryLine(upowerBattery{Model: "Keyboard", BatteryLevel: 4, HasPercentage: true, Percentage: 55})
	if strings.Contains(got, "55 percent") || !strings.Contains(got, "battery level normal") {
		t.Fatal(got)
	}
}
