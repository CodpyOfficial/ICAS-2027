"""Generate the downloadable promotional files of the ICAS website.

Outputs (all under web/static/files/):
  promo/icas-emblem.svg                  vector emblem (copy)
  promo/icas-logo-with-year.png          emblem + wordmark with the edition year, 1600x560, transparent
  promo/icas-logo.png                    emblem + wordmark without year, 1600x560, transparent
  promo/icas-logo-pack.zip               the three files above
  promo/icas-x-header-1500x500.png       X (Twitter) header
  promo/icas-x-post-1600x900.png         X post, large
  promo/icas-x-post-1200x675.png         X post
  promo/icas-linkedin-cover-1128x191.png LinkedIn cover
  promo/icas-linkedin-post-1200x627.png  LinkedIn post
  promo/icas-square-post-1080x1080.png   square post
  ICAS-CFP-flyer.pdf                     one-page A4 call-for-papers flyer
  ICAS-slide-template.pptx               official 16:9 presentation template (mandatory)
and web/static/img/og-image.png, the 1200x630 social preview of the site.

Everything is rendered from HTML with headless Microsoft Edge or Google
Chrome, so the images use the same fonts and emblem as the website. Text
(dates, venue, slot counts, topics, requirements) is read from
content/site.json: re-run this script after editing it.

Requirements: Python 3.9+, Pillow, python-pptx, and Edge or Chrome.
Usage:  python tools/make_assets.py [--browser PATH]
"""

import argparse
import html
import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
import zipfile
from pathlib import Path

from PIL import Image

ROOT = Path(__file__).resolve().parent.parent
STATIC = ROOT / "web" / "static"
FILES = STATIC / "files"
PROMO = FILES / "promo"
EMBLEM = STATIC / "img" / "icas-emblem.svg"
MASCOT = STATIC / "img" / "mascot-paper.png"

BROWSER_CANDIDATES = [
    r"C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe",
    r"C:\Program Files\Microsoft\Edge\Application\msedge.exe",
    r"C:\Program Files\Google\Chrome\Application\chrome.exe",
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
    "/usr/bin/google-chrome", "/usr/bin/chromium", "/usr/bin/chromium-browser", "/usr/bin/microsoft-edge",
]

FONTS = ("@import url('https://fonts.googleapis.com/css2?family=Montserrat:wght@500;600;700;800"
         "&family=Open+Sans:wght@400;600;700&display=block');")

BASE_CSS = FONTS + """
:root { --navy:#142c55; --navy9:#0a1628; --navy7:#1d3d72; --coral:#e8694b; --rose:#f6e4df; --ink:#1b2533; --muted:#5b6878; }
* { box-sizing: border-box; margin: 0; padding: 0; }
html, body { overflow: hidden; }
body { font-family: 'Montserrat', 'Segoe UI', Arial, sans-serif; color: var(--ink); }
.dark { color: #fff;
  background: radial-gradient(900px 500px at 85% 20%, rgba(232,105,75,.30), transparent 60%),
              linear-gradient(135deg, var(--navy9) 0%, var(--navy) 60%, var(--navy7) 100%); }
.acr { font-weight: 800; letter-spacing: .02em; line-height: 1; }
.acr span { color: var(--coral); }
.coral { color: var(--coral); }
.pill { display: inline-block; border: 1px solid rgba(255,255,255,.25); background: rgba(255,255,255,.07); border-radius: 12px; }
"""


def esc(s):
    return html.escape(str(s), quote=True)


def show(p):
    """Path relative to the project when possible, for progress output."""
    try:
        return Path(p).resolve().relative_to(ROOT)
    except ValueError:
        return Path(p).name


def find_browser(explicit):
    if explicit:
        return explicit
    for c in BROWSER_CANDIDATES:
        if os.path.exists(c):
            return c
    for name in ("msedge", "google-chrome", "chromium", "chrome"):
        p = shutil.which(name)
        if p:
            return p
    sys.exit("No Edge/Chrome found; pass --browser PATH")


