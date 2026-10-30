package main

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

const upowerService = "org.freedesktop.UPower"

type upowerBattery struct {
	NativePath    string
	Model         string
	Kind          uint32
	Percentage    float64
	HasPercentage bool
	BatteryLevel  uint32
	State         uint32
	Present       bool
	HasPresent    bool
	PowerSupply   bool
}

func upowerRead(ctx context.Context, conn *dbus.Conn, path dbus.ObjectPath) (upowerBattery, bool) {
	var b upowerBattery
	var props map[string]dbus.Variant
	call := conn.Object(upowerService, path).CallWithContext(ctx,
		"org.freedesktop.DBus.Properties.GetAll", 0, "org.freedesktop.UPower.Device")
	if call.Store(&props) != nil {
		return b, false
	}
	read := func(key string) interface{} {
		v, ok := props[key]
		if !ok {
			return nil
		}
		return v.Value()
	}
	if v, ok := read("Type").(uint32); ok {
		b.Kind = v
	} else {
		return b, false
	}
	if v, ok := read("NativePath").(string); ok {
		b.NativePath = v
	}
	if v, ok := read("Model").(string); ok {
		b.Model = strings.TrimSpace(v)
	}
	if v, ok := read("Percentage").(float64); ok && !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 100 {
		b.Percentage, b.HasPercentage = v, true
	}
	if v, ok := read("BatteryLevel").(uint32); ok {
		b.BatteryLevel = v
	}
	if v, ok := read("State").(uint32); ok {
		b.State = v
	}
	if v, ok := read("IsPresent").(bool); ok {
		b.Present, b.HasPresent = v, true
	}
	if v, ok := read("PowerSupply").(bool); ok {
		b.PowerSupply = v
	}
	return b, true
}

func sysfsBatteryPaths(root string) map[string]bool {
	found := make(map[string]bool)
	entries, err := os.ReadDir(root)
	if err != nil {
		return found
	}
	for _, e := range entries {
		dir := filepath.Join(root, e.Name())
		if !strings.EqualFold(powerAttribute(dir, "type"), "Battery") {
			continue
		}
		if present, ok := powerNumber(dir, "present"); ok && present == 0 {
			continue
		}
		found[e.Name()] = true
		// UPower NativePath for kernel-backed devices usually matches sysfs basename.
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			found[filepath.Base(resolved)] = true
		}
	}
	return found
}

func upowerBatteryLine(b upowerBattery) string {
	name := b.Model
	if name == "" {
		name = filepath.Base(b.NativePath)
	}
	if name == "" || name == "." || name == "/" {
		name = "Peripheral battery"
	}
	pieces := []string{name}
	// UPower marks coarse keyboard/mouse charge levels via BatteryLevel. In that
	// case its numerical Percentage may be a placeholder ("should be ignored").
	levelNames := map[uint32]string{2: "critical", 3: "low", 4: "normal", 5: "high", 6: "full"}
	if level, ok := levelNames[b.BatteryLevel]; ok {
		pieces = append(pieces, "battery level "+level)
	} else if b.HasPercentage {
		pieces = append(pieces, fmt.Sprintf("%.0f percent", b.Percentage))
	}
	stateNames := map[uint32]string{1: "charging", 2: "discharging", 4: "fully charged", 5: "pending charge", 6: "pending discharge"}
	if state, ok := stateNames[b.State]; ok {
		pieces = append(pieces, state)
	}
	return strings.Join(pieces, ", ") + "."
}

func filterUPowerBatteries(devices []upowerBattery, existing map[string]bool) []string {
	var lines []string
	seen := make(map[string]bool)
	for _, b := range devices {
		// 2=battery, 3=UPS, 5=mouse, 6=keyboard, 7=PDA, 8=phone,
		// 9=media player, 10=tablet, 11=computer, 12=gaming input,
		// 13=pen, 14=touchpad, 15=modem, 16=network, 17=headset,
		// 18=speakers, 19=headphones, 20=video, 21=other audio, 22=remote.
		// Only include peripherals here: laptop and UPS remain in sysfs report.
		if b.Kind < 5 || b.PowerSupply || (b.HasPresent && !b.Present) {
			continue
		}
		native := filepath.Base(b.NativePath)
		if existing[native] || existing[b.NativePath] || seen[b.NativePath] {
			continue
		}
		seen[b.NativePath] = true
		lines = append(lines, upowerBatteryLine(b))
	}
	sort.Strings(lines)
	return lines
}

func upowerAdditionalBatteries(root string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil
	}
	defer conn.Close()
	var paths []dbus.ObjectPath
	call := conn.Object(upowerService, "/org/freedesktop/UPower").CallWithContext(ctx,
		"org.freedesktop.UPower.EnumerateDevices", 0)
	if call.Store(&paths) != nil {
		return nil
	}
	devices := make([]upowerBattery, 0, len(paths))
	for _, path := range paths {
		if ctx.Err() != nil {
			break
		}
		if b, ok := upowerRead(ctx, conn, path); ok {
			devices = append(devices, b)
		}
	}
	return filterUPowerBatteries(devices, sysfsBatteryPaths(root))
}
