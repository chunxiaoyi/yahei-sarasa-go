#!/usr/bin/env python3
"""Regenerates the tiny custom-font fixtures used by custom/*_test.go.

Each font maps thousands of code points onto a handful of glyphs, so GB2312
coverage is complete while the file stays a few KiB. Run from this folder:
    python3 make_fixtures.py
"""
import io
from fontTools.fontBuilder import FontBuilder
from fontTools.pens.ttGlyphPen import TTGlyphPen
from fontTools.pens.t2CharStringPen import T2CharStringPen
from fontTools.ttLib import TTFont, TTCollection, newTable
from fontTools.ttLib.tables import otTables

GB = open("../../charset/data/gb2312-level1.txt", encoding="utf-8").read() + open("../../charset/data/gb2312-level2.txt", encoding="utf-8").read()
PUNCT = open("../../charset/data/cjk-punctuation.txt", encoding="utf-8").read()
BIG5 = open("../../charset/data/big5-level1.txt", encoding="utf-8").read()
ASCII = [chr(c) for c in range(0x20, 0x7F)]
LATIN1 = [chr(c) for c in range(0xA0, 0x100)]


def charmap(cjk=True):
    cps = [ord(c) for c in ASCII + LATIN1]
    if cjk:
        cps += [ord(c) for c in GB + PUNCT + BIG5]
    m = {}
    for cp in sorted(set(cps)):
        if cp == 0x20:
            m[cp] = "space"
        elif cp < 0x100:
            m[cp] = "latin"
        else:
            m[cp] = "han"
    return m


def tt_glyph(w, h=700):
    pen = TTGlyphPen(None)
    pen.moveTo((50, -50)); pen.lineTo((50 + w, -50)); pen.lineTo((50 + w, h)); pen.lineTo((50, h)); pen.closePath()
    return pen.glyph()


def base(name, weight, cjk=True, cff=False, fs_sel=0x40, mac=0, os2v=4, family="Fixture Sans SC", italic_angle=0, tall=False):
    fb = FontBuilder(1000, isTTF=not cff)
    order = [".notdef", "space", "latin", "han"]
    fb.setupGlyphOrder(order)
    fb.setupCharacterMap(charmap(cjk))
    stem = {300: 40, 400: 70, 500: 85, 600: 100, 700: 130}.get(weight, 70)
    if cff:
        cs = {}
        for g in order:
            pen = T2CharStringPen(600, None)
            if g not in (".notdef", "space"):
                pen.moveTo((50, -50)); pen.lineTo((50 + stem, -50)); pen.lineTo((50 + stem, 700)); pen.lineTo((50, 700)); pen.closePath()
            cs[g] = pen.getCharString()
        fb.setupCFF(name.replace(" ", ""), {"FullName": family + " " + name}, cs, {})
    else:
        glyphs = {g: tt_glyph(stem, 1400 if (tall and g == "han") else 700) for g in order}
        glyphs["space"] = TTGlyphPen(None).glyph()
        fb.setupGlyf(glyphs)
    fb.setupHorizontalMetrics({g: (600, 50) for g in order})
    fb.setupHorizontalHeader(ascent=900, descent=-200)
    fb.setupNameTable({"familyName": family, "styleName": name, "version": "Version 2.500; fixture",
                       "copyright": "Copyright Fixture Foundry", "licenseDescription": "Fixture license",
                       "licenseInfoURL": "https://example.invalid/license",
                       "uniqueFontIdentifier": family + " " + name, "psName": (family + "-" + name).replace(" ", "")})
    fb.setupOS2(usWeightClass=weight, fsSelection=fs_sel, sTypoAscender=900, sTypoDescender=-200, sTypoLineGap=0,
                usWinAscent=900, usWinDescent=200, version=os2v, ulCodePageRange1=0x1)
    fb.setupPost(italicAngle=italic_angle)
    fb.setupHead(unitsPerEm=1000, macStyle=mac, flags=0x3, fontRevision=2.5)
    fb.font["head"].created = fb.font["head"].modified = 3600000000
    return fb


def save(fb_or_font, path, flavor=None):
    font = fb_or_font.font if hasattr(fb_or_font, "font") else fb_or_font
    font.flavor = flavor
    font.save(path)


save(base("Light", 300), "fixture-light.ttf")
save(base("Regular", 400), "fixture-regular.ttf")
save(base("Medium", 500), "fixture-medium.ttf")
save(base("Bold", 700, fs_sel=0x20, mac=1), "fixture-bold.ttf")
save(base("Regular", 400, cjk=False, family="Fixture Latin"), "fixture-latin.ttf")
save(base("Regular", 400, cff=True, family="Fixture Serif SC"), "fixture-cff.otf")
save(base("Regular", 400, os2v=3, family="Fixture Old"), "fixture-os2v3.ttf")
save(base("Italic", 400, fs_sel=0x01, mac=2, italic_angle=-12, family="Fixture Slant"), "fixture-italic.ttf")
save(base("Regular", 400, tall=True, family="Fixture Tall"), "fixture-tall.ttf")

# WOFF 1.0 of the regular face.
f = TTFont("fixture-regular.ttf"); save(f, "fixture-regular.woff", flavor="woff")

# Mac-only cmap (platform 0 records only) + STAT + DSIG.
f = TTFont("fixture-regular.ttf")
f["cmap"].tables = [t for t in f["cmap"].tables if t.platformID == 0]
dsig = newTable("DSIG"); dsig.ulVersion = 1; dsig.usFlag = 0; dsig.usNumSigs = 0; dsig.signatureRecords = []; f["DSIG"] = dsig
stat = newTable("STAT"); stat.table = otTables.STAT(); stat.table.Version = 0x00010001
stat.table.DesignAxisRecordSize = 8; stat.table.DesignAxisCount = 0; stat.table.DesignAxisRecord = None
stat.table.AxisValueCount = 0; stat.table.AxisValueArray = None; stat.table.ElidedFallbackNameID = 2
f["STAT"] = stat
f.save("fixture-maccmap.ttf")

# Symbol font: only a (3,0) cmap.
f = TTFont("fixture-latin.ttf")
from fontTools.ttLib.tables._c_m_a_p import cmap_format_4
sub = cmap_format_4(4); sub.platformID, sub.platEncID, sub.language = 3, 0, 0
sub.cmap = {0xF020 + i: "latin" for i in range(60)}
f["cmap"].tables = [sub]
f.save("fixture-symbol.ttf")

# Two-face collection (Simplified + Japanese named), sharing glyph tables.
sc = TTFont("fixture-regular.ttf"); jp = TTFont("fixture-bold.ttf")
for rec in jp["name"].names:
    if rec.nameID in (1, 4, 6, 16):
        rec.string = rec.toUnicode().replace("Fixture Sans SC", "Fixture Sans JP").replace("FixtureSansSC", "FixtureSansJP")
c = TTCollection(); c.fonts = [jp, sc]; c.save("fixture-pair.ttc")

# Variable font: fvar + (empty) gvar + STAT, default wght 400.
fb = base("Regular", 400, family="Fixture VF")
fb.setupFvar([("wght", 300, 400, 700, "Weight")], [{"stylename": "Light", "location": {"wght": 300}}, {"stylename": "Bold", "location": {"wght": 700}}])
fb.setupGvar({})
fb.setupStat([{"tag": "wght", "name": "Weight", "values": [{"value": 400, "name": "Regular", "flags": 2}]}])
save(fb, "fixture-vf.ttf")

print("ok")
