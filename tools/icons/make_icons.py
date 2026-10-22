"""Generates the class and role icons the Discord bot uploads as emoji.

Original artwork: a rounded tile in the class colour with a bold dark glyph, drawn at
512 px and downsampled to 128 px so edges stay smooth at emoji size (~22 px).

    python tools/icons/make_icons.py            # writes backend/assets/emoji/*.png
    python tools/icons/make_icons.py --sheet    # also writes a contact sheet to dist/icons.png
"""
import math
import os
import sys

from PIL import Image, ImageDraw

S = 512          # draw size
OUT = 128        # emoji size
R = 110          # tile corner radius
INK = (24, 22, 28, 255)
OUT_DIR = os.path.join("backend", "assets", "emoji")

CLASS_COLORS = {
    "warrior": (198, 155, 109),
    "paladin": (244, 140, 186),
    "hunter": (170, 211, 114),
    "rogue": (255, 244, 104),
    "priest": (240, 240, 240),
    "shaman": (0, 112, 221),
    "mage": (63, 199, 235),
    "warlock": (135, 136, 238),
    "druid": (255, 124, 10),
}
ROLE_COLORS = {
    "tank": (90, 120, 200),
    "healer": (80, 190, 110),
    "melee": (210, 80, 80),
    "ranged": (215, 160, 60),
    "absent": (110, 110, 118),
}


def tile(color):
    im = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    d = ImageDraw.Draw(im)
    d.rounded_rectangle((16, 16, S - 16, S - 16), radius=R, fill=color + (255,))
    # Inner bevel: a slightly darker rim so tiles read as objects, not flat squares.
    rim = tuple(max(0, c - 45) for c in color) + (255,)
    d.rounded_rectangle((16, 16, S - 16, S - 16), radius=R, outline=rim, width=14)
    return im, d


def rot(points, deg, cx=S / 2, cy=S / 2):
    a = math.radians(deg)
    out = []
    for x, y in points:
        dx, dy = x - cx, y - cy
        out.append((cx + dx * math.cos(a) - dy * math.sin(a), cy + dx * math.sin(a) + dy * math.cos(a)))
    return out


def sword(d, deg=0, cx=S / 2, cy=S / 2, scale=1.0):
    # blade, guard, grip, pommel
    blade = [(cx - 26 * scale, cy - 190 * scale), (cx + 26 * scale, cy - 190 * scale), (cx + 26 * scale, cy + 40 * scale),
             (cx, cy + 75 * scale), (cx - 26 * scale, cy + 40 * scale)]
    tip = [(cx - 26 * scale, cy - 190 * scale), (cx, cy - 235 * scale), (cx + 26 * scale, cy - 190 * scale)]
    guard = [(cx - 95 * scale, cy + 40 * scale), (cx + 95 * scale, cy + 40 * scale), (cx + 95 * scale, cy + 76 * scale), (cx - 95 * scale, cy + 76 * scale)]
    grip = [(cx - 18 * scale, cy + 76 * scale), (cx + 18 * scale, cy + 76 * scale), (cx + 18 * scale, cy + 160 * scale), (cx - 18 * scale, cy + 160 * scale)]
    for poly in (blade, tip, guard, grip):
        d.polygon(rot(poly, deg, cx, cy), fill=INK)
    px, py = rot([(cx, cy + 185 * scale)], deg, cx, cy)[0]
    d.ellipse((px - 32 * scale, py - 32 * scale, px + 32 * scale, py + 32 * scale), fill=INK)


def glyph_warrior(d):
    sword(d, deg=-35)


def glyph_paladin(d):
    # two-headed war hammer on a long haft
    d.rounded_rectangle((236, 150, 276, 440), radius=18, fill=INK)
    d.rounded_rectangle((120, 80, 392, 210), radius=26, fill=INK)
    c = CLASS_COLORS["paladin"] + (255,)
    d.rounded_rectangle((150, 108, 230, 182), radius=12, fill=c)
    d.rounded_rectangle((282, 108, 362, 182), radius=12, fill=c)
    d.ellipse((216, 400, 296, 480), fill=INK)


def glyph_hunter(d):
    # big recurve bow facing right with a nocked arrow
    d.arc((30, 50, 410, 462), start=-78, end=78, fill=INK, width=38)      # limb
    d.line((262, 56, 262, 456), fill=INK, width=18)                       # string between limb tips
    d.line((100, 256, 440, 256), fill=INK, width=30)                      # arrow shaft
    d.polygon([(482, 256), (406, 200), (406, 312)], fill=INK)             # head
    d.polygon([(100, 256), (160, 210), (184, 232), (140, 256), (184, 280), (160, 302)], fill=INK)  # fletching


def glyph_rogue(d):
    sword(d, deg=-40, cx=S / 2, cy=S / 2, scale=0.78)
    sword(d, deg=40, cx=S / 2, cy=S / 2, scale=0.78)


def glyph_priest(d):
    # holy cross with flared ends
    d.rounded_rectangle((230, 90, 282, 430), radius=16, fill=INK)
    d.rounded_rectangle((110, 190, 402, 242), radius=16, fill=INK)
    d.ellipse((216, 176, 296, 256), fill=INK)