class Renderer:
    def __init__(self, browser, workdir):
        self.browser = browser
        self.workdir = Path(workdir)
        self.profile = self.workdir / "profile"

    def _run(self, args, out):
        """Run the browser and wait for out to be written. On Windows the
        msedge.exe launcher returns before the headless work is finished."""
        out = Path(out)
        if out.exists():
            out.unlink()
        # A fresh profile per run: a lingering instance must not swallow the next job.
        self.runs = getattr(self, "runs", 0) + 1
        cmd = [self.browser, "--headless=new", "--disable-gpu", "--hide-scrollbars", "--no-first-run",
               "--no-default-browser-check", "--disable-extensions", f"--user-data-dir={self.profile}{self.runs}",
               "--virtual-time-budget=10000"] + args
        subprocess.run(cmd, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=120)
        deadline = time.time() + 90
        last = -1
        while time.time() < deadline:
            if out.exists():
                size = out.stat().st_size
                if size > 0 and size == last:
                    return
                last = size
            time.sleep(0.5)
        sys.exit(f"browser did not produce {out}")

    def page(self, name, body, css, width, height, title=""):
        # The <title> becomes the document title of printed PDFs.
        doc = (f"<!doctype html><html><head><meta charset='utf-8'><title>{esc(title or name)}</title><style>{BASE_CSS}"
               f"html,body{{width:{width}px;height:{height}px}}{css}</style></head><body>{body}</body></html>")
        path = self.workdir / f"{name}.html"
        path.write_text(doc, encoding="utf-8")
        return path

    def png(self, html_path, out, width, height, transparent=False):
        args = [f"--window-size={width},{height}", f"--screenshot={out}"]
        if transparent:
            args.append("--default-background-color=00000000")
        self._run(args + [html_path.as_uri()], out)
        with Image.open(out) as im:
            im.load()
            size = im.size
            if size != (width, height):  # some browser versions add/remove a few pixels
                fixed = im.crop((0, 0, width, height)) if im.width >= width and im.height >= height else im.resize((width, height))
        if size != (width, height):
            fixed.save(out)
        print(f"  {show(out)}  {width}x{height}")

    def pdf(self, html_path, out):
        self._run(["--no-pdf-header-footer", "--print-to-pdf-no-header", f"--print-to-pdf={out}", html_path.as_uri()], out)
        print(f"  {show(out)}")


def facts(site):
    c = site["conference"]
    tracks = {t["key"]: t for t in site["tracks"]}
    loc = ", ".join(x for x in (c.get("city"), c.get("country")) if x)
    when = c.get("dates") or ""
    where_when = " · ".join(x for x in (loc, when) if x) or "Venue and dates to be announced"
    return {
        "acr": c["acronym"], "year": c["year"], "name": c["name"],
        "ho": tracks["with-workshop"]["slots"], "th": tracks["without-workshop"]["slots"],
        "where_when": where_when,
        "url": (c.get("baseURL") or "").replace("https://", "").replace("http://", "").rstrip("/"),
    }


def logos(r, f, emblem_uri):
    css = """
    body { background: transparent; }
    .logo { display: flex; align-items: center; gap: 56px; padding: 40px 60px; height: 560px; }
    .logo img { width: 480px; height: 480px; flex: none; }
    .acr { font-size: 210px; color: var(--navy); white-space: nowrap; }
    .acr.with-year { font-size: 148px; }
    .name { margin-top: 26px; font-size: 44px; font-weight: 600; color: var(--muted); line-height: 1.25; }
    """
    for name, year in (("icas-logo-with-year", f" <span>{esc(f['year'])}</span>"), ("icas-logo", "")):
        cls = "acr with-year" if year else "acr"
        body = (f"<div class='logo'><img src='{emblem_uri}' alt=''><div><div class='{cls}'>{esc(f['acr'])}{year}</div>"
                f"<div class='name'>{esc(f['name']).replace(' on ', '<br>on ', 1)}</div></div></div>")
        r.png(r.page(name, body, css, 1600, 560), PROMO / f"{name}.png", 1600, 560, transparent=True)


