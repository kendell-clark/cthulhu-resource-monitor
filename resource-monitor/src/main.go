package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/godbus/dbus/v5"
	"net"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"git.stormux.org/storm/cthulhu/sdk/go/tentacle"
)

const doublePressWindow = 900 * time.Millisecond

// Monitor remembers the last command and its announcement.
type Monitor struct {
	mu       sync.Mutex
	lastID   string
	lastTime time.Time
	lastText string
}

func announce(text string) tentacle.Interaction {
	return tentacle.Interaction{
		Presentation: tentacle.Presentation{
			Speech:  text,
			Braille: text,
		},
		Done: true,
	}
}

func copyText(text string) tentacle.Interaction {
	return tentacle.Interaction{
		Presentation: tentacle.Presentation{
			Speech:  "Copied to clipboard.",
			Braille: "Copied to clipboard.",
		},
		Clipboard: &text,
		Done:      true,
	}
}

// handle announces a report on first press and copies it on second press.
func (m *Monitor) handle(id string, report func() (string, error)) tentacle.Handler {
	return func(ctx context.Context, _ json.RawMessage) (any, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		m.mu.Lock()
		defer m.mu.Unlock()

		now := time.Now()
		if m.lastID == id && m.lastText != "" &&
			now.Sub(m.lastTime) <= doublePressWindow {
			text := m.lastText
			m.lastID = ""
			m.lastText = ""
			m.lastTime = time.Time{}
			return copyText(text), nil
		}

		m.lastID = ""
		m.lastText = ""

		text, err := report()
		if err != nil {
			return announce("Resource information unavailable."), nil
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		m.lastID = id
		m.lastTime = time.Now()
		m.lastText = text
		return announce(text), nil
	}
}

// memoryReport reads Linux memory information from /proc/meminfo.
func memoryReport() (string, error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return "", err
	}

	values := make(map[string]uint64)

	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		key := strings.TrimSuffix(fields[0], ":")
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err == nil {
			values[key] = value
		}
	}

	total := values["MemTotal"]
	available := values["MemAvailable"]

	if total == 0 {
		return "", fmt.Errorf("memory total unavailable")
	}

	used := total - available
	percentage := float64(used) / float64(total) * 100

	// Values in /proc/meminfo are expressed in KiB.
	const kibPerGiB = 1024 * 1024

	return fmt.Sprintf(
		"Memory usage: %.1f percent. %.1f gigabytes used, %.1f gigabytes available, %.1f gigabytes total.",
		percentage,
		float64(used)/kibPerGiB,
		float64(available)/kibPerGiB,
		float64(total)/kibPerGiB,
	), nil
}

// cpuTimes reads cumulative CPU counters from /proc/stat.
func cpuTimes() (idle, total uint64, err error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, err
	}

	line := strings.SplitN(string(data), "\n", 2)[0]
	fields := strings.Fields(line)

	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, fmt.Errorf("invalid CPU statistics")
	}

	for i, field := range fields[1:] {
		value, parseErr := strconv.ParseUint(field, 10, 64)
		if parseErr != nil {
			return 0, 0, parseErr
		}

		total += value

		if i == 3 || i == 4 {
			idle += value
		}
	}

	return idle, total, nil
}

// cpuReport measures CPU usage over 200 milliseconds.
func cpuReport() (string, error) {
	idle1, total1, err := cpuTimes()
	if err != nil {
		return "", err
	}

	time.Sleep(200 * time.Millisecond)

	idle2, total2, err := cpuTimes()
	if err != nil {
		return "", err
	}

	if total2 <= total1 || idle2 < idle1 {
		return "", fmt.Errorf("invalid CPU counter difference")
	}

	totalDelta := total2 - total1
	idleDelta := idle2 - idle1

	if idleDelta > totalDelta {
		return "", fmt.Errorf("invalid CPU idle time")
	}

	usage := 100 * float64(totalDelta-idleDelta) / float64(totalDelta)

	return fmt.Sprintf("CPU usage: %.1f percent.", usage), nil
}

