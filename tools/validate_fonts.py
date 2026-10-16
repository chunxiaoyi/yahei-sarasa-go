#!/usr/bin/env python3
"""Independent check of a generated font folder with fontTools.

The Go program verifies every face as it builds it; this script re-checks the
written files with a different implementation, against the same embedded
Windows 11 golden records, so a bug in the Go side cannot hide itself.

    pip install fonttools
    python tools/validate_fonts.py result/            # or a package's fonts/ folder

Checks per face: name IDs 1/2/3/4/6/16/17 equal the golden records exactly
(no extra identity records), OS/2 weight + fsSelection, head ClearType bit,
Windows Unicode cmap present, Segoe UI cmap inside the Windows 11 whitelist,
and the documented face order of each collection.
"""
import json
import pathlib
import re
import sys

from fontTools.ttLib import TTCollection, TTFont

ROOT = pathlib.Path(__file__).resolve().parent.parent
GOLDEN = json.loads((ROOT / "internal/config/testdata/windows11-name-golden.json").read_text(encoding="utf-8"))
FS_SELECTION = {
    "Regular": 0x0040, "RegularUI": 0x0040, "Bold": 0x0020, "BoldUI": 0x0020,
    "Light": 0x0100, "LightUI": 0x0100, "SimSun": 0x0040, "NSimSun": 0x0040,
    "SimSunExtB": 0x0040, "SegoeUIRegular": 0x0040,
}
SEMIBOLD = {
    "D2SemiBold": [
        (3, 1, 0x409, 1, "Microsoft YaHei"), (3, 1, 0x409, 2, "SemiBold"),
        (3, 1, 0x409, 3, "Microsoft YaHei-SemiBold"), (3, 1, 0x409, 4, "Microsoft YaHei SemiBold"),
        (3, 1, 0x409, 6, "MicrosoftYaHei-SemiBold"), (3, 1, 0x409, 16, "Microsoft YaHei"),
        (3, 1, 0x409, 17, "SemiBold"), (3, 1, 0x804, 1, "微软雅黑"), (3, 1, 0x804, 2, "SemiBold"),
        (3, 1, 0x804, 3, "微软雅黑-SemiBold"), (3, 1, 0x804, 4, "微软雅黑 SemiBold"),
        (3, 1, 0x804, 6, "MicrosoftYaHei-SemiBold"), (3, 1, 0x804, 16, "微软雅黑"), (3, 1, 0x804, 17, "SemiBold"),
    ],
    "D2SemiBoldUI": [
        (3, 1, 0x409, 1, "Microsoft YaHei UI"), (3, 1, 0x409, 2, "SemiBold"),
        (3, 1, 0x409, 3, "Microsoft YaHei UI-SemiBold"), (3, 1, 0x409, 4, "Microsoft YaHei UI SemiBold"),
        (3, 1, 0x409, 6, "MicrosoftYaHeiUI-SemiBold"), (3, 1, 0x409, 16, "Microsoft YaHei UI"),
        (3, 1, 0x409, 17, "SemiBold"), (3, 1, 0x804, 1, "Microsoft YaHei UI"), (3, 1, 0x804, 2, "SemiBold"),
        (3, 1, 0x804, 3, "Microsoft YaHei UI SemiBold"), (3, 1, 0x804, 4, "Microsoft Yahei UI SemiBold"),
        (3, 1, 0x804, 6, "MicrosoftYaHeiUI-SemiBold"), (3, 1, 0x804, 16, "Microsoft YaHei UI"), (3, 1, 0x804, 17, "SemiBold"),
    ],
}
LAYOUT = {
    "msyh.ttc": ["Regular", "RegularUI"],
    "msyhbd.ttc": ["Bold", "BoldUI", "D2SemiBold", "D2SemiBoldUI"],
    "msyhl.ttc": ["Light", "LightUI"],
    "simsun.ttc": ["SimSun", "NSimSun"],
    "simsunb.ttf": ["SimSunExtB"],
    "segoeui.ttf": ["SegoeUIRegular"],
}
IDENTITY_IDS = {1, 2, 3, 4, 6, 16, 17}