def social_card(f, emblem_uri, mascot_uri, w, h, layout):
    """Return (body, css) for a dark call-for-papers card."""
    tracks = (f"<div class='tracks'><div class='pill t'><b>{f['ho']}</b><span>Papers with workshop</span><small>25-min talk + 90-min workshop</small></div>"
              f"<div class='pill t'><b>{f['th']}</b><span>Papers without workshop</span><small>talks of about 15 minutes</small></div></div>")
    brand = (f"<div class='brand'><img src='{emblem_uri}' alt=''><div><div class='acr'>{esc(f['acr'])} <span>{esc(f['year'])}</span></div>"
             f"<div class='name'>{esc(f['name'])}</div></div></div>")
    share = "<div class='share'>Workshop speakers receive <b class='coral'>40%</b> of the workshop registration fees as feedback</div>"
    meta = f"<div class='meta'>{esc(f['where_when'])}{(' · ' + esc(f['url'])) if f['url'] else ''}</div>"
    s = w / 1200  # scale factor relative to a 1200px-wide design
    if layout == "square":
        s = w / 1080
        css = f"""
        .card {{ width:{w}px; height:{h}px; padding:{70*s}px; display:flex; flex-direction:column; align-items:center; text-align:center; position:relative; }}
        .brand {{ display:flex; flex-direction:column; align-items:center; gap:{18*s}px; }}
        .brand img {{ width:{260*s}px; height:{260*s}px; }}
        .acr {{ font-size:{96*s}px; }}
        .name {{ font-size:{30*s}px; font-weight:600; color:#cfd9ea; margin-top:{10*s}px; }}
        .cfp {{ font-size:{84*s}px; font-weight:800; color:var(--coral); margin-top:{44*s}px; }}
        .tracks {{ display:flex; gap:{24*s}px; margin-top:{30*s}px; }}
        .t {{ padding:{20*s}px {28*s}px; display:flex; flex-direction:column; min-width:{330*s}px; }}
        .t b {{ font-size:{64*s}px; line-height:1; }} .t span {{ font-size:{28*s}px; font-weight:700; margin-top:{6*s}px; }} .t small {{ font-size:{20*s}px; color:#b9c6dc; margin-top:{4*s}px; font-family:'Open Sans',sans-serif; }}
        .share {{ margin-top:{34*s}px; font-size:{30*s}px; font-weight:600; max-width:{600*s}px; }}
        .meta {{ position:absolute; left:{70*s}px; bottom:{60*s}px; font-size:{22*s}px; color:#b9c6dc; text-align:left; font-weight:600; }}
        .mascot {{ position:absolute; right:{24*s}px; bottom:{12*s}px; width:{200*s}px; }}
        """
        body = f"<div class='card dark'>{brand}<div class='cfp'>Call for Papers</div>{tracks}{share}{meta}<img class='mascot' src='{mascot_uri}' alt=''></div>"
        return body, css
    if layout == "banner":  # LinkedIn cover: very wide and short
        css = f"""
        .card {{ width:{w}px; height:{h}px; display:flex; align-items:center; gap:22px; padding:0 34px 0 300px; position:relative; }}
        .brand {{ display:flex; align-items:center; gap:16px; }}
        .brand img {{ width:128px; height:128px; }}
        .acr {{ font-size:46px; }} .name {{ font-size:15px; font-weight:600; color:#cfd9ea; margin-top:6px; }}
        .cfp {{ margin-left:auto; text-align:right; font-size:30px; font-weight:800; color:var(--coral); line-height:1.1; }}
        .cfp small {{ display:block; font-size:14px; color:#fff; font-weight:600; margin-top:6px; }}
        """
        body = (f"<div class='card dark'>{brand}<div class='cfp'>Call for Papers<small>{f['ho']} papers with workshop · "
                f"{f['th']} papers without workshop</small></div></div>")
        return body, css
    # wide layouts (X header, X/LinkedIn posts)
    header = layout == "header"
    if header:  # a short banner: scale type to the height, not the width
        s = h / 560
    css = f"""
    .card {{ width:{w}px; height:{h}px; padding:{(60 if not header else 46)*s}px {70*s}px; position:relative; display:flex; flex-direction:column; justify-content:center; }}
    .brand {{ display:flex; align-items:center; gap:{22*s}px; }}
    .brand img {{ width:{(150 if not header else 120)*s}px; height:{(150 if not header else 120)*s}px; }}
    .acr {{ font-size:{(78 if not header else 64)*s}px; }}
    .name {{ font-size:{24*s}px; font-weight:600; color:#cfd9ea; margin-top:{8*s}px; }}
    .cfp {{ font-size:{(76 if not header else 60)*s}px; font-weight:800; color:var(--coral); margin-top:{(34 if not header else 22)*s}px; }}
    .tracks {{ display:flex; gap:{20*s}px; margin-top:{(26 if not header else 16)*s}px; }}
    .t {{ padding:{16*s}px {22*s}px; display:flex; flex-direction:column; min-width:{250*s}px; }}
    .t b {{ font-size:{48*s}px; line-height:1; }} .t span {{ font-size:{22*s}px; font-weight:700; margin-top:{4*s}px; }} .t small {{ font-size:{16*s}px; color:#b9c6dc; margin-top:{3*s}px; font-family:'Open Sans',sans-serif; }}
    .share {{ margin-top:{(26 if not header else 16)*s}px; font-size:{24*s}px; font-weight:600; max-width:{620*s}px; }}
    .meta {{ margin-top:{(22 if not header else 12)*s}px; font-size:{18*s}px; color:#b9c6dc; font-weight:600; }}
    .mascot {{ position:absolute; right:{30*s}px; bottom:{0}px; width:{(430 if not header else 300)*s}px; }}
    """
    if header:  # keep the bottom-left free for the profile picture
        css += f".card {{ padding-left:{470}px; }} .share {{ display:none; }} .name {{ white-space:nowrap; }}"
    body = f"<div class='card dark'>{brand}<div class='cfp'>Call for Papers</div>{tracks}{share}{meta}<img class='mascot' src='{mascot_uri}' alt=''></div>"
    return body, css


