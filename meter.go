package main

import (
	"math"
	"math/rand"
)

// loadPowerFactor is the assumed power factor of the facility load (inductive).
// PV inverter and BESS PCS are assumed to operate at unity PF, so the only source
// of reactive power at the PCC is the load itself.
const loadPowerFactor = 0.95

// Meter 是 PCC（公共连接点）的电表，唯一一台。
// 它聚合所有 PCS / PV 的功率与单台负载，得到入网功率和能量。
type Meter struct {
	bank *SlaveBank

	gridVoltage float64

	// 当前 tick 输入
	gridPowerKW   float64
	loadPowerKW   float64

	// 累计能量
	forwardKWh float64
	reverseKWh float64
}

func NewMeter(cfg MeterConfig, gridVoltage float64, bank *SlaveBank) *Meter {
	_ = cfg
	return &Meter{
		bank:        bank,
		gridVoltage: gridVoltage,
	}
}

// Update 根据 load / ΣPCS / ΣPV 重新计算电表功率并累计能量。
// 公约：gridPowerKW > 0 = 从电网买电；< 0 = 向电网卖电。
//   PCS actualPowerKW: 正充负放（充电时从电网取电）
//   PV  actualPowerKW: 永远 >= 0（注入）
//   Load actualPowerKW: 永远 >= 0（消耗）
func (m *Meter) Update(dtSeconds, loadPowerKW, totalPCSKW, totalPVKW float64) {
	m.loadPowerKW = loadPowerKW
	m.gridPowerKW = loadPowerKW + totalPCSKW - totalPVKW

	if dtSeconds <= 0 {
		return
	}
	deltaKWh := m.gridPowerKW * dtSeconds / 3600.0
	if deltaKWh > 0 {
		m.forwardKWh += deltaKWh
	} else {
		m.reverseKWh += -deltaKWh
	}
}

func (m *Meter) Sync() {
	gridPowerKW := m.gridPowerKW

	// 无功只来自负载（PV/PCS 在 unity PF）；负载是感性的，Q >= 0
	tanPhi := math.Sqrt(1-loadPowerFactor*loadPowerFactor) / loadPowerFactor
	reactiveKVar := m.loadPowerKW * tanPhi

	apparentKVA := math.Sqrt(gridPowerKW*gridPowerKW + reactiveKVar*reactiveKVar)

	// PF 幅值 = |P|/S；Q ≥ 0 → PF 符号为正
	pfMag := 1.0
	if apparentKVA > 0 {
		pfMag = math.Abs(gridPowerKW) / apparentKVA
	}

	// 能量寄存器（S32, 0.01 kWh → ×100）
	combinedKWh := m.forwardKWh + m.reverseKWh
	m.bank.WriteS32(RegMeterCombinedEnergyHi, int32(combinedKWh*100))
	m.bank.WriteS32(RegMeterForwardEnergyHi, int32(m.forwardKWh*100))
	m.bank.WriteS32(RegMeterReverseEnergyHi, int32(m.reverseKWh*100))

	// 三相电压 (U16, 0.1 V) ±0.5% jitter
	phaseVoltages := [3]float64{}
	phaseVoltRegs := [3]uint16{RegMeterVoltageA, RegMeterVoltageB, RegMeterVoltageC}
	for i, reg := range phaseVoltRegs {
		jitter := 1.0 + (rand.Float64()*0.01 - 0.005)
		v := m.gridVoltage * jitter
		phaseVoltages[i] = v
		m.bank.WriteU16(reg, uint16(v*10))
	}

	// 三相电流 (S32, 0.1 A)：幅值跟视在功率，符号跟有功方向
	phasePowerKW := gridPowerKW / 3.0
	phaseApparentKVA := apparentKVA / 3.0
	phaseCurrentHi := [3]uint16{RegMeterCurrentAHi, RegMeterCurrentBHi, RegMeterCurrentCHi}
	for i, hiReg := range phaseCurrentHi {
		currentA := 0.0
		if phaseVoltages[i] > 0 {
			mag := phaseApparentKVA * 1000.0 / phaseVoltages[i]
			if phasePowerKW < 0 {
				currentA = -mag
			} else {
				currentA = mag
			}
		}
		m.bank.WriteS32(hiReg, int32(currentA*10))
	}

	// 有功 (S32, 0.001 kW)
	m.bank.WriteS32(RegMeterActivePWTotalHi, int32(gridPowerKW*1000))
	phasePW1000 := int32(phasePowerKW * 1000)
	m.bank.WriteS32(RegMeterActivePWAHi, phasePW1000)
	m.bank.WriteS32(RegMeterActivePWBHi, phasePW1000)
	m.bank.WriteS32(RegMeterActivePWCHi, phasePW1000)

	// 无功 (S32, 0.001 kVar)
	m.bank.WriteS32(RegMeterReactivePWTotalHi, int32(reactiveKVar*1000))
	phaseReact1000 := int32(reactiveKVar / 3.0 * 1000)
	m.bank.WriteS32(RegMeterReactivePWAHi, phaseReact1000)
	m.bank.WriteS32(RegMeterReactivePWBHi, phaseReact1000)
	m.bank.WriteS32(RegMeterReactivePWCHi, phaseReact1000)

	// 视在 (S32, 0.001 kVA)
	m.bank.WriteS32(RegMeterApparentPWTotalHi, int32(apparentKVA*1000))
	phaseAppar1000 := int32(phaseApparentKVA * 1000)
	m.bank.WriteS32(RegMeterApparentPWAHi, phaseAppar1000)
	m.bank.WriteS32(RegMeterApparentPWBHi, phaseAppar1000)
	m.bank.WriteS32(RegMeterApparentPWCHi, phaseAppar1000)

	// PF (S32, 0.001)；非负
	pf1000 := int32(pfMag * 1000)
	m.bank.WriteS32(RegMeterPFTotalHi, pf1000)
	m.bank.WriteS32(RegMeterPFAHi, pf1000)
	m.bank.WriteS32(RegMeterPFBHi, pf1000)
	m.bank.WriteS32(RegMeterPFCHi, pf1000)

	// 频率 50 ± 0.05 Hz
	freqHz := 50.0 + (rand.Float64()*2-1)*0.05
	m.bank.WriteU16(RegMeterFrequency, uint16(freqHz*100))
}
