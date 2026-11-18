"""Render the public HTML policy as a printable PDF (requires reportlab).

The public HTML is the source of truth; regenerate after changing its wording.
Run from any directory: python scripts/render_privacy_policy.py
"""

from dataclasses import dataclass, field
from html import escape
from html.parser import HTMLParser
from pathlib import Path
import re
import shutil

from reportlab.lib import colors
from reportlab.lib.enums import TA_LEFT
from reportlab.lib.pagesizes import A4
from reportlab.lib.styles import ParagraphStyle
from reportlab.platypus import (
    SimpleDocTemplate, Paragraph, Spacer, Table, TableStyle, PageBreak,
)


@dataclass
class Node:
    tag: str
    attrs: dict = field(default_factory=dict)
    children: list = field(default_factory=list)


class DocumentParser(HTMLParser):
    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.root = Node("document")
        self.stack = [self.root]

    def handle_starttag(self, tag, attrs):
        node = Node(tag, dict(attrs))
        self.stack[-1].children.append(node)
        if tag not in {"meta", "link", "br", "img", "input", "hr"}:
            self.stack.append(node)

    def handle_startendtag(self, tag, attrs):
        self.handle_starttag(tag, attrs)
        if self.stack[-1].tag == tag:
            self.stack.pop()

    def handle_endtag(self, tag):
        for i in range(len(self.stack) - 1, 0, -1):
            if self.stack[i].tag == tag:
                del self.stack[i:]
                break

    def handle_data(self, data):
        self.stack[-1].children.append(data)


def nodes(node, tag):
    for child in node.children:
        if isinstance(child, Node):
            if child.tag == tag:
                yield child
            yield from nodes(child, tag)


def inline(node):
    if isinstance(node, str):
        return escape(re.sub(r"\s+", " ", node).replace("·", " / ").replace("→", " > "))
    content = "".join(inline(c) for c in node.children)
    if node.tag in {"strong", "b"}:
        return f"<b>{content}</b>"
    if node.tag == "code":
        return f'<font face="Courier" size="8.5">{content}</font>'
    if node.tag == "br":
        return "<br/>"
    if node.tag == "a":
        return f'<a href="{escape(node.attrs.get("href", ""), quote=True)}" color="#245640"><u>{content}</u></a>'
    return content


root = Path(__file__).resolve().parents[1]
source = root / "web/public/privacy.html"
out = root / "output/pdf/location-keeper-privacy-policy.pdf"
out.parent.mkdir(parents=True, exist_ok=True)
parser = DocumentParser()
parser.feed(source.read_text())
article = next(n for n in nodes(parser.root, "article") if n.attrs.get("id") == "privacy-policy")
ink = colors.HexColor("#183f34")
body = ParagraphStyle("Body", fontName="Helvetica", fontSize=9.6, leading=14, textColor=ink, spaceAfter=9)
heading = ParagraphStyle("Heading", parent=body, fontName="Helvetica-Bold", fontSize=14, leading=18, spaceBefore=16, spaceAfter=10, keepWithNext=True)
small = ParagraphStyle("Small", parent=body, fontSize=8.4, leading=12, textColor=colors.HexColor("#53665b"))
title = ParagraphStyle("Title", parent=body, fontName="Helvetica-Bold", fontSize=31, leading=37, spaceAfter=12)
label = ParagraphStyle("Label", parent=small, fontName="Helvetica-Bold", fontSize=8.5, leading=12, spaceAfter=12)
bullet = ParagraphStyle("Bullet", parent=body, leftIndent=12, bulletIndent=0, spaceAfter=8)
cell = ParagraphStyle("Cell", parent=body, fontSize=9, leading=13, spaceAfter=0, alignment=TA_LEFT)
story = [Paragraph("LOCATION KEEPER / PRIVACY", label), Paragraph("Privacy Policy", title)]
meta = next(n for n in nodes(article, "p") if n.attrs.get("class") == "legal-meta")
story += [Paragraph(inline(meta), small), Spacer(1, 9)]
summary = next(n for n in nodes(article, "div") if n.attrs.get("class") == "legal-summary")
summary_box = Table([[Paragraph(inline(summary), body)]], colWidths=[A4[0] - 100])
summary_box.setStyle(TableStyle([("BACKGROUND", (0, 0), (-1, -1), colors.HexColor("#e9efdf")), ("BOX", (0, 0), (-1, -1), .5, colors.HexColor("#dce5d0")), ("TOPPADDING", (0, 0), (-1, -1), 13), ("BOTTOMPADDING", (0, 0), (-1, -1), 6), ("LEFTPADDING", (0, 0), (-1, -1), 14), ("RIGHTPADDING", (0, 0), (-1, -1), 14)]))
story += [summary_box, Spacer(1, 3)]
for section in nodes(article, "section"):
    h2 = next(nodes(section, "h2"))
    if h2.attrs.get("id") in {"google-permissions", "uses", "retention"}:
        story.append(PageBreak())
    for child in section.children:
        if not isinstance(child, Node):
            continue
        if child.tag == "h2":
            story.append(Paragraph(inline(child), heading))
        elif child.tag == "p":
            story.append(Paragraph(inline(child), body))
        elif child.tag == "ul":
            story.extend(Paragraph(inline(li), bullet, bulletText="-") for li in nodes(child, "li"))
        elif child.tag == "table":
            rows = []
            for row in nodes(child, "tr"):
                rows.append([Paragraph(inline(c), cell) for c in row.children if isinstance(c, Node) and c.tag in {"th", "td"}])
            table = Table(rows, colWidths=[113, A4[0] - 213], repeatRows=1, hAlign="LEFT")
            table.setStyle(TableStyle([("BACKGROUND", (0, 0), (-1, 0), colors.HexColor("#e9efdf")), ("GRID", (0, 0), (-1, -1), .4, colors.HexColor("#d9e1d1")), ("VALIGN", (0, 0), (-1, -1), "TOP"), ("TOPPADDING", (0, 0), (-1, -1), 10), ("BOTTOMPADDING", (0, 0), (-1, -1), 10), ("LEFTPADDING", (0, 0), (-1, -1), 11), ("RIGHTPADDING", (0, 0), (-1, -1), 11)]))
            story += [table, Spacer(1, 14)]


def page_chrome(canvas, doc):
    canvas.saveState()
    width, height = A4
    canvas.setStrokeColor(colors.HexColor("#d9e1d1"))
    canvas.line(50, 43, width - 50, 43)
    canvas.setFont("Helvetica", 7.5)
    canvas.setFillColor(colors.HexColor("#53665b"))
    canvas.drawString(50, 29, "Location Keeper | Privacy Policy | 18 November 2026")
    canvas.drawRightString(width - 50, 29, f"Page {doc.page}")
    if doc.page > 1:
        canvas.drawString(50, height - 29, "LOCATION KEEPER / PRIVACY")
    canvas.restoreState()


doc = SimpleDocTemplate(str(out), pagesize=A4, topMargin=48, bottomMargin=59, leftMargin=50, rightMargin=50, title="Privacy Policy | Location Keeper", author="Ratnakar Ravada")
doc.build(story, onFirstPage=page_chrome, onLaterPages=page_chrome)
shutil.copyfile(out, root / "web/public/privacy-policy.pdf")
print(out)