def social(r, f, emblem_uri, mascot_uri):
    specs = [
        ("icas-x-header-1500x500", 1500, 500, "header"),
        ("icas-x-post-1600x900", 1600, 900, "post"),
        ("icas-x-post-1200x675", 1200, 675, "post"),
        ("icas-linkedin-cover-1128x191", 1128, 191, "banner"),
        ("icas-linkedin-post-1200x627", 1200, 627, "post"),
        ("icas-square-post-1080x1080", 1080, 1080, "square"),
    ]
    for name, w, h, layout in specs:
        body, css = social_card(f, emblem_uri, mascot_uri, w, h, layout)
        r.png(r.page(name, body, css, w, h), PROMO / f"{name}.png", w, h)


def flyer(r, site, f, emblem_uri, mascot_uri):
    tracks = {t["key"]: t for t in site["tracks"]}
    ho, th = tracks["with-workshop"], tracks["without-workshop"]
    topics = "".join(f"<li{' class=practice' if t.get('practice') else ''}><b>{esc(t['title'])}</b></li>" for t in site["topics"])

    def req_item(q):
        # Workshop-only items carry a star instead of the long title suffix;
        # their sub-bullets are left to the website to keep the flyer to one page.
        title = q["title"].split(" (")[0]
        star = "*" if q.get("applies") == "with-workshop" else ""
        items = "" if star else "".join(f" {esc(i)}" for i in q.get("items") or [])
        return f"<li><b>{esc(title)}{star}:</b> {esc(q['text'])}{items}</li>"

    reqs = "".join(req_item(q) for q in site["submissionRequirements"])
    crit = "".join(f"<li><b>{esc(c['title'])}:</b> {esc(c['text'])}</li>" for c in site["reviewCriteria"])
    dates = "".join(
        f"<tr><td>{esc(d['label'])}</td><td>{esc(d['date']) if d['date'] else '<i>TBA</i>'}</td></tr>"
        for d in site["importantDates"])
    url = f["url"] or "the conference website"
    css = """
    @page { size: A4; margin: 0; }
    html, body { width: 210mm; height: 297mm; }
    body { font-family: 'Open Sans', 'Segoe UI', Arial, sans-serif; font-size: 8pt; line-height: 1.34; }
    h2 { font-family: 'Montserrat', sans-serif; font-size: 10pt; color: var(--navy); text-transform: uppercase; letter-spacing: .04em; margin: 7px 0 3px; }
    .top { height: 46mm; padding: 4mm 12mm 0; display: flex; gap: 6mm; align-items: center; position: relative; }
    .top img.emblem { width: 38mm; height: 38mm; }
    .top .acr { font-family: 'Montserrat'; font-size: 36pt; }
    .top .name { font-family: 'Montserrat'; font-weight: 600; font-size: 11.5pt; color: #cfd9ea; margin-top: 1.5mm; }
    .top .cfp { font-family: 'Montserrat'; font-weight: 800; font-size: 22pt; color: var(--coral); margin-top: 2.5mm; letter-spacing: .02em; }
    .top .when { font-family: 'Montserrat'; font-weight: 600; font-size: 9pt; margin-top: 1.5mm; }
    .top img.mascot { position: absolute; right: 10mm; bottom: 2mm; width: 34mm; }
    .looking { padding: 3mm 12mm 0; }
    .boxes { display: grid; grid-template-columns: 1fr 1fr; gap: 4mm; }
    .box { border: 1px solid #dfe4ec; border-radius: 3mm; padding: 2.5mm 3.5mm; }
    .box.ho { border-top: 3px solid var(--coral); }
    .box.th { border-top: 3px solid var(--navy7); }
    .box h3 { font-family: 'Montserrat'; font-size: 10pt; color: var(--navy); }
    .box .slots { font-family: 'Montserrat'; font-weight: 700; color: var(--coral); font-size: 8.5pt; }
    .incentive { margin-top: 2.5mm; padding: 2mm 3.5mm; border-radius: 3mm; background: #fde8e1; font-weight: 600; color: #7a2e1d; }
    .wrap { display: grid; grid-template-columns: 1.18fr 1fr; gap: 7mm; padding: 1mm 12mm 0; }
    ul { padding-left: 4mm; } li { margin: .5mm 0; }
    ol.topics { padding-left: 5mm; } ol.topics li { margin: .3mm 0; }
    ol.topics li.practice b { color: var(--coral); }
    .note { font-size: 7.4pt; color: #5b6878; margin: .5mm 0 1mm; }
    table { width: 100%; border-collapse: collapse; font-size: 7.7pt; }
    td { padding: .8mm 0; border-bottom: 1px dashed #dfe4ec; } td:last-child { text-align: right; font-weight: 700; color: var(--navy); white-space: nowrap; padding-left: 2mm; }
    .foot { position: absolute; left: 0; right: 0; bottom: 0; height: 10mm; background: var(--navy9); color: #b9c6dc; font-size: 7.8pt; display: flex; align-items: center; justify-content: center; gap: 3mm; font-family: 'Montserrat'; font-weight: 600; }
    .foot b { color: var(--coral); }
    """
    body = f"""
    <div class="top dark">
      <img class="emblem" src="{emblem_uri}" alt="">
      <div>
        <div class="acr">{esc(f['acr'])} <span>{esc(f['year'])}</span></div>
        <div class="name">{esc(f['name'])}</div>
        <div class="cfp">CALL FOR PAPERS</div>
        <div class="when">{esc(f['where_when'])}</div>
      </div>
      <img class="mascot" src="{mascot_uri}" alt="">
    </div>
    <div class="looking">
      <h2>What we are looking for</h2>
      <div class="boxes">
        <div class="box ho"><h3>Papers with Workshop</h3><div class="slots">{ho['slots']} speaking slots</div>
          <ul><li>Papers capable of engaging the audience interactively.</li>
          <li>A <b>25-minute presentation</b> in the morning, then a <b>90-minute workshop</b> in the afternoon.</li>
          <li>A workshop for <b>25 or 50 participants</b>, with 10 or 20 equipment sets (if needed) and at least one <b>teaching assistant</b>.</li>
          <li>Speakers prepare the equipment and provide the <b>laptop hardware requirements</b>; attendees bring their own laptops.</li></ul></div>
        <div class="box th"><h3>Papers without Workshop</h3><div class="slots">{th['slots']} speaking slots</div>
          <ul><li>Academic papers relevant to the conference theme.</li>
          <li>About <b>15 minutes</b> per speaker, in sessions running concurrently with the afternoon workshops.</li>
          <li>Not strictly limited to the theme: any compelling, relevant research is welcome.</li></ul></div>
      </div>
      <div class="incentive">Speaker feedback: 40% of the workshop registration fees are given to the speakers of each workshop, based on attendance. Choose an engaging topic and draw in as many participants as possible!</div>
    </div>
    <div class="wrap">
      <div>
        <h2>Proposal submission requirements</h2>
        <p>All submissions must be in English and include:</p>
        <ul>{reqs}</ul>
        <p class="note">* Papers with workshop only.</p>
        <h2>Review criteria</h2>
        <p>Double-blind peer review against:</p>
        <ul>{crit}</ul>
      </div>
      <div>
        <h2>Conference theme</h2>
        <p class="note">Implementation topics come first.</p>
        <ol class="topics">{topics}</ol>
        <h2>Important dates</h2>
        <table>{dates}</table>
      </div>
    </div>
    <div class="foot"><span>{esc(f['acr'])} {esc(f['year'])} · {esc(f['name'])}</span><b>Submit your proposal online at {esc(url)}</b></div>
    """
    page = r.page("flyer", body, css, 794, 1123, title=f"{f['acr']} {f['year']} Call for Papers")
    r.pdf(page, FILES / "ICAS-CFP-flyer.pdf")