def segoe_whitelist():
    src = (ROOT / "internal/config/segoe_coverage_d4.go").read_text(encoding="utf-8")
    ranges = [(int(a, 16), int(b, 16)) for a, b in re.findall(r"\{0x([0-9A-F]{4}), 0x([0-9A-F]{4})\}", src)]
    allowed = set()
    for a, b in ranges:
        allowed.update(range(a, b + 1))
    assert len(allowed) == 3996, len(allowed)
    return allowed


def expected(key):
    if key in SEMIBOLD:
        return set(SEMIBOLD[key]), 600, 0x0100
    g = GOLDEN[key]
    recs = {(r["platform_id"], r["encoding_id"], r["language_id"], r["name_id"], r["value"]) for r in g["records"]}
    return recs, g["weight"], FS_SELECTION[key]


def check_face(font, key, allowed, errors, where):
    want, weight, fs = expected(key)
    got = set()
    for n in font["name"].names:
        if n.nameID in IDENTITY_IDS:
            got.add((n.platformID, n.platEncID, n.langID, n.nameID, n.toUnicode()))
    if got != want:
        missing, extra = want - got, got - want
        errors.append(f"{where}: identity mismatch; missing {sorted(missing)[:3]} extra {sorted(extra)[:3]}")
    ids = {n.nameID for n in font["name"].names}
    if not ids <= IDENTITY_IDS | {0, 5, 13, 14}:
        errors.append(f"{where}: unexpected name IDs {sorted(ids - IDENTITY_IDS - {0, 5, 13, 14})}")
    os2 = font["OS/2"]
    if os2.usWeightClass != weight:
        errors.append(f"{where}: weight {os2.usWeightClass} want {weight}")
    if os2.fsSelection != fs:
        errors.append(f"{where}: fsSelection {os2.fsSelection:#06x} want {fs:#06x}")
    if not font["head"].flags & (1 << 13):
        errors.append(f"{where}: head ClearType bit 13 not set")
    tables = {(t.platformID, t.platEncID) for t in font["cmap"].tables}
    if not tables & {(3, 1), (3, 10)}:
        errors.append(f"{where}: no Windows Unicode cmap")
    if key == "SegoeUIRegular":
        cmap = font["cmap"].getBestCmap()
        outside = [cp for cp in cmap if cp not in allowed]
        if outside:
            errors.append(f"{where}: {len(outside)} code points outside the Win11 Segoe whitelist, e.g. U+{outside[0]:04X}")
        if any(cp not in cmap for cp in range(0x20, 0x7F)):
            errors.append(f"{where}: ASCII incomplete")
        if any(0x4E00 <= cp <= 0x9FFF for cp in cmap):
            errors.append(f"{where}: Segoe UI claims CJK (would break YaHei UI fallback)")


def main(folder):
    folder = pathlib.Path(folder)
    if (folder / "fonts").is_dir():
        folder = folder / "fonts"
    allowed = segoe_whitelist()
    errors, checked = [], 0
    present = sorted(p.name for p in folder.iterdir() if p.suffix.lower() in (".ttf", ".ttc"))
    for name in present:
        keys = LAYOUT.get(name)
        if keys is None:
            errors.append(f"{name}: not a known output file")
            continue
        path = folder / name
        if name.endswith(".ttc"):
            fonts = TTCollection(str(path)).fonts
        else:
            fonts = [TTFont(str(path))]
        if len(fonts) != len(keys):
            errors.append(f"{name}: {len(fonts)} faces, want {len(keys)}")
            continue
        for i, (font, key) in enumerate(zip(fonts, keys)):
            check_face(font, key, allowed, errors, f"{name}#{i} ({key})")
            checked += 1
    for e in errors:
        print("FAIL", e)
    print(f"{checked} faces in {len(present)} files checked, {len(errors)} problem(s)")
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1] if len(sys.argv) > 1 else "result"))
