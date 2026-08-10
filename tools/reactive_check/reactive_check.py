#!/usr/bin/env python3
"""AWS 测试机上验证 emu-rs ←→ virtual_bess 的 PCS 无功链路。

覆盖三种无功模式：恒定无功、恒定功率因数、Q-U。
下行走 emu 北向 Modbus（1501，slave 1）→ IEC61850 MMS → virtual_bess；
回读同时看 emu 北向遥测和 virtual_bess 自己的 PCS 寄存器，确认两端一致。
Q-U 需要的电压偏差通过 virtual_bess 的 Modbus 口（18502，PCS slave 1）强制。

5007/5137 用的是二级 EMS 枚举（1=PF 2=恒定无功 4=Q-U），由 emu 设备级点表的
dzPoint / statusPoint 与 IES900 原生码 0/1/2 互相换码；本脚本刻意走 EMS 枚举，
以便同时验证换码方向是否正确。

前置：目标机若有二级 EMS（AWS 上是 ems_II.service）在持续下发有功设定，
必须先停掉，否则有功被压在 0，恒定功率因数模式没有可观测的无功。
"""

import socket
import struct
import sys
import time

EMU_NORTH = ("127.0.0.1", 1501)
EMU_SLAVE = 1
BESS_MODBUS = ("127.0.0.1", 18502)
BESS_PCS_SLAVE = 1

# 与 AWS /opt/virtual_bess/config.yaml 保持一致：PCS 交流出口相电压 720V，单机额定 2500kW。
# Q-U 的电压偏差按额定相电压折算，改现场配置时这两个常量必须同步。
PCS_NOMINAL_V = 720.0
PCS_RATED_KW = 2500.0

# emu 北向（标准点表 2.0）
REG_REACTIVE_SET = 30014  # S16 0.1kVAr
REG_PF_SET = 5006  # S16 0.001
REG_MODE_SET = 5007  # U16
REG_MODE_READ = 5137  # U16
REG_ACTIVE_SET = 30010  # S16 0.1kW，负充正放
REG_TOTAL_ACTIVE = 30061  # S16 0.1kW
REG_TOTAL_REACTIVE = 30062  # S16 0.1kVAr
REG_POWER_FACTOR = 30060  # S16 0.01

# 5007 下发 / 5137 回读用的二级 EMS 枚举
EMS_MODE_PF = 1
EMS_MODE_Q = 2
EMS_MODE_QU = 4

# 测试用有功设定：500kW 放电，占额定 2500kW 的 20%，留足无功余量。
TEST_ACTIVE_KW = 500.0
# tan(acos 0.9)
PF090_TAN = 0.484322

# virtual_bess PCS slave 自身寄存器
BESS_REACTIVE_CMD = 30014  # S16 0.1kVAr
BESS_MODE_CMD = 30015  # U16
BESS_PF_CMD = 30016  # S16 0.001
BESS_GRID_VOLTAGE = 30020  # U16 0.1V，仿真专用
BESS_TOTAL_REACTIVE = 30062  # S16 0.1kVAr


class Modbus:
    def __init__(self, addr, slave):
        self.sock = socket.create_connection(addr, timeout=5)
        self.slave = slave
        self.tid = 0

    def _txn(self, pdu):
        self.tid = (self.tid + 1) % 0xFFFF
        self.sock.sendall(struct.pack(">HHHB", self.tid, 0, len(pdu) + 1, self.slave) + pdu)
        header = self._recv(7)
        length = struct.unpack(">H", header[4:6])[0]
        resp = self._recv(length - 1)
        if resp[0] & 0x80:
            raise RuntimeError(f"modbus exception {resp[1]} for pdu {pdu.hex()}")
        return resp[1:]

    def _recv(self, n):
        buf = b""
        while len(buf) < n:
            chunk = self.sock.recv(n - len(buf))
            if not chunk:
                raise RuntimeError("connection closed")
            buf += chunk
        return buf

    def read(self, addr):
        body = self._txn(struct.pack(">BHH", 3, addr, 1))
        return struct.unpack(">H", body[1:3])[0]

    def read_s16(self, addr):
        return struct.unpack(">h", struct.pack(">H", self.read(addr)))[0]

    def write(self, addr, value):
        self._txn(struct.pack(">BHH", 6, addr, value & 0xFFFF))

    def write_s16(self, addr, value):
        self.write(addr, struct.unpack(">H", struct.pack(">h", value))[0])

    def close(self):
        self.sock.close()


