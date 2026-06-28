package simulator

import "virtual_bess/internal/mbserver"

// System-level status registers (read-only)
const (
	RegSysRunning        = 1   // 0-none, 1-running
	RegSysFault          = 2   // 0-none, 1-other fault, 2-charge fault, 3-discharge fault
	RegSysStandby        = 3   // 0-none, 1-standby
	RegEMUBMSComm        = 4   // 0-offline, 1-online
	RegEMUPCSComm        = 5   // 0-offline, 1-online
	RegSysRunMode        = 100 // 0-local manual, 1-local auto, 2-remote passive
	RegSysMaxChargePW    = 101 // U16, 0.1 kW, synced with RegBMSMaxChargePW
	RegSysMaxDischargePW = 102 // U16, 0.1 kW, synced with RegBMSMaxDischargePW
	RegSysActualPower    = 103 // U16, 0.1 kW, current actual total power
	RegBMSMasterMode     = 104 // 1-master mode
	RegBMSClusterCount   = 105 // number of BMS clusters

	RegMaxChargePWSetting    = 700 // U16, 0.1 kW, read/write, max allowed charge power override
	RegMaxDischargePWSetting = 701 // U16, 0.1 kW, read/write, max allowed discharge power override
)

// PCS control registers (writable)
const (
	RegPCSGridMode      = 30000 // 0-grid-tied, 1-off-grid
	RegPCSRunMode       = 30001 // 2-constant power
	RegPCSFaultReset    = 30002 // 1-reset
	RegPCSStartup       = 30003 // 1-start
	RegPCSShutdown      = 30004 // 1-shutdown
	RegPCSEStop         = 30005 // 1-emergency stop
	RegPCSRemoteLocal   = 30006 // 0-local, 1-remote
	RegPCSPowerCmdAlias = 3010  // S16, 0.1kW, alias of RegPCSPowerCmd（真机约定：负充正放）
	RegPCSPowerCmd      = 30010 // S16, 0.1kW, negative=charge, positive=discharge（对齐真机 IES1000/IES900）
)

// PV control registers (writable)
const (
	RegPVStartup      = 60000 // 1-start
	RegPVShutdown     = 60001 // 1-shutdown
	RegPVPercentLimit = 60002 // U16, 0.1%, 1000=100.0%
	RegPVFixedLimit   = 60003 // U16, 0.1 kW
)

// PV status registers (read-only)
const (
	RegPVRunStatus          = 60100 // 1-stopped, 2-standby, 5-running
	RegPVTotalEnergyHi      = 60140 // U32 high word, 1 kWh
	RegPVTotalEnergyLo      = 60141
	RegPVDailyEnergyHi      = 60142
	RegPVDailyEnergyLo      = 60143
	RegPVMonthlyEnergyHi    = 60144
	RegPVMonthlyEnergyLo    = 60145
	RegPVYearlyEnergyHi     = 60146
	RegPVYearlyEnergyLo     = 60147
	RegPVRatedPower         = 60148 // U16, 0.1 kW
	RegPVFaultAlarm         = 60149 // U16
	RegPVACVoltageA         = 60150 // U16, 0.1 V
	RegPVACVoltageB         = 60151
	RegPVACVoltageC         = 60152
	RegPVACCurrentA         = 60153 // S16, 0.1 A
	RegPVACCurrentB         = 60154
	RegPVACCurrentC         = 60155
	RegPVGridFrequency      = 60156 // U16, 0.01 Hz
	RegPVPowerFactor        = 60157 // S16, 0.001
	RegPVACActivePower      = 60158 // S16, 0.1 kW
	RegPVACReactivePower    = 60159 // S16, 0.1 kW
	RegPVInverterEfficiency = 60160 // U16, 0.1%
	RegPVDailyPeakPower     = 60161 // S16, 0.1 kW
	RegPVApparentPower      = 60162 // U16, 0.1 kVA

	RegPVDCInputPower = 60280 // S16, 0.1 kW
	RegPVInternalTemp = 60281 // S16, 0.1 °C
	RegPVDCVoltage    = 60282 // U16, 0.1 V
	RegPVDCCurrent    = 60283 // S16, 0.1 A
)

