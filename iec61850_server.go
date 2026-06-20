//go:build iec61850

package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"aiwatt.net/ems/go-common/zaplog"
	"github.com/go-bindings/iec61850"
)

// iec61850_model.cfg 由 tools/gen_iec61850_model.py 从现场 CID
// (docs/IES1000_IES900_CO_V2.5.cid) 全量转换而来，模型结构与现场逐字段对齐：
// LD0(公用)/CTRL(控制)/MEAS(测量)/PIGO(GOOSE)，LN 带 set/ctl/meas 等前缀。
//
//go:embed iec61850_model.cfg
var iec61850ModelConfig []byte

type iec61850Server struct {
	sim     *Simulator
	battery *BatteryUnit
	server  *iec61850.IedServer
	model   *iec61850.IedModel

	// IED 名（如 TEMPLATE / pcs01），决定 MMS 域名与全部对象引用前缀。
	iedName string
	// CID 模型中各逻辑节点的对象引用前缀（IED 名 + LD inst + 带前缀 LN），随 iedName 计算。
	refCtrlSet  string // 遥调（APC，setpoint，direct-with-normal-security）
	refCtrlGapc string // 遥控（SPC，开关机/复位/待机）
	refMeasPcs  string // PCS 遥测
	refMeasBms  string // BMS 遥测
	refPigo     string // GOOSE 遥测（与 dsGOOSE1 顺序一致）
	refAlarmPcs string // PCS 离散告警（alarmGGIO1.AlmN.stVal，emu fwPoint 来源）

	// 对象引用 -> 模型节点缓存，避免每个 Sync tick 重复字符串解析。
	nodeCache map[string]*iec61850.ModelNode

	goosePublisher       *iec61850.GoosePublisher
	gooseInterval        time.Duration
	gooseLastPublish     time.Time
	gooseTimeAllowedMS   uint32
	reactiveSetpointKVAr float32
}

type iec61850MultiServer struct {
	servers []*iec61850Server
}

// iec61850TelemetryValues 是 GOOSE dsGOOSE1 / PIGO measGGIO1.AnIn1-9 的 9 个值，
// 顺序与现场 CID 数据集严格一致，不可随意调整。
type iec61850TelemetryValues struct {
	nowMs             int64
	ratedPowerKW      float32
	socPercent        float32
	pcsStatus         int32
	activeKW          float32
	reactiveKVAr      float32
	maxChargeKW       float32
	maxDischargeKW    float32
	activeSetpointKW  float32
	reactSetpointKVAr float32
}

func startIEC61850Server(cfg IEC61850Config, sim *Simulator) (IEC61850Service, error) {
	if !cfg.Enabled {
		return noopIEC61850Service{}, nil
	}
	devices := effectiveIEC61850Devices(cfg, sim)
	multi := &iec61850MultiServer{}
	for _, device := range devices {
		server, err := startIEC61850Device(device, sim)
		if err != nil {
			multi.Close()
			return nil, err
		}
		multi.servers = append(multi.servers, server)
	}
	return multi, nil
}

func effectiveIEC61850Devices(cfg IEC61850Config, sim *Simulator) []IEC61850DeviceConfig {
	if len(cfg.Devices) > 0 {
		devices := make([]IEC61850DeviceConfig, len(cfg.Devices))
		copy(devices, cfg.Devices)
		for i := range devices {
			devices[i].IEDName = effectiveIEDName(cfg, devices[i], true)
		}
		return devices
	}
	device := IEC61850DeviceConfig{
		Address: cfg.Address,
		GOOSE:   cfg.GOOSE,
	}
	if sim != nil && len(sim.batteries) > 0 {
		device.PCSSlaveID = sim.batteries[0].pcs.SlaveID
	}
	device.IEDName = effectiveIEDName(cfg, device, false)
	return []IEC61850DeviceConfig{device}
}

