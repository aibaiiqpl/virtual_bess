package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"virtual_bess/internal/buildinfo"
	iec61850sim "virtual_bess/internal/protocol/iec61850"
	"virtual_bess/internal/simulator"
)

// runDetails 打印当前二进制的构建信息和生效配置，用于确认现场部署的是哪一版、
// 以及仿真实际按哪个时区运行；只读，不启动任何监听。
func runDetails(args []string) int {
	fs := flag.NewFlagSet("details", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config file (optional)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	printBuild(os.Stdout)

	cfg, err := simulator.LoadConfig(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config %q: %v\n", *cfgPath, err)
		return 1
	}
	if err := simulator.SetSiteTimezone(cfg.Timezone); err != nil {
		fmt.Fprintf(os.Stderr, "failed to apply timezone: %v\n", err)
		return 1
	}
	// 与 run 一样先应用全局仿真参数，details 才能展示真正生效的值。
	simulator.SetGridFrequency(cfg.Grid.Frequency)
	printConfig(os.Stdout, *cfgPath, cfg)
	return 0
}

// printBuild 输出构建期信息（版本、提交、构建时间、运行平台和编译变体）。
func printBuild(w io.Writer) {
	info := buildinfo.Get()
	commit := info.Commit
	if info.Dirty {
		commit += " (dirty)"
	}
	variant := "simple"
	if iec61850sim.Supported {
		variant = "iec61850"
	}
	fmt.Fprintln(w, "build:")
	field(w, "version", info.Version)
	field(w, "commit", commit)
	field(w, "build time", info.BuildTime)
	field(w, "go", info.GoVersion)
	field(w, "platform", info.Platform)
	field(w, "variant", variant)
}

// printConfig 输出生效配置摘要，重点是时区换算结果和各协议端点。
func printConfig(w io.Writer, path string, cfg *simulator.Config) {
	fmt.Fprintln(w, "config:")
	field(w, "file", configSource(path))
	field(w, "timezone", timezoneDetail(cfg.Timezone))
	field(w, "host time", time.Now().Format(time.RFC3339))
	field(w, "modbus tcp", cfg.Modbus.Address)
	field(w, "modbus rtu-over-tcp", orDisabled(cfg.Modbus.RTUOverTCPAddress))
	field(w, "iec61850", endpointState(cfg.IEC61850.Enabled, cfg.IEC61850.Address, len(cfg.IEC61850.Devices)))
	field(w, "xn3477", endpointState(cfg.XN3477.Enabled, "", len(cfg.XN3477.Devices)))
	field(w, "can", endpointState(cfg.CAN.Enabled, "", len(cfg.CAN.Devices)))
	field(w, "grid", fmt.Sprintf("%.1f V / %.2f Hz", cfg.Grid.Voltage, simulator.GridFrequency()))
	field(w, "devices", fmt.Sprintf("%d battery_unit(s), %d pv_unit(s), %d meter(s), %d load(s), %d ac + %d lc, %d temp_humid",
		len(cfg.BatteryUnits), len(cfg.PVUnits), len(cfg.Meters), len(cfg.Loads),
		len(cfg.ACUnits), len(cfg.LCUnits), len(cfg.TemperatureHumid)))
	field(w, "state file", orDisabled(cfg.State.File))
	field(w, "log file", orDisabled(cfg.Log.File))
}

// timezoneDetail 把配置时区展开为「配置值 → 站点当前时间」，
// 便于一眼确认 PV/负荷曲线实际跑在哪个时钟上。
func timezoneDetail(configured string) string {
	loc := simulator.SiteLocation()
	source := configured
	if source == "" {
		source = "(unset, host local)"
	}
	now := time.Now().In(loc)
	zone, offset := now.Zone()
	return fmt.Sprintf("%s -> %s, site time %s (%s UTC%+g)",
		source, loc, now.Format(time.RFC3339), zone, float64(offset)/3600)
}

// configSource 说明配置来源：未指定或文件缺失时程序使用内置默认配置。
func configSource(path string) string {
	if path == "" {
		return "(none, built-in defaults)"
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Sprintf("%s (missing, built-in defaults)", path)
	}
	return path
}

func endpointState(enabled bool, address string, devices int) string {
	if !enabled {
		return "disabled"
	}
	if address != "" {
		return fmt.Sprintf("enabled, %s, %d device(s)", address, devices)
	}
	return fmt.Sprintf("enabled, %d device(s)", devices)
}

func orDisabled(value string) string {
	if value == "" {
		return "disabled"
	}
	return value
}

func field(w io.Writer, label, value string) {
	fmt.Fprintf(w, "  %-20s %s\n", label+":", value)
}
