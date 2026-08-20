package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"virtual_bess/internal/buildinfo"
	"virtual_bess/internal/mbserver"
	cansim "virtual_bess/internal/protocol/can"
	iec61850sim "virtual_bess/internal/protocol/iec61850"
	xn3477sim "virtual_bess/internal/protocol/xn3477"
	"virtual_bess/internal/simulator"
	"virtual_bess/internal/zaplog"
)

// services 汇总一次运行里所有需要关闭的服务端，
// 让启动失败的回滚和正常退出走同一条关闭路径。
type services struct {
	modbus   *mbserver.Server
	xn3477   *xn3477sim.Service
	iec61850 iec61850sim.IEC61850Service
	canPubs  []*cansim.CANPublisher
	sim      *simulator.Simulator
}

// Close 按与启动相反的顺序关闭全部服务端，允许对部分启动的实例调用。
func (s *services) Close() {
	cansim.ClosePublishers(s.canPubs)
	if s.iec61850 != nil {
		s.iec61850.Close()
	}
	if s.xn3477 != nil {
		s.xn3477.Close()
	}
	if s.modbus != nil {
		s.modbus.Close()
	}
}

// syncProtocols 把本轮仿真结果推给非 Modbus 协议服务端（Modbus 寄存器由仿真器直接写）。
func (s *services) syncProtocols() {
	s.xn3477.Sync()
	s.iec61850.Sync()
}

// run 执行仿真主流程：解析 flag、加载配置、启动服务端并进入 tick 循环，
// 收到 SIGINT/SIGTERM 后落盘状态并退出；返回值为进程退出码。
func run(args []string) int {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfgPath := fs.String("config", "", "path to config file (optional)")
	port := fs.Int("port", 0, "modbus TCP port (overrides config)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg, err := simulator.LoadConfig(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		return 1
	}
	if *port > 0 {
		cfg.Modbus.Address = fmt.Sprintf(":%d", *port)
	}
	// 时区必须在任何仿真计算之前生效，非法值直接退出而不是悄悄用主机时区。
	if err := simulator.SetSiteTimezone(cfg.Timezone); err != nil {
		fmt.Fprintf(os.Stderr, "failed to apply timezone: %v\n", err)
		return 1
	}
	simulator.SetGridFrequency(cfg.Grid.Frequency)

	if cfg.Log.File != "" {
		zaplog.InitZapLogger(cfg.Log.Console, cfg.Log.File, cfg.Log.Level)
	} else {
		zaplog.InitZapLogger(true, "", cfg.Log.Level)
	}
	defer zaplog.Defer()

	info := buildinfo.Get()
	zaplog.Infof("starting virtual BESS version=%s commit=%s: %d battery_unit(s), %d pv_unit(s), %d meter(s), %d load(s), site timezone %s",
		info.Version, info.GitCommitSha,
		len(cfg.BatteryUnits), len(cfg.PVUnits), len(cfg.Meters), len(cfg.Loads),
		simulator.SiteLocation())

	svc, err := startServices(cfg)
	if err != nil {
		zaplog.Errorf("failed to start services: %v", err)
		return 1
	}
	defer svc.Close()

	restoreState(cfg, svc.sim)
	serveLoop(cfg, svc)
	return 0
}

// startServices 按依赖顺序启动 Modbus、XN3477、IEC61850 和 CAN 服务端，
// 任何一步失败都会关闭已启动的部分并返回带上下文的错误。
func startServices(cfg *simulator.Config) (*services, error) {
	svc := &services{modbus: mbserver.NewServer()}
	if err := svc.modbus.ListenTCP(cfg.Modbus.Address); err != nil {
		return nil, fmt.Errorf("start modbus tcp server on %s: %w", cfg.Modbus.Address, err)
	}
	zaplog.Infof("modbus TCP server listening on %s", cfg.Modbus.Address)

	// RTU over TCP 端口与 MBAP 端口共用同一个 Server（同一份寄存器区），
	// 只是帧格式不同；留空即不开。
	if cfg.Modbus.RTUOverTCPAddress != "" {
		if err := svc.modbus.ListenRTUOverTCP(cfg.Modbus.RTUOverTCPAddress); err != nil {
			svc.Close()
			return nil, fmt.Errorf("start modbus rtu-over-tcp server on %s: %w", cfg.Modbus.RTUOverTCPAddress, err)
		}
		zaplog.Infof("modbus RTU-over-TCP server listening on %s", cfg.Modbus.RTUOverTCPAddress)
	}

	svc.sim = simulator.NewSimulator(cfg, svc.modbus)

	var err error
	if svc.xn3477, err = xn3477sim.StartServer(cfg.XN3477, svc.sim); err != nil {
		svc.Close()
		return nil, fmt.Errorf("start xn3477 servers: %w", err)
	}
	if svc.iec61850, err = iec61850sim.StartServer(cfg.IEC61850, svc.sim); err != nil {
		svc.Close()
		return nil, fmt.Errorf("start iec61850 server: %w", err)
	}
	svc.iec61850.Sync()

	if svc.canPubs, err = cansim.StartPublishers(cfg.CAN, svc.sim); err != nil {
		svc.Close()
		return nil, fmt.Errorf("start can publishers: %w", err)
	}
	return svc, nil
}

// restoreState 在启动时回放持久化状态；读取失败只记录错误，不阻断仿真启动。
func restoreState(cfg *simulator.Config, sim *simulator.Simulator) {
	if cfg.State.File == "" {
		return
	}
	st, err := simulator.LoadState(cfg.State.File)
	if err != nil {
		zaplog.Errorf("load state %s: %v", cfg.State.File, err)
		return
	}
	if st == nil {
		return
	}
	sim.RestoreLocked(st)
	zaplog.Infof("restored state from %s: %d meter(s), %d battery(s), %d pv(s)",
		cfg.State.File, len(st.Meters), len(st.Batteries), len(st.PVs))
}

// saveState 落盘当前仿真状态。
func saveState(cfg *simulator.Config, sim *simulator.Simulator) {
	if err := simulator.SaveState(cfg.State.File, sim.SnapshotLocked()); err != nil {
		zaplog.Errorf("save state %s: %v", cfg.State.File, err)
	}
}

// serveLoop 每秒推进一次仿真，并按配置周期落盘状态，直到收到退出信号。
func serveLoop(cfg *simulator.Config, svc *services) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var saveC <-chan time.Time
	if cfg.State.File != "" {
		saveTicker := time.NewTicker(time.Duration(cfg.State.Interval) * time.Second)
		defer saveTicker.Stop()
		saveC = saveTicker.C
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	for {
		select {
		case <-ticker.C:
			svc.sim.Tick()
			svc.syncProtocols()
		case <-saveC:
			saveState(cfg, svc.sim)
		case sig := <-sigCh:
			zaplog.Infof("received signal %v, shutting down", sig)
			if cfg.State.File != "" {
				saveState(cfg, svc.sim)
			}
			return
		}
	}
}