func startIEC61850Device(cfg IEC61850DeviceConfig, sim *Simulator) (*iec61850Server, error) {
	battery, err := findIEC61850Battery(sim, cfg.PCSSlaveID)
	if err != nil {
		return nil, err
	}
	host, port, err := splitIEC61850Address(cfg.Address)
	if err != nil {
		return nil, err
	}

	iedName := cfg.IEDName
	if iedName == "" {
		iedName = "TEMPLATE"
	}
	model, err := loadIEC61850Model(iedName)
	if err != nil {
		return nil, err
	}
	server := iec61850.NewServerWithConfig(iec61850.NewServerConfig(), model)
	server.SetServerIdentity("INOVANCE", "IES900 virtual PCS", "V2.5")
	if host != "" {
		if err := server.SetMmsLocalIpAddress(host); err != nil {
			model.Destroy()
			return nil, fmt.Errorf("set IEC 61850 bind address %q: %w", host, err)
		}
	}

	svc, err := newIEC61850Service(sim, battery, model, server, iedName)
	if err != nil {
		server.Destroy()
		model.Destroy()
		return nil, err
	}
	if err := svc.configureGOOSE(cfg.GOOSE); err != nil {
		svc.Close()
		return nil, err
	}
	if err := svc.installControlHandlers(); err != nil {
		svc.Close()
		return nil, err
	}
	server.Start(port)
	if !server.IsRunning() {
		svc.Close()
		return nil, fmt.Errorf("IEC 61850 MMS server failed to start on %s for PCS slave %d", cfg.Address, cfg.PCSSlaveID)
	}
	return svc, nil
}

func findIEC61850Battery(sim *Simulator, pcsSlaveID uint8) (*BatteryUnit, error) {
	if sim == nil {
		return nil, fmt.Errorf("IEC 61850 requires simulator")
	}
	if pcsSlaveID == 0 {
		if len(sim.batteries) == 0 {
			return nil, fmt.Errorf("IEC 61850 requires at least one battery unit")
		}
		return sim.batteries[0], nil
	}
	for _, battery := range sim.batteries {
		if battery.pcs.SlaveID == pcsSlaveID {
			return battery, nil
		}
	}
	return nil, fmt.Errorf("IEC 61850 references unknown PCS slave %d", pcsSlaveID)
}