// PCS status registers (read-only)
const (
	RegPCSRemoteStatus = 30049 // 0-local, 1-remote
	RegPCSSysStatus    = 30050 // 1-stopped, 2-standby, 3-charging, 4-discharging
	RegPCSGridStatus   = 30051 // 0-grid-tied, 1-off-grid
	RegPCSAlarmStatus  = 30052 // 0-normal, 1-alarm
	RegPCSFaultStatus  = 30053 // 0-normal, 1-fault

	RegPCSPowerFactor   = 30060 // S16, 0.01
	RegPCSTotalActivePW = 30061 // S16, 0.1 kW
	RegPCSTotalReactPW  = 30062 // S16, 0.1 kVAr
	RegPCSTotalApparent = 30063 // U16, 0.1 kVA
	RegPCSActivePWA     = 30064 // S16, 0.1 kW
	RegPCSActivePWB     = 30065
	RegPCSActivePWC     = 30066
	RegPCSReactPWA      = 30067 // S16, 0.1 kVAr
	RegPCSReactPWB      = 30068
	RegPCSReactPWC      = 30069
	RegPCSVoltageA      = 30070 // U16, 0.1 V
	RegPCSVoltageB      = 30071
	RegPCSVoltageC      = 30072
	RegPCSCurrentA      = 30073 // S16, 0.1 A
	RegPCSCurrentB      = 30074
	RegPCSCurrentC      = 30075
	RegPCSFrequency     = 30076 // U16, 0.01 Hz
	RegPCSDCVoltage     = 30077 // S16, 0.1 V
	RegPCSDCCurrent     = 30078 // S16, 0.1 A
	RegPCSDCPower       = 30079 // S16, 0.1 kW
	RegPCSInternalTemp  = 30080 // S16, 0.1 °C
	RegPCSIGBTTempA     = 30081
	RegPCSIGBTTempB     = 30082
	RegPCSIGBTTempC     = 30083
)

// PCS fault registers (read-only)
const (
	RegPCSDCUnderVolt = 30180 // 1-DC undervoltage fault (BMS not closed)
)

// BMS control registers (writable)
const (
	RegBMSFaultReset = 40000 // 1-reset
	RegBMSCloseHV    = 40001 // 1-close contactor (energize)
	RegBMSOpenHV     = 40002 // 1-open contactor (de-energize)
)

// BMS status registers (read-only)
const (
	RegBMSFaultStatus     = 40100 // 0-normal, 1-fault
	RegBMSAlarmStatus     = 40101 // 0-normal, 1-alarm
	RegBMSSysStatus       = 40102 // 0-starting, 1-standby, 2-stopped, 3-charging, 4-discharging
	RegBMSChargeForbid    = 40103 // 0-normal, 1-forbidden
	RegBMSDischargeForbid = 40104 // 0-normal, 1-forbidden
	RegBMSSOC             = 40105 // U16, 0.1 %
	RegBMSSOH             = 40106 // U16, 0.1 %
	RegBMSRemainCharge    = 40107 // U16, 0.1 kWh
	RegBMSRemainDischarge = 40108 // U16, 0.1 kWh
	RegBMSVoltage         = 40109 // U16, 0.1 V
	RegBMSCurrent         = 40110 // S16, 0.1 A
	RegBMSPower           = 40111 // S16, 0.1 kW
	RegBMSMaxChargePW     = 40120 // U16, 0.1 kW
	RegBMSMaxDischargePW  = 40121 // U16, 0.1 kW
	RegBMSMaxChargeI      = 40122 // U16, 0.1 A
	RegBMSMaxDischargeI   = 40123 // U16, 0.1 A

	RegBMSCellVMax    = 40124 // U16, 0.001 V, max single-cell voltage
	RegBMSCellVMaxIdx = 40125 // U16, max-voltage cell index
	RegBMSCellVMin    = 40126 // U16, 0.001 V, min single-cell voltage
	RegBMSCellVMinIdx = 40127 // U16, min-voltage cell index
	RegBMSCellVAvg    = 40128 // U16, 0.001 V, average single-cell voltage
	RegBMSCellTMax    = 40129 // S16, 0.1 °C, max single-cell temperature
	RegBMSCellTMaxIdx = 40130 // U16, max-temperature cell index
	RegBMSCellTMin    = 40131 // S16, 0.1 °C, min single-cell temperature
	RegBMSCellTMinIdx = 40132 // U16, min-temperature cell index
	RegBMSCellTAvg    = 40133 // S16, 0.1 °C, average single-cell temperature
	RegBMSCellVSpread = 40134 // U16, 0.001 V, cell voltage spread (max - min)
	RegBMSCellTSpread = 40135 // U16, 0.1 °C, cell temperature spread (max - min)
)

