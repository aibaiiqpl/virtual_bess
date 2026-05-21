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

	if cfg.Log.File != "" {
		zaplog.InitZapLogger(cfg.Log.Console, cfg.Log.File, cfg.Log.Level)
	} else {
		zaplog.InitZapLogger(true, "", cfg.Log.Level)
	}
	defer zaplog.Defer()

	zaplog.Infof("starting virtual BESS: %d battery_unit(s), %d pv_unit(s), meter slave %d",
		len(cfg.BatteryUnits), len(cfg.PVUnits), cfg.Meter.SlaveID)

	server := mbserver.NewServer()
	if err := server.ListenTCP(cfg.Modbus.Address); err != nil {
		zaplog.Errorf("failed to start modbus server on %s: %v", cfg.Modbus.Address, err)
		os.Exit(1)
	}
	zaplog.Infof("modbus TCP server listening on %s", cfg.Modbus.Address)

	sim := NewSimulator(cfg, server)

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-ticker.C:
			sim.Tick()
		case sig := <-sigCh:
			zaplog.Infof("received signal %v, shutting down", sig)
			server.Close()
			return
		}
	}
}