// loadIEC61850Model 把内嵌模型加载为运行时 IedModel；iedName 用于把模板里的 IED 名
// TEMPLATE 替换成本端点实际 IED 名，从而让每套 PCS 暴露独立的 MMS 域名与对象引用。
func loadIEC61850Model(iedName string) (*iec61850.IedModel, error) {
	tmpDir, err := os.MkdirTemp("", "virtual-bess-iec61850-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	cfgBytes := iec61850ModelConfig
	if iedName != "" && iedName != "TEMPLATE" {
		// 模板首行为 MODEL(TEMPLATE){，IED 名在 .cfg 中只此一处，替换即改写全部对象引用前缀。
		cfgBytes = bytes.Replace(cfgBytes, []byte("MODEL(TEMPLATE)"), []byte("MODEL("+iedName+")"), 1)
	}
	modelPath := filepath.Join(tmpDir, "model.cfg")
	if err := os.WriteFile(modelPath, cfgBytes, 0600); err != nil {
		return nil, err
	}
	model, err := iec61850.CreateModelFromConfigFileEx(modelPath)
	if err != nil {
		return nil, err
	}
	if model.Model == nil {
		return nil, fmt.Errorf("failed to load embedded IEC 61850 model")
	}
	return model, nil
}

func newIEC61850Service(sim *Simulator, battery *BatteryUnit, model *iec61850.IedModel, server *iec61850.IedServer, iedName string) (*iec61850Server, error) {
	if iedName == "" {
		iedName = "TEMPLATE"
	}
	return &iec61850Server{
		sim:         sim,
		battery:     battery,
		model:       model,
		server:      server,
		iedName:     iedName,
		refCtrlSet:  iedName + "CTRL/setGGIO1",
		refCtrlGapc: iedName + "CTRL/ctlGAPC1",
		refMeasPcs:  iedName + "MEAS/measGGIO1",
		refMeasBms:  iedName + "MEAS/measGGIO2",
		refPigo:     iedName + "PIGO/measGGIO1",
		refAlarmPcs: iedName + "MEAS/alarmGGIO1",
		nodeCache:   make(map[string]*iec61850.ModelNode),
	}, nil
}

// node 按对象引用解析模型节点并缓存；引用拼写错误（模型里不存在）直接返回 nil。
func (s *iec61850Server) node(ref string) *iec61850.ModelNode {
	if n, ok := s.nodeCache[ref]; ok {
		return n
	}
	n := s.model.GetModelNodeByObjectReference(ref)
	s.nodeCache[ref] = n
	return n
}

// installControlHandlers 给可控 DO 注册 Operate 回调。遥调走 APC（ctlVal 为浮点），
// 遥控走 SPC（ctlVal 为布尔）；二者 ctlModel 均为 direct-with-normal-security。
func (s *iec61850Server) installControlHandlers() error {
	bindings := []struct {
		ref     string
		handler iec61850.ControlHandler
	}{
		{s.refCtrlSet + ".APCS1", s.ctlActivePower},   // 有功功率设定
		{s.refCtrlSet + ".APCS2", s.ctlReactivePower}, // 无功功率设定
		{s.refCtrlSet + ".APCS9", s.ctlPCSCommand},    // PCS 控制命令 0关1开2复位3待机
		{s.refCtrlSet + ".APCS10", s.ctlRunMode},      // PCS 运行模式 0并网1离网
		{s.refCtrlGapc + ".SPCSO2", s.ctlStartStop},   // PCS 开关机
		{s.refCtrlGapc + ".SPCSO5", s.ctlFaultReset},  // 故障复位
		{s.refCtrlGapc + ".SPCSO6", s.ctlStandby},     // 待机命令
	}
	for _, b := range bindings {
		n := s.node(b.ref)
		if n == nil {
			return fmt.Errorf("IEC 61850 control object %s not found in model", b.ref)
		}
		s.server.SetControlHandler(n, b.handler)
	}
	return nil
}

func (s *iec61850Server) ctlActivePower(_ *iec61850.ModelNode, _ *iec61850.ControlAction, v *iec61850.MmsValue, _ bool) iec61850.ControlHandlerResult {
	kw, ok := ctlFloat(v)
	if !ok || !fitsPCSCommandRegister(kw) {
		return iec61850.CONTROL_RESULT_FAILED
	}
	raw := int16(math.Round(float64(kw * 10)))
	if s.writeTargetPCS(RegPCSPowerCmd, uint16(raw)) != nil {
		return iec61850.CONTROL_RESULT_FAILED
	}
	return iec61850.CONTROL_RESULT_OK
}

func (s *iec61850Server) ctlReactivePower(_ *iec61850.ModelNode, _ *iec61850.ControlAction, v *iec61850.MmsValue, _ bool) iec61850.ControlHandlerResult {
	kvar, ok := ctlFloat(v)
	if !ok {
		return iec61850.CONTROL_RESULT_FAILED
	}
	// 仿真器寄存器无无功设定项，仅缓存做回读与 GOOSE 上送。
	s.reactiveSetpointKVAr = kvar
	return iec61850.CONTROL_RESULT_OK
}

func (s *iec61850Server) ctlPCSCommand(_ *iec61850.ModelNode, _ *iec61850.ControlAction, v *iec61850.MmsValue, _ bool) iec61850.ControlHandlerResult {
	cmd, ok := ctlInt(v)
	if !ok {
		return iec61850.CONTROL_RESULT_FAILED
	}
	var err error
	switch cmd {
	case 0:
		err = s.writeTargetPCS(RegPCSShutdown, 1)
	case 1:
		err = s.writeTargetPCS(RegPCSStartup, 1)
	case 2:
		err = s.writeTargetPCS(RegPCSFaultReset, 1)
	case 3:
		err = s.writeTargetPCS(RegPCSPowerCmd, 0)
	default:
		return iec61850.CONTROL_RESULT_FAILED
	}
	if err != nil {
		return iec61850.CONTROL_RESULT_FAILED
	}
	return iec61850.CONTROL_RESULT_OK
}

func (s *iec61850Server) ctlRunMode(_ *iec61850.ModelNode, _ *iec61850.ControlAction, v *iec61850.MmsValue, _ bool) iec61850.ControlHandlerResult {
	mode, ok := ctlInt(v)
	if !ok || (mode != 0 && mode != 1) {
		return iec61850.CONTROL_RESULT_FAILED
	}
	if s.writeTargetPCS(RegPCSGridMode, uint16(mode)) != nil {
		return iec61850.CONTROL_RESULT_FAILED
	}
	return iec61850.CONTROL_RESULT_OK
}

func (s *iec61850Server) ctlStartStop(_ *iec61850.ModelNode, _ *iec61850.ControlAction, v *iec61850.MmsValue, _ bool) iec61850.ControlHandlerResult {
	on, ok := ctlBool(v)
	if !ok {
		return iec61850.CONTROL_RESULT_FAILED
	}
	var reg uint16 = RegPCSShutdown
	if on {
		reg = RegPCSStartup
	}
	if s.writeTargetPCS(reg, 1) != nil {
		return iec61850.CONTROL_RESULT_FAILED
	}
	return iec61850.CONTROL_RESULT_OK
}

func (s *iec61850Server) ctlFaultReset(_ *iec61850.ModelNode, _ *iec61850.ControlAction, v *iec61850.MmsValue, _ bool) iec61850.ControlHandlerResult {
	on, ok := ctlBool(v)
	if !ok || !on {
		return iec61850.CONTROL_RESULT_FAILED
	}
	if s.writeTargetPCS(RegPCSFaultReset, 1) != nil {
		return iec61850.CONTROL_RESULT_FAILED
	}
	return iec61850.CONTROL_RESULT_OK
}

func (s *iec61850Server) ctlStandby(_ *iec61850.ModelNode, _ *iec61850.ControlAction, v *iec61850.MmsValue, _ bool) iec61850.ControlHandlerResult {
	on, ok := ctlBool(v)
	if !ok || !on {
		return iec61850.CONTROL_RESULT_FAILED
	}
	if s.writeTargetPCS(RegPCSPowerCmd, 0) != nil {
		return iec61850.CONTROL_RESULT_FAILED
	}
	return iec61850.CONTROL_RESULT_OK
}

func (s *iec61850Server) Sync() {
	bu := s.targetBattery()
	if bu == nil {
		return
	}
	nowMs := int64(s.sim.nowFunc().UnixMilli())

	s.server.LockDataModel()
	s.updatePcsMeas(bu, nowMs)
	s.updateBmsMeas(bu, nowMs)
	values := s.updatePigo(bu, nowMs)
	s.updateSetReadback(bu, nowMs)
	s.updateAlarms(bu, nowMs)
	s.server.UnlockDataModel()

	if err := s.publishGOOSE(values); err != nil {
		zaplog.Errorf("publish IEC 61850 GOOSE: %v", err)
	}
}

// gooseValues 汇总 dsGOOSE1 / PIGO 的 9 个值；顺序由现场 CID 固定。
func (s *iec61850Server) gooseValues(bu *BatteryUnit, nowMs int64) iec61850TelemetryValues {
	pcs := bu.pcs
	bms := bu.bms
	return iec61850TelemetryValues{
		nowMs:             nowMs,
		ratedPowerKW:      float32(bu.ratedPowerKW),
		socPercent:        float32(bms.ReadU16(RegBMSSOC)) / 10,
		pcsStatus:         int32(iec61850PCSStatus(pcs)),
		activeKW:          float32(uint16ToInt16(pcs.ReadU16(RegPCSTotalActivePW))) / 10,
		reactiveKVAr:      float32(uint16ToInt16(pcs.ReadU16(RegPCSTotalReactPW))) / 10,
		maxChargeKW:       float32(bms.ReadU16(RegBMSMaxChargePW)) / 10,
		maxDischargeKW:    float32(bms.ReadU16(RegBMSMaxDischargePW)) / 10,
		activeSetpointKW:  float32(uint16ToInt16(pcs.ReadU16(RegPCSPowerCmd))) / 10,
		reactSetpointKVAr: s.reactiveSetpointKVAr,
	}
}

// updatePcsMeas 填 MEAS/measGGIO1（PCS 遥测）核心点位；现场预留/累计电量等点保持默认 0。
func (s *iec61850Server) updatePcsMeas(bu *BatteryUnit, nowMs int64) {
	pcs := bu.pcs
	bms := bu.bms
	s.setFloat(s.refMeasPcs+".AnIn1", float32(pcs.ReadU16(RegPCSVoltageA))/10, nowMs)
	s.setFloat(s.refMeasPcs+".AnIn2", float32(pcs.ReadU16(RegPCSVoltageB))/10, nowMs)
	s.setFloat(s.refMeasPcs+".AnIn3", float32(pcs.ReadU16(RegPCSVoltageC))/10, nowMs)
	s.setFloat(s.refMeasPcs+".AnIn4", float32(uint16ToInt16(pcs.ReadU16(RegPCSCurrentA)))/10, nowMs)
	s.setFloat(s.refMeasPcs+".AnIn5", float32(uint16ToInt16(pcs.ReadU16(RegPCSCurrentB)))/10, nowMs)
	s.setFloat(s.refMeasPcs+".AnIn6", float32(uint16ToInt16(pcs.ReadU16(RegPCSCurrentC)))/10, nowMs)
	s.setFloat(s.refMeasPcs+".AnIn7", float32(uint16ToInt16(pcs.ReadU16(RegPCSTotalActivePW)))/10, nowMs)
	s.setFloat(s.refMeasPcs+".AnIn8", float32(uint16ToInt16(pcs.ReadU16(RegPCSTotalReactPW)))/10, nowMs)
	s.setFloat(s.refMeasPcs+".AnIn9", float32(uint16ToInt16(pcs.ReadU16(RegPCSPowerFactor)))/100, nowMs)
	s.setFloat(s.refMeasPcs+".AnIn10", float32(uint16ToInt16(bms.ReadU16(RegBMSPower)))/10, nowMs)
	s.setFloat(s.refMeasPcs+".AnIn11", float32(pcs.ReadU16(RegPCSTotalApparent))/10, nowMs)
	s.setFloat(s.refMeasPcs+".AnIn12", float32(bms.ReadU16(RegBMSMaxDischargePW))/10, nowMs)
	s.setFloat(s.refMeasPcs+".AnIn13", float32(bms.ReadU16(RegBMSMaxChargePW))/10, nowMs)
	s.setFloat(s.refMeasPcs+".AnIn14", float32(bms.ReadU16(RegBMSMaxChargePW))/10, nowMs)
	s.setFloat(s.refMeasPcs+".AnIn15", float32(bms.ReadU16(RegBMSMaxDischargePW))/10, nowMs)
	// 直流侧（对齐现场 CID）：AnIn41=电池电压，AnIn42=直流电流；
	// 复用 syncPCSDC 写入的 PCS 直流寄存器，确保 MMS 与 Modbus 两个北向口一致
	s.setFloat(s.refMeasPcs+".AnIn41", float32(pcs.ReadU16(RegPCSDCVoltage))/10, nowMs)
	s.setFloat(s.refMeasPcs+".AnIn42", float32(uint16ToInt16(pcs.ReadU16(RegPCSDCCurrent)))/10, nowMs)
	// AnIn45=电网频率（对齐现场 CID 与设备级点表 grid_frequency←AnIn45）；
	// 此前只在 Modbus 口写了 RegPCSFrequency，MMS 口漏发导致 MMS 客户端读到默认 0
	s.setFloat(s.refMeasPcs+".AnIn45", float32(pcs.ReadU16(RegPCSFrequency))/100, nowMs)
}

// updateBmsMeas 填 MEAS/measGGIO2（BMS 遥测）核心点位。
func (s *iec61850Server) updateBmsMeas(bu *BatteryUnit, nowMs int64) {
	bms := bu.bms
	s.setFloat(s.refMeasBms+".AnIn1", float32(bms.ReadU16(RegBMSSOC))/10, nowMs)
	s.setFloat(s.refMeasBms+".AnIn2", float32(bms.ReadU16(RegBMSMaxChargePW))/10, nowMs)
	s.setFloat(s.refMeasBms+".AnIn3", float32(bms.ReadU16(RegBMSMaxDischargePW))/10, nowMs)
	s.setFloat(s.refMeasBms+".AnIn4", float32(bms.ReadU16(RegBMSSysStatus)), nowMs)
	s.setFloat(s.refMeasBms+".AnIn5", float32(bms.ReadU16(RegBMSAlarmStatus)), nowMs)
	s.setFloat(s.refMeasBms+".AnIn6", float32(bms.ReadU16(RegBMSVoltage))/10, nowMs)
	s.setFloat(s.refMeasBms+".AnIn7", float32(uint16ToInt16(bms.ReadU16(RegBMSCurrent)))/10, nowMs)
	s.setFloat(s.refMeasBms+".AnIn8", float32(bms.ReadU16(RegBMSMaxChargeI))/10, nowMs)
	s.setFloat(s.refMeasBms+".AnIn9", float32(bms.ReadU16(RegBMSMaxDischargeI))/10, nowMs)
	s.setFloat(s.refMeasBms+".AnIn10", float32(bms.ReadU16(RegBMSCellVMax))/1000, nowMs)
	s.setFloat(s.refMeasBms+".AnIn11", float32(bms.ReadU16(RegBMSCellVMin))/1000, nowMs)
	s.setFloat(s.refMeasBms+".AnIn12", float32(bms.ReadU16(RegBMSSOH))/10, nowMs)
}

// 告警触发阈值：满充判定与单体过压判定（贴近 cellVoltageFull=3.6V 曲线）。
const (
	alarmSOCHighPercent = 98.0 // SOC ≥ 98% 视为储能电池电压过高（满充）
	alarmCellVMaxHighMV = 3550 // 单体最高电压 ≥ 3.55V 视为电池电压过高
)

// updateAlarms 按当前物理状态置位/消除 PCS IED 的离散告警点（alarmGGIO1.AlmN.stVal）。
//
// 这些点是 emu 设备级点表 [fwPoint] 的源点，emu 轮询读到 stVal=1 即生成 FwMsg 上报故障，
// 读到 0 即视为消除并归档到历史。告警纯由当前状态推导（无锁存）：故障条件消失或现场
// 复位（PCS/BMS FaultReset 已在 ProcessControls 里清除内部标志）后，下一拍 stVal 回 0。
//
// 映射对齐 AWS 现场 PCS-IEC61850-MMS.csv：
//   - Alm12 直流侧全母线软件欠压 ← PCS 空载启动（BMS 高压未闭合）触发的直流欠压故障
//   - Alm2  储能电池电压过高     ← 满充（SOC 过高）
//   - Alm4  电池电压过高         ← 单体最高电压过高
func (s *iec61850Server) updateAlarms(bu *BatteryUnit, nowMs int64) {
	s.setBool(s.refAlarmPcs+".Alm12", bu.PcsDCUnderVoltFault(), nowMs)
	s.setBool(s.refAlarmPcs+".Alm2", bu.SOC() >= alarmSOCHighPercent, nowMs)
	s.setBool(s.refAlarmPcs+".Alm4", bu.bms.ReadU16(RegBMSCellVMax) >= alarmCellVMaxHighMV, nowMs)
}

// updatePigo 填 PIGO/measGGIO1（GOOSE 遥测 9 值），并返回这组值供 GOOSE 发布复用。
func (s *iec61850Server) updatePigo(bu *BatteryUnit, nowMs int64) iec61850TelemetryValues {
	g := s.gooseValues(bu, nowMs)
	s.setFloat(s.refPigo+".AnIn1", g.ratedPowerKW, nowMs)
	s.setFloat(s.refPigo+".AnIn2", g.socPercent, nowMs)
	s.setInt(s.refPigo+".AnIn3", g.pcsStatus, nowMs)
	s.setFloat(s.refPigo+".AnIn4", g.activeKW, nowMs)
	s.setFloat(s.refPigo+".AnIn5", g.reactiveKVAr, nowMs)
	s.setFloat(s.refPigo+".AnIn6", g.maxChargeKW, nowMs)
	s.setFloat(s.refPigo+".AnIn7", g.maxDischargeKW, nowMs)
	s.setFloat(s.refPigo+".AnIn8", g.activeSetpointKW, nowMs)
	s.setFloat(s.refPigo+".AnIn9", g.reactSetpointKVAr, nowMs)
	return g
}

// updateSetReadback 把遥调设定值回填到 setGGIO1 各 APC 的 mxVal（MX 读侧）。
func (s *iec61850Server) updateSetReadback(bu *BatteryUnit, nowMs int64) {
	pcs := bu.pcs
	activeSet := float32(uint16ToInt16(pcs.ReadU16(RegPCSPowerCmd))) / 10
	s.setMxVal(s.refCtrlSet+".APCS1", activeSet, nowMs)
	s.setMxVal(s.refCtrlSet+".APCS2", s.reactiveSetpointKVAr, nowMs)
	s.setMxVal(s.refCtrlSet+".APCS10", float32(pcs.ReadU16(RegPCSGridMode)), nowMs)
}

func (s *iec61850Server) configureGOOSE(cfg IEC61850GOOSEConfig) error {
	if !cfg.Enabled {
		return nil
	}
	appID, err := parseIEC61850AppID(cfg.AppID)
	if err != nil {
		return err
	}
	dstMAC, err := parseIEC61850MAC(cfg.DstMAC)
	if err != nil {
		return err
	}
	publisher, err := iec61850.NewGoosePublisherEx(iec61850.GoosePublisherConf{
		InterfaceID: cfg.InterfaceID,
		CommParameters: iec61850.CommParameters{
			AppID:        appID,
			DstAddr:      dstMAC,
			VlanID:       cfg.VLANID,
			VlanPriority: cfg.VLANPriority,
		},
	}, !cfg.DisableVLAN)
	if err != nil {
		return fmt.Errorf("create IEC 61850 GOOSE publisher on %s: %w", cfg.InterfaceID, err)
	}
	// GoCBRef / dataset 前缀随 IED 名变化，保证多端点 GOOSE 控制块互不冲突。
	goCbRef := s.iedName + "PIGO/LLN0$GO$gocb1"
	publisher.SetGoCbRef(goCbRef)
	publisher.SetGoID(goCbRef)
	publisher.SetDataSetRef(s.iedName + "PIGO/LLN0$dsGOOSE1")
	publisher.SetConfRev(1)
	publisher.SetTimeAllowedToLive(cfg.TimeAllowedToLiveMS)

	s.goosePublisher = publisher
	s.gooseInterval = time.Duration(cfg.IntervalMS) * time.Millisecond
	s.gooseTimeAllowedMS = cfg.TimeAllowedToLiveMS
	return nil
}

func (s *iec61850Server) publishGOOSE(values iec61850TelemetryValues) error {
	if s.goosePublisher == nil {
		return nil
	}
	now := time.UnixMilli(values.nowMs)
	if !s.gooseLastPublish.IsZero() && now.Sub(s.gooseLastPublish) < s.gooseInterval {
		return nil
	}
	dataSet, err := buildIEC61850GooseDataSet(values)
	if err != nil {
		return err
	}
	defer dataSet.Destroy()
	s.goosePublisher.SetTimeAllowedToLive(s.gooseTimeAllowedMS)
	if err := s.goosePublisher.Publish(dataSet); err != nil {
		return err
	}
	s.gooseLastPublish = now
	return nil
}

// buildIEC61850GooseDataSet 按 dsGOOSE1 的 FCDA 顺序构造 GOOSE 数据集，
// AnIn3(PCS 状态) 为 Int32，其余为 Float，顺序不可调整。
func buildIEC61850GooseDataSet(values iec61850TelemetryValues) (*iec61850.LinkedListValue, error) {
	dataSet := iec61850.NewLinkedListValue()
	add := func(mmsType iec61850.MmsType, value interface{}) error {
		return dataSet.Add(&iec61850.MmsValue{Type: mmsType, Value: value})
	}
	for _, item := range []struct {
		mmsType iec61850.MmsType
		value   interface{}
	}{
		{iec61850.Float, values.ratedPowerKW},
		{iec61850.Float, values.socPercent},
		{iec61850.Int32, values.pcsStatus},
		{iec61850.Float, values.activeKW},
		{iec61850.Float, values.reactiveKVAr},
		{iec61850.Float, values.maxChargeKW},
		{iec61850.Float, values.maxDischargeKW},
		{iec61850.Float, values.activeSetpointKW},
		{iec61850.Float, values.reactSetpointKVAr},
	} {
		if err := add(item.mmsType, item.value); err != nil {
			dataSet.Destroy()
			return nil, err
		}
	}
	return dataSet, nil
}

func (s *iec61850Server) Close() {
	if s.goosePublisher != nil {
		s.goosePublisher.Close()
	}
	s.server.Stop()
	s.server.Destroy()
	s.model.Destroy()
}

func (s *iec61850MultiServer) Sync() {
	for _, server := range s.servers {
		server.Sync()
	}
}

func (s *iec61850MultiServer) Close() {
	for _, server := range s.servers {
		server.Close()
	}
	s.servers = nil
}

// setFloat 更新某测量 DO 的 mag.f 及其时间戳；DO 缺失时静默跳过（模型已对齐 CID，正常不会发生）。
func (s *iec61850Server) setFloat(doRef string, value float32, nowMs int64) {
	if n := s.node(doRef + ".mag.f"); n != nil {
		s.server.UpdateFloatAttributeValue(n, value)
	}
	if t := s.node(doRef + ".t"); t != nil {
		s.server.UpdateUTCTimeAttributeValue(t, nowMs)
	}
}

// setInt 更新整型测量 DO 的 mag.i 及其时间戳（如 PCS 系统状态）。
func (s *iec61850Server) setInt(doRef string, value int32, nowMs int64) {
	if n := s.node(doRef + ".mag.i"); n != nil {
		s.server.UpdateInt32AttributeValue(n, value)
	}
	if t := s.node(doRef + ".t"); t != nil {
		s.server.UpdateUTCTimeAttributeValue(t, nowMs)
	}
}

// setBool 更新某 SPS 告警 DO 的 stVal 及其时间戳；DO 缺失时静默跳过（模型已对齐 CID）。
// 用于 alarmGGIO1.AlmN.stVal —— emu 侧 [fwPoint] 的源点，置 1 即上报对应故障，置 0 即消除。
func (s *iec61850Server) setBool(doRef string, value bool, nowMs int64) {
	if n := s.node(doRef + ".stVal"); n != nil {
		s.server.UpdateBooleanAttributeValue(n, value)
	}
	if t := s.node(doRef + ".t"); t != nil {
		s.server.UpdateUTCTimeAttributeValue(t, nowMs)
	}
}

// setMxVal 更新 APC 控制 DO 的 mxVal.f（设定值回读侧）及时间戳。
func (s *iec61850Server) setMxVal(doRef string, value float32, nowMs int64) {
	if n := s.node(doRef + ".mxVal.f"); n != nil {
		s.server.UpdateFloatAttributeValue(n, value)
	}
	if t := s.node(doRef + ".t"); t != nil {
		s.server.UpdateUTCTimeAttributeValue(t, nowMs)
	}
}

func (s *iec61850Server) targetBattery() *BatteryUnit {
	if s.battery != nil {
		return s.battery
	}
	if s.sim != nil && len(s.sim.batteries) > 0 {
		return s.sim.batteries[0]
	}
	return nil
}

func (s *iec61850Server) writeTargetPCS(register, value uint16) error {
	bu := s.targetBattery()
	if bu == nil {
		return fmt.Errorf("IEC 61850 requires target battery unit")
	}
	return s.sim.writeHolding(bu.pcs.SlaveID, register, value)
}

// ctlFloat 从控制 ctlVal 取浮点：APC 的 ctlVal 是 AnalogueValue 结构体（取首元素 f），
// 也兼容直接传入 Float/Integer 的情况。
func ctlFloat(value *iec61850.MmsValue) (float32, bool) {
	if value == nil {
		return 0, false
	}
	switch value.Type {
	case iec61850.Float:
		if v, ok := value.Value.(float32); ok {
			return v, true
		}
	case iec61850.Integer:
		if v, ok := value.Value.(int64); ok {
			return float32(v), true
		}
	case iec61850.Structure, iec61850.Array:
		if elems, ok := value.Value.([]*iec61850.MmsValue); ok && len(elems) > 0 {
			return ctlFloat(elems[0])
		}
	}
	return 0, false
}

// ctlBool 从控制 ctlVal 取布尔：SPC 的 ctlVal 为布尔，兼容结构体包裹的情况。
func ctlBool(value *iec61850.MmsValue) (bool, bool) {
	if value == nil {
		return false, false
	}
	switch value.Type {
	case iec61850.Boolean:
		if v, ok := value.Value.(bool); ok {
			return v, true
		}
	case iec61850.Structure, iec61850.Array:
		if elems, ok := value.Value.([]*iec61850.MmsValue); ok && len(elems) > 0 {
			return ctlBool(elems[0])
		}
	}
	return false, false
}

// ctlInt 把控制 ctlVal 当作整数命令（要求接近整数，拒绝明显的小数）。
func ctlInt(value *iec61850.MmsValue) (int, bool) {
	f, ok := ctlFloat(value)
	if !ok {
		return 0, false
	}
	rounded := math.Round(float64(f))
	if math.Abs(float64(f)-rounded) > 0.0001 {
		return 0, false
	}
	return int(rounded), true
}

func fitsPCSCommandRegister(kw float32) bool {
	raw := math.Round(float64(kw * 10))
	return raw >= math.MinInt16 && raw <= math.MaxInt16
}

func iec61850PCSStatus(pcs *SlaveBank) int {
	if pcs.ReadU16(RegPCSFaultStatus) != 0 {
		return 6
	}
	if pcs.ReadU16(RegPCSGridStatus) != 0 && pcs.ReadU16(RegPCSSysStatus) != 1 {
		return 5
	}
	switch pcs.ReadU16(RegPCSSysStatus) {
	case 1:
		return 0
	case 2:
		return 1
	case 3:
		return 2
	case 4:
		return 3
	default:
		return 4
	}
}
