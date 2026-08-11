package xn3477

import "virtual_bess/internal/simulator"

const (
	xnBAUFaultFlags          = 0x1001
	xnBAUMainStatus          = 0x1031
	xnBAUWorkStatus          = 0x1032
	xnBAUClusterCutInFail    = 0x1036
	xnBAUClusterGridFail     = 0x1076
	xnBAUVoltage             = 0x2001
	xnBAUCurrent             = 0x2002
	xnBAUSOC                 = 0x2004
	xnBAUSOH                 = 0x2005
	xnBAURemainingCharge     = 0x2007
	xnBAURemainingDischarge  = 0x2008
	xnBAUMaxChargeCurrent    = 0x2009
	xnBAUMaxDischargeCurrent = 0x200A
	xnBAUMaxCellVoltageRack  = 0x200D
	xnBAUMaxCellVoltagePack  = 0x200E
	xnBAUMaxCellVoltageNo    = 0x200F
	xnBAUMaxCellVoltage      = 0x2010
	xnBAUMinCellVoltageRack  = 0x2011
	xnBAUMinCellVoltagePack  = 0x2012
	xnBAUMinCellVoltageNo    = 0x2013
	xnBAUMinCellVoltage      = 0x2014
	xnBAUAvgCellVoltage      = 0x2015
	xnBAUMaxCellTempRack     = 0x2016
	xnBAUMaxCellTempPack     = 0x2017
	xnBAUMaxCellTempNo       = 0x2018
	xnBAUMaxCellTemp         = 0x2019
	xnBAUMinCellTempRack     = 0x201A
	xnBAUMinCellTempPack     = 0x201B
	xnBAUMinCellTempNo       = 0x201C
	xnBAUMinCellTemp         = 0x201D
	xnBAUAvgCellTemp         = 0x201E
	xnBAUTotalCharge         = 0x201F
	xnBAUTotalDischarge      = 0x2021
	xnBAUMaxDischargePower   = 0x2026
	xnBAUMaxChargePower      = 0x2027
	xnBAUDailyDischarge      = 0x2028
	xnBAUDailyCharge         = 0x2029

	xnBAUHVControl          = 0x3001
	xnBAUClusterEnableStart = 0x3005
	xnBAUFaultReset         = 0x3047

	xnBCUFaultFlags          = 0x0012
	xnBCUWarningFlags        = 0x0013
	xnBCUMainStatus          = 0x0019
	xnBCUWorkStatus          = 0x001D
	xnBCUMaxCellVoltage      = 0x0210
	xnBCUMaxCellVoltageNo    = 0x0211
	xnBCUMaxCellVoltagePack  = 0x0212
	xnBCUMinCellVoltage      = 0x0214
	xnBCUMinCellVoltageNo    = 0x0215
	xnBCUMinCellVoltagePack  = 0x0216
	xnBCUCellVoltageDelta    = 0x0218
	xnBCUAvgCellVoltage      = 0x0219
	xnBCUMaxCellTemp         = 0x021A
	xnBCUMaxCellTempNo       = 0x021B
	xnBCUMaxCellTempPack     = 0x021C
	xnBCUMinCellTemp         = 0x021E
	xnBCUMinCellTempNo       = 0x021F
	xnBCUMinCellTempPack     = 0x0220
	xnBCUAvgCellTemp         = 0x0222
	xnBCUMaxChargeCurrent    = 0x0225
	xnBCUMaxDischargeCurrent = 0x0226
	xnBCUSOC                 = 0x0227
	xnBCUSOH                 = 0x0228
	xnBCUCellTempDelta       = 0x0229
	xnBCUVoltage             = 0x0231
	xnBCUCurrent             = 0x0232
	xnBCUTotalCharge         = 0x024C
	xnBCUTotalDischarge      = 0x024E
	xnBCUFaultReset          = 0x0403
	xnBCUCellVoltageStart    = 0x1401
	xnBCUCellTempStart       = 0x1801
	xnBCUCellCount           = 512
)

