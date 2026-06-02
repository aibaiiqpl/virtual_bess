# Virtual BESS

虚拟储能系统模拟器，通过 Modbus TCP 对外暴露 PCS / BMS / PV / 电表寄存器，用于开发调试。
单端口、按 **slaveId** 路由，支持同时模拟多套 PCS+BMS 和多台 PV 逆变器。

## 依赖

- [go-common](https://cnb.cool/aiwatt/ems/go-common)（mbserver、zaplog）

## 拓扑模型

- **电池单元（battery_unit）**：1 PCS slave + 1 BMS slave 紧密配对，可配置 N 套。
- **PV 单元（pv_unit）**：1 slave 一套，可配置 M 套，所有 PV 共享同一份随机天气。
- **电表（meter）**：单台，挂在 PCC 点，聚合所有 PCS/PV/Load 的功率。
- **负载（load）**：单台，纯内部模拟，不暴露到 modbus。

电表入网功率公式：
```
gridPowerKW = loadPowerKW + Σ(PCS.actualPowerKW) − Σ(PV.actualPowerKW)
正值=买电，负值=卖电
```

## 配置

编辑 `config.yaml`：

```yaml
modbus:
  address: ":502"

iec61850:
  enabled: false
  address: ":102"

grid:
  voltage: 220

battery_units:
  - pcs_slave_id: 1
    bms_slave_id: 11
    rated_capacity_kwh: 261
    rated_power_kw: 120
    initial_soc: 30.0
    soh: 100.0
    battery_voltage: 800
    cluster_count: 1
  - pcs_slave_id: 2
    bms_slave_id: 12
    rated_capacity_kwh: 200
    rated_power_kw: 100
    initial_soc: 50.0
    soh: 95.0
    battery_voltage: 800
    cluster_count: 2

pv_units:
  - slave_id: 21
    rated_power_kw: 120
  - slave_id: 22
    rated_power_kw: 80

meter:
  slave_id: 31

load:
  rated_power_kw: 80
```

启动时校验：所有 slave_id 不能为 0 且不能重复，至少 1 个 battery_unit。

## 构建和运行

```bash
go build -o virtual_bess .
./virtual_bess -config config.yaml
```

启用 IEC 61850 MMS 服务端时：

```bash
go build -tags iec61850 -o virtual_bess .
```

并在配置中设置：

```yaml
iec61850:
  enabled: true
  address: ":102"
  goose:
    enabled: false
    interface: "eth0"
    appid: "0100"
    dst_mac: "01-0C-CD-01-01-00"
    vlan_priority: 4
    vlan_id: 0
    interval_ms: 1000
    time_allowed_to_live_ms: 5000
  devices:
    - pcs_slave_id: 1
      address: ":102"
      goose:
        enabled: false
        interface: "eth0"
        appid: "0100"
    - pcs_slave_id: 2
      address: ":1102"
      goose:
        enabled: false
        interface: "eth0"
        appid: "0101"
```

## 操作流程

以第一套电池单元为例（pcs_slave_id=1, bms_slave_id=11）：

1. BMS 合闸：向 **slave 11** 写 40001 = 1
2. PCS 开机：向 **slave 1** 写 30003 = 1
3. 下发功率：向 **slave 1** 写 30010 或 3010 = 500（充电 50kW）或 -500（放电 50kW）
4. 读取状态：从 **slave 11** 读 40105（SOC）、从 **slave 1** 读 30061（PCS 实际功率）

> 未合闸就启动 PCS 会触发直流侧欠压故障（30180 = 1）。
> PCS 在就地模式（30006 = 0）时功率指令不生效。

## IEC 61850 点位

当前 IEC 61850 服务端按 `docs/IES1000_IES900_CO_V2.5.cid` 和点位说明接入 PCS/BMS 核心点位。遥调使用 MMS write 写 `SP`，遥测使用 MMS read 读 `MX`。GOOSE 发布使用 CID 中的 `TEMPLATEPIGO/LLN0$GO$gocb1` / `dsGOOSE1`，默认目的 MAC `01-0C-CD-01-01-00`、VLAN priority `4`。

如果未配置 `iec61850.devices`，IEC 61850 默认只绑定第一套电池单元。配置 `devices` 后，一个进程会为多套 PCS 启动多套 IEC 61850 端点，每个端点通过 `pcs_slave_id` 绑定对应储能单元。这样 Modbus、MMS、GOOSE 共享同一个仿真器，总电表仍能聚合全部 PCS/PV/负载。

MMS 通过不同 TCP 端口区分端点，例如 `:102`、`:1102`。GOOSE 是二层以太网组播，不使用 MMS TCP 端口；多端点需要配置不同 `APPID`，通常也会结合 `goCbRef`、目的 MAC、VLAN 一起区分。启用 GOOSE 时必须配置实际网卡名，通常也需要运行进程具备原始以太网发包权限。

| 对象引用 | FC | 方向 | 含义 |
|----------|----|------|------|
| `TEMPLATECTRL/GGIO1.APCS1.setMag.f` | SP | 写 | 有功功率设定，kW，正充负放 |
| `TEMPLATECTRL/GGIO1.APCS2.setMag.f` | SP | 写 | 无功功率设定，kVar，仅回显 |
| `TEMPLATECTRL/GGIO1.APCS9.setMag.f` | SP | 写 | PCS 控制命令：0 关机，1 开机，2 复位，3 待机 |
| `TEMPLATECTRL/GGIO1.APCS10.setMag.f` | SP | 写 | PCS 运行模式：0 并网，1 离网 |
| `TEMPLATEPIGO/GGIO1.AnIn1.mag.f` | MX | 读 | 额定功率，kW |
| `TEMPLATEPIGO/GGIO1.AnIn2.mag.f` | MX | 读 | 电池组 SOC，% |
| `TEMPLATEPIGO/GGIO1.AnIn3.mag.i` | MX | 读 | PCS 系统状态-值模式 |
| `TEMPLATEPIGO/GGIO1.AnIn4.mag.f` | MX | 读 | 输出总有功功率，kW |
| `TEMPLATEPIGO/GGIO1.AnIn5.mag.f` | MX | 读 | 输出总无功功率，kVar |
| `TEMPLATEPIGO/GGIO1.AnIn6.mag.f` | MX | 读 | 电池组最大充电功率，kW |
| `TEMPLATEPIGO/GGIO1.AnIn7.mag.f` | MX | 读 | 电池组最大放电功率，kW |
| `TEMPLATEPIGO/GGIO1.AnIn8.mag.f` | MX | 读 | 有功功率设定值，kW |
| `TEMPLATEPIGO/GGIO1.AnIn9.mag.f` | MX | 读 | 无功功率设定值，kVar |

GOOSE `dsGOOSE1` 发布同一组 `TEMPLATEPIGO/GGIO1.AnIn1` - `AnIn9` 遥测值，顺序与上表一致。

## Modbus 点位表

下表的地址是每个 slave 内部的寄存器地址；不同 slave 共用同一套地址布局。

### PCS 控制（FC 06/16，PCS slave）

| 地址  | 名称           | 类型 | 备注                          |
|-------|----------------|------|-------------------------------|
| 30000 | 并/离网设置     | U16  | 0-并网 1-离网                 |
| 30001 | 运行模式设置   | U16  | 默认 2（恒功率）              |
| 30002 | 故障复位       | U16  | 1-复位                       |
| 30003 | 设备开机       | U16  | 1-开机                       |
| 30004 | 设备关机       | U16  | 1-关机                       |
| 30005 | 远程急停       | U16  | 1-急停                       |
| 30006 | 远程/就地设置   | U16  | 0-就地 1-远程，默认 1         |
| 30010/3010 | 充放电功率指令 | S16  | 0.1kW，正充负放（两个寄存器互为别名） |

### PCS 状态（FC 03，PCS slave）

| 地址      | 名称           | 类型 | 系数 | 单位 |
|-----------|----------------|------|------|------|
| 30049     | 远程控制状态   | U16  | 1    | -    |
| 30050     | 系统状态       | U16  | 1    | -    |
| 30051     | 并离网状态     | U16  | 1    | -    |
| 30052     | 总告警状态     | U16  | 1    | -    |
| 30053     | 总故障状态     | U16  | 1    | -    |
| 30060     | 功率因数       | S16  | 0.01 | -    |
| 30061     | 总有功功率     | S16  | 0.1  | kW   |
| 30062     | 总无功功率     | S16  | 0.1  | kVAr |
| 30063     | 总视在功率     | U16  | 0.1  | kVA  |
| 30064-66  | 三相有功功率   | S16  | 0.1  | kW   |
| 30067-69  | 三相无功功率   | S16  | 0.1  | kVAr |
| 30070-72  | 三相电压       | U16  | 0.1  | V    |
| 30073-75  | 三相电流       | S16  | 0.1  | A    |
| 30076     | 电网频率       | U16  | 0.01 | Hz   |
| 30077     | 直流电压       | S16  | 0.1  | V    |
| 30078     | 直流电流       | S16  | 0.1  | A    |
| 30079     | 直流功率       | S16  | 0.1  | kW   |
| 30080     | 内部温度       | S16  | 0.1  | °C   |
| 30081-83  | IGBT温度A/B/C  | S16  | 0.1  | °C   |
| 30180     | 直流侧欠压故障 | U16  | 1    | -    |

### 系统/EMU 概览（FC 03，PCS slave）

| 地址 | 名称 | 类型 | 系数 | 单位 |
|------|------|------|------|------|
| 1    | 运行状态 | U16 | 1 | - |
| 2    | 故障状态 | U16 | 1 | - |
| 3    | 待机状态 | U16 | 1 | - |
| 4    | EMU-BMS 通讯 | U16 | 1 | - |
| 5    | EMU-PCS 通讯 | U16 | 1 | - |
| 100  | 运行模式 | U16 | 1 | - |
| 101  | 系统最大充电功率 | U16 | 0.1 | kW |
| 102  | 系统最大放电功率 | U16 | 0.1 | kW |
| 103  | 系统实际功率 | U16 | 0.1 | kW |
| 104  | BMS 主从模式 | U16 | 1 | - |
| 105  | BMS 簇数 | U16 | 1 | - |
| 700  | 最大充电功率外部设定 | U16 | 0.1 | kW |
| 701  | 最大放电功率外部设定 | U16 | 0.1 | kW |

### BMS 控制（FC 06/16，BMS slave）

| 地址  | 名称         | 类型 | 备注        |
|-------|--------------|------|-------------|
| 40000 | 故障复位     | U16  | 1-复位      |
| 40001 | 上高压指令   | U16  | 1-合闸      |
| 40002 | 下高压指令   | U16  | 1-分闸      |

### BMS 状态（FC 03，BMS slave）

| 地址  | 名称             | 类型 | 系数 | 单位 |
|-------|------------------|------|------|------|
| 40100 | 总故障状态       | U16  | 1    | -    |
| 40101 | 总告警状态       | U16  | 1    | -    |
| 40102 | 系统状态         | U16  | 1    | -    |
| 40103 | 禁充状态         | U16  | 1    | -    |
| 40104 | 禁放状态         | U16  | 1    | -    |
| 40105 | SOC              | U16  | 0.1  | %    |
| 40106 | SOH              | U16  | 0.1  | %    |
| 40107 | 剩余充电电量     | U16  | 0.1  | kWh  |
| 40108 | 剩余放电电量     | U16  | 0.1  | kWh  |
| 40109 | 电池总电压       | U16  | 0.1  | V    |
| 40110 | 电池总电流       | S16  | 0.1  | A    |
| 40111 | 电池总功率       | S16  | 0.1  | kW   |
| 40120 | 最大允许充电功率 | U16  | 0.1  | kW   |
| 40121 | 最大允许放电功率 | U16  | 0.1  | kW   |
| 40122 | 最大允许充电电流 | U16  | 0.1  | A    |
| 40123 | 最大允许放电电流 | U16  | 0.1  | A    |

### BMS 簇（FC 04，Input Registers，BMS slave）

每簇 stride=1600，第 N 簇地址 = `N*1600 + 偏移`，偏移定义见 `registers.go`。

### PV 控制（FC 06/16，PV slave）

| 地址  | 名称             | 类型 | 系数 | 单位 | 备注 |
|-------|------------------|------|------|------|------|
| 60000 | 开机             | U16  | 1    | -    | 1-开机，处理后清零 |
| 60001 | 关机             | U16  | 1    | -    | 1-关机，处理后清零 |
| 60002 | 有功功率百分比设置 | U16  | 0.1  | %    | 0-1000，1000=100.0% |
| 60003 | 有功功率固定值降额 | U16  | 0.1  | kW   | 0-Pmax |

> 60002 和 60003 以最新写入的寄存器为准。光伏默认开机，6:00-18:00 按日曲线发电，13:00-15:00 达到峰值（额定功率的 90%）。

### PV 状态（FC 03，PV slave）

| 地址      | 名称             | 类型 | 系数  | 单位 |
|-----------|------------------|------|-------|------|
| 60100     | 运行状态         | U16  | 1     | -    |
| 60140-41  | 累计发电量       | U32  | 1     | kWh  |
| 60142-43  | 当日发电量       | U32  | 1     | kWh  |
| 60144-45  | 当月发电量       | U32  | 1     | kWh  |
| 60146-47  | 当年发电量       | U32  | 1     | kWh  |
| 60148     | 额定功率         | U16  | 0.1   | kW   |
| 60149     | 故障告警码       | U16  | 1     | -    |
| 60150-52  | 交流侧电压 A/B/C | U16  | 0.1   | V    |
| 60153-55  | 交流侧电流 A/B/C | S16  | 0.1   | A    |
| 60156     | 电网频率         | U16  | 0.01  | Hz   |
| 60157     | 功率因数         | S16  | 0.001 | -    |
| 60158     | 交流侧有功功率   | S16  | 0.1   | kW   |
| 60159     | 交流侧无功功率   | S16  | 0.1   | kW   |
| 60160     | 逆变器效率       | U16  | 0.1   | %    |
| 60161     | 当天峰值有功功率 | S16  | 0.1   | kW   |
| 60162     | 总视在功率       | U16  | 0.1   | kVA  |
| 60280     | DC 输入总功率    | S16  | 0.1   | kW   |
| 60281     | 逆变器内部温度   | S16  | 0.1   | °C   |
| 60282     | DC 总电压        | U16  | 0.1   | V    |
| 60283     | DC 总电流        | S16  | 0.1   | A    |

### 电表（FC 03，Meter slave）

地址在 `registers.go` 中定义，包括能量、三相电压电流、有功/无功/视在、功率因数、频率。