// Cluster Input Register layout: each cluster occupies a block with stride 1600.
// Cluster N starts at N*1600, data offsets 1~32 within each block.
const (
	IRClusterStride = 1600

	OffClusterStatus          = 1  // 0-offline,1-standby,2-stopped,3-charging,4-discharging,5-running,6-fault
	OffClusterSOC             = 2  // U16, 0.1 %
	OffClusterSOH             = 3  // U16, 0.1 %
	OffClusterRemainCharge    = 4  // U16, 0.1 kWh
	OffClusterRemainDischarge = 5  // U16, 0.1 kWh
	OffClusterVoltage         = 6  // U16, 0.1 V
	OffClusterCurrent         = 7  // S16, 0.1 A
	OffClusterPower           = 8  // S16, 0.1 kW
	OffClusterTotalChargeHi   = 9  // U32 high word, 0.1 kWh
	OffClusterTotalChargeLo   = 10 // U32 low word
	OffClusterTotalDischHi    = 11 // U32 high word, 0.1 kWh
	OffClusterTotalDischLo    = 12 // U32 low word
	OffClusterSessChargeHi    = 13 // U32 high word, 0.1 kWh (session)
	OffClusterSessChargeLo    = 14 // U32 low word
	OffClusterSessDischHi     = 15 // U32 high word, 0.1 kWh (session)
	OffClusterSessDischLo     = 16 // U32 low word
	OffClusterMaxChargePW     = 17 // U16, 0.1 kW
	OffClusterMaxDischargePW  = 18 // U16, 0.1 kW
	OffClusterMaxChargeI      = 19 // U16, 0.1 A
	OffClusterMaxDischargeI   = 20 // U16, 0.1 A
	OffClusterCellVMax        = 21 // U16, 0.001 V, max single-cell voltage
	OffClusterCellVMaxIdx     = 22 // U16, max-voltage cell index
	OffClusterCellVMin        = 23 // U16, 0.001 V, min single-cell voltage
	OffClusterCellVMinIdx     = 24 // U16, min-voltage cell index
	OffClusterCellVAvg        = 25 // U16, 0.001 V, average single-cell voltage
	OffClusterCellTMax        = 26 // S16, 0.1 °C, max single-cell temperature
	OffClusterCellTMaxIdx     = 27 // U16, max-temperature cell index
	OffClusterCellTMin        = 28 // S16, 0.1 °C, min single-cell temperature
	OffClusterCellTMinIdx     = 29 // U16, min-temperature cell index
	OffClusterCellTAvg        = 30 // S16, 0.1 °C, average single-cell temperature
	OffClusterCellVSpread     = 31 // U16, 0.001 V, cell voltage spread (max - min)
	OffClusterCellTSpread     = 32 // U16, 0.1 °C, cell temperature spread (max - min)
)

