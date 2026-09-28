package simulator

import (
	"math"
	"math/rand"
)

// loadPowerFactor is the assumed power factor of the facility load (inductive).
// PV inverter and BESS PCS are assumed to operate at unity PF, so the only source
// of reactive power at the PCC is the load itself.
const loadPowerFactor = 0.95

// Meter 表示一台电表。可以是主关口（PCC，聚合全部源）或子电表
// （只聚合配置中指定的 PCS / PV / 负载子集）。
type Meter struct {
	bank *SlaveBank
	name string

	nominalFrequencyHz float64
	frequencySource    string

	gridVoltage float64 // 一次侧电压 V
	ptRatio     float64 // 电压互感器变比（一次/二次）
	ctRatio     float64 // 电流互感器变比（一次/二次）

	// 当前 tick 输入
	gridPowerKW     float64
	loadPowerKW     float64
	pcsReactiveKVAr float64

	// 累计能量
	forwardKWh float64
	reverseKWh float64
}

func NewMeter(cfg MeterConfig, grid GridConfig, bank *SlaveBank) *Meter {
	v := cfg.Voltage
	if v == 0 {
		v = grid.Voltage
	}
	pt := cfg.PTRatio
	if pt <= 0 {
		// 自动选择能让二次值落入 U16(0.1V) 范围的最小整数变比。
		// U16 上限对应 6553.5 V，留 5% 余量。
		pt = math.Ceil(v / 6200.0)
		if pt < 1 {
			pt = 1
		}
	}
	ct := cfg.CTRatio
	if ct <= 0 {
		ct = 1
	}
	m := &Meter{
		bank:               bank,
		name:               cfg.Name,
		gridVoltage:        v,
		ptRatio:            pt,
		ctRatio:            ct,
		nominalFrequencyHz: grid.Frequency,
		frequencySource:    grid.FrequencySource,
	}
	// Modbus 模式启动时也给出有效初值；之后 Sync 不再覆盖外部写入。
	m.bank.WriteU16(RegMeterFrequency, uint16(m.nominalFrequencyHz*100))
	return m
}

// Update 根据 load / ΣPCS / ΣPV 重新计算电表功率并累计能量。
// 公约：gridPowerKW > 0 = 从电网买电；< 0 = 向电网卖电。
//
//	PCS actualPowerKW: 正充负放（充电时从电网取电）
//	PV  actualPowerKW: 永远 >= 0（注入）
//	Load actualPowerKW: 永远 >= 0（消耗）
func (m *Meter) Update(dtSeconds, loadPowerKW, totalPCSKW, totalPVKW float64) {
	m.UpdateWithReactive(dtSeconds, loadPowerKW, totalPCSKW, totalPVKW, 0)
}