def glyph_shaman(d):
    d.polygon([(300, 70), (170, 290), (250, 290), (205, 450), (350, 220), (268, 220)], fill=INK)


def glyph_mage(d):
    # four-point star (arcane spark) over a ring
    d.ellipse((150, 150, 362, 362), outline=INK, width=24)
    pts = []
    for i in range(8):
        r = 190 if i % 2 == 0 else 60
        a = math.radians(i * 45 - 90)
        pts.append((S / 2 + r * math.cos(a), S / 2 + r * math.sin(a)))
    d.polygon(pts, fill=INK)


def glyph_warlock(d):
    # skull
    d.ellipse((136, 90, 376, 330), fill=INK)
    d.rounded_rectangle((186, 300, 326, 400), radius=30, fill=INK)
    c = CLASS_COLORS["warlock"] + (255,)
    d.ellipse((178, 170, 248, 240), fill=c)
    d.ellipse((264, 170, 334, 240), fill=c)
    d.polygon([(256, 250), (236, 300), (276, 300)], fill=c)
    for x in (206, 240, 274, 308):
        d.rectangle((x - 8, 360, x + 8, 420), fill=INK)
        d.rectangle((x - 3, 346, x + 3, 400), fill=c)


def glyph_druid(d):
    # paw: pad + four toes
    d.ellipse((166, 230, 346, 430), fill=INK)
    for cx, cy, r in ((150, 190, 46), (222, 120, 50), (290, 120, 50), (362, 190, 46)):
        d.ellipse((cx - r, cy - r, cx + r, cy + r), fill=INK)


def glyph_tank(d):
    d.polygon([(256, 70), (420, 130), (400, 300), (256, 450), (112, 300), (92, 130)], fill=INK)
    c = ROLE_COLORS["tank"] + (255,)
    d.polygon([(256, 130), (370, 172), (356, 292), (256, 396), (156, 292), (142, 172)], fill=c)
    d.rectangle((236, 150, 276, 380), fill=INK)
    d.rectangle((176, 220, 336, 260), fill=INK)


def glyph_healer(d):
    d.rounded_rectangle((216, 90, 296, 422), radius=20, fill=INK)
    d.rounded_rectangle((90, 216, 422, 296), radius=20, fill=INK)


def glyph_melee(d):
    sword(d, deg=-45, scale=0.9)
    sword(d, deg=45, scale=0.9)


def glyph_ranged(d):
    d.ellipse((96, 96, 416, 416), outline=INK, width=30)
    d.ellipse((176, 176, 336, 336), outline=INK, width=30)
    d.ellipse((226, 226, 286, 286), fill=INK)
    d.line((256, 256, 440, 72), fill=INK, width=24)
    d.polygon([(440, 72), (370, 84), (428, 142)], fill=INK)


def glyph_absent(d):
    d.ellipse((96, 96, 416, 416), outline=INK, width=36)
    d.line((150, 362, 362, 150), fill=INK, width=36)


GLYPHS = {
    "warrior": glyph_warrior, "paladin": glyph_paladin, "hunter": glyph_hunter, "rogue": glyph_rogue,
    "priest": glyph_priest, "shaman": glyph_shaman, "mage": glyph_mage, "warlock": glyph_warlock,
    "druid": glyph_druid, "tank": glyph_tank, "healer": glyph_healer, "melee": glyph_melee,
    "ranged": glyph_ranged, "absent": glyph_absent,
}


def make(name):
    color = CLASS_COLORS.get(name) or ROLE_COLORS[name]
    im, d = tile(color)
    GLYPHS[name](d)
    return im.resize((OUT, OUT), Image.LANCZOS)


def main():
    os.makedirs(OUT_DIR, exist_ok=True)
    icons = {}
    for name in GLYPHS:
        icons[name] = make(name)
        icons[name].save(os.path.join(OUT_DIR, name + ".png"))
    print("wrote", len(icons), "icons to", OUT_DIR)
    if "--sheet" in sys.argv:
        names = list(GLYPHS)
        cols = 7
        rows = (len(names) + cols - 1) // cols
        sheet = Image.new("RGBA", (cols * 140, rows * 140), (54, 57, 63, 255))
        for i, n in enumerate(names):
            sheet.paste(icons[n], ((i % cols) * 140 + 6, (i // cols) * 140 + 6), icons[n])
        # a row at real emoji size to judge legibility
        strip = Image.new("RGBA", (len(names) * 28, 28), (54, 57, 63, 255))
        for i, n in enumerate(names):
            small = icons[n].resize((22, 22), Image.LANCZOS)
            strip.paste(small, (i * 28 + 3, 3), small)
        os.makedirs("dist", exist_ok=True)
        sheet.save(os.path.join("dist", "icons.png"))
        strip.save(os.path.join("dist", "icons-small.png"))
        print("sheet at dist/icons.png, emoji-size strip at dist/icons-small.png")


if __name__ == "__main__":
    main()