def slide_template(f, emblem_png):
    from pptx import Presentation
    from pptx.dml.color import RGBColor
    from pptx.enum.shapes import MSO_SHAPE
    from pptx.enum.text import PP_ALIGN
    from pptx.util import Inches, Pt

    navy, navy9, coral, white, muted = (RGBColor(0x14, 0x2C, 0x55), RGBColor(0x0A, 0x16, 0x28),
                                        RGBColor(0xE8, 0x69, 0x4B), RGBColor(0xFF, 0xFF, 0xFF), RGBColor(0x5B, 0x68, 0x78))
    prs = Presentation()
    prs.slide_width, prs.slide_height = Inches(13.333), Inches(7.5)
    blank = prs.slide_layouts[6]
    footer = f"{f['acr']} {f['year']} · {f['name']}"

    def bg(slide, color):
        fill = slide.background.fill
        fill.solid()
        fill.fore_color.rgb = color

    def text(slide, x, y, w, h, value, size, color, bold=False, align=PP_ALIGN.LEFT, name=None):
        box = slide.shapes.add_textbox(Inches(x), Inches(y), Inches(w), Inches(h))
        if name:
            box.name = name
        tf = box.text_frame
        tf.word_wrap = True
        p = tf.paragraphs[0]
        p.alignment = align
        run = p.add_run()
        run.text = value
        run.font.size, run.font.bold, run.font.color.rgb, run.font.name = Pt(size), bold, color, "Arial"
        return box

    def bullets(slide, x, y, w, h, items, size=22):
        box = slide.shapes.add_textbox(Inches(x), Inches(y), Inches(w), Inches(h))
        box.name = "Body"
        tf = box.text_frame
        tf.word_wrap = True
        for i, item in enumerate(items):
            p = tf.paragraphs[0] if i == 0 else tf.add_paragraph()
            p.space_after = Pt(10)
            run = p.add_run()
            run.text = "•  " + item
            run.font.size, run.font.color.rgb, run.font.name = Pt(size), RGBColor(0x1B, 0x25, 0x33), "Arial"

    def chrome(slide, title):
        bg(slide, white)
        text(slide, 0.6, 0.4, 10.6, 1.0, title, 32, navy, bold=True, name="Title")
        slide.shapes.add_picture(str(emblem_png), Inches(12.0), Inches(0.35), Inches(0.85), Inches(0.85))
        text(slide, 0.6, 6.85, 9.0, 0.4, footer, 11, muted)

    # 1. Title slide
    s = prs.slides.add_slide(blank)
    bg(s, navy9)
    s.shapes.add_picture(str(emblem_png), Inches(0.9), Inches(1.6), Inches(4.2), Inches(4.2))
    text(s, 5.6, 1.9, 7.2, 1.8, "Presentation title", 40, white, bold=True, name="Title")
    text(s, 5.6, 3.8, 7.2, 0.6, "Speaker name · Affiliation", 20, RGBColor(0xCF, 0xD9, 0xEA), name="Speaker")
    text(s, 5.6, 4.5, 7.2, 0.6, "Paper with workshop · Paper without workshop", 16, coral, bold=True, name="Track")
    text(s, 5.6, 6.6, 7.2, 0.5, footer, 12, RGBColor(0xB9, 0xC6, 0xDC))
    s.notes_slide.notes_text_frame.text = ("Official ICAS template: its use is mandatory for all presentations. "
                                          "Replace the title, speaker and track; keep the layout, colours, emblem and footer.")

    # 2. Content slide
    s = prs.slides.add_slide(blank)
    chrome(s, "Slide title")
    bullets(s, 0.6, 1.6, 11.6, 4.8, ["First key point", "Second key point", "Third key point — keep slides short and readable from the back of the room"])
    s.notes_slide.notes_text_frame.text = "Content slide: duplicate it for each topic. Use 18 pt or larger."

    # 3. Section divider
    s = prs.slides.add_slide(blank)
    bg(s, coral)
    text(s, 0.9, 2.6, 11.5, 1.4, "Section title", 44, white, bold=True, name="Title")
    text(s, 0.9, 3.9, 11.5, 0.8, "Workshop milestone or talk section", 20, white, name="Subtitle")
    s.notes_slide.notes_text_frame.text = "Section divider: use between workshop milestones or parts of the talk."

    # 4. Two columns: text + figure
    s = prs.slides.add_slide(blank)
    chrome(s, "Text and figure")
    bullets(s, 0.6, 1.6, 5.8, 4.8, ["Explain the idea on the left", "Show the result on the right", "One message per slide"], size=20)
    ph = s.shapes.add_shape(MSO_SHAPE.ROUNDED_RECTANGLE, Inches(6.9), Inches(1.6), Inches(5.8), Inches(4.8))
    ph.name = "Figure placeholder"
    ph.fill.solid()
    ph.fill.fore_color.rgb = RGBColor(0xF4, 0xF6, 0xFA)
    ph.line.color.rgb = RGBColor(0xC5, 0xCD, 0xD9)
    ph.text_frame.text = "Insert figure, video or code here"
    for p in ph.text_frame.paragraphs:
        p.alignment = PP_ALIGN.CENTER
        for run in p.runs:
            run.font.size, run.font.color.rgb, run.font.name = Pt(16), muted, "Arial"
    s.notes_slide.notes_text_frame.text = "Two-column slide: replace the grey box with your figure."

    # 5. Closing slide
    s = prs.slides.add_slide(blank)
    bg(s, navy9)
    s.shapes.add_picture(str(emblem_png), Inches(5.42), Inches(0.8), Inches(2.5), Inches(2.5))
    text(s, 0.9, 3.6, 11.5, 1.2, "Thank you — questions?", 40, white, bold=True, align=PP_ALIGN.CENTER, name="Title")
    text(s, 0.9, 4.8, 11.5, 0.6, "name@example.org · github.com/your-project", 18, RGBColor(0xCF, 0xD9, 0xEA), align=PP_ALIGN.CENTER, name="Contact")
    text(s, 0.9, 6.6, 11.5, 0.5, footer, 12, RGBColor(0xB9, 0xC6, 0xDC), align=PP_ALIGN.CENTER)

    prs.core_properties.title = f"{f['acr']} {f['year']} official presentation template"
    prs.core_properties.subject = "Mandatory slide template for all ICAS presentations"
    # Replace python-pptx's default package metadata with the conference's own.
    prs.core_properties.author = f"{f['acr']} Organizing Committee"
    prs.core_properties.last_modified_by = f"{f['acr']} Organizing Committee"
    prs.core_properties.comments = ""
    out = FILES / "ICAS-slide-template.pptx"
    prs.save(out)
    print(f"  {out.relative_to(ROOT)}")


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--browser", help="path to msedge/chrome")
    args = ap.parse_args()

    site = json.loads((ROOT / "content" / "site.json").read_text(encoding="utf-8"))
    f = facts(site)
    PROMO.mkdir(parents=True, exist_ok=True)
    browser = find_browser(args.browser)
    print(f"Rendering with {browser}")
    emblem_uri, mascot_uri = EMBLEM.as_uri(), MASCOT.as_uri()

    # Browser processes may hold files a moment after exiting; leftovers in
    # the system temp directory are harmless.
    with tempfile.TemporaryDirectory(ignore_cleanup_errors=True) as tmp:
        r = Renderer(browser, tmp)
        shutil.copyfile(EMBLEM, PROMO / "icas-emblem.svg")
        logos(r, f, emblem_uri)
        social(r, f, emblem_uri, mascot_uri)
        flyer(r, site, f, emblem_uri, mascot_uri)
        body, css = social_card(f, emblem_uri, mascot_uri, 1200, 630, "post")
        r.png(r.page("og", body, css, 1200, 630), STATIC / "img" / "og-image.png", 1200, 630)
        # A square, transparent emblem PNG for the slide template.
        emblem_png = Path(tmp) / "emblem.png"
        page = r.page("emblem", f"<img src='{emblem_uri}' style='width:800px;height:800px;display:block'>", "body{background:transparent}", 800, 800)
        r.png(page, emblem_png, 800, 800, transparent=True)
        slide_template(f, emblem_png)

    with zipfile.ZipFile(PROMO / "icas-logo-pack.zip", "w", zipfile.ZIP_DEFLATED) as z:
        for name in ("icas-emblem.svg", "icas-logo-with-year.png", "icas-logo.png"):
            z.write(PROMO / name, name)
    print("  web/static/files/promo/icas-logo-pack.zip")


if __name__ == "__main__":
    main()