// UpdateWithReactive 同时聚合 PCS 无功；正值表示从电网吸收感性无功。
func (m *Meter) UpdateWithReactive(dtSeconds, loadPowerKW, totalPCSKW, totalPVKW, totalPCSKVAr float64) {
	m.loadPowerKW = loadPowerKW
	m.gridPowerKW = loadPowerKW + totalPCSKW - totalPVKW
	m.pcsReactiveKVAr = totalPCSKVAr

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

	// 负载无功与 PCS 无功在 PCC 叠加；PCS 正值为感性吸收，负值为容性补偿。
	tanPhi := math.Sqrt(1-loadPowerFactor*loadPowerFactor) / loadPowerFactor
	reactiveKVar := m.loadPowerKW*tanPhi + m.pcsReactiveKVAr

	apparentKVA := math.Sqrt(gridPowerKW*gridPowerKW + reactiveKVar*reactiveKVar)

	// PF 幅值 = |P|/S；Q ≥ 0 → PF 符号为正
	pfMag := 1.0
	if apparentKVA > 0 {
		pfMag = math.Abs(gridPowerKW) / apparentKVA
	}

	// 二次侧 = 一次侧 / (PT × CT)，与电压/电流约定一致，EMS 端统一 × PT × CT 还原。
	ptct := m.ptRatio * m.ctRatio

	// 能量寄存器（S32, 0.01 kWh → ×100）
	combinedKWh := m.forwardKWh + m.reverseKWh
	m.bank.WriteS32(RegMeterCombinedEnergyHi, int32(combinedKWh*100/ptct))
	m.bank.WriteS32(RegMeterForwardEnergyHi, int32(m.forwardKWh*100/ptct))
	m.bank.WriteS32(RegMeterReverseEnergyHi, int32(m.reverseKWh*100/ptct))

	// 三相电压 ±0.5% jitter；phaseVoltages 保留一次侧，寄存器存二次侧 (U16, 0.1 V)
	phaseVoltages := [3]float64{}
	phaseVoltRegs := [3]uint16{RegMeterVoltageA, RegMeterVoltageB, RegMeterVoltageC}
	for i, reg := range phaseVoltRegs {
		jitter := 1.0 + (rand.Float64()*0.01 - 0.005)
		v := m.gridVoltage * jitter
		phaseVoltages[i] = v
		secondary := v / m.ptRatio
		m.bank.WriteU16(reg, uint16(secondary*10))
	}

	// 线电压（相间）：平衡三相下 |Uxy| = √3 × 相电压。取相邻两相幅值均值 ×√3，
	// 让线电压跟随相电压 jitter。二次侧同样除 PT，S32(0.1 V)。
	lineVoltRegs := [3]uint16{RegMeterLineVoltageABHi, RegMeterLineVoltageBCHi, RegMeterLineVoltageCAHi}
	for i, hiReg := range lineVoltRegs {
		vLine := math.Sqrt(3) * (phaseVoltages[i] + phaseVoltages[(i+1)%3]) / 2.0
		secondary := vLine / m.ptRatio
		m.bank.WriteS32(hiReg, int32(secondary*10))
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
		m.bank.WriteS32(hiReg, int32(currentA/m.ctRatio*10))
	}

	// 有功 (S32, 0.001 kW)，写二次侧
	m.bank.WriteS32(RegMeterActivePWTotalHi, int32(gridPowerKW*1000/ptct))
	phasePW1000 := int32(phasePowerKW * 1000 / ptct)
	m.bank.WriteS32(RegMeterActivePWAHi, phasePW1000)
	m.bank.WriteS32(RegMeterActivePWBHi, phasePW1000)
	m.bank.WriteS32(RegMeterActivePWCHi, phasePW1000)

	// 无功 (S32, 0.001 kVar)，写二次侧
	m.bank.WriteS32(RegMeterReactivePWTotalHi, int32(reactiveKVar*1000/ptct))
	phaseReact1000 := int32(reactiveKVar / 3.0 * 1000 / ptct)
	m.bank.WriteS32(RegMeterReactivePWAHi, phaseReact1000)
	m.bank.WriteS32(RegMeterReactivePWBHi, phaseReact1000)
	m.bank.WriteS32(RegMeterReactivePWCHi, phaseReact1000)

	// 视在 (S32, 0.001 kVA)，写二次侧
	m.bank.WriteS32(RegMeterApparentPWTotalHi, int32(apparentKVA*1000/ptct))
	phaseAppar1000 := int32(phaseApparentKVA * 1000 / ptct)
	m.bank.WriteS32(RegMeterApparentPWAHi, phaseAppar1000)
	m.bank.WriteS32(RegMeterApparentPWBHi, phaseAppar1000)
	m.bank.WriteS32(RegMeterApparentPWCHi, phaseAppar1000)

	// PF (S32, 0.001)；非负
	pf1000 := int32(pfMag * 1000)
	m.bank.WriteS32(RegMeterPFTotalHi, pf1000)
	m.bank.WriteS32(RegMeterPFAHi, pf1000)
	m.bank.WriteS32(RegMeterPFBHi, pf1000)
	m.bank.WriteS32(RegMeterPFCHi, pf1000)

	if m.frequencySource == frequencySourceRandom {
		// 随机模式下，频率围绕电网标称值 ±0.05 Hz 抖动。
		freqHz := m.nominalFrequencyHz + (rand.Float64()*2-1)*0.05
		m.bank.WriteU16(RegMeterFrequency, uint16(freqHz*100))
	}
}
