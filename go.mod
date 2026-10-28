module github.com/kendell-clark/cthulhu-resource-monitor

go 1.24.0

require git.stormux.org/storm/cthulhu v0.0.0

require github.com/godbus/dbus/v5 v5.1.0

require github.com/pelletier/go-toml/v2 v2.4.3 // indirect

// The experimental SDK is developed alongside this repository.
replace git.stormux.org/storm/cthulhu => ../cthulhu
