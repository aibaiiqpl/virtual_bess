package simulator

import "fmt"

// 本文件实现「注入功率 → 母线电压偏移」的等效阻抗模型。PCS 交流出口母线和电表所在的
// 并网点各持有一份配置，共用这里的换算与惯性逼近。
//
// 用的是配网常用的电压灵敏度近似：
//
//	ΔU/Un ≈ (P·R_pu + Q·X_pu) / S_base
//
// P、Q 一律取「向电网注入为正」，于是：
//   - 放电（注入有功）抬高电压，充电（吸收有功）压低电压；
//   - 发出容性无功抬高电压，吸收感性无功压低电压。
//
// 这正是 Q-U 下垂赖以成立的因果链：没有这条链，下垂模式就是开环空转。
//
// 近似忽略了相角差和阻抗压降的二阶项。仿真器要的是方向和量级正确，
// 不是潮流计算精度；在 |ΔU| < 10% 的范围内该近似的误差可以忽略。
//
// 两个节点各自对「自己的上游等效电源」建模，PCS 母线不叠加并网点的电压跌落。
// 真机里 PCS 侧还隔着一台升压变，两级压降串联；分开建模会少算一点耦合，
// 但换来两个节点互不依赖、可各自回归，对联调够用。

// GridCouplingConfig 描述一条母线相对上游电源的等效阻抗。
type GridCouplingConfig struct {
	// BaseKVA 标幺基准容量；<=0 表示由使用方自动取
	//（PCS 取本单元额定容量，电表取站内 PCS 额定容量之和）。
	BaseKVA float64 `yaml:"base_kva"`
	// ResistancePU 等效电阻（标幺），决定有功对电压的影响强度。
	ResistancePU float64 `yaml:"resistance_pu"`
	// ReactancePU 等效电抗（标幺），决定无功对电压的影响强度。
	ReactancePU float64 `yaml:"reactance_pu"`
	// ResponseSeconds 电压有效值的一阶滤波时间常数（秒），必须 > 0。
	//
	// Q-U 下垂是负反馈闭环，环路增益约等于 ReactancePU / (FullResponse - Deadband)，
	// 现场常见整定下该值大于 1；没有惯性，无功出力会在相邻 tick 之间自激翻转。
	ResponseSeconds float64 `yaml:"response_seconds"`
}

// maxVoltageDeviationPU 电压偏移的数值护栏。并网点的聚合注入可能超过基准容量
// （子电表沿用整站基准时尤其如此），线性外推会把电压推成负值并让 U16 寄存器回绕。
//
// 放宽到 0.5：关口电抗已刻意整定成远超物理值的量级（见 defaultGridCoupling），
// 站点申报的满额无功在这个电抗下就能推开 30%，护栏留在 0.3 会让整个申报区间的上半段
// 全贴在鞍上，看不出无功和电压的对应关系。0.5 对 U16 仍然安全：10 kV 站点最高到
// 15 kV，二次侧 150 V 写进寄存器是 1500，离回绕很远。
const maxVoltageDeviationPU = 0.5

// maxCouplingPU 是单项等效阻抗的整定上界，只用来拦住量纲写错一类的输入。
//
// 它不再按「额定功率下不得贴住护栏」来定：仿真器刻意把关口电抗放大到物理值的几十倍，
// 真正防止电压跑飞的是 maxVoltageDeviationPU 的运行时钳位，不是这条启动校验。
const maxCouplingPU = 2.0

// defaultPCSCoupling PCS 出口母线的默认等效阻抗：
// 一台 uk≈6%、X/R≈4 的升压变，标幺基准取 PCS 自身额定容量。
func defaultPCSCoupling() GridCouplingConfig {
	return GridCouplingConfig{ResistancePU: 0.015, ReactancePU: 0.06, ResponseSeconds: 2}
}