FAILURES = []


def check(name, got, want, tol=0):
    ok = abs(got - want) <= tol
    print(f"  [{'OK ' if ok else 'FAIL'}] {name}: got {got}, want {want}±{tol}")
    if not ok:
        FAILURES.append(name)


def settle(seconds=8):
    """emu 轮询、MMS 下发、virtual_bess tick 都是秒级，留足一轮闭环时间。"""
    time.sleep(seconds)


def main():
    emu = Modbus(EMU_NORTH, EMU_SLAVE)
    bess = Modbus(BESS_MODBUS, BESS_PCS_SLAVE)

    try:
        print("== 0. 基线：清空强制电压，恒定无功模式，有功 %.0fkW 放电 ==" % TEST_ACTIVE_KW)
        bess.write(BESS_GRID_VOLTAGE, 0)
        emu.write(REG_MODE_SET, EMS_MODE_Q)
        emu.write_s16(REG_REACTIVE_SET, 0)
        emu.write_s16(REG_ACTIVE_SET, int(TEST_ACTIVE_KW * 10))
        settle()
        # 北向 30010 与 30061 都是 EMU-V2.0 的负充正放：写 +500kW（放电），
        # 回读也必须是 +500kW。符号反了说明 61850 适配层的充放方向又错了。
        check("有功遥测 30061 (kW，含符号)", emu.read_s16(REG_TOTAL_ACTIVE) * 0.1,
              TEST_ACTIVE_KW, 10)

        print("== 1. 恒定无功：5007=2(EMS) ⇒ 原生 0，30014=2000（200kVAr 感性）==")
        emu.write_s16(REG_REACTIVE_SET, 2000)
        settle()
        check("无功模式回读 5137 (EMS 枚举)", emu.read(REG_MODE_READ), EMS_MODE_Q)
        check("PCS 侧原生无功模式 30015", bess.read(BESS_MODE_CMD), 0)
        check("PCS 侧无功指令 30014 (kVAr)", bess.read_s16(BESS_REACTIVE_CMD) * 0.1, 200, 0.1)
        check("emu 总无功 30062 (kVAr)", emu.read_s16(REG_TOTAL_REACTIVE) * 0.1, 200, 5)
        check("PCS 总无功 30062 (kVAr)", bess.read_s16(BESS_TOTAL_REACTIVE) * 0.1, 200, 5)

        print("== 2. 恒定无功：30014=-1500（150kVAr 容性）==")
        emu.write_s16(REG_REACTIVE_SET, -1500)
        settle()
        check("emu 总无功 30062 (kVAr)", emu.read_s16(REG_TOTAL_REACTIVE) * 0.1, -150, 5)

        print("== 3. 恒定功率因数：5007=1(EMS) ⇒ 原生 1，5006=900（PF 0.9 感性）==")
        emu.write(REG_MODE_SET, EMS_MODE_PF)
        emu.write_s16(REG_PF_SET, 900)
        settle()
        check("无功模式回读 5137 (EMS 枚举)", emu.read(REG_MODE_READ), EMS_MODE_PF)
        check("PCS 侧原生无功模式 30015", bess.read(BESS_MODE_CMD), 1)
        check("PCS 侧功率因数设定 30016", bess.read_s16(BESS_PF_CMD) * 0.001, 0.9, 0.001)
        active = emu.read_s16(REG_TOTAL_ACTIVE) * 0.1
        check("总无功跟随有功和 PF (kVAr)", emu.read_s16(REG_TOTAL_REACTIVE) * 0.1,
              abs(active) * PF090_TAN, 8)
        check("功率因数回读 30060", emu.read_s16(REG_POWER_FACTOR) * 0.01, 0.9, 0.02)

        print("== 4. 恒定功率因数：5006=-900（PF -0.9 容性）==")
        emu.write_s16(REG_PF_SET, -900)
        settle()
        check("PCS 侧功率因数设定 30016", bess.read_s16(BESS_PF_CMD) * 0.001, -0.9, 0.001)
        active = emu.read_s16(REG_TOTAL_ACTIVE) * 0.1
        check("总无功转为容性 (kVAr)", emu.read_s16(REG_TOTAL_REACTIVE) * 0.1,
              -abs(active) * PF090_TAN, 8)
        check("功率因数回读 30060 转负", emu.read_s16(REG_POWER_FACTOR) * 0.01, -0.9, 0.02)

        print("== 5. 恒定 PF：有功归零 ⇒ 无功归零（无功随有功成比例）==")
        emu.write_s16(REG_ACTIVE_SET, 0)
        settle()
        check("有功归零后总无功 30062", emu.read_s16(REG_TOTAL_REACTIVE) * 0.1, 0, 1)
        emu.write_s16(REG_ACTIVE_SET, int(TEST_ACTIVE_KW * 10))
        settle()

        print(f"== 6. Q-U：5007=4(EMS) ⇒ 原生 2，电压 {PCS_NOMINAL_V:.1f}V（额定）在死区内 ==")
        emu.write(REG_MODE_SET, EMS_MODE_QU)
        bess.write(BESS_GRID_VOLTAGE, int(PCS_NOMINAL_V * 10))
        settle()
        check("无功模式回读 5137 (EMS 枚举)", emu.read(REG_MODE_READ), EMS_MODE_QU)
        check("PCS 侧原生无功模式 30015", bess.read(BESS_MODE_CMD), 2)
        check("死区内 emu 总无功 30062", emu.read_s16(REG_TOTAL_REACTIVE) * 0.1, 0, 1)

        print("== 7. Q-U：过压 +3% ⇒ 约 50% 额定无功，感性 ==")
        bess.write(BESS_GRID_VOLTAGE, int(PCS_NOMINAL_V * 1.03 * 10))
        settle()
        check("过压 emu 总无功 30062", emu.read_s16(REG_TOTAL_REACTIVE) * 0.1,
              0.5 * PCS_RATED_KW, 60)
        check("功率因数 30060 为正（感性）",
              1 if emu.read_s16(REG_POWER_FACTOR) > 0 else -1, 1)

        print("== 8. Q-U：欠压 -3% ⇒ 约 50% 额定无功，容性 ==")
        bess.write(BESS_GRID_VOLTAGE, int(PCS_NOMINAL_V * 0.97 * 10))
        settle()
        check("欠压 emu 总无功 30062", emu.read_s16(REG_TOTAL_REACTIVE) * 0.1,
              -0.5 * PCS_RATED_KW, 60)
        check("功率因数 30060 为负（容性）",
              1 if emu.read_s16(REG_POWER_FACTOR) > 0 else -1, -1)

        print("== 9. Q-U：欠压饱和 -10% ⇒ 无功受视在容量钳制 ==")
        bess.write(BESS_GRID_VOLTAGE, int(PCS_NOMINAL_V * 0.90 * 10))
        settle()
        # 有功 500kW 占用容量后，无功上限 = sqrt(2500² - 500²) = 2449.5 kVAr
        headroom = (PCS_RATED_KW ** 2 - TEST_ACTIVE_KW ** 2) ** 0.5
        check("饱和 emu 总无功 30062", emu.read_s16(REG_TOTAL_REACTIVE) * 0.1, -headroom, 40)

        print("== 10. 复位：恒定无功、设定清零、电压恢复额定 ==")
        bess.write(BESS_GRID_VOLTAGE, 0)
        emu.write(REG_MODE_SET, EMS_MODE_Q)
        emu.write_s16(REG_REACTIVE_SET, 0)
        emu.write_s16(REG_PF_SET, 1000)
        emu.write_s16(REG_ACTIVE_SET, 0)
        settle()
        check("复位后无功模式 5137", emu.read(REG_MODE_READ), EMS_MODE_Q)
        check("复位后总无功 30062", emu.read_s16(REG_TOTAL_REACTIVE) * 0.1, 0, 1)
        check("复位后总有功 30061", emu.read_s16(REG_TOTAL_ACTIVE) * 0.1, 0, 1)
    finally:
        emu.close()
        bess.close()

    print()
    if FAILURES:
        print(f"FAILED: {len(FAILURES)} check(s): {', '.join(FAILURES)}")
        return 1
    print("ALL CHECKS PASSED")
    return 0


if __name__ == "__main__":
    sys.exit(main())
