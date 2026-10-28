package main

import "testing"

func TestRouteGetJSONVPN(t *testing.T) {
	cases := []struct{ name, input, want string }{
		{"WireGuard policy route", `[ {"dst":"1.1.1.1","dev":"wg0","table":51820} ]`, "wg0"},
		{"OpenVPN tunnel", `[ {"dst":"1.1.1.1","dev":"tun0","gateway":"10.8.0.1"} ]`, "tun0"},
		{"ordinary WiFi", `[ {"dst":"1.1.1.1","dev":"wlp1s0"} ]`, "wlp1s0"},
		{"IPv6 tunnel", `[ {"dst":"2606:4700:4700::1111","dev":"wg0"} ]`, "wg0"},
		{"blackhole", `[ {"dst":"1.1.1.1","type":"blackhole","dev":"wg0"} ]`, ""},
		{"loopback", `[ {"dst":"1.1.1.1","dev":"lo"} ]`, ""},
		{"malformed", `{`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseRouteGetJSON([]byte(tc.input)); got != tc.want {
				t.Fatalf("got %q; want %q", got, tc.want)
			}
		})
	}
}

func TestIPv6DefaultRoutes(t *testing.T) {
	zero := "00000000000000000000000000000000"
	// Fields: dest prefix source prefix next-hop metric ref use flags iface
	line := func(metric, iface string) string {
		return zero + " 00 " + zero + " 00 " + zero + " " + metric + " 00000000 00000000 00000003 " + iface + "\n"
	}
	input := line("00000258", "wlp1s0") + line("00000032", "wg0")
	if got := parseIPv6DefaultRouteInterface(input); got != "wg0" {
		t.Fatalf("got %q; want wg0", got)
	}
	if got := parseIPv6DefaultRouteInterface("garbage\n"); got != "" {
		t.Fatalf("unexpected interface %q", got)
	}
}
