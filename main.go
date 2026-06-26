package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"aiwatt.net/ems/go-common/mbserver"
	"aiwatt.net/ems/go-common/zaplog"
)

func main() {
	cfgPath := flag.String("config", "", "path to config file (optional)")
	port := flag.Int("port", 0, "modbus TCP port (overrides config)")
	flag.Parse()

	cfg, err := LoadConfig(*cfgPath)
	if err != nil {
		panic("failed to load config: " + err.Error())
	}

	if *port > 0 {
		cfg.Modbus.Address = fmt.Sprintf(":%d", *port)
	}

	SetPVTimezone(cfg.Timezone)
	SetGridFrequency(cfg.Grid.Frequency)

	if cfg.Log.File != "" {
		zaplog.InitZapLogger(cfg.Log.Console, cfg.Log.File, cfg.Log.Level)
	} else {
		zaplog.InitZapLogger(true, "", cfg.Log.Level)
	}
	defer zaplog.Defer()

	zaplog.Infof("starting virtual BESS: %d battery_unit(s), %d pv_unit(s), %d meter(s), %d load(s)",
		len(cfg.BatteryUnits), len(cfg.PVUnits), len(cfg.Meters), len(cfg.Loads))

	server := mbserver.NewServer()
	if err := server.ListenTCP(cfg.Modbus.Address); err != nil {
		zaplog.Errorf("failed to start modbus server on %s: %v", cfg.Modbus.Address, err)
		os.Exit(1)
	}
	zaplog.Infof("modbus TCP server listening on %s", cfg.Modbus.Address)

	sim := NewSimulator(cfg, server)
	iec61850Service, err := startIEC61850Server(cfg.IEC61850, sim)
	if err != nil {
		zaplog.Errorf("failed to start IEC 61850 server: %v", err)
		server.Close()
		os.Exit(1)
	}
	defer iec61850Service.Close()
	iec61850Service.Sync()

	// 启动时加载持久化状态
	if cfg.State.File != "" {
		st, err := LoadState(cfg.State.File)
		if err != nil {
			zaplog.Errorf("load state %s: %v", cfg.State.File, err)
		} else if st != nil {
			sim.RestoreLocked(st)
			zaplog.Infof("restored state from %s: %d meter(s), %d battery(s), %d pv(s)",
				cfg.State.File, len(st.Meters), len(st.Batteries), len(st.PVs))
		}
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var saveTicker *time.Ticker
	var saveC <-chan time.Time
	if cfg.State.File != "" {
		saveTicker = time.NewTicker(time.Duration(cfg.State.Interval) * time.Second)
		defer saveTicker.Stop()
		saveC = saveTicker.C
	}

	saveNow := func() {
		st := sim.SnapshotLocked()
		if err := SaveState(cfg.State.File, st); err != nil {
			zaplog.Errorf("save state %s: %v", cfg.State.File, err)
		}
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-ticker.C:
			sim.Tick()
			iec61850Service.Sync()
		case <-saveC:
			saveNow()
		case sig := <-sigCh:
			zaplog.Infof("received signal %v, shutting down", sig)
			if cfg.State.File != "" {
				saveNow()
			}
			server.Close()
			return
		}
	}
}
