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

Name IDs 0 and 5 remain project-controlled: copyright is `Made from sarasa by GPT`, and version comes from the Sarasa source.

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