// 运行状态 0x1031（BAU）/ 0x0019（BCU）枚举。
const (
	xnRunNormal             = 0
	xnRunChargeForbidden    = 1
	xnRunDischargeForbidden = 2
	xnRunStandby            = 3 // 协议「待机」，本项目语义为下高压
	xnRunStopped            = 4
)

// 充放电状态 0x1032（BAU）/ 0x001D（BCU）枚举。
const (
	xnChgDsgRest      = 0
	xnChgDsgDischarge = 1
	xnChgDsgCharge    = 2
)

func (ep *endpoint) sync() {
	ep.syncBAU()
	for clusterIndex, bank := range ep.clusters {
		ep.syncBCU(clusterIndex, bank)
	}
}

func (ep *endpoint) syncBAU() {
	source := ep.battery.BMSBank()
	dest := ep.bau

	dest.WriteU16(xnBAUFaultFlags, source.ReadU16(simulator.RegBMSFaultStatus))
	mainStatus, workStatus := xnStatus(
		source.ReadU16(simulator.RegBMSSysStatus),
		source.ReadU16(simulator.RegBMSChargeForbid),
		source.ReadU16(simulator.RegBMSDischargeForbid),
	)
	dest.WriteU16(xnBAUMainStatus, mainStatus)
	dest.WriteU16(xnBAUWorkStatus, workStatus)

	copyHoldingU16(dest, xnBAUVoltage, source, simulator.RegBMSVoltage)
	dest.WriteS32(
		xnBAUCurrent,
		int32(int16(source.ReadU16(simulator.RegBMSCurrent))),
	)
	copyHoldingU16(dest, xnBAUSOC, source, simulator.RegBMSSOC)
	copyHoldingU16(dest, xnBAUSOH, source, simulator.RegBMSSOH)
	dest.WriteU16(
		xnBAURemainingCharge,
		source.ReadU16(simulator.RegBMSRemainCharge)/10,
	)
	dest.WriteU16(
		xnBAURemainingDischarge,
		source.ReadU16(simulator.RegBMSRemainDischarge)/10,
	)
	copyHoldingU16(dest, xnBAUMaxChargeCurrent, source, simulator.RegBMSMaxChargeI)
	copyHoldingU16(dest, xnBAUMaxDischargeCurrent, source, simulator.RegBMSMaxDischargeI)

	dest.WriteU16(xnBAUMaxCellVoltageRack, 1)
	dest.WriteU16(xnBAUMaxCellVoltagePack, 1)
	copyHoldingU16(dest, xnBAUMaxCellVoltageNo, source, simulator.RegBMSCellVMaxIdx)
	copyHoldingU16(dest, xnBAUMaxCellVoltage, source, simulator.RegBMSCellVMax)
	dest.WriteU16(xnBAUMinCellVoltageRack, 1)
	dest.WriteU16(xnBAUMinCellVoltagePack, 1)
	copyHoldingU16(dest, xnBAUMinCellVoltageNo, source, simulator.RegBMSCellVMinIdx)
	copyHoldingU16(dest, xnBAUMinCellVoltage, source, simulator.RegBMSCellVMin)
	copyHoldingU16(dest, xnBAUAvgCellVoltage, source, simulator.RegBMSCellVAvg)

	dest.WriteU16(xnBAUMaxCellTempRack, 1)
	dest.WriteU16(xnBAUMaxCellTempPack, 1)
	copyHoldingU16(dest, xnBAUMaxCellTempNo, source, simulator.RegBMSCellTMaxIdx)
	copyHoldingU16(dest, xnBAUMaxCellTemp, source, simulator.RegBMSCellTMax)
	dest.WriteU16(xnBAUMinCellTempRack, 1)
	dest.WriteU16(xnBAUMinCellTempPack, 1)
	copyHoldingU16(dest, xnBAUMinCellTempNo, source, simulator.RegBMSCellTMinIdx)
	copyHoldingU16(dest, xnBAUMinCellTemp, source, simulator.RegBMSCellTMin)
	copyHoldingU16(dest, xnBAUAvgCellTemp, source, simulator.RegBMSCellTAvg)

	dest.WriteU32(xnBAUTotalCharge, source.ReadU32(simulator.RegBMSTotalCharge)/10)
	dest.WriteU32(xnBAUTotalDischarge, source.ReadU32(simulator.RegBMSTotalDischarge)/10)
	dest.WriteU16(xnBAUDailyCharge, 0)
	dest.WriteU16(xnBAUDailyDischarge, 0)
	dest.WriteU16(
		xnBAUMaxChargePower,
		source.ReadU16(simulator.RegBMSMaxChargePW)/10,
	)
	dest.WriteU16(
		xnBAUMaxDischargePower,
		source.ReadU16(simulator.RegBMSMaxDischargePW)/10,
	)
}

