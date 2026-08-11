#!/usr/bin/env python3
"""生成 ECS 的 GOAI Agent Infra 初赛 19 页方案 PPT。

页面结构对齐官方初赛模板，视觉语言沿用 ECS 蓝色品牌。运行证据仅引用
docs/demo-assets/ 中的固定资产，不读取凭证或运行时数据。
"""

from __future__ import annotations

import os
import hashlib
import re
import shutil
import subprocess
import tempfile
import urllib.request
from copy import deepcopy
from pathlib import Path

from PIL import Image
from pypdf import PdfReader
from pptx import Presentation
from pptx.dml.color import RGBColor
from pptx.enum.shapes import MSO_CONNECTOR, MSO_SHAPE
from pptx.enum.text import MSO_ANCHOR, PP_ALIGN
from pptx.util import Inches, Pt
from pptx.oxml.ns import qn
from pptx.oxml.xmlchemy import OxmlElement


ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "docs" / "ECS-Agent-Infra-初赛方案PPT-套模板版.pptx"
ASSETS = ROOT / "docs" / "demo-assets"
PDF_OUT = OUT.with_suffix(".pdf")
DEFAULT_SOFFICE_VERSION = (
    "LibreOfficeDev 26.8.0.0.alpha0 2c87e51eeaa2b413ff4ae097b2705eea1995d8e5"
)

W, H = 13.333, 7.5
NAVY = "07152F"
NAVY_2 = "0B2048"
BLUE = "246BFD"
CYAN = "16D9C6"
YELLOW = "FFD45A"
PINK = "FF5C8A"
WHITE = "F7FAFF"
MUTED = "9BB0D0"
LINE = "244B83"
CARD = "102A57"
CARD_2 = "0C244A"
GREEN = "42D392"
FONT_CN = "Source Han Sans CN"
FONT_LATIN = FONT_CN
PDF_FONT_CN = FONT_CN
PDF_FONT_FILE = ROOT / ".cache" / "presentation-fonts" / "SourceHanSansCN-Regular.otf"
PDF_FONT_URL = (
    "https://raw.githubusercontent.com/adobe-fonts/source-han-sans/2.005R/"
    "SubsetOTF/CN/SourceHanSansCN-Regular.otf"
)
PDF_FONT_SHA256 = "e2bc8a2e7f37474b774fff8db758681ece40bb6947a90d571bce9dd60671a8e4"


def rgb(value: str) -> RGBColor:
    return RGBColor.from_string(value)


def set_run(run, size=16, color=WHITE, bold=False, font=FONT_CN):
    run.font.name = font
    properties = run._r.get_or_add_rPr()
    east_asia = properties.find(qn("a:ea"))
    if east_asia is None:
        east_asia = OxmlElement("a:ea")
        properties.append(east_asia)
    east_asia.set("typeface", font)
    run.font.size = Pt(size)
    run.font.bold = bold
    run.font.color.rgb = rgb(color)


def text(slide, value, x, y, w, h, size=16, color=WHITE, bold=False,
         align=PP_ALIGN.LEFT, valign=MSO_ANCHOR.TOP, margin=0.05,
         font=FONT_CN, line_spacing=1.05):
    shape = slide.shapes.add_textbox(Inches(x), Inches(y), Inches(w), Inches(h))
    tf = shape.text_frame
    tf.clear()
    tf.margin_left = Inches(margin)
    tf.margin_right = Inches(margin)
    tf.margin_top = Inches(margin)
    tf.margin_bottom = Inches(margin)
    tf.vertical_anchor = valign
    tf.word_wrap = True
    lines = str(value).split("\n")
    for idx, line in enumerate(lines):
        p = tf.paragraphs[0] if idx == 0 else tf.add_paragraph()
        p.alignment = align
        p.line_spacing = line_spacing
        p.space_after = Pt(0)
        r = p.add_run()
        r.text = line
        set_run(r, size=size, color=color, bold=bold, font=font)
    return shape


def rect(slide, x, y, w, h, fill=CARD, line=LINE, radius=True, line_width=1):
    kind = MSO_SHAPE.ROUNDED_RECTANGLE if radius else MSO_SHAPE.RECTANGLE
    shape = slide.shapes.add_shape(kind, Inches(x), Inches(y), Inches(w), Inches(h))
    shape.fill.solid()
    shape.fill.fore_color.rgb = rgb(fill)
    shape.line.color.rgb = rgb(line)
    shape.line.width = Pt(line_width)
    if radius:
        try:
            shape.adjustments[0] = 0.08
        except (IndexError, ValueError):
            pass
    return shape


def line(slide, x1, y1, x2, y2, color=LINE, width=1.2, arrow=False):
    shape = slide.shapes.add_connector(
        MSO_CONNECTOR.STRAIGHT, Inches(x1), Inches(y1), Inches(x2), Inches(y2)
    )
    shape.line.color.rgb = rgb(color)
    shape.line.width = Pt(width)
    if arrow:
        try:
            shape.line.end_arrowhead = True
        except AttributeError:
            pass
    return shape


