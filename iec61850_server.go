//go:build iec61850

package main

import (
	_ "embed"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"aiwatt.net/ems/go-common/zaplog"
	"github.com/go-bindings/iec61850"
)

//go:embed iec61850_model.cfg
var iec61850ModelConfig []byte

type iec61850Server struct {
	sim     *Simulator
	battery *BatteryUnit
	server  *iec61850.IedServer
	model   *iec61850.IedModel

	activePowerSetpoint *iec61850.ModelNode
	reactiveSetpoint    *iec61850.ModelNode
	pcsCommand          *iec61850.ModelNode
	pcsRunMode          *iec61850.ModelNode

	ratedPower        *iec61850.ModelNode
	soc               *iec61850.ModelNode
	pcsStatus         *iec61850.ModelNode
	activePower       *iec61850.ModelNode
	reactivePower     *iec61850.ModelNode
	maxChargePower    *iec61850.ModelNode
	maxDischargePower *iec61850.ModelNode
	activeSetReadback *iec61850.ModelNode
	reactSetReadback  *iec61850.ModelNode

	goosePublisher       *iec61850.GoosePublisher
	gooseInterval        time.Duration
	gooseLastPublish     time.Time
	gooseTimeAllowedMS   uint32
	reactiveSetpointKVAr float32
}

type iec61850MultiServer struct {
	servers []*iec61850Server
}

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
	pcsRunMode        float32
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
		return cfg.Devices
	}
	device := IEC61850DeviceConfig{
		Address: cfg.Address,
		GOOSE:   cfg.GOOSE,
	}
	if sim != nil && len(sim.batteries) > 0 {
		device.PCSSlaveID = sim.batteries[0].pcs.SlaveID
	}
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

	model, err := loadIEC61850Model()
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

	svc, err := newIEC61850Service(sim, battery, model, server)
	if err != nil {
		server.Destroy()
		model.Destroy()
		return nil, err
	}
	if err := svc.configureGOOSE(cfg.GOOSE); err != nil {
		svc.Close()
		return nil, err
	}
	svc.installWriteHandlers()
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