// defaultGridCoupling 并网点的默认等效阻抗。时间常数比 PCS 侧更大，对应电表更慢的
// 有效值刷新。
//
// ReactancePU 取 1.2，是按短路容量算出的物理值（约 0.04）的三十倍。这不是建模误差，
// 是本仿真器的用途决定的：它要把上游 Q(U) 闭环推到看得见的幅度，而物理值下并网点
// 几乎不动——满额无功才推开 1%，十几 kvar 的小指令连电压寄存器 10 V 的刻度都跨不过
// 一格，闭环在数据上等于开环空转，什么都测不出来。
//
// 1.2 折合 800 V/MVAr（S基准 15 MVA、10 kV 站）：15 kvar 推开 12 V，1 MVAr 推开 8%，
// 站点申报的 3.75 MVAr 推开 30%。趋势图上无功和电压的对应关系一眼可见。
//
// 代价必须清楚：上游 Q(U) 的环路增益同比放大。L = 曲线斜率[MVAr/pu] × X/S基准，
// Pmax 9.5 MW 的站在 0.875→0.95 这种常规斜率上 L≈4，陡段 L≈12。上游默认 30% 逼近
// 步长下 k(1+L) 远大于 1，一定发散——这正是要观察的现象；要让它收敛，把 Q(U) 页面的
// 逼近步长降到 1/(1+L) 以下（L=4 对应 20%，陡段对应 8%）。
//
// 换句话说，这个默认值把仿真站做成了一个「弱电网」，专门用来检验上游步长整定是否够
// 保守。要仿真真实站点的响应，在 config.yaml 的 grid.coupling.reactance_pu 里覆盖成
// 0.04 量级。
//
// 还要注意灵敏度不是唯一的限制项：闭环稳态下电压能偏离多远由曲线零点决定——
// ΔU = (自然电压 − 曲线零点) × L/(1+L)，工作点落在曲线平段时无功本身就是 0，
// 这时候灵敏度调多大都看不到电压动。
//
// 需要另一档灵敏度时在 config.yaml 的 grid.coupling.reactance_pu 里覆盖，不必改代码。
func defaultGridCoupling() GridCouplingConfig {
	return GridCouplingConfig{ResistancePU: 0.010, ReactancePU: 1.200, ResponseSeconds: 3}
}

// resolveCoupling 补齐未整定的等效阻抗字段，返回可直接使用的配置。
//
// 整块判空而不是逐字段判空：只有这样才允许把 R 或 X 显式整定为 0，
// 单独观察另一个分量对电压的贡献——逐字段兜底会把这个 0 又填回默认值。
func resolveCoupling(cfg, fallback GridCouplingConfig, autoBaseKVA float64) GridCouplingConfig {
	if cfg == (GridCouplingConfig{}) {
		cfg = fallback
	}
	if cfg.ResponseSeconds <= 0 {
		cfg.ResponseSeconds = fallback.ResponseSeconds
	}
	if cfg.BaseKVA <= 0 {
		cfg.BaseKVA = autoBaseKVA
	}
	return cfg
}

// busVoltageTarget 按注入功率算稳态母线电压。
// injectKW / injectKVAr 一律「向电网注入为正」，调用方负责换向。
func busVoltageTarget(c GridCouplingConfig, nominalV, injectKW, injectKVAr float64) float64 {
	if nominalV <= 0 || c.BaseKVA <= 0 {
		return nominalV
	}
	deviation := (injectKW*c.ResistancePU + injectKVAr*c.ReactancePU) / c.BaseKVA
	if deviation > maxVoltageDeviationPU {
		deviation = maxVoltageDeviationPU
	}
	if deviation < -maxVoltageDeviationPU {
		deviation = -maxVoltageDeviationPU
	}
	return nominalV * (1 + deviation)
}

// relaxVoltage 按一阶惯性把当前电压推向目标值，模拟电压有效值的测量滤波。
// dt <= 0 时不动（同一 tick 内重复调用不应改变状态）。
func relaxVoltage(current, target, dt, tauSeconds float64) float64 {
	if dt <= 0 {
		return current
	}
	if tauSeconds <= 0 {
		return target
	}
	return current + (target-current)*dt/(tauSeconds+dt)
}

// validate 在启动时拦住会让电压响应失真的整定值。
// 这些错误在运行时不会报任何异常：负阻抗只是把充放电对电压的方向整个反过来，
// 过大的阻抗只是让电压长期贴在护栏上，两种都要等到对着数据发懵才会被发现。
func (c GridCouplingConfig) validate(name string) error {
	if c.ResistancePU < 0 || c.ReactancePU < 0 {
		return fmt.Errorf("%s: resistance_pu/reactance_pu must not be negative", name)
	}
	if c.ResistancePU > maxCouplingPU || c.ReactancePU > maxCouplingPU {
		return fmt.Errorf("%s: resistance_pu/reactance_pu must not exceed %.1f pu", name, maxCouplingPU)
	}
	if c.BaseKVA < 0 {
		return fmt.Errorf("%s: base_kva must not be negative", name)
	}
	if c.ResponseSeconds < 0 {
		return fmt.Errorf("%s: response_seconds must not be negative", name)
	}
	return nil
}