func (ep *endpoint) syncBCU(clusterIndex int, dest *simulator.SlaveBank) {
	source := ep.battery.BMSBank()
	offset := uint16(clusterIndex * simulator.IRClusterStride)
	read := func(register uint16) uint16 {
		return source.ReadInputU16(offset + register)
	}
	read32 := func(register uint16) uint32 {
		return source.ReadInputU32(offset + register)
	}

	status := read(simulator.OffClusterStatus)
	mainStatus, workStatus := xnClusterStatus(
		status,
		source.ReadU16(simulator.RegBMSChargeForbid),
		source.ReadU16(simulator.RegBMSDischargeForbid),
	)
	if status == simulator.ClusterStatusFault {
		dest.WriteU16(xnBCUFaultFlags, 1)
	} else {
		dest.WriteU16(xnBCUFaultFlags, 0)
	}
	dest.WriteU16(xnBCUWarningFlags, 0)
	dest.WriteU16(xnBCUMainStatus, mainStatus)
	dest.WriteU16(xnBCUWorkStatus, workStatus)

	dest.WriteU16(xnBCUSOC, read(simulator.OffClusterSOC))
	dest.WriteU16(xnBCUSOH, read(simulator.OffClusterSOH))
	dest.WriteU16(xnBCUVoltage, read(simulator.OffClusterVoltage))
	dest.WriteU16(xnBCUCurrent, read(simulator.OffClusterCurrent))
	dest.WriteU32(xnBCUTotalCharge, read32(simulator.OffClusterTotalChargeHi)/10)
	dest.WriteU32(xnBCUTotalDischarge, read32(simulator.OffClusterTotalDischHi)/10)
	dest.WriteU16(xnBCUMaxChargeCurrent, read(simulator.OffClusterMaxChargeI))
	dest.WriteU16(xnBCUMaxDischargeCurrent, read(simulator.OffClusterMaxDischargeI))

	dest.WriteU16(xnBCUMaxCellVoltage, read(simulator.OffClusterCellVMax))
	dest.WriteU16(xnBCUMaxCellVoltageNo, read(simulator.OffClusterCellVMaxIdx))
	dest.WriteU16(xnBCUMaxCellVoltagePack, 1)
	dest.WriteU16(xnBCUMinCellVoltage, read(simulator.OffClusterCellVMin))
	dest.WriteU16(xnBCUMinCellVoltageNo, read(simulator.OffClusterCellVMinIdx))
	dest.WriteU16(xnBCUMinCellVoltagePack, 1)
	dest.WriteU16(xnBCUCellVoltageDelta, read(simulator.OffClusterCellVSpread))
	dest.WriteU16(xnBCUAvgCellVoltage, read(simulator.OffClusterCellVAvg))

	dest.WriteU16(xnBCUMaxCellTemp, read(simulator.OffClusterCellTMax))
	dest.WriteU16(xnBCUMaxCellTempNo, read(simulator.OffClusterCellTMaxIdx))
	dest.WriteU16(xnBCUMaxCellTempPack, 1)
	dest.WriteU16(xnBCUMinCellTemp, read(simulator.OffClusterCellTMin))
	dest.WriteU16(xnBCUMinCellTempNo, read(simulator.OffClusterCellTMinIdx))
	dest.WriteU16(xnBCUMinCellTempPack, 1)
	dest.WriteU16(xnBCUAvgCellTemp, read(simulator.OffClusterCellTAvg))
	dest.WriteU16(xnBCUCellTempDelta, read(simulator.OffClusterCellTSpread))

	cellVoltages := make([]uint16, xnBCUCellCount)
	cellTemps := make([]uint16, xnBCUCellCount)
	for i := range cellVoltages {
		cellVoltages[i] = read(simulator.OffClusterCellVAvg)
		cellTemps[i] = read(simulator.OffClusterCellTAvg)
	}
	dest.Holding.UpdateUint16Data(xnBCUCellVoltageStart, cellVoltages...)
	dest.Holding.UpdateUint16Data(xnBCUCellTempStart, cellTemps...)
}

