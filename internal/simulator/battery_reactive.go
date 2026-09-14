package simulator

import (
	"math"
	"math/rand"
)

// 本文件实现 PCS 的无功控制律：把「无功模式 + 对应设定值 + 并网点电压」换算成本 tick 的
// 无功出力目标，供 BatteryUnit.ProcessPowerCommand 调用。
//
// 模式编码取现场 IES900/IES1000 的 A13「无功功率设定模式」原生码（协议 V2.5 p.4，范围 0~2），
// 与二级 EMS 侧 5007/5137 的枚举不同——两者之间的换码由 emu 的设备级点表 dzPoint /
// statusPoint 完成（见 csv-config/projects/latvia/common/points/pcs/PCS-IEC61850-MMS.csv），
// 仿真器只认原生码，避免在设备侧再做一次不一致的映射。
const (
	ReactiveModeConstQ  uint16 = 0 // 恒定无功：直接跟随 RegPCSReactivePowerCmd
	ReactiveModeConstPF uint16 = 1 // 恒定功率因数：由 RegPCSPowerFactorCmd 和当前有功推导
	ReactiveModeQU      uint16 = 2 // Q-U 下垂：由并网点电压偏差推导
)

// Q-U 默认曲线：欧洲并网导则（EN 50549 系列）常见整定，1% 死区、5% 偏差满出力。
const (
	defaultQUDeadbandPU     = 0.01
	defaultQUFullResponsePU = 0.05
)

// 功率因数设定的下限：|PF| 再小换算出的无功会趋于无穷，真机同样有最小可整定值。
// |PF| 小于该值时按该值处理，保证 tan(acos(pf)) 有界。
const minSettablePowerFactor = 0.05

// updateReactiveOutput 按当前无功模式刷新本 tick 的无功出力，最后按视在容量钳制。
// 调用前 actualPowerKW 必须已经是本 tick 的有功出力：恒定 PF / Q-U 都以它为输入。
func (bu *BatteryUnit) updateReactiveOutput() {
	mode := bu.pcs.ReadU16(RegPCSReactiveModeCmd)
	target := bu.reactiveTargetKVAr(mode, bu.actualPowerKW)
	if mode != ReactiveModeConstPF && mode != ReactiveModeQU {
		// 只有恒定无功是在跟踪一个独立设定值，才存在跟踪误差；恒定 PF 与 Q-U 的出力
		// 是由已经带抖动的有功 / 电压推导出来的，再叠一层独立抖动会破坏「功率因数恒定」
		// 这个二级 EMS 会直接核对的不变量。
		target *= 1.0 + (rand.Float64()*0.01 - 0.005)
	}
	bu.actualReactiveKVAr = limitReactiveByApparent(target, bu.actualPowerKW, bu.ratedPowerKW)
}

// reactiveTargetKVAr 按无功模式计算无功出力目标（未加抖动，未按视在容量钳制）。
// activeKW 用内部「负充正放」语义（只取绝对值参与计算），无功正=感性、负=容性，
// 与 RegPCSReactivePowerCmd 一致。
func (bu *BatteryUnit) reactiveTargetKVAr(mode uint16, activeKW float64) float64 {
	switch mode {
	case ReactiveModeConstPF:
		return bu.reactiveFromPowerFactor(activeKW)
	case ReactiveModeQU:
		return bu.reactiveFromVoltageDroop()
	default:
		// 未知模式与 0 一律按恒定无功处理：真机对越界模式不会改变已有出力方式。
		return float64(uint16ToInt16(bu.pcs.ReadU16(RegPCSReactivePowerCmd))) * 0.1
	}
}

// reactiveFromPowerFactor 恒定功率因数模式：Q = |P| * tan(acos|PF|)，符号跟随 PF 符号。
// 有功为 0 时无功也为 0——恒定 PF 是「无功随有功成比例」，没有有功就没有基准。
func (bu *BatteryUnit) reactiveFromPowerFactor(activeKW float64) float64 {
	pf := float64(uint16ToInt16(bu.pcs.ReadU16(RegPCSPowerFactorCmd))) * 0.001
	if pf == 0 {
		// 寄存器初值 0 不能当成「纯无功」，否则未整定 PF 时一进入该模式就满发无功。
		pf = 1
	}
	magnitude := math.Abs(pf)
	if magnitude > 1 {
		magnitude = 1
	}
	if magnitude < minSettablePowerFactor {
		magnitude = minSettablePowerFactor
	}
	reactive := math.Abs(activeKW) * math.Tan(math.Acos(magnitude))
	if pf < 0 {
		return -reactive
	}
	return reactive
}

// reactiveFromVoltageDroop Q-U 模式：电压高于额定则吸收无功（感性，正），低于额定则发出
// 无功（容性，负），死区内不动作，超过满出力偏差后饱和在额定容量。
//
// 取的是「整定电压」而不是遥测寄存器里的带抖动值：真机 Q-U 的输入是滤波后的电压有效值，
// 直接用每 tick ±0.5% 的抖动值会让出力在死区边界反复翻转，也无法写确定性回归。
func (bu *BatteryUnit) reactiveFromVoltageDroop() float64 {
	nominal := bu.pcsACVoltage
	if nominal <= 0 {
		return 0
	}
	deviation := bu.gridPhaseVoltage()/nominal - 1
	magnitude := math.Abs(deviation)
	if magnitude <= bu.quDeadband {
		return 0
	}
	ratio := (magnitude - bu.quDeadband) / (bu.quFullResponse - bu.quDeadband)
	if ratio > 1 {
		ratio = 1
	}
	if deviation < 0 {
		return -ratio * bu.ratedPowerKW
	}
	return ratio * bu.ratedPowerKW
}

// gridPhaseVoltage 返回本 tick 用于控制与遥测的并网点相电压基准值：
// 写过 RegPCSGridVoltageCmd 时用强制值，否则用配置额定值。
func (bu *BatteryUnit) gridPhaseVoltage() float64 {
	if forced := bu.pcs.ReadU16(RegPCSGridVoltageCmd); forced != 0 {
		return float64(forced) * 0.1
	}
	return bu.pcsACVoltage
}

// limitReactiveByApparent 按视在容量钳制无功：额定容量取 ratedPowerKW（kVA），
// 有功已占用的部分不能再用于无功，对应真机「有功优先」的容量分配。
func limitReactiveByApparent(reactiveKVAr, activeKW, ratedKVA float64) float64 {
	headroom := ratedKVA*ratedKVA - activeKW*activeKW
	if headroom <= 0 {
		return 0
	}
	limit := math.Sqrt(headroom)
	if reactiveKVAr > limit {
		return limit
	}
	if reactiveKVAr < -limit {
		return -limit
	}
	return reactiveKVAr
}