// diskReport reports space on the filesystem containing /.
func diskReport() (string, error) {
	var stats syscall.Statfs_t
	if err := syscall.Statfs("/", &stats); err != nil {
		return "", err
	}

	blockSize := uint64(stats.Bsize)
	total := stats.Blocks * blockSize
	free := stats.Bfree * blockSize
	available := stats.Bavail * blockSize

	if total == 0 || free > total {
		return "", fmt.Errorf("invalid filesystem statistics")
	}

	used := total - free
	percentage := 100 * float64(used) / float64(total)

	const gib = 1024 * 1024 * 1024

	return fmt.Sprintf(
		"Disk usage: %.1f percent. %.1f gigabytes used, %.1f gigabytes available, %.1f gigabytes total.",
		percentage,
		float64(used)/gib,
		float64(available)/gib,
		float64(total)/gib,
	), nil
}

// networkCounters reads received and transmitted bytes from /proc/net/dev.
func networkCounters(name string) (received, transmitted uint64, err error) {
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return 0, 0, err
	}

	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) != name {
			continue
		}

		fields := strings.Fields(parts[1])
		if len(fields) < 16 {
			return 0, 0, fmt.Errorf("invalid network statistics")
		}

		received, err = strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return 0, 0, err
		}

		transmitted, err = strconv.ParseUint(fields[8], 10, 64)
		return received, transmitted, err
	}

	return 0, 0, fmt.Errorf("interface %s not found", name)
}

// networkConnectionName retrieves the active NetworkManager connection
// associated with the selected network interface.
func networkConnectionName(interfaceName string) (string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 900*time.Millisecond)
	defer cancel()

	conn, err := dbus.SystemBus()
	if err != nil {
		return "", ""
	}
	defer conn.Close()

	nm := conn.Object(
		"org.freedesktop.NetworkManager",
		dbus.ObjectPath("/org/freedesktop/NetworkManager"),
	)

	var devicesVariant dbus.Variant
	if err := nm.CallWithContext(
		ctx,
		"org.freedesktop.DBus.Properties.Get",
		0,
		"org.freedesktop.NetworkManager",
		"Devices",
	).Store(&devicesVariant); err != nil {
		return "", ""
	}

	devices, ok := devicesVariant.Value().([]dbus.ObjectPath)
	if !ok {
		return "", ""
	}

	for _, path := range devices {
		device := conn.Object("org.freedesktop.NetworkManager", path)

		getProperty := func(property string) (dbus.Variant, error) {
			var result dbus.Variant
			err := device.CallWithContext(
				ctx,
				"org.freedesktop.DBus.Properties.Get",
				0,
				"org.freedesktop.NetworkManager.Device",
				property,
			).Store(&result)
			return result, err
		}

		ifaceVariant, err := getProperty("Interface")
		if err != nil {
			continue
		}

		name, _ := ifaceVariant.Value().(string)
		if name != interfaceName {
			continue
		}

		activeVariant, err := getProperty("ActiveConnection")
		if err != nil {
			return "", ""
		}

		activePath, _ := activeVariant.Value().(dbus.ObjectPath)
		if activePath == "" || activePath == "/" {
			return "", ""
		}

		active := conn.Object("org.freedesktop.NetworkManager", activePath)

		getActiveProperty := func(property string) (dbus.Variant, error) {
			var result dbus.Variant
			err := active.CallWithContext(
				ctx,
				"org.freedesktop.DBus.Properties.Get",
				0,
				"org.freedesktop.NetworkManager.Connection.Active",
				property,
			).Store(&result)
			return result, err
		}

		idVariant, err := getActiveProperty("Id")
		if err != nil {
			return "", ""
		}

		typeVariant, err := getActiveProperty("Type")
		if err != nil {
			return "", ""
		}

		connectionName, _ := idVariant.Value().(string)
		connectionType, _ := typeVariant.Value().(string)

		switch connectionType {
		case "802-11-wireless":
			connectionType = "Wi-Fi"
		case "802-3-ethernet":
			connectionType = "Ethernet"
		}

		return connectionName, connectionType
	}

	return "", ""
}

// formatNetworkRate formats a transfer rate using an appropriate unit.
func formatNetworkRate(bytesPerSecond float64) string {
	const kib = 1024.0
	const mib = 1024.0 * 1024.0

	switch {
	case bytesPerSecond >= mib:
		return fmt.Sprintf("%.2f megabytes per second", bytesPerSecond/mib)
	case bytesPerSecond >= kib:
		return fmt.Sprintf("%.1f kilobytes per second", bytesPerSecond/kib)
	default:
		return fmt.Sprintf("%.0f bytes per second", bytesPerSecond)
	}
}