// Meter status registers (read-only, point of common coupling)
const (
	RegMeterCombinedEnergyHi  = 10010 // S32 hi, 0.01 kWh — combined active energy (forward+reverse)
	RegMeterCombinedEnergyLo  = 10011
	RegMeterForwardEnergyHi   = 10012 // S32 hi, 0.01 kWh — import from grid
	RegMeterForwardEnergyLo   = 10013
	RegMeterReverseEnergyHi   = 10014 // S32 hi, 0.01 kWh — export to grid
	RegMeterReverseEnergyLo   = 10015
	RegMeterVoltageA          = 10016 // U16, 0.1 V
	RegMeterVoltageB          = 10017
	RegMeterVoltageC          = 10018
	RegMeterCurrentAHi        = 10019 // S32 hi, 0.1 A
	RegMeterCurrentALo        = 10020
	RegMeterCurrentBHi        = 10021
	RegMeterCurrentBLo        = 10022
	RegMeterCurrentCHi        = 10023
	RegMeterCurrentCLo        = 10024
	RegMeterActivePWTotalHi   = 10025 // S32 hi, 0.001 kW
	RegMeterActivePWTotalLo   = 10026
	RegMeterActivePWAHi       = 10027
	RegMeterActivePWALo       = 10028
	RegMeterActivePWBHi       = 10029
	RegMeterActivePWBLo       = 10030
	RegMeterActivePWCHi       = 10031
	RegMeterActivePWCLo       = 10032
	RegMeterReactivePWTotalHi = 10033 // S32 hi, 0.001 kVar
	RegMeterReactivePWTotalLo = 10034
	RegMeterReactivePWAHi     = 10035
	RegMeterReactivePWALo     = 10036
	RegMeterReactivePWBHi     = 10037
	RegMeterReactivePWBLo     = 10038
	RegMeterReactivePWCHi     = 10039
	RegMeterReactivePWCLo     = 10040
	RegMeterApparentPWTotalHi = 10041 // S32 hi, 0.001 kVA
	RegMeterApparentPWTotalLo = 10042
	RegMeterApparentPWAHi     = 10043
	RegMeterApparentPWALo     = 10044
	RegMeterApparentPWBHi     = 10045
	RegMeterApparentPWBLo     = 10046
	RegMeterApparentPWCHi     = 10047
	RegMeterApparentPWCLo     = 10048
	RegMeterPFTotalHi         = 10049 // S32 hi, 0.001
	RegMeterPFTotalLo         = 10050
	RegMeterPFAHi             = 10051
	RegMeterPFALo             = 10052
	RegMeterPFBHi             = 10053
	RegMeterPFBLo             = 10054
	RegMeterPFCHi             = 10055
	RegMeterPFCLo             = 10056
	RegMeterFrequency         = 10057 // U16, 0.01 Hz
)

// clusterIR returns the absolute Input Register address for a given cluster index and offset.
func clusterIR(clusterIdx int, offset uint16) uint16 {
	return uint16(clusterIdx*IRClusterStride) + offset
}

// uint32ToRegs splits a uint32 into high and low uint16 words (big-endian).
func uint32ToRegs(v uint32) (hi, lo uint16) {
	return uint16(v >> 16), uint16(v & 0xFFFF)
}

// int16ToUint16 converts a signed int16 value to uint16 for register storage.
func int16ToUint16(v int16) uint16 {
	return uint16(v)
}

// uint16ToInt16 converts a uint16 register value back to signed int16.
func uint16ToInt16(v uint16) int16 {
	return int16(v)
}

// writeS32Holding writes a signed 32-bit value to two consecutive holding registers (big-endian word order).
func writeS32Holding(s *mbserver.Server, highReg uint16, value int32) {
	u := uint32(value)
	s.HoldingRegisters[highReg] = uint16(u >> 16)
	s.HoldingRegisters[highReg+1] = uint16(u & 0xFFFF)
}