def circle(slide, label, x, y, diameter=0.34, fill=BLUE, size=11):
    shape = slide.shapes.add_shape(
        MSO_SHAPE.OVAL, Inches(x), Inches(y), Inches(diameter), Inches(diameter)
    )
    shape.fill.solid()
    shape.fill.fore_color.rgb = rgb(fill)
    shape.line.fill.background()
    text(slide, label, x, y + 0.005, diameter, diameter - 0.01, size=size,
         bold=True, align=PP_ALIGN.CENTER, valign=MSO_ANCHOR.MIDDLE, margin=0)
    return shape


def background(slide, page=None):
    bg = slide.background.fill
    bg.solid()
    bg.fore_color.rgb = rgb(NAVY)
    # 右上角品牌光晕。
    halo = slide.shapes.add_shape(MSO_SHAPE.OVAL, Inches(10.70), Inches(0), Inches(2.60), Inches(2.60))
    halo.fill.solid()
    halo.fill.fore_color.rgb = rgb("102E6C")
    halo.line.fill.background()
    if page is not None:
        text(slide, "GOAI 2026 · AGENT INFRA", 0.55, 0.25, 3.2, 0.25,
             size=8.5, color=CYAN, bold=True, valign=MSO_ANCHOR.MIDDLE)
        text(slide, f"{page:02}", 12.0, 0.25, 0.65, 0.25,
             size=8.5, color=MUTED, bold=True, align=PP_ALIGN.RIGHT,
             valign=MSO_ANCHOR.MIDDLE)
        line(slide, 0.55, 7.12, 12.75, 7.12, color="17477B", width=0.8)


def title(slide, heading, subtitle, page):
    background(slide, page)
    text(slide, heading, 0.6, 0.68, 11.8, 0.55, size=27, bold=True)
    text(slide, subtitle, 0.62, 1.30, 11.8, 0.35, size=12.2, color=MUTED)


def tag(slide, value, x, y, w, fill=BLUE, color=WHITE):
    rect(slide, x, y, w, 0.34, fill=fill, line=fill, radius=True, line_width=0)
    text(slide, value, x, y + 0.01, w, 0.29, size=8.8, color=color, bold=True,
         align=PP_ALIGN.CENTER, valign=MSO_ANCHOR.MIDDLE, margin=0.01)


def card(slide, heading, body, x, y, w, h, accent=BLUE, number=None,
         heading_size=15, body_size=11.5):
    rect(slide, x, y, w, h, fill=CARD_2, line=LINE)
    rect(slide, x, y, 0.06, h, fill=accent, line=accent, radius=False, line_width=0)
    if number is not None:
        circle(slide, str(number), x + 0.22, y + 0.22, fill=accent, size=9)
        hx = x + 0.68
    else:
        hx = x + 0.28
    text(slide, heading, hx, y + 0.18, w - (hx - x) - 0.22, 0.34,
         size=heading_size, bold=True)
    text(slide, body, x + 0.28, y + 0.67, w - 0.5, h - 0.82,
         size=body_size, color=MUTED, line_spacing=1.10)


def section_slide(prs, page, number, heading, score=None, note=""):
    slide = prs.slides.add_slide(prs.slide_layouts[6])
    background(slide, page)
    text(slide, f"{number:02}", 0.75, 1.10, 1.6, 1.0, size=58, color=CYAN, bold=True)
    text(slide, heading, 2.25, 1.35, 9.3, 0.85, size=37, bold=True)
    line(slide, 2.3, 2.45, 11.6, 2.45, color=BLUE, width=2.4)
    if score:
        tag(slide, f"评分权重  {score}", 2.28, 2.85, 1.72, fill=BLUE)
    if note:
        text(slide, note, 2.3, 3.55, 8.8, 1.0, size=17, color=MUTED, line_spacing=1.12)
    return slide


def picture_crop(slide, path, x, y, w, h):
    with Image.open(path) as im:
        iw, ih = im.size
    box_ratio = w / h
    image_ratio = iw / ih
    if image_ratio > box_ratio:
        shown_w = ih * box_ratio
        crop = (iw - shown_w) / iw / 2
        pic = slide.shapes.add_picture(str(path), Inches(x), Inches(y), Inches(w), Inches(h))
        pic.crop_left = crop
        pic.crop_right = crop
    else:
        shown_h = iw / box_ratio
        crop = (ih - shown_h) / ih / 2
        pic = slide.shapes.add_picture(str(path), Inches(x), Inches(y), Inches(w), Inches(h))
        pic.crop_top = crop
        pic.crop_bottom = crop
    return pic