// wifiLinkSpeed retrieves the current Wi-Fi device bitrate from
// NetworkManager. The value is reported in kilobits per second.
func wifiLinkSpeed(interfaceName string) (float64, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	conn, err := dbus.SystemBus()
	if err != nil {
		return 0, false
	}
	defer conn.Close()

	nm := conn.Object(
		"org.freedesktop.NetworkManager",
		dbus.ObjectPath("/org/freedesktop/NetworkManager"),
	)

	var devicesVariant dbus.Variant
	if err := nm.CallWithContext(
		ctx,
		"org.freedesktop.DBus.Properties.Get",
		0,
		"org.freedesktop.NetworkManager",
		"Devices",
	).Store(&devicesVariant); err != nil {
		return 0, false
	}

	devices, ok := devicesVariant.Value().([]dbus.ObjectPath)
	if !ok {
		return 0, false
	}

	for _, path := range devices {
		device := conn.Object("org.freedesktop.NetworkManager", path)

		var ifaceVariant dbus.Variant
		if err := device.CallWithContext(
			ctx,
			"org.freedesktop.DBus.Properties.Get",
			0,
			"org.freedesktop.NetworkManager.Device",
			"Interface",
		).Store(&ifaceVariant); err != nil {
			continue
		}

		name, ok := ifaceVariant.Value().(string)
		if !ok || name != interfaceName {
			continue
		}

		// Bitrate is exposed by the wireless device interface.
		var bitrateVariant dbus.Variant
		if err := device.CallWithContext(
			ctx,
			"org.freedesktop.DBus.Properties.Get",
			0,
			"org.freedesktop.NetworkManager.Device.Wireless",
			"Bitrate",
		).Store(&bitrateVariant); err != nil {
			return 0, false
		}

		bitrate, ok := bitrateVariant.Value().(uint32)
		if !ok || bitrate == 0 {
			return 0, false
		}

		return float64(bitrate) / 1000.0, true
	}

	return 0, false
}

func sampleNetworkReport() (string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}

	var selected net.Interface
	var addresses []net.Addr

	// Prefer the kernel's default-route interface, then an active interface.
	selected, addresses = chooseNetworkInterface(interfaces, readDefaultRouteInterface())

	if selected.Name == "" {
		return "Network: no active interface detected.", nil
	}

	var ipv4, ipv6 []string

	for _, addr := range addresses {
		ip, _, err := net.ParseCIDR(addr.String())
		if err != nil {
			continue
		}

		if ip.To4() != nil {
			ipv4 = append(ipv4, ip.String())
		} else if !ip.IsLinkLocalUnicast() {
			ipv6 = append(ipv6, ip.String())
		}
	}

	sampleStart := time.Now()
	rx1, tx1, err := networkCounters(selected.Name)
	if err != nil {
		return "", err
	}

	time.Sleep(500 * time.Millisecond)

	rx2, tx2, err := networkCounters(selected.Name)
	sampleElapsed := time.Since(sampleStart).Seconds()
	if err != nil {
		return "", err
	}

	if rx2 < rx1 || tx2 < tx1 {
		return "", fmt.Errorf("network counters reset during sampling")
	}

	const mib = 1024 * 1024
	const gib = 1024 * 1024 * 1024

	connectionName, connectionType := networkConnectionName(selected.Name)

	parts := []string{}

	if connectionName != "" {
		parts = append(parts, "Network: "+connectionName+".")
	}

	if connectionType != "" {
		parts = append(parts, "Connection type: "+connectionType+".")
	}

	parts = append(parts, fmt.Sprintf("Network interface: %s.", selected.Name))

	if connectionType == "Wi-Fi" {
		if speed, ok := wifiLinkSpeed(selected.Name); ok {
			parts = append(parts,
				fmt.Sprintf("Wi-Fi link speed: %.1f megabits per second.", speed))
		}
	}

	if len(ipv4) > 0 {
		parts = append(parts, "IPv4: "+strings.Join(ipv4, ", ")+".")
	}

	if len(ipv6) > 0 {
		parts = append(parts, "IPv6: "+strings.Join(ipv6, ", ")+".")
	}

	parts = append(parts,
		fmt.Sprintf(
			"Download: %s. Upload: %s.",
			formatNetworkRate(float64(rx2-rx1)/sampleElapsed),
			formatNetworkRate(float64(tx2-tx1)/sampleElapsed),
		),
		fmt.Sprintf(
			"Total received: %.2f gigabytes. Total sent: %.2f gigabytes.",
			float64(rx2)/gib,
			float64(tx2)/gib,
		),
	)

	return strings.Join(parts, " "), nil
}

