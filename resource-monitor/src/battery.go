package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const powerSupplyRoot = "/sys/class/power_supply"

func powerAttribute(dir, name string) string {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func powerNumber(dir, name string) (int64, bool) {
	s := powerAttribute(dir, name)
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil
}

func batteryDuration(dir string) string {
	// Only calculate a remaining-time estimate when actually discharging.
	if !strings.EqualFold(powerAttribute(dir, "status"), "Discharging") {
		return ""
	}
	var hours float64
	if energy, ok := powerNumber(dir, "energy_now"); ok && energy > 0 {
		if watts, ok := powerNumber(dir, "power_now"); ok && watts > 0 {
			hours = float64(energy) / float64(watts)
		}
	}
	if hours == 0 {
		if charge, ok := powerNumber(dir, "charge_now"); ok && charge > 0 {
			if current, ok := powerNumber(dir, "current_now"); ok && current > 0 {
				hours = float64(charge) / float64(current)
			}
		}
	}
	if hours <= 0 || hours > 24*30 {
		return ""
	}
	duration := time.Duration(hours * float64(time.Hour))
	// Round to whole minutes to avoid false precision.
	minutes := int64(duration.Round(time.Minute) / time.Minute)
	if minutes < 1 {
		return "less than a minute"
	}
	h, m := minutes/60, minutes%60
	if h == 0 {
		return fmt.Sprintf("%d minutes", m)
	}
	if m == 0 {
		return fmt.Sprintf("%d hours", h)
	}
	return fmt.Sprintf("%d hours, %d minutes", h, m)
}

func batteryReportFrom(root string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return "No battery information available.", nil
		}
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var internal, peripheral, supplies []string
	for _, entry := range entries {
		dir := filepath.Join(root, entry.Name())
		typ := strings.ToLower(powerAttribute(dir, "type"))
		if typ == "mains" || typ == "usb" || typ == "usb_c" || typ == "usb_pd" {
			if online, ok := powerNumber(dir, "online"); ok {
				supplies = append(supplies, fmt.Sprintf("%s: %s", entry.Name(), map[bool]string{true: "connected", false: "disconnected"}[online != 0]))
			}
			continue
		}
		if typ != "battery" {
			continue
		}
		if present, ok := powerNumber(dir, "present"); ok && present == 0 {
			continue
		}
		scope := strings.ToLower(powerAttribute(dir, "scope"))
		name := strings.TrimSpace(powerAttribute(dir, "model_name"))
		if name == "" {
			name = entry.Name()
		}
		isPeripheral := scope == "device"
		if !isPeripheral {
			name = "Laptop battery " + name
		}
		pieces := []string{name}
		if capacity, ok := powerNumber(dir, "capacity"); ok && capacity >= 0 && capacity <= 100 {
			pieces = append(pieces, fmt.Sprintf("%d percent", capacity))
		} else if level := powerAttribute(dir, "capacity_level"); level != "" && !strings.EqualFold(level, "Unknown") {
			pieces = append(pieces, "battery level "+strings.ToLower(level))
		}
		if status := powerAttribute(dir, "status"); status != "" && !strings.EqualFold(status, "Unknown") {
			pieces = append(pieces, strings.ToLower(status))
		}
		if remaining := batteryDuration(dir); remaining != "" && !isPeripheral {
			pieces = append(pieces, "estimated time remaining "+remaining)
		}
		line := strings.Join(pieces, ", ") + "."
		if isPeripheral {
			peripheral = append(peripheral, line)
		} else {
			internal = append(internal, line)
		}
	}
	var result []string
	result = append(result, internal...)
	if len(supplies) > 0 {
		result = append(result, "External power: "+strings.Join(supplies, ", ")+".")
	}
	result = append(result, peripheral...)
	if len(result) == 0 {
		return "No battery information available.", nil
	}
	return strings.Join(result, " "), nil
}

func batteryReport() (string, error) {
	base, err := batteryReportFrom(powerSupplyRoot)
	if err != nil {
		return "", err
	}
	extra := upowerAdditionalBatteries(powerSupplyRoot)
	if len(extra) == 0 {
		return base, nil
	}
	if base == "No battery information available." {
		return strings.Join(extra, " "), nil
	}
	return base + " " + strings.Join(extra, " "), nil
}
