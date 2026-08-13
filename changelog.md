# yahei-sarasa-semibold-go — D4

> **D4 严格从 D2 分支。** D2 已验证解决 WinUI `600 -> Bold 700` 的问题；D4 只继续解决正文中 `Segoe UI Regular 400` 截走英文/数字、导致中英文混排风格不一致的问题。

## D4 的目标

D2 实机 DirectWrite probe 显示正文会被拆成：

```text
"网络"
  -> Microsoft YaHei UI Regular 400
  -> MSYH.TTC
  -> 实际 SarasaUiSC-SemiBold

" | Intel(R) 82574L Gigabit Network Connection"
  -> Segoe UI Regular 400
  -> SEGOEUI.TTF
  -> 微软原版 Segoe UI
```

D3 曾把完整 Sarasa cmap 复制给 `Segoe UI Regular`，因此英文/数字统一了，但 Segoe UI 也突然宣称支持中文，破坏了 D2 正常的中文 fallback，导致“高级网络设置”再次异常。

D4 修正这一点：

```text
Segoe UI Regular 400 identity
+ SarasaUiSC-SemiBold glyphs
+ Windows 11 原版 segoeui.ttf cmap coverage 白名单
```

预期行为：

```text
中文
  Segoe UI cmap 中不存在
  -> fallback 到 Microsoft YaHei UI
  -> D2 的 Sarasa-backed 400/600 face

英文、数字、原版 Segoe 覆盖且 Sarasa 也有的字符
  -> Segoe UI Regular 400
  -> 实际使用 SarasaUiSC-SemiBold glyph
```

因此目标不是让整个字符串变成一个 Segoe glyph run，而是恢复 Windows 原来的 fallback 分工，同时让两个 glyph run 的实际视觉字形都来自 Sarasa SemiBold。

## D2 基线完整保留

D4 不带入 C2 vertical metrics 修改，也不修改 D2 已验证成功的 YaHei 结构：

```text
300  Microsoft YaHei / UI Light
     <- SarasaUiSC-Light

400  Microsoft YaHei / UI Regular
     <- SarasaUiSC-SemiBold

600  Microsoft YaHei / UI SemiBold
     <- SarasaUiSC-SemiBold

700  Microsoft YaHei / UI Bold
     <- SarasaUiSC-Bold
```

`msyhbd.ttc` face 顺序仍是：

```text
face 0  Microsoft YaHei Bold         700
face 1  Microsoft YaHei UI Bold      700
face 2  Microsoft YaHei SemiBold     600
face 3  Microsoft YaHei UI SemiBold  600
```

## D4 新增的 `segoeui.ttf`

Windows 身份来自用户提供的 Windows 11 原版 `segoeui.ttf`：

```text
Family            Segoe UI
Subfamily         Regular
Unique ID         Segoe UI Regular
Full name         Segoe UI
PostScript name   SegoeUI
OS/2 weight       400
OS/2 fsSelection  0x0040
```

字形源仍是：

```text
SarasaUiSC-SemiBold.ttf
```

### cmap 白名单

仓库内 `internal/config/segoe_coverage_d4.go` 保存原版 Windows 11 `segoeui.ttf` 的 BMP coverage 范围快照。

来源文件 SHA-256：

```text
8134dbcd09e7b123c9a7f229d49cffbcb01352cc72ea5e1076b65d0dca9f73cd
```

原版 Segoe UI Regular 的 cmap 共覆盖 3,996 个 codepoint，且只有 format-4 BMP cmap。

D4 的输出是一个**交集**：

```text
D4 Segoe cmap = Windows 11 Segoe whitelist ∩ SarasaUiSC-SemiBold cmap
```

因此 D4 不会新增任何原版 Segoe 没有的 codepoint，也不会重新接管 CJK。

需要注意：Sarasa 并不包含原版 Segoe 的全部 3,996 个字符。在用于本地验证的 Sarasa/D2 源上，最终交集为 1,863 个映射。原版 Segoe 支持但 Sarasa 不支持的较少见字符会改走系统 fallback；D4 是针对中文 Windows UI 的受控实验版本，并不声称保持 Segoe 的完整多语言 coverage。

## 输出

`result/` 恰好 6 个文件：

```text
msyh.ttc
msyhbd.ttc
msyhl.ttc
simsun.ttc
simsunb.ttf
segoeui.ttf
```

D4 对同一组 Sarasa source 做回归验证时，前 5 个文件与 D2 **byte-for-byte 完全一致**；唯一新增输出是 `segoeui.ttf`。

## 下载逻辑

仍然只有 Go 标准库，无 Python / FontForge / 7z 运行时依赖。

网络模式继续通过 GitHub Release + HTTP Range 只取：

```text
SarasaUiSC-SemiBold.ttf
SarasaUiSC-Bold.ttf
SarasaUiSC-Light.ttf
```

原版 Windows `segoeui.ttf` **不需要在运行时提供**；它只用于开发期提取 name identity 与 cmap whitelist，快照已经嵌入源码。

也可以离线：

```powershell
go run . -source-dir D:\Sarasa
```

## D4 安装后的关键验证

重新运行之前的 DirectWrite probe。

正文应重新恢复为两个 run：

```text
"网络"
  -> Microsoft YaHei UI Regular 400
  -> MSYH.TTC

" | Intel(R)..."
  -> Segoe UI Regular 400
  -> SEGOEUI.TTF
```

但两边实际 glyph 都应来自 Sarasa SemiBold。

最关键的是标题：

```text
requested weight=600, text="高级网络设置"
  -> Microsoft YaHei UI SemiBold 600
  -> MSYHBD.TTC face 3
```

如果这两项同时成立，D4 就达到了预期：保留 D2 的 600 修复，同时保留 D3 的英文/数字视觉统一。

## 测试

```powershell
go test ./...
go vet ./...
```

D4 额外测试包括：

- Windows 11 Segoe coverage 快照总数固定为 3,996；
- ASCII / 常用 Latin 在白名单内；
- CJK 与非 BMP 不在白名单；
- cmap filter 只保留允许 codepoint；
- format-4 glyph ID round-trip；
- 输出 cmap 不允许出现 whitelist 外 codepoint。