// uptimeReport reads Linux system uptime from /proc/uptime.
func uptimeReport() (string, error) {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return "", fmt.Errorf("cannot read system uptime: %w", err)
	}

	fields := strings.Fields(string(data))
	if len(fields) < 1 {
		return "", fmt.Errorf("invalid /proc/uptime contents")
	}

	seconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return "", fmt.Errorf("invalid uptime value: %w", err)
	}

	totalMinutes := int64(seconds) / 60

	days := totalMinutes / (24 * 60)
	hours := (totalMinutes / 60) % 24
	minutes := totalMinutes % 60

	var parts []string

	if days > 0 {
		unit := "days"
		if days == 1 {
			unit = "day"
		}
		parts = append(parts, fmt.Sprintf("%d %s", days, unit))
	}

	if hours > 0 {
		unit := "hours"
		if hours == 1 {
			unit = "hour"
		}
		parts = append(parts, fmt.Sprintf("%d %s", hours, unit))
	}

	if minutes > 0 {
		unit := "minutes"
		if minutes == 1 {
			unit = "minute"
		}
		parts = append(parts, fmt.Sprintf("%d %s", minutes, unit))
	}

	if len(parts) == 0 {
		parts = append(parts, "less than one minute")
	}

	return "System uptime: " + strings.Join(parts, ", ") + ".", nil
}

// osRelease reads the standard Linux os-release metadata.
// /etc/os-release takes precedence over /usr/lib/os-release.
func osRelease() map[string]string {
	result := make(map[string]string)

	var data []byte
	var err error

	for _, path := range []string{
		"/etc/os-release",
		"/usr/lib/os-release",
	} {
		data, err = os.ReadFile(path)
		if err == nil {
			break
		}
	}

	if err != nil {
		return result
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		// os-release permits shell-style quoted strings.
		// strconv.Unquote handles common double-quoted values.
		if len(value) >= 2 && value[0] == '"' {
			if decoded, err := strconv.Unquote(value); err == nil {
				value = decoded
			} else {
				value = strings.Trim(value, `"`)
			}
		} else if len(value) >= 2 && value[0] == '\'' &&
			value[len(value)-1] == '\'' {
			value = value[1 : len(value)-1]
		}

		result[key] = value
	}

	return result
}

// distroReport reports distribution, kernel, architecture,
// desktop environment, and display-server information.
func distroReport() (string, error) {
	release := osRelease()

	distro := release["PRETTY_NAME"]
	if distro == "" {
		distro = release["NAME"]
	}
	if distro == "" {
		distro = "Unknown Linux distribution"
	}

	parts := []string{
		"Operating system: " + distro + ".",
	}

	var uts syscall.Utsname
	if err := syscall.Uname(&uts); err == nil {
		decode := func(value []int8) string {
			var bytes []byte
			for _, c := range value {
				if c == 0 {
					break
				}
				bytes = append(bytes, byte(c))
			}
			return string(bytes)
		}

		kernel := decode(uts.Release[:])
		arch := decode(uts.Machine[:])

		if kernel != "" {
			parts = append(parts, "Kernel: "+kernel+".")
		}
		if arch != "" {
			parts = append(parts, "Architecture: "+arch+".")
		}
	}

	desktop := os.Getenv("XDG_CURRENT_DESKTOP")
	if desktop == "" {
		desktop = os.Getenv("DESKTOP_SESSION")
	}
	if desktop != "" {
		parts = append(parts, "Desktop environment: "+desktop+".")
	}

	session := os.Getenv("XDG_SESSION_TYPE")
	switch strings.ToLower(session) {
	case "x11":
		session = "X11"
	case "wayland":
		session = "Wayland"
	}

	if session != "" {
		parts = append(parts, "Session type: "+session+".")
	}

	return strings.Join(parts, " "), nil
}