func copyHoldingU16(
	dest *simulator.SlaveBank,
	destAddress uint16,
	source *simulator.SlaveBank,
	sourceAddress uint16,
) {
	dest.WriteU16(destAddress, source.ReadU16(sourceAddress))
}

// xnStatus 把内部 BMS 系统状态映射到 XN3477 的运行状态 0x1031 + 充放电状态 0x1032。
//
// 关键约束：协议里「正常」和「待机」不是同义词。0x1031=0 表示高压已闭合、系统在线，
// 0x1031=3/4 表示高压未闭合；现场点表也据此把 3、4 都读成「下高压/停机」。所以静置
// （高压闭合但功率为 0）必须报 0，不能报 3，否则 EMS 侧会一直看到下高压，上高压指令
// 看起来永远不生效。
// 禁充/禁放同样挤在 0x1031 这一个枚举里，只在高压闭合时才有意义。
func xnStatus(status, chargeForbidden, dischargeForbidden uint16) (main, work uint16) {
	switch status {
	case simulator.BMSStatusStandby:
		return xnOnlineMain(chargeForbidden, dischargeForbidden), xnChgDsgRest
	case simulator.BMSStatusCharging:
		return xnOnlineMain(chargeForbidden, dischargeForbidden), xnChgDsgCharge
	case simulator.BMSStatusDischarging:
		return xnOnlineMain(chargeForbidden, dischargeForbidden), xnChgDsgDischarge
	default:
		// 启动中和停机都视为高压未闭合
		return xnRunStandby, xnChgDsgRest
	}
}

// xnClusterStatus 映射簇状态到 BCU 的 0x0019 + 0x001D，枚举与 BAU 侧一致。
// 仿真器没有簇级禁充禁放，沿用整堆标志，保证簇与堆的上报方向一致。
func xnClusterStatus(status, chargeForbidden, dischargeForbidden uint16) (main, work uint16) {
	switch status {
	case simulator.ClusterStatusStandby, simulator.ClusterStatusRunning:
		return xnOnlineMain(chargeForbidden, dischargeForbidden), xnChgDsgRest
	case simulator.ClusterStatusCharging:
		return xnOnlineMain(chargeForbidden, dischargeForbidden), xnChgDsgCharge
	case simulator.ClusterStatusDischarging:
		return xnOnlineMain(chargeForbidden, dischargeForbidden), xnChgDsgDischarge
	case simulator.ClusterStatusStopped:
		return xnRunStandby, xnChgDsgRest
	default:
		// 离线和故障簇按停机上报
		return xnRunStopped, xnChgDsgRest
	}
}

// xnOnlineMain 返回高压已闭合时的运行状态：优先上报禁充/禁放，否则为正常。
func xnOnlineMain(chargeForbidden, dischargeForbidden uint16) uint16 {
	switch {
	case chargeForbidden == 1:
		return xnRunChargeForbidden
	case dischargeForbidden == 1:
		return xnRunDischargeForbidden
	default:
		return xnRunNormal
	}
}