func loadIEC61850Model() (*iec61850.IedModel, error) {
	tmpDir, err := os.MkdirTemp("", "virtual-bess-iec61850-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	modelPath := filepath.Join(tmpDir, "model.cfg")
	if err := os.WriteFile(modelPath, iec61850ModelConfig, 0600); err != nil {
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

func newIEC61850Service(sim *Simulator, battery *BatteryUnit, model *iec61850.IedModel, server *iec61850.IedServer) (*iec61850Server, error) {
	svc := &iec61850Server{sim: sim, battery: battery, model: model, server: server}

	required := map[string]**iec61850.ModelNode{
		"TEMPLATECTRL/GGIO1.APCS1.setMag.f":  &svc.activePowerSetpoint,
		"TEMPLATECTRL/GGIO1.APCS2.setMag.f":  &svc.reactiveSetpoint,
		"TEMPLATECTRL/GGIO1.APCS9.setMag.f":  &svc.pcsCommand,
		"TEMPLATECTRL/GGIO1.APCS10.setMag.f": &svc.pcsRunMode,
		"TEMPLATEPIGO/GGIO1.AnIn1.mag.f":     &svc.ratedPower,
		"TEMPLATEPIGO/GGIO1.AnIn2.mag.f":     &svc.soc,
		"TEMPLATEPIGO/GGIO1.AnIn3.mag.i":     &svc.pcsStatus,
		"TEMPLATEPIGO/GGIO1.AnIn4.mag.f":     &svc.activePower,
		"TEMPLATEPIGO/GGIO1.AnIn5.mag.f":     &svc.reactivePower,
		"TEMPLATEPIGO/GGIO1.AnIn6.mag.f":     &svc.maxChargePower,
		"TEMPLATEPIGO/GGIO1.AnIn7.mag.f":     &svc.maxDischargePower,
		"TEMPLATEPIGO/GGIO1.AnIn8.mag.f":     &svc.activeSetReadback,
		"TEMPLATEPIGO/GGIO1.AnIn9.mag.f":     &svc.reactSetReadback,
	}
	for ref, target := range required {
		node := model.GetModelNodeByObjectReference(ref)
		if node == nil {
			return nil, fmt.Errorf("IEC 61850 model node %s not found", ref)
		}
		*target = node
	}
	return svc, nil
}

func (s *iec61850Server) installWriteHandlers() {
	s.server.SetWriteAccessPolicy(iec61850.SP, iec61850.AccessPolicyAllow)
	s.server.SetHandleWriteAccess(s.activePowerSetpoint, s.handleActivePowerWrite)
	s.server.SetHandleWriteAccess(s.reactiveSetpoint, s.handleReactivePowerWrite)
	s.server.SetHandleWriteAccess(s.pcsCommand, s.handlePCSCommandWrite)
	s.server.SetHandleWriteAccess(s.pcsRunMode, s.handlePCSRunModeWrite)
}

func (s *iec61850Server) handleActivePowerWrite(_ *iec61850.ModelNode, value *iec61850.MmsValue) iec61850.MmsDataAccessError {
	if s.applyActivePowerSetpoint(value) {
		return iec61850.DATA_ACCESS_ERROR_SUCCESS
	}
	return iec61850.DATA_ACCESS_ERROR_OBJECT_VALUE_INVALID
}

func (s *iec61850Server) handleReactivePowerWrite(_ *iec61850.ModelNode, value *iec61850.MmsValue) iec61850.MmsDataAccessError {
	if s.applyReactivePowerSetpoint(value) {
		return iec61850.DATA_ACCESS_ERROR_SUCCESS
	}
	return iec61850.DATA_ACCESS_ERROR_OBJECT_VALUE_INVALID
}

func (s *iec61850Server) handlePCSCommandWrite(_ *iec61850.ModelNode, value *iec61850.MmsValue) iec61850.MmsDataAccessError {
	if s.applyPCSCommand(value) {
		return iec61850.DATA_ACCESS_ERROR_SUCCESS
	}
	return iec61850.DATA_ACCESS_ERROR_OBJECT_VALUE_INVALID
}

func (s *iec61850Server) handlePCSRunModeWrite(_ *iec61850.ModelNode, value *iec61850.MmsValue) iec61850.MmsDataAccessError {
	if s.applyPCSRunMode(value) {
		return iec61850.DATA_ACCESS_ERROR_SUCCESS
	}
	return iec61850.DATA_ACCESS_ERROR_OBJECT_VALUE_INVALID
}

func (s *iec61850Server) applyActivePowerSetpoint(value *iec61850.MmsValue) bool {
	kw, ok := mmsFloat32(value)
	if !ok || !fitsPCSCommandRegister(kw) {
		return false
	}
	raw := int16(math.Round(float64(kw * 10)))
	return s.writeTargetPCS(RegPCSPowerCmd, uint16(raw)) == nil
}

func (s *iec61850Server) applyReactivePowerSetpoint(value *iec61850.MmsValue) bool {
	kvar, ok := mmsFloat32(value)
	if !ok {
		return false
	}
	s.reactiveSetpointKVAr = kvar
	return true
}

func (s *iec61850Server) applyPCSCommand(value *iec61850.MmsValue) bool {
	cmd, ok := roundedMMSInt(value)
	if !ok {
		return false
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
		return false
	}
	return err == nil
}

func (s *iec61850Server) applyPCSRunMode(value *iec61850.MmsValue) bool {
	mode, ok := roundedMMSInt(value)
	if !ok {
		return false
	}
	switch mode {
	case 0:
		if err := s.writeTargetPCS(RegPCSGridMode, 0); err != nil {
			return false
		}
	case 1:
		if err := s.writeTargetPCS(RegPCSGridMode, 1); err != nil {
			return false
		}
	default:
		return false
	}
	return true
}

func (s *iec61850Server) Sync() {
	values, ok := s.telemetryValues()
	if !ok {
		return
	}

	s.server.LockDataModel()
	s.updateMMS(values)
	s.server.UnlockDataModel()

	if err := s.publishGOOSE(values); err != nil {
		zaplog.Errorf("publish IEC 61850 GOOSE: %v", err)
	}
}

func (s *iec61850Server) telemetryValues() (iec61850TelemetryValues, bool) {
	bu := s.targetBattery()
	if bu == nil {
		return iec61850TelemetryValues{}, false
	}
	pcs := bu.pcs
	bms := bu.bms
	return iec61850TelemetryValues{
		nowMs:             int64(s.sim.nowFunc().UnixMilli()),
		ratedPowerKW:      float32(bu.ratedPowerKW),
		socPercent:        float32(bms.ReadU16(RegBMSSOC)) / 10,
		pcsStatus:         int32(iec61850PCSStatus(pcs)),
		activeKW:          float32(uint16ToInt16(pcs.ReadU16(RegPCSTotalActivePW))) / 10,
		reactiveKVAr:      0,
		maxChargeKW:       float32(bms.ReadU16(RegBMSMaxChargePW)) / 10,
		maxDischargeKW:    float32(bms.ReadU16(RegBMSMaxDischargePW)) / 10,
		activeSetpointKW:  float32(uint16ToInt16(pcs.ReadU16(RegPCSPowerCmd))) / 10,
		reactSetpointKVAr: s.reactiveSetpointKVAr,
		pcsRunMode:        float32(pcs.ReadU16(RegPCSGridMode)),
	}, true
}

func (s *iec61850Server) updateMMS(values iec61850TelemetryValues) {
	s.updateFloat(s.activePowerSetpoint, values.activeSetpointKW, values.nowMs)
	s.updateFloat(s.reactiveSetpoint, values.reactSetpointKVAr, values.nowMs)
	s.updateFloat(s.pcsCommand, 0, values.nowMs)
	s.updateFloat(s.pcsRunMode, values.pcsRunMode, values.nowMs)

	s.updateFloat(s.ratedPower, values.ratedPowerKW, values.nowMs)
	s.updateFloat(s.soc, values.socPercent, values.nowMs)
	s.server.UpdateInt32AttributeValue(s.pcsStatus, values.pcsStatus)
	if t := s.pcsStatusTimeNode(); t != nil {
		s.server.UpdateUTCTimeAttributeValue(t, values.nowMs)
	}
	s.updateFloat(s.activePower, values.activeKW, values.nowMs)
	s.updateFloat(s.reactivePower, values.reactiveKVAr, values.nowMs)
	s.updateFloat(s.maxChargePower, values.maxChargeKW, values.nowMs)
	s.updateFloat(s.maxDischargePower, values.maxDischargeKW, values.nowMs)
	s.updateFloat(s.activeSetReadback, values.activeSetpointKW, values.nowMs)
	s.updateFloat(s.reactSetReadback, values.reactSetpointKVAr, values.nowMs)
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
	publisher.SetGoCbRef("TEMPLATEPIGO/LLN0$GO$gocb1")
	publisher.SetGoID("TEMPLATEPIGO/LLN0$GO$gocb1")
	publisher.SetDataSetRef("TEMPLATEPIGO/LLN0$dsGOOSE1")
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

func (s *iec61850Server) updateFloat(node *iec61850.ModelNode, value float32, nowMs int64) {
	s.server.UpdateFloatAttributeValue(node, value)
	if t := siblingTimeNode(s.model, node.ObjectReference); t != nil {
		s.server.UpdateUTCTimeAttributeValue(t, nowMs)
	}
}

func (s *iec61850Server) pcsStatusTimeNode() *iec61850.ModelNode {
	return s.model.GetModelNodeByObjectReference("TEMPLATEPIGO/GGIO1.AnIn3.t")
}

func siblingTimeNode(model *iec61850.IedModel, ref string) *iec61850.ModelNode {
	for _, suffix := range []string{".mag.f", ".mag.i"} {
		if len(ref) > len(suffix) && ref[len(ref)-len(suffix):] == suffix {
			return model.GetModelNodeByObjectReference(ref[:len(ref)-len(suffix)] + ".t")
		}
	}
	return nil
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

func mmsFloat32(value *iec61850.MmsValue) (float32, bool) {
	if value == nil {
		return 0, false
	}
	switch v := value.Value.(type) {
	case float32:
		return v, true
	case int64:
		return float32(v), true
	case uint32:
		return float32(v), true
	default:
		return 0, false
	}
}

func roundedMMSInt(value *iec61850.MmsValue) (int, bool) {
	f, ok := mmsFloat32(value)
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