// cpuModel returns the processor model reported by Linux.
func cpuModel() string {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return "Unknown processor"
	}

	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}

		switch strings.TrimSpace(key) {
		case "model name", "Processor", "Hardware":
			model := strings.TrimSpace(value)
			if model != "" {
				return model
			}
		}
	}

	return "Unknown processor"
}

// physicalCoreCount counts unique physical package/core pairs.
// If topology information is unavailable, it returns zero.
func procPhysicalCoreCount() int {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return 0
	}

	unique := make(map[string]bool)

	for _, block := range strings.Split(string(data), "\n\n") {
		var physicalID, coreID string
		hasPhysical, hasCore := false, false

		for _, line := range strings.Split(block, "\n") {
			key, value, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}

			switch strings.TrimSpace(key) {
			case "physical id":
				physicalID = strings.TrimSpace(value)
				hasPhysical = true
			case "core id":
				coreID = strings.TrimSpace(value)
				hasCore = true
			}
		}

		if hasPhysical && hasCore {
			unique[physicalID+":"+coreID] = true
		}
	}

	return len(unique)
}

type cpuSnapshot struct {
	idle  uint64
	total uint64
}

// readCPUTimes reads aggregate and per-logical-processor counters.
func readCPUTimes() (map[string]cpuSnapshot, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return nil, err
	}

	result := make(map[string]cpuSnapshot)

	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)

		if len(fields) < 5 {
			continue
		}

		name := fields[0]
		if name != "cpu" &&
			!(strings.HasPrefix(name, "cpu") && len(name) > 3 &&
				name[3] >= '0' && name[3] <= '9') {
			continue
		}

		var values []uint64
		for _, field := range fields[1:] {
			value, err := strconv.ParseUint(field, 10, 64)
			if err != nil {
				values = nil
				break
			}
			values = append(values, value)
		}

		if len(values) < 4 {
			continue
		}

		var total uint64
		for _, value := range values {
			total += value
		}

		idle := values[3]
		if len(values) > 4 {
			idle += values[4]
		}

		result[name] = cpuSnapshot{
			idle:  idle,
			total: total,
		}
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("no CPU counters available")
	}

	return result, nil
}