def build():
    prs = Presentation()
    prs.slide_width = Inches(W)
    prs.slide_height = Inches(H)
    prs.core_properties.title = "ECS · GOAI Agent Infra 初赛方案"
    prs.core_properties.subject = "可授权、可验证、可追溯的可信会议督办闭环"
    prs.core_properties.author = "ECS Team"
    prs.core_properties.last_modified_by = "ECS Team"
    prs.core_properties.comments = "基于仓库可复现证据生成；未包含凭证或真实组织数据。"

    # 01 封面
    s = prs.slides.add_slide(prs.slide_layouts[6])
    background(s, 1)
    text(s, "ECS", 0.75, 1.15, 3.5, 0.85, size=48, bold=True)
    text(s, "可信会议督办闭环", 0.78, 2.10, 7.5, 0.72, size=31, bold=True)
    text(s, "让多 Agent 的每次行动都可授权、可验证、可追溯", 0.82, 2.93, 8.2, 0.45,
         size=16, color=MUTED)
    tag(s, "AgentTeams 协作层", 0.82, 3.65, 1.75, fill=BLUE)
    tag(s, "ECS 可信执行层", 2.72, 3.65, 1.85, fill=CYAN, color=NAVY)
    tag(s, "APACHE-2.0", 4.72, 3.65, 1.45, fill=YELLOW, color=NAVY)
    # 播放键品牌图形
    rect(s, 9.55, 1.50, 2.18, 2.18, fill="103D9A", line=BLUE)
    tri = s.shapes.add_shape(MSO_SHAPE.ISOSCELES_TRIANGLE, Inches(10.23), Inches(2.00), Inches(0.92), Inches(1.15))
    tri.rotation = 90
    tri.fill.solid(); tri.fill.fore_color.rgb = rgb(WHITE); tri.line.fill.background()
    text(s, "GOAI 2026 · 新智基座 Agent Infra", 0.82, 5.55, 5.6, 0.36, size=13, color=CYAN, bold=True)
    text(s, "ECS Team  ·  初赛方案  ·  ECS 0.3.0 兼容线", 0.82, 6.00, 5.8, 0.32, size=11.5, color=MUTED)

    # 02 P0
    title(s := prs.slides.add_slide(prs.slide_layouts[6]), "P0 · 一页纸速览", "用一页对齐场景、方案、差异化、复用价值和当前证据。", 2)
    items = [
        ("项目与场景", "ECS：可信会议督办闭环\n面向公共管理和企业管理中\n需要审批、派发和追责的会后行动。", BLUE),
        ("真实问题", "纪要有了，事情却没发生：\n责任人/期限缺失、高风险动作越权、\n执行结果无法证明。", PINK),
        ("核心方案", "AgentTeams 编排多 Agent；\nECS 用 Stage→Skill→Connector 执行，\n人工审批后验证 Receipt。", CYAN),
        ("创新与差异", "小模型只理解意图与补参；\n权限、Schema、状态、重试、回执和审计\n留在确定性 Go 内核。", YELLOW),
        ("开放 / 复用", "复用 Tool / Skill / Office 契约；\n提供双路径回归、AgentTeams 补丁\n与多平台交付资产；采用 Apache-2.0。", BLUE),
        ("当前进展", "4 角色 · 5 个闭环 Skill\n双路径×5 · 3 节点 Trace\nQwen3 0.6B / 1.7B 证据已留存。", GREEN),
    ]
    for i, (h, b, a) in enumerate(items):
        x = 0.62 + (i % 3) * 4.18
        y = 1.88 + (i // 3) * 2.31
        card(s, h, b, x, y, 3.82, 1.95, accent=a, heading_size=14.5, body_size=11.1)

    # 03 目录
    title(s := prs.slides.add_slide(prs.slide_layouts[6]), "目录", "按官方初赛模板的八个章节组织。", 3)
    toc = [
        ("01", "场景与价值"), ("02", "方案总览"), ("03", "多 Agent 协同设计"), ("04", "Skill 工程体系"),
        ("05", "工程落地与安全审计"), ("06", "开放 / 开源计划"), ("07", "落地计划与进展"), ("08", "团队介绍"),
    ]
    for i, (n, name) in enumerate(toc):
        x = 0.78 + (i % 2) * 6.1
        y = 1.83 + (i // 2) * 1.18
        rect(s, x, y, 5.55, 0.88, fill=CARD_2, line=LINE)
        text(s, n, x + 0.25, y + 0.18, 0.68, 0.38, size=18, color=CYAN, bold=True)
        text(s, name, x + 1.05, y + 0.19, 4.1, 0.38, size=16, bold=True)

    # 04/05 场景
    section_slide(prs, 4, 1, "场景与价值", "25%", "从会议纪要到真实行动，把“责任、执行、验证”变成可检查的管理闭环。")
    title(s := prs.slides.add_slide(prs.slide_layouts[6]), "会议纪要生成了，但事情仍然没有发生", "目标用户：需要跨部门协作、审批和结果追踪的公共管理者、企业管理者与项目团队。", 5)
    pains = [
        ("责任不完整", "责任人、期限或原文依据缺失时，\n模型容易自行补全。", PINK),
        ("动作不可控", "任务、日历和通知一旦越权，\n或发生重复执行，代价将落到真实组织。", YELLOW),
        ("结果不可证", "只有最终文本，没有审批和回执；\n失败位置与恢复过程无法核验。", BLUE),
    ]
    for i, (h, b, a) in enumerate(pains):
        card(s, h, b, 0.7 + i * 4.18, 1.92, 3.78, 1.72, accent=a, number=i + 1)
    text(s, "DEMO 验收指标", 0.78, 4.08, 2.4, 0.38, size=15, color=CYAN, bold=True)
    metrics = [("0", "未审批外部调用"), ("5×2", "成功/失败路径回归"), ("100%", "关键动作 Trace / Receipt"), ("3", "同 Trace 真实节点")]
    for i, (v, label) in enumerate(metrics):
        x = 0.76 + i * 3.08
        rect(s, x, 4.62, 2.74, 1.26, fill=CARD, line=LINE)
        text(s, v, x + 0.18, 4.78, 2.35, 0.42, size=24, color=CYAN, bold=True, align=PP_ALIGN.CENTER)
        text(s, label, x + 0.15, 5.30, 2.4, 0.3, size=10.5, color=MUTED, align=PP_ALIGN.CENTER)
    text(s, "注：上述为工程 Demo 验收数据，不是真实客户业务效益。", 0.78, 6.24, 8.8, 0.27, size=9.8, color=MUTED)

    # 06/07 方案
    section_slide(prs, 6, 2, "方案总览", None, "AgentTeams 负责协作与拆解，ECS 负责能力约束、可信执行和证据留存。")
    title(s := prs.slides.add_slide(prs.slide_layouts[6]), "AgentTeams 协作层 × ECS 可信执行层", "task_id / trace_id 贯通任务拆解、执行契约与审计证据。", 7)
    layers = [
        ("INPUT", "会议材料\n目标 / 参与人", PINK),
        ("AGENTTEAMS", "角色编排 · 任务拆解\n上下文传递 · 状态追踪", BLUE),
        ("ECS OFFICE", "Work Order · Stage\n签名 office_grant", CYAN),
        ("CAPABILITY", "Skill · JSON Schema\nConnector Tool · scope", YELLOW),
        ("TRUSTED RUN", "Approval · Attempt\nOutbox · Receipt\nVerify · Compensate", GREEN),
        ("EVIDENCE", "Session · Trace · Metrics\n回放 / 导出 / 保留策略", BLUE),
    ]
    for i, (h, b, a) in enumerate(layers):
        x = 0.55 + i * 2.08
        rect(s, x, 2.08, 1.78, 2.68, fill=CARD_2, line=a)
        tag(s, h, x + 0.17, 2.27, 1.43, fill=a, color=NAVY if a in (CYAN, YELLOW, GREEN) else WHITE)
        text(s, b, x + 0.17, 3.05, 1.43, 1.05, size=10.3, color=WHITE, align=PP_ALIGN.CENTER,
             valign=MSO_ANCHOR.MIDDLE, line_spacing=1.14)
        if i < len(layers) - 1:
            text(s, "→", x + 1.79, 3.15, 0.30, 0.35, size=20, color=CYAN, bold=True, align=PP_ALIGN.CENTER)
    text(s, "五项映射", 0.75, 5.30, 1.25, 0.3, size=12.5, color=CYAN, bold=True)
    text(s, "角色编排→Stage   任务拆解→Skill   上下文传递→签名 grant   协同执行→Stage→Skill→Connector   状态追踪→Trace",
         2.0, 5.25, 10.3, 0.55, size=11.4, color=WHITE)
    text(s, "选型原因：AgentTeams 提供赛题要求的协作基座；ECS 不重复协作框架，而是补齐真实动作的安全与恢复边界。", 0.75, 6.03, 11.8, 0.34, size=10.5, color=MUTED)

    # 08/09 Agent Identity
    section_slide(prs, 8, 3, "多 Agent 协同设计", "25%", "四个职能角色通过结构化交接完成一个闭环，高风险动作始终由人承担责任。")
    title(s := prs.slides.add_slide(prs.slide_layouts[6]), "Agent Identity 清单：四个角色、四类交付物、一条 Trace", "对齐官方附录 A；显式声明能力与决策边界。", 9)
    agents = [
        ("meeting_planner", "规划员", "会议类型、范围与参与人", "标题/与会人 → scope_plan", "meeting.scope.plan", "不执行外部动作", BLUE),
        ("minutes_clerk", "纪要员", "决定、行动项、责任人和期限", "原文 → 可追溯事实", "meeting.fact.extract", "缺失字段不自行补齐", CYAN),
        ("decision_reviewer", "核验员", "完整性、一致性和原文依据", "行动项 → blockers", "meeting.evidence.verify", "不替代人类审批", PINK),
        ("followup_coordinator", "督办员", "审批后派发、回执核验与补偿", "approved task → receipt", "dispatch + receipt.verify", "无有效 grant 则禁止调用", YELLOW),
    ]
    for i, (name, role, cap, io, dep, bound, accent) in enumerate(agents):
        x = 0.55 + i * 3.15
        rect(s, x, 1.82, 2.87, 4.48, fill=CARD_2, line=accent)
        circle(s, str(i + 1), x + 0.20, 2.02, fill=accent, size=9)
        text(s, role, x + 0.68, 2.00, 1.85, 0.3, size=15, bold=True)
        text(s, name, x + 0.22, 2.48, 2.42, 0.30, size=9.2, color=accent, bold=True, font=FONT_LATIN)
        text(s, "CAPABILITIES", x + 0.22, 2.92, 2.3, 0.22, size=8.3, color=MUTED, bold=True, font=FONT_LATIN)
        text(s, cap, x + 0.22, 3.16, 2.35, 0.64, size=10.2)
        text(s, "INPUT → OUTPUT", x + 0.22, 3.88, 2.3, 0.22, size=8.3, color=MUTED, bold=True, font=FONT_LATIN)
        text(s, io, x + 0.22, 4.12, 2.35, 0.45, size=9.7)
        text(s, "DEPENDENCY", x + 0.22, 4.65, 2.3, 0.22, size=8.3, color=MUTED, bold=True, font=FONT_LATIN)
        text(s, dep, x + 0.22, 4.89, 2.35, 0.35, size=9.2, color=CYAN, font=FONT_LATIN)
        text(s, "DECISION BOUNDARY", x + 0.22, 5.35, 2.3, 0.22, size=8.3, color=MUTED, bold=True, font=FONT_LATIN)
        text(s, bound, x + 0.22, 5.59, 2.35, 0.43, size=9.4)
    text(s, "Trace：task_id / stage_id / skill_run / tool_run 全链关联。当前四角色已拥有独立 Skill profile，但共享 executor 安全主体；独立 principal 是复赛增强项。",
         0.68, 6.47, 12.0, 0.32, size=9.5, color=MUTED)

    # 10/11 Skill
    section_slide(prs, 10, 4, "Skill 工程体系", "25%", "Skill 不是一次性角色台词，而是可验证、可版本化、可复用的稳定能力合同。")
    title(s := prs.slides.add_slide(prs.slide_layouts[6]), "五个闭环 Skill 覆盖会议督办全流程", "用 office.action.dispatch 展开完整契约；其余 Skill 遵循同一生命周期。", 11)
    skill_names = [
        ("meeting.scope.plan", "范围规划", BLUE),
        ("meeting.fact.extract", "事实提取", BLUE),
        ("meeting.evidence.verify", "证据核验", PINK),
        ("office.action.dispatch", "动作派发", YELLOW),
        ("execution.receipt.verify", "回执验证", CYAN),
    ]
    for i, (name, desc, accent) in enumerate(skill_names):
        y = 1.74 + i * 0.91
        rect(s, 0.58, y, 4.1, 0.68, fill=CARD_2, line=accent)
        text(s, f"{i+1:02}", 0.80, y + 0.15, 0.43, 0.25, size=10.3, color=accent, bold=True)
        text(s, name, 1.33, y + 0.08, 2.82, 0.26, size=11.3, bold=True, font=FONT_LATIN)
        text(s, desc, 1.33, y + 0.36, 2.3, 0.20, size=9.0, color=MUTED)
    rect(s, 4.98, 1.82, 7.72, 4.62, fill=CARD_2, line=YELLOW)
    tag(s, "office.action.dispatch  v1.0.0", 5.25, 2.04, 2.72, fill=YELLOW, color=NAVY)
    specs = [
        ("用途 / 关系", "followup_coordinator 在人工批准后创建会后行动"),
        ("输入 / 输出", "verified action + target + idempotency_key → structured receipt"),
        ("调用条件", "evidence verified ∧ office_grant valid ∧ approval approved"),
        ("依赖工具", "office_action_dispatch @builtin；Connector 可替换"),
        ("失败处理", "超时/确定失败→补偿；结果未知→recovery_required 幂等恢复"),
        ("安全边界", "scope + JSON Schema + 签名 grant + 人工审批；禁止模型自授权"),
        ("验证 / 复用", "receipt verifier + Trace；可迁移到工单、日历、通知与 MCP adapter"),
    ]
    for i, (k, v) in enumerate(specs):
        y = 2.63 + i * 0.49
        text(s, k, 5.28, y, 1.35, 0.26, size=9.6, color=CYAN, bold=True)
        text(s, v, 6.66, y, 5.68, 0.32, size=9.6, color=WHITE)
    text(s, "生命周期：SKILL.md + skill.json → Manifest/Handler 一致性校验 → 固定用例 → 版本兼容声明 → 可回滚交付",
         0.72, 6.60, 11.9, 0.28, size=9.4, color=MUTED)

    # 12/13 工程证据
    section_slide(prs, 12, 5, "工程落地、运行验证与安全可审计", "20%", "批准前不执行；执行后有回执；结果未知时不伪装成功。")
    title(s := prs.slides.add_slide(prs.slide_layouts[6]), "四张运行画面，对应四个可检查边界", "固定回归证据与实时三节点 Trace 分别标注，不将 fixture 伪装成现场运行。", 13)
    shots = [
        ("01  OFFICE 建单 / 派发", ASSETS / "office-control-console.png", "固定回归数据集"),
        ("02  人工审批门", ASSETS / "approval-and-handoff.png", "固定回归数据集"),
        ("03  证据时间线", ASSETS / "evidence-timeline.png", "固定回归数据集"),
        ("04  三节点 TRACE 树", ASSETS / "cross-node-trace.png", "2026-08-07\n实时三节点回归"),
    ]
    for i, (label, path, source) in enumerate(shots):
        x = 0.55 + (i % 2) * 6.28
        y = 1.78 + (i // 2) * 2.38
        rect(s, x, y, 5.98, 2.16, fill=CARD_2, line=LINE)
        picture_crop(s, path, x + 0.12, y + 0.40, 3.82, 1.57)
        text(s, label, x + 0.13, y + 0.10, 4.6, 0.22, size=9.5, color=CYAN, bold=True)
        text(s, source, x + 4.10, y + 0.65, 1.65, 0.52, size=9.4, color=WHITE, bold=True, align=PP_ALIGN.CENTER)
        text(s, "来源已标注\n可核验画面", x + 4.10, y + 1.27, 1.65, 0.48, size=8.8, color=MUTED, align=PP_ALIGN.CENTER)
    text(s, "证据索引：docs/demo-assets/README.md · 固定回归 evidence/meeting-followup/reference · 三节点 trace=f18684b48bc024429d92c492",
         0.68, 6.64, 11.9, 0.27, size=8.8, color=MUTED)

    # 14/15 开放与合规
    section_slide(prs, 14, 6, "开放 / 开源计划", "5%", "不用“开源”包装私有项目；用可评审交付包、稳定契约与可复现证据提供复用价值。")
    title(s := prs.slides.add_slide(prs.slide_layouts[6]), "开放边界、外部集成与模型使用全部显式披露", "项目按 Apache-2.0 开源；可复用价值来自契约和证据，而不是推荐组件的数量。", 15)
    disclosures = [
        ("授权与依赖", "LICENSE 采用 Apache-2.0；NOTICE、分享规则、\nTHIRD_PARTY_NOTICES 与关键版本已进仓。", BLUE),
        ("MCP 适配边界", "当前不运行 MCP Server。可保留 capability、Skill、权限与\nReceipt 合同新增 adapter；MCP 传输、会话、工具发现和鉴权\n映射仍需实现与验证。", CYAN),
        ("官方 Skills 复用", "不为堆叠工具而强接。ECS Skill 保留 JSON Schema、scope、\ngrant 与 Receipt 边界；接入阿里云官方用云 Skills 时通过\nConnector adapter 复用。", YELLOW),
        ("模型、数据与可替换性", "在线演示可配置 OpenAI-compatible 服务；交付包留存 Qwen3\n0.6B/1.7B 本地评测证据。安装包不嵌入 Key；凭证由使用者\n在设备安全存储中配置并负责轮换。", PINK),
    ]
    for i, (h, b, a) in enumerate(disclosures):
        x = 0.62 + (i % 2) * 6.16
        y = 1.84 + (i // 2) * 2.13
        card(s, h, b, x, y, 5.70, 1.82, accent=a, heading_size=14, body_size=10.7)
    text(s, "生产化边界：Office Connector 仍为 local-task:// 演示实现；真实组织系统、TLS/mTLS、外部 Tool 沙箱与独立 Agent principal 不伪装为已完成。",
         0.75, 6.38, 11.8, 0.36, size=10, color=MUTED)

    # 16/17 计划
    section_slide(prs, 16, 7, "落地计划与进展", None, "初赛交付已从“方案”前移到“可运行证据”；复赛聚焦真实组织集成和独立安全主体。")
    title(s := prs.slides.add_slide(prs.slide_layouts[6]), "从可复现闭环，走向真实组织试点", "每个里程碑都用运行入口、证据和失败边界验收。", 17)
    phases = [
        ("初赛 · 已完成", "main 交付快照\n兼容 ECS 0.3.0\n双路径×5 · 三节点 Trace\nQwen 运行证据", GREEN),
        ("复赛 · 增强", "真实组织 Connector\n独立 Agent principal\nAgent Identity 运行验证\n现场/断网手册", BLUE),
        ("决赛 · 试点", "至少 1 位用户反馈\n实名审批与工单\n评测基线与成本\n可验证评审包", YELLOW),
    ]
    for i, (h, b, a) in enumerate(phases):
        x = 0.75 + i * 4.15
        card(s, h, b, x, 2.0, 3.72, 3.20, accent=a, number=i + 1, heading_size=15, body_size=12.2)
        if i < 2:
            text(s, "→", x + 3.72, 3.28, 0.42, 0.42, size=24, color=CYAN, bold=True, align=PP_ALIGN.CENTER)
    text(s, "主要风险", 0.78, 5.65, 1.4, 0.30, size=12.2, color=PINK, bold=True)
    text(s, "真实 Connector 接入延迟 → 保留幂等 Mock/回放路径   ·   会场网络不可用 → 本地 Qwen + 预录视频   ·   密钥/数据泄漏 → 独立凭证与脱敏交付包",
         2.16, 5.58, 10.1, 0.63, size=10.5, color=WHITE)

    # 18/19 团队
    section_slide(prs, 18, 8, "团队介绍", None, "以 Git 提交、GitHub Issue 和 PR 为事实口径，不填写无法验证的学历或职业背景。")
    title(s := prs.slides.add_slide(prs.slide_layouts[6]), "架构与实现 × 独立评审与质量治理", "两人团队用“快速实现 → 证据审计 → 边界修正 → 回归交付”的方式协作。", 19)
    team = [
        ("项目负责人", "架构与全栈实现", "107 条 Git 作者提交", [
            "设计并实现分布式 Go 内核、Agent/Skill/Office 分层",
            "落地关系授权、审批、Receipt、补偿与 durable execution",
            "完成 Android / iOS / HarmonyOS / Windows / Browser / Linux 交付",
            "负责 CI、AgentTeams E2E、小模型回归、Demo 与部署",
        ], BLUE),
        ("质量评审贡献者", "安全与交付质量", "9 提交 · 20 Issue · 4 PR（3 合并）", [
            "持续审计授权、跨节点能力收窄、Trace 与构建复现性",
            "发现并推动修复安全 P1、Manifest/Handler 漂移与文档失真",
            "建立 AGENTS/Harness 治理入口与仓库自检规约",
            "加固 PPT 字体、版式、备注安全和运行证据来源",
        ], CYAN),
    ]
    for i, (name, role, metric, bullets, accent) in enumerate(team):
        x = 0.70 + i * 6.28
        rect(s, x, 1.90, 5.86, 4.58, fill=CARD_2, line=accent)
        circle(s, str(i + 1), x + 0.27, 2.18, 0.45, fill=accent, size=10)
        text(s, name, x + 0.88, 2.10, 4.25, 0.36, size=19, bold=True)
        text(s, role, x + 0.30, 2.72, 5.1, 0.30, size=11.5, color=accent, bold=True)
        tag(s, metric, x + 0.30, 3.18, 3.15 if i == 0 else 4.15, fill=accent,
            color=NAVY if accent == CYAN else WHITE)
        for j, bullet in enumerate(bullets):
            circle(s, "", x + 0.32, 3.82 + j * 0.58, 0.12, fill=accent, size=1)
            text(s, bullet, x + 0.58, 3.70 + j * 0.58, 4.86, 0.50, size=10.4, color=WHITE)
    text(s, "统计基线 main@a3de47a（2026-08-09）；贡献统计已按作者身份归并。团队职责仅基于可核验贡献，不外推个人背景。",
         0.76, 6.67, 11.8, 0.25, size=8.8, color=MUTED)

    assert len(prs.slides) == 19, len(prs.slides)
    OUT.parent.mkdir(parents=True, exist_ok=True)
    prs.save(OUT)
    print(OUT)


def refresh_existing_roadshow_copy():
    """同步修正旧路演 deck 中已过期的许可和 MCP 表述。"""
    replacements = {
        "SKILL.md / 本地 RAG 已接入 · MCP 按需适配":
            "SKILL.md Grounding 已接入 · Connector 契约可迁移 MCP",
        "四个核心 Skill 覆盖提取、核验、派发和回执":
            "五个闭环 Skill 覆盖规划、提取、核验、派发和回执",
        "SKILL.md Grounding 已接入 · Connector 契约可迁移 MCP":
            "Connector 合同已稳定 · MCP adapter 仍需实现与验证",
    }
    decks = [
        ROOT / "docs" / "ECS-Agent-Infra-可信会议督办闭环.pptx",
        ROOT / "docs" / "ECS-Agent-Infra-可信会议督办闭环-路演备注版.pptx",
    ]
    for deck in decks:
        if not deck.is_file():
            print(f"{deck}: skipped (not included in this source archive)")
            continue
        prs = Presentation(deck)
        changed = 0
        if prs.core_properties.author != "ECS Team":
            prs.core_properties.author = "ECS Team"
            changed += 1
        if prs.core_properties.last_modified_by != "ECS Team":
            prs.core_properties.last_modified_by = "ECS Team"
            changed += 1

        def replace_text(shape, value):
            nonlocal changed
            runs = [run for paragraph in shape.text_frame.paragraphs for run in paragraph.runs]
            if not runs:
                shape.text = value
            else:
                runs[0].text = value
                for run in runs[1:]:
                    run.text = ""
            changed += 1

        def set_font_size(shape, points):
            shape.text_frame.word_wrap = True
            for paragraph in shape.text_frame.paragraphs:
                for run in paragraph.runs:
                    run.font.size = Pt(points)

        for slide in prs.slides:
            for shape in slide.shapes:
                if not getattr(shape, "has_text_frame", False):
                    continue
                for paragraph in shape.text_frame.paragraphs:
                    for run in paragraph.runs:
                        if run.text in replacements:
                            run.text = replacements[run.text]
                            changed += 1

        skill_slide = prs.slides[6]
        skill_shapes = {
            shape.text.strip(): shape
            for shape in skill_slide.shapes
            if getattr(shape, "has_text_frame", False) and shape.text.strip()
        }
        if "四个核心 Skill 承接会议督办闭环" in skill_shapes:
            replace_text(skill_shapes["四个核心 Skill 承接会议督办闭环"], "五个闭环 Skill 承接会议督办全流程")

        if "meeting.scope.plan" not in skill_shapes:
            by_id = {shape.shape_id: shape for shape in skill_slide.shapes}
            source_ids = (8, 9, 10, 11, 12)
            next_id = max(by_id)
            cloned_ids = []
            for source_id in source_ids:
                clone = deepcopy(by_id[source_id]._element)
                next_id += 1
                clone.xpath(".//p:cNvPr")[0].set("id", str(next_id))
                clone.xpath(".//p:cNvPr")[0].set("name", f"scope-plan-{next_id}")
                skill_slide.shapes._spTree.insert_element_before(clone, "p:extLst")
                cloned_ids.append(next_id)
            by_id = {shape.shape_id: shape for shape in skill_slide.shapes}
            row_groups = [
                [by_id[shape_id] for shape_id in cloned_ids],
                [by_id[shape_id] for shape_id in (8, 9, 10, 11, 12)],
                [by_id[shape_id] for shape_id in (13, 14, 15, 16, 17)],
                [by_id[shape_id] for shape_id in (18, 19, 20, 21, 22)],
                [by_id[shape_id] for shape_id in (23, 24, 25, 26, 27)],
            ]
            row_copy = [
                ("01", "meeting.scope.plan", "范围规划", "标题 / 与会人 → scope_plan"),
                ("02", "meeting.fact.extract", "会议事实提取", "原始材料 → 决策 / 行动项 / 待确认项"),
                ("03", "meeting.evidence.verify", "证据核验", "草稿 → 责任人 / 日期 / 原文依据"),
                ("04", "office.action.dispatch", "办公动作派发", "核验结果 → 任务 / 日历 / 通知"),
                ("05", "execution.receipt.verify", "执行回执验证", "外部回执 → 完整性 / 幂等 / 成功判定"),
            ]
            for index, (shapes, values) in enumerate(zip(row_groups, row_copy)):
                number, background, name, description, contract = shapes
                base_y = 2.14 + index * 0.82
                number.top = Inches(base_y + 0.18)
                number.height = Inches(0.25)
                background.top = Inches(base_y)
                background.height = Inches(0.67)
                name.top = Inches(base_y + 0.11)
                name.height = Inches(0.24)
                description.top = Inches(base_y + 0.11)
                description.height = Inches(0.25)
                contract.top = Inches(base_y + 0.10)
                contract.height = Inches(0.42)
                for shape, value in zip((number, name, description, contract), values):
                    replace_text(shape, value)
                set_font_size(number, 11.0)
                set_font_size(name, 11.0)
                set_font_size(description, 12.5)
                set_font_size(contract, 10.5)
        if changed:
            prs.save(deck)
        print(f"{deck}: replacements={changed}")


def export_pdf():
    """Convert the freshly generated deck with the pinned LibreOffice build."""
    font_file = Path(os.environ.get("ECS_GOAI_PDF_FONT_FILE", PDF_FONT_FILE))
    if not font_file.is_file() and font_file == PDF_FONT_FILE:
        font_file.parent.mkdir(parents=True, exist_ok=True)
        temporary_font = font_file.with_suffix(".download")
        try:
            urllib.request.urlretrieve(PDF_FONT_URL, temporary_font)
            temporary_font.replace(font_file)
        finally:
            temporary_font.unlink(missing_ok=True)
    if not font_file.is_file():
        raise RuntimeError(f"required PDF export font is missing: {font_file}")
    font_digest = hashlib.sha256(font_file.read_bytes()).hexdigest()
    if font_digest != PDF_FONT_SHA256:
        raise RuntimeError(
            f"PDF export font changed: expected {PDF_FONT_SHA256}, found {font_digest}"
        )
    soffice = shutil.which("soffice")
    if not soffice:
        raise RuntimeError("soffice is required to generate the submission PDF")
    expected = os.environ.get("ECS_GOAI_SOFFICE_VERSION", DEFAULT_SOFFICE_VERSION)
    actual = subprocess.run(
        [soffice, "--version"], check=True, capture_output=True, text=True
    ).stdout.strip()
    if actual != expected:
        raise RuntimeError(f"expected soffice {expected!r}; found {actual!r}")
    with tempfile.TemporaryDirectory(prefix="ecs-goai-fontconfig-") as temporary:
        fontconfig_root = Path(temporary)
        cache = fontconfig_root / "cache"
        cache.mkdir()
        config = fontconfig_root / "fonts.conf"
        config.write_text(
            '<?xml version="1.0"?>\n'
            '<!DOCTYPE fontconfig SYSTEM "fonts.dtd">\n'
            '<fontconfig>\n'
            '  <dir>' + str(font_file.parent) + '</dir>\n'
            '  <match target="pattern">\n'
            '    <test name="family" qual="any"><string>' + FONT_CN + '</string></test>\n'
            '    <edit name="family" mode="assign"><string>' + PDF_FONT_CN + '</string></edit>\n'
            '  </match>\n'
            '  <cachedir>' + str(cache) + '</cachedir>\n'
            '</fontconfig>\n'
        )
        environment = {**os.environ, "FONTCONFIG_FILE": str(config)}
        subprocess.run(
            [soffice, "--headless", "--convert-to", "pdf", "--outdir", str(OUT.parent), str(OUT)],
            check=True,
            env=environment,
        )
    reader = PdfReader(PDF_OUT)
    pages = len(reader.pages)
    pdf_fonts = set()

    def collect_used_fonts(resources, content):
        data = content.get_data() if content is not None else b""
        for font_key, font_ref in resources.get("/Font", {}).items():
            pattern = re.escape(str(font_key).encode()) + rb"\s+[0-9.]+\s+Tf"
            if re.search(pattern, data):
                font_name = str(font_ref.get_object().get("/BaseFont", ""))
                if font_name:
                    pdf_fonts.add(font_name)
        for object_key, object_ref in resources.get("/XObject", {}).items():
            use_pattern = re.escape(str(object_key).encode()) + rb"\s+Do"
            if not re.search(use_pattern, data):
                continue
            obj = object_ref.get_object()
            if obj.get("/Subtype") == "/Form":
                collect_used_fonts(obj.get("/Resources", {}), obj)

    for page in reader.pages:
        collect_used_fonts(page.get("/Resources", {}), page.get_contents())
    if not any("SourceHanSansCN" in name for name in pdf_fonts):
        raise RuntimeError(f"PDF did not embed the pinned CJK font: {sorted(pdf_fonts)}")
    substitutions = ("Liberation", "LinuxLibertine", "FrankRuehl")
    if any(marker in name for name in pdf_fonts for marker in substitutions):
        raise RuntimeError(f"PDF contains substituted presentation fonts: {sorted(pdf_fonts)}")
    if len(Presentation(OUT).slides) != 19 or pages != 19:
        raise RuntimeError(f"PPTX/PDF page mismatch: pptx=19 pdf={pages}")
    print(PDF_OUT)


if __name__ == "__main__":
    build()
    refresh_existing_roadshow_copy()
    export_pdf()
