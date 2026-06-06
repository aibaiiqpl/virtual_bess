#!/usr/bin/env python3
# 用途：把真实设备 CID（SCL）转换成 libiec61850 ConfigFileParser 能加载的 .cfg 静态模型。
# 背景：go-bindings/iec61850 运行时只吃 .cfg（CreateModelFromConfigFileEx），不直接吃 SCL；
#       genmodel 只产出 C 静态模型。因此用本脚本把 CID 全模型（LD/LN(带prefix)/DO/DA，含嵌套
#       Struct）展开成 .cfg，保证 virtual_bess 暴露的 61850 模型与现场 CID 逐字段对齐。
# 不生成 DataSet / ReportControl / GoCB：GOOSE 由 Go 端独立 publisher 按 dsGOOSE1 顺序发布，
#       MMS 走直接读写，二者都不依赖模型内的控制块。
#
# 用法: python3 tools/gen_iec61850_model.py docs/IES1000_IES900_CO_V2.5.cid iec61850_model.cfg

import sys
import xml.etree.ElementTree as ET

# bType -> libiec61850 DataAttributeType 枚举值（见 iec61850_model.h）
BTYPE = {
    "BOOLEAN": 0, "INT8": 1, "INT16": 2, "INT32": 3, "INT64": 4, "INT128": 5,
    "INT8U": 6, "INT16U": 7, "INT24U": 8, "INT32U": 9,
    "FLOAT32": 10, "FLOAT64": 11, "Enum": 12,
    "Octet64": 13, "Octet6": 14, "Octet8": 15,
    "VisString32": 16, "VisString64": 17, "VisString65": 18,
    "VisString129": 19, "VisString255": 20, "Unicode255": 21,
    "Timestamp": 22, "Quality": 23, "Check": 24, "CodedEnum": 25,
    "Struct": 27, "EntryTime": 28,
}
# FunctionalConstraint -> 枚举值（见 iec61850_common.h）
FC = {
    "ST": 0, "MX": 1, "SP": 2, "SV": 3, "CF": 4, "DC": 5, "SG": 6, "SE": 7,
    "SR": 8, "OR": 9, "BL": 10, "EX": 11, "CO": 12, "US": 13, "MS": 14,
    "RP": 15, "BR": 16, "LG": 17, "GO": 18,
}
# ctlModel 字符串 -> CtlModelKind 枚举值
CTLMODEL = {
    "status-only": 0, "direct-with-normal-security": 1,
    "sbo-with-normal-security": 2, "direct-with-enhanced-security": 3,
    "sbo-with-enhanced-security": 4,
}
CONSTRUCTED = 27


def strip_ns(root):
    for e in root.iter():
        if "}" in e.tag:
            e.tag = e.tag.split("}", 1)[1]


def trg_ops(da):
    """根据 dchg/qchg/dupd 计算 triggerOptions 位掩码（dchg=1 qchg=2 dupd=4）。"""
    v = 0
    if da.get("dchg") == "true":
        v |= 1
    if da.get("qchg") == "true":
        v |= 2
    if da.get("dupd") == "true":
        v |= 4
    return v


