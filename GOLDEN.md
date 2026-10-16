# Windows 11 golden source — D4

## YaHei / SimSun originals

- `msyh.ttc`: `d79c55e68b1131eea0cc1c47be4f572d964f28c682e143db2ad09c1e4cb07a3f`
- `msyhbd.ttc`: `4508821b3dffe01f0ef5e5326a3e60df705a44633858811f67b6982dce3f6ee6`
- `msyhl.ttc`: `7e9bdf90bb5d3fe1b5975fc8ae31944b8fa674122261f92c28d4ec0b9c482fa1`
- `simsun.ttc`: `120a51d2b14eb588700a21e15fc301f0ea55d7c1a4e1f3ae61db83b7c8d42cd6`
- `simsunb.ttf`: `a67e70adce24390c43a0c5dfc0856dc81660e91489db930999aa0973780e8a67`

## Segoe UI Regular original

D4 additionally uses the user-supplied Windows 11 original:

- `segoeui.ttf`: `8134dbcd09e7b123c9a7f229d49cffbcb01352cc72ea5e1076b65d0dca9f73cd`

From it D4 snapshots two things only:

1. Name IDs 1/2/3/4/6/16/17 into `windows11-name-golden.json`.
2. Its 3,996-codepoint BMP cmap coverage as ranges in `segoe_coverage_d4.go`.

The Microsoft font bytes are not embedded or distributed.

Name IDs 0 and 5 remain project-controlled: copyright is `Made from sarasa by GPT and Claude` (v1 wrote `Made from sarasa by GPT`), and version comes from the Sarasa source. Name ID 0 is not used by Windows font matching.

## C1 fsSelection carried through D2/D4

- YaHei / YaHei UI Regular: `0x0040`
- YaHei / YaHei UI Bold: `0x0020`
- YaHei / YaHei UI Light: `0x0100`
- SimSun / NSimSun / SimSun-ExtB: `0x0040`
- Segoe UI Regular: `0x0040`

Vertical metrics remain Sarasa's values; C2 is not part of D4.

## D2 SemiBold extension retained

```text
Microsoft YaHei SemiBold     600
Microsoft YaHei UI SemiBold  600
```

These are project experimental identities because Windows 11 has no original YaHei SemiBold face.

## D4 cmap rule

D4 is based on D2, not D3.

The added `segoeui.ttf` is built from `SarasaUiSC-SemiBold.ttf`, then its Unicode mapping is restricted to:

```text
Windows 11 Segoe UI Regular coverage ∩ Sarasa source coverage
```

This is specifically designed to avoid the D3 failure mode where copying Sarasa's entire cmap caused Segoe UI to claim Chinese and bypass Microsoft YaHei UI fallback.

## D4-Regular 400-path rule (former `regular` branch, now the `regular` preset)

D4-Regular keeps every D4 behavior except that both 400-weight UI glyph sources move together from SemiBold to true Regular:

```text
D4:         Microsoft YaHei / UI Regular 400 <- SarasaUiSC-SemiBold.ttf
D4-Regular: Microsoft YaHei / UI Regular 400 <- SarasaUiSC-Regular.ttf

D4:         Segoe UI Regular 400 <- SarasaUiSC-SemiBold.ttf
D4-Regular: Segoe UI Regular 400 <- SarasaUiSC-Regular.ttf
```

The Segoe UI Windows 11 identity and D4 cmap whitelist strategy remain unchanged. D2 SemiBold 600 remains SarasaUiSC-SemiBold; SimSun/NSimSun/ExtB remain SarasaUiSC-SemiBold; Bold and Light remain unchanged.

## v2: one code base, two presets

Since v2 the two branches are two presets over one build path (`internal/recipe`). Every golden value above is applied by the same functions as before, in the same order. The only intended difference from the original programs is the name ID 0 string (`... by GPT and Claude`); with the v1 string the output is byte-for-byte identical:

- `semibold` preset = D4 (`main` @ b9aa06c)
- `regular` preset = D4-Regular (`regular` @ ee9e874)

`internal/recipe/testdata/regression-sarasa.json` pins the SHA-256 of all six outputs of both original programs for Sarasa 1.0.42 (hinted and unhinted) and for a synthetic fixture. The tests check two things:

1. Built with the v1 copyright string, every output matches the original program byte for byte.
2. Built normally, every face differs from (1) only in the name ID 0 record; all other tables are byte-identical.

The hashes of the Windows 11 originals listed at the top are also embedded (`config.Windows11OriginalSHA256`) so the GUI can tell an original system file from a replaced one.

## Custom fonts and the golden source

Custom builds use exactly the same identity records, weights, fsSelection values, face order and Segoe UI whitelist. They differ only where the golden source does not speak:

- Name ID 0 keeps the source font's own copyright notice (instead of `Made from sarasa by GPT and Claude`), and the source's license description / URL (IDs 13/14) are carried over. Name ID 5 follows the same version rule as the presets.
- `head.macStyle` is set to agree with the golden fsSelection (bold bit only on the 700 faces).
- Source fonts are normalised first (variation tables / STAT / DSIG removed, Windows cmap records added when missing, OS/2 v2-3 raised to v4, coverage-backed code page bits). None of this is ever applied to the Sarasa presets.
