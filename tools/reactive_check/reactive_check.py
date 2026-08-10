#!/usr/bin/env python3
"""AWS 测试机上验证 emu-rs ←→ virtual_bess 的 PCS 无功链路。

覆盖三种无功模式：恒定无功、恒定功率因数、Q-U。
下行走 emu 北向 Modbus（1501，slave 1）→ IEC61850 MMS → virtual_bess；
回读同时看 emu 北向遥测和 virtual_bess 自己的 PCS 寄存器，确认两端一致。
Q-U 需要的电压偏差通过 virtual_bess 的 Modbus 口（18502，PCS slave 1）强制。

注意：本机 ems_II.service 每 2 秒把有功设定写成 0，有功功率无法在测试期间保持非零，
因此恒定功率因数模式只验证设定值传递链路与「有功为 0 时无功为 0」，
无功随有功成比例的数值、以及带符号的功率因数回读（有功为 0 时恒为 0，无法判别方向）
由 battery_reactive_test.go 的单元测试覆盖。
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
REG_TOTAL_ACTIVE = 30061  # S16 0.1kW
REG_TOTAL_REACTIVE = 30062  # S16 0.1kVAr
REG_POWER_FACTOR = 30060  # S16 0.01

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
        print("== 0. 基线：清空强制电压，回到恒定无功、无功设定 0 ==")
        bess.write(BESS_GRID_VOLTAGE, 0)
        emu.write(REG_MODE_SET, 0)
        emu.write_s16(REG_REACTIVE_SET, 0)
        settle()
        print(f"  有功遥测 30061 = {emu.read_s16(REG_TOTAL_ACTIVE) * 0.1} kW"
              f"（ems_II 持续下发 0，预期即为 0）")

        print("== 1. 恒定无功：5007=0，30014=200（20kVAr 感性）==")
        emu.write_s16(REG_REACTIVE_SET, 200)
        settle()
        check("无功模式回读 5137", emu.read(REG_MODE_READ), 0)
        check("PCS 侧无功指令 30014 (kVAr)", bess.read_s16(BESS_REACTIVE_CMD) * 0.1, 20, 0.1)
        check("emu 总无功 30062 (kVAr)", emu.read_s16(REG_TOTAL_REACTIVE) * 0.1, 20, 1.0)
        check("PCS 总无功 30062 (kVAr)", bess.read_s16(BESS_TOTAL_REACTIVE) * 0.1, 20, 1.0)

        print("== 2. 恒定无功：30014=-150（15kVAr 容性）==")
        emu.write_s16(REG_REACTIVE_SET, -150)
        settle()
        check("emu 总无功 30062 (kVAr)", emu.read_s16(REG_TOTAL_REACTIVE) * 0.1, -15, 1.0)

        print("== 3. 恒定功率因数：5007=1，5006=900（PF 0.9 感性）==")
        emu.write(REG_MODE_SET, 1)
        emu.write_s16(REG_PF_SET, 900)
        settle()
        check("无功模式回读 5137", emu.read(REG_MODE_READ), 1)
        check("PCS 侧功率因数设定 30016", bess.read_s16(BESS_PF_CMD) * 0.001, 0.9, 0.001)
        check("PCS 侧无功模式 30015", bess.read(BESS_MODE_CMD), 1)
        active = emu.read_s16(REG_TOTAL_ACTIVE) * 0.1
        reactive = emu.read_s16(REG_TOTAL_REACTIVE) * 0.1
        # Q = |P| * tan(acos 0.9) = |P| * 0.4843；本机 P 被 ems_II 压在 0，故预期 0。
        check("总无功跟随有功和 PF", reactive, abs(active) * 0.4843, 1.0)
        check("恒定 PF 模式忽略旧的无功设定", reactive, 0, 1.0)

        print("== 4. 恒定功率因数：5006=-900（PF -0.9 容性）==")
        emu.write_s16(REG_PF_SET, -900)
        settle()
        check("PCS 侧功率因数设定 30016", bess.read_s16(BESS_PF_CMD) * 0.001, -0.9, 0.001)

        print(f"== 5. Q-U：5007=2，电压 {PCS_NOMINAL_V:.1f}V（额定）在死区内 ==")
        emu.write(REG_MODE_SET, 2)
        bess.write(BESS_GRID_VOLTAGE, int(PCS_NOMINAL_V * 10))
        settle()
        check("无功模式回读 5137", emu.read(REG_MODE_READ), 2)
        check("PCS 侧无功模式 30015", bess.read(BESS_MODE_CMD), 2)
        check("死区内 emu 总无功 30062", emu.read_s16(REG_TOTAL_REACTIVE) * 0.1, 0, 0.5)

        print("== 6. Q-U：过压 +3% ⇒ 约 50% 额定无功，感性 ==")
        bess.write(BESS_GRID_VOLTAGE, int(PCS_NOMINAL_V * 1.03 * 10))
        settle()
        check("过压 emu 总无功 30062", emu.read_s16(REG_TOTAL_REACTIVE) * 0.1,
              0.5 * PCS_RATED_KW, 60)

        print("== 7. Q-U：欠压 -3% ⇒ 约 50% 额定无功，容性 ==")
        bess.write(BESS_GRID_VOLTAGE, int(PCS_NOMINAL_V * 0.97 * 10))
        settle()
        check("欠压 emu 总无功 30062", emu.read_s16(REG_TOTAL_REACTIVE) * 0.1,
              -0.5 * PCS_RATED_KW, 60)

        print("== 8. Q-U：欠压饱和 -10% ⇒ 满额定无功，容性 ==")
        bess.write(BESS_GRID_VOLTAGE, int(PCS_NOMINAL_V * 0.90 * 10))
        settle()
        check("饱和 emu 总无功 30062", emu.read_s16(REG_TOTAL_REACTIVE) * 0.1,
              -PCS_RATED_KW, 30)

        print("== 9. 复位：恒定无功、设定清零、电压恢复额定 ==")
        bess.write(BESS_GRID_VOLTAGE, 0)
        emu.write(REG_MODE_SET, 0)
        emu.write_s16(REG_REACTIVE_SET, 0)
        emu.write_s16(REG_PF_SET, 1000)
        settle()
        check("复位后无功模式 5137", emu.read(REG_MODE_READ), 0)
        check("复位后总无功 30062", emu.read_s16(REG_TOTAL_REACTIVE) * 0.1, 0, 0.5)
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