class Gen:
    def __init__(self, root):
        dtt = root.find("DataTypeTemplates")
        self.lnodetype = {x.get("id"): x for x in dtt.iter("LNodeType")}
        self.dotype = {x.get("id"): x for x in dtt.iter("DOType")}
        self.datype = {x.get("id"): x for x in dtt.iter("DAType")}
        self.out = []

    def emit(self, depth, text):
        # libiec61850 的 ConfigFileParser 对行首空白敏感，每行必须顶格，不能缩进。
        del depth
        self.out.append(text)

    def bda_lines(self, datype_id, fc, depth):
        """递归展开 DAType 的 BDA 列表（用于 Struct 类型 DA），FC 继承父 DA。"""
        dat = self.datype.get(datype_id)
        if dat is None:
            return
        for bda in dat.findall("BDA"):
            self.da_line(bda, fc, depth, is_bda=True)

    def da_line(self, da, fc, depth, is_bda=False):
        name = da.get("name")
        btype = da.get("bType")
        trg = 0 if is_bda else trg_ops(da)
        if btype == "Struct":
            self.emit(depth, f"DA({name} 0 {CONSTRUCTED} {fc} {trg} 0){{")
            self.bda_lines(da.get("type"), fc, depth + 1)
            self.emit(depth, "}")
            return
        tcode = BTYPE.get(btype)
        if tcode is None:
            raise SystemExit(f"未知 bType: {btype} (DA={name})")
        self.emit(depth, f"DA({name} 0 {tcode} {fc} {trg} 0);")

    def do_block(self, do_name, dotype_id, ctlmodel_val, depth):
        dot = self.dotype.get(dotype_id)
        if dot is None:
            raise SystemExit(f"DOType 缺失: {dotype_id}")
        self.emit(depth, f"DO({do_name} 0){{")
        for child in dot:
            if child.tag == "DA":
                fc = FC[child.get("fc")]
                name = child.get("name")
                if name == "ctlModel" and ctlmodel_val is not None:
                    # 控制模型必须落默认值，否则 direct-with-normal-security 控制不可操作
                    self.emit(depth + 1, f"DA(ctlModel 0 {BTYPE['Enum']} {fc} 0 0)={ctlmodel_val};")
                else:
                    self.da_line(child, fc, depth + 1)
            elif child.tag == "SDO":
                # 本 CID 未使用 SDO；保留分支以便将来扩展
                raise SystemExit("遇到 SDO，当前转换器未实现")
        self.emit(depth, "}")

    def ln_block(self, ln, depth):
        prefix = ln.get("prefix") or ""
        cls = ln.get("lnClass")
        inst = ln.get("inst") or ""
        ln_name = f"{prefix}{cls}{inst}"
        lnt = self.lnodetype.get(ln.get("lnType"))
        if lnt is None:
            raise SystemExit(f"LNodeType 缺失: {ln.get('lnType')}")
        # 实例侧 DOI 的 ctlModel 默认值（按 DO 名归集）
        ctlmodels = {}
        for doi in ln.findall("DOI"):
            for dai in doi.iter("DAI"):
                if dai.get("name") == "ctlModel":
                    val = dai.find("Val")
                    if val is not None and val.text in CTLMODEL:
                        ctlmodels[doi.get("name")] = CTLMODEL[val.text]
        self.emit(depth, f"LN({ln_name}){{")
        for do in lnt.findall("DO"):
            self.do_block(do.get("name"), do.get("type"),
                          ctlmodels.get(do.get("name")), depth + 1)
        self.emit(depth, "}")

    def generate(self, root, only_lds=None):
        ied = root.find("IED")
        ied_name = ied.get("name")
        self.emit(0, f"MODEL({ied_name}){{")
        for ld in root.iter("LDevice"):
            if only_lds and ld.get("inst") not in only_lds:
                continue
            self.emit(1, f"LD({ld.get('inst')}){{")
            for ln in list(ld):
                if ln.tag in ("LN", "LN0"):
                    self.ln_block(ln, 2)
            self.emit(1, "}")
        self.emit(0, "}")
        return "\n".join(self.out) + "\n"


def main():
    if len(sys.argv) != 3:
        raise SystemExit("用法: gen_iec61850_model.py <cid> <out.cfg>")
    cid, out = sys.argv[1], sys.argv[2]
    root = ET.parse(cid).getroot()
    strip_ns(root)
    only = None
    env_only = __import__("os").environ.get("ONLY_LDS")
    if env_only:
        only = set(env_only.split(","))
    text = Gen(root).generate(root, only_lds=only)
    with open(out, "w", encoding="utf-8") as f:
        f.write(text)
    print(f"generated {out}: {len(text.splitlines())} lines")


if __name__ == "__main__":
    main()
