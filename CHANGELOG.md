# Changelog

## v0.4.1 - 开源整理与发布准备

- 移除私有 `go-common` 依赖，将当前用到的 Modbus server 与日志能力收敛到仓库内部，保证 public 仓库可独立构建。
- 整理代码结构：入口放到 `cmd/virtual_bess`，核心仿真放到 `internal/simulator`，Modbus server 放到 `internal/mbserver`，IEC 61850 放到 `internal/protocol/iec61850`。
- 补充 MIT License、README 项目背景和未来计划，说明项目用于 EMS 无真实设备场景下的联调和回归测试。

## v0.4.0 - 日本 2/8 项目场景

- 针对日本 2/8 项目维护独立分支，增加大阪 60Hz、双储能、无 PV、6.6kV 关口表和辅负载表配置。
- 增加电网频率配置，支持不同项目场景下的 PCS 频率寄存器输出。

## v0.3.0 - IEC 61850 协议仿真

- 增加 IEC 61850 MMS / GOOSE 服务端，模型由现场 CID 转换生成，覆盖 `LD0`、`CTRL`、`MEAS`、`PIGO` 逻辑设备。
- 支持遥调、遥控、遥测和 GOOSE 数据集，核心 PCS/BMS 点位接入真实电池仿真状态。
- 支持每端点独立 IED 名和多 BESS IEC 61850 端点，Modbus、MMS、GOOSE 共享同一个仿真器。
- 增加 PCS 离散告警注入，用于端到端验证 EMS 故障告警链路。

## v0.2.0 - 复杂场站仿真

- 从单套 PCS/BMS 扩展为可配置的复杂场站，支持多套储能 BESS、多台 PV、关口表、子电表、负载和天气模拟。
- Modbus TCP 改为单端口按 `slaveId` 路由，不同虚拟设备共享进程但保持独立寄存器空间。
- 增加电表聚合、状态持久化和多场景配置，便于构造 EMS 联调环境。

## v0.1.0 - 基础 PCS/BMS 仿真

- 提供最小可用的 Modbus TCP PCS/BMS 模拟器，覆盖功率指令、PCS 开停机、BMS 高压合分闸、SOC/SOH 和基础状态寄存器。
- 增加构建脚本、启动脚本和服务配置，支持本地运行与嵌入式环境部署。