// detailedCPUReport samples all logical processors and reports usage.
func sampleDetailedCPUReport() string {
	first, err := readCPUTimes()
	if err != nil {
		return "CPU utilization unavailable."
	}

	time.Sleep(250 * time.Millisecond)

	second, err := readCPUTimes()
	if err != nil {
		return "CPU utilization unavailable."
	}

	usage := func(before, after cpuSnapshot) float64 {
		if after.total <= before.total || after.idle < before.idle {
			return 0
		}

		totalDelta := after.total - before.total
		idleDelta := after.idle - before.idle

		if idleDelta > totalDelta {
			return 0
		}

		return 100 * float64(totalDelta-idleDelta) / float64(totalDelta)
	}

	parts := []string{}

	if before, ok := first["cpu"]; ok {
		if after, ok := second["cpu"]; ok {
			parts = append(parts,
				fmt.Sprintf("Overall CPU usage: %.1f percent.",
					usage(before, after)))
		}
	}

	ids := make([]int, 0, len(first))
	for name := range first {
		if !strings.HasPrefix(name, "cpu") || name == "cpu" {
			continue
		}
		id, err := strconv.Atoi(strings.TrimPrefix(name, "cpu"))
		if err == nil && id >= 0 {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	for _, id := range ids {
		name := fmt.Sprintf("cpu%d", id)
		before, okBefore := first[name]
		after, okAfter := second[name]
		if !okBefore || !okAfter {
			continue
		}
		parts = append(parts, fmt.Sprintf("Logical processor %d: %.1f percent.", id+1, usage(before, after)))
	}

	if len(parts) == 0 {
		return "CPU utilization unavailable."
	}

	return strings.Join(parts, " ")
}

// systemSummaryReport combines all available system information.
func systemSummaryReport() (string, error) {
	parts := []string{}

	// Start slow measurements together; collect results in a fixed order.
	type measured struct {
		text string
		err  error
	}
	cpuResult := make(chan string, 1)
	netResult := make(chan measured, 1)
	batteryResult := make(chan measured, 1)
	go func() { cpuResult <- detailedCPUReport() }()
	go func() { value, err := networkReport(); netResult <- measured{value, err} }()
	go func() { value, err := cachedBatteryReport(); batteryResult <- measured{value, err} }()

	if hostname, err := os.Hostname(); err == nil && hostname != "" {
		parts = append(parts, "Hostname: "+hostname+".")
	}

	release := osRelease()
	distro := release["PRETTY_NAME"]
	if distro == "" {
		distro = release["NAME"]
	}
	if distro != "" {
		parts = append(parts, "Operating system: "+distro+".")
	}

	parts = append(parts, "Processor: "+cpuModel()+".")

	logical := runtime.NumCPU()
	physical := physicalCoreCount()

	if physical > 0 {
		parts = append(parts,
			fmt.Sprintf("%d physical cores, %d logical processors.",
				physical, logical))
	} else {
		parts = append(parts,
			fmt.Sprintf("%d logical processors.", logical))
	}

	parts = append(parts, <-cpuResult)

	for _, report := range []func() (string, error){memoryReport, diskReport} {
		if value, err := report(); err == nil && value != "" {
			parts = append(parts, value)
		}
	}
	if result := <-netResult; result.err == nil && result.text != "" {
		parts = append(parts, result.text)
	}
	if result := <-batteryResult; result.err == nil && result.text != "" {
		parts = append(parts, result.text)
	}
	if value, err := uptimeReport(); err == nil && value != "" {
		parts = append(parts, value)
	}

	return strings.Join(parts, " "), nil
}

func bindings(key string) []tentacle.Binding {
	return []tentacle.Binding{
		{Layout: "desktop", Scope: "global", Key: key},
		{Layout: "laptop", Scope: "global", Key: key},
	}
}

func main() {
	stopSampler := startSampler()
	defer stopSampler()

	monitor := &Monitor{}

	commands := []tentacle.Command{
		{
			ID:           "battery",
			Title:        "Battery Information",
			Description:  "Report laptop and peripheral battery information; press twice to copy",
			Presentation: true,
			Interaction:  true,
			Bindings:     bindings("Alt+Cthulhu+8"),
			Handle:       monitor.handle("battery", cachedBatteryReport),
		},

		{
			ID:           "summary",
			Title:        "System Summary",
			Description:  "Report comprehensive system information; press twice to copy",
			Presentation: true,
			Interaction:  true,
			Bindings:     bindings("Alt+Cthulhu+1"),
			Handle:       monitor.handle("summary", systemSummaryReport),
		},

		{
			ID:           "distro",
			Title:        "Linux Distribution Information",
			Description:  "Report Linux distribution and system details; press twice to copy",
			Presentation: true,
			Interaction:  true,
			Bindings:     bindings("Alt+Cthulhu+7"),
			Handle:       monitor.handle("distro", distroReport),
		},

		{
			ID:           "uptime",
			Title:        "System Uptime",
			Description:  "Report system uptime; press twice to copy",
			Presentation: true,
			Interaction:  true,
			Bindings:     bindings("Alt+Cthulhu+6"),
			Handle:       monitor.handle("uptime", uptimeReport),
		},

		{
			ID:           "network",
			Title:        "Network Information",
			Description:  "Report network information; press twice to copy",
			Presentation: true,
			Interaction:  true,
			Bindings:     bindings("Alt+Cthulhu+5"),
			Handle:       monitor.handle("network", networkReport),
		},

		{
			ID:           "disk",
			Title:        "Disk Usage",
			Description:  "Report disk usage; press twice to copy",
			Presentation: true,
			Interaction:  true,
			Bindings:     bindings("Alt+Cthulhu+4"),
			Handle:       monitor.handle("disk", diskReport),
		},

		{
			ID:           "cpu",
			Title:        "CPU Usage",
			Description:  "Report CPU usage; press twice to copy",
			Presentation: true,
			Interaction:  true,
			Bindings:     bindings("Alt+Cthulhu+2"),
			Handle:       monitor.handle("cpu", cpuReport),
		},

		{
			ID:           "memory",
			Title:        "Memory Usage",
			Description:  "Report memory usage; press twice to copy",
			Presentation: true,
			Interaction:  true,
			Bindings:     bindings("Alt+Cthulhu+3"),
			Handle:       monitor.handle("memory", memoryReport),
		},
	}

	if err := tentacle.Serve(os.Stdin, os.Stdout, commands); err != nil {
		os.Exit(1)
	}
}
