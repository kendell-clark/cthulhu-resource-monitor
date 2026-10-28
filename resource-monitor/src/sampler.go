package main

import (
	"sync"
	"time"
)

// Background snapshots avoid waiting for CPU and network sampling on each keypress.
// Readers use an on-demand fallback during initial warm-up or after stale samples.
const cpuRefresh = time.Second
const networkRefresh = 2 * time.Second
const snapshotMaxAge = 5 * time.Second

type cachedCPU struct {
	text string
	at   time.Time
}
type cachedNetwork struct {
	text string
	err  error
	at   time.Time
}

var snapshots struct {
	sync.RWMutex
	cpu     cachedCPU
	network cachedNetwork
}

func detailedCPUReport() string {
	snapshots.RLock()
	value := snapshots.cpu
	snapshots.RUnlock()
	if !value.at.IsZero() && time.Since(value.at) < snapshotMaxAge {
		return value.text
	}
	return sampleDetailedCPUReport()
}

func networkReport() (string, error) {
	snapshots.RLock()
	value := snapshots.network
	snapshots.RUnlock()
	if !value.at.IsZero() && time.Since(value.at) < snapshotMaxAge {
		return value.text, value.err
	}
	return sampleNetworkReport()
}

// startSampler launches independent workers so CPU and network sampling overlap.
// Both workers stop after Serve returns. Their timers are cleaned up on exit.
func startSampler() func() {
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(cpuRefresh)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			default:
			}
			text := sampleDetailedCPUReport()
			snapshots.Lock()
			snapshots.cpu = cachedCPU{text: text, at: time.Now()}
			snapshots.Unlock()
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
		}
	}()
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(networkRefresh)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			default:
			}
			text, err := sampleNetworkReport()
			snapshots.Lock()
			snapshots.network = cachedNetwork{text: text, err: err, at: time.Now()}
			snapshots.Unlock()
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { close(stop); wg.Wait() }
}
