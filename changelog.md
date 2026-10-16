# yahei-sarasa-go — v2.0

> **两个 Sarasa 预设（SemiBold 版 = D4，标准版 = D4-Regular）、图形界面、自定义字体，以及安装到系统与还原。** 预设规则的实验记录见下方 D4 一节。

## 变化

- **图形界面**：双击 `YaHeiSarasa.exe`。两个预设按钮（SemiBold 版、标准版），一键下载、生成、校验、打包。界面是程序内置的本地网页，用系统自带的 Edge 以应用窗口显示，文件选择用 Windows 原生对话框。
- **自定义字体**：TTF / OTF / TTC / OTC / WOFF；按两个预设的规则自动分配字重，可手动调整；生成前体检并解释每个风险，阻止会让中文界面缺字的组合；对常见问题自动修正（只作用于自定义字体）。
- **打包**：`fonts/` + `manifest.json` + `SHA256SUMS.txt` + `安装说明.txt`，可选 zip。
- **系统字体页**：查看 6 个目标文件是 Win11 原版（与金标准哈希一致）、本工具生成还是其他替换，一键备份并校验。
- **下载**：Sarasa 缓存（第二个预设不再联网）；GitHub API 限流时改走网页地址；Range 请求失败自动重试；解压先写临时文件再改名；支持系统代理、指定代理和镜像前缀；也可以直接用本地 SarasaUi-TTF-*.zip。
- **命令行**：`build`（`-preset semibold|regular`，默认 semibold；`-out/-zip/-cache`）、`custom`、`inspect`。
- **验证**：两个预设的基准输出哈希写入回归测试，逐字节比对；`tools/validate_fonts.py` 用 fontTools 独立复核。
- 版权字段（name ID 0）为 `Made from sarasa by GPT and Claude`，不参与 Windows 字体匹配；系统字体页把它和 `Made from sarasa by GPT` 都识别为本工具生成。
- 安装与还原：在「系统字体」页把字体包安装到系统、还原到任意一份备份、只清空字体缓存，都在下次重启时生效（MoveFileEx + PendingFileRenameOperations），换完字体后同一次重启里清空 `FontCache` 文件夹和 `FNTCACHE.DAT`。每次替换前把当前 6 个文件照原样备份（不论是否原版；非原版会提醒，但照样备份，因为它们往往比原版更难再找到），按哈希去重、按快照保存，只在用户要求时删除。安装前后两次校验哈希与字体身份；新文件沿用原文件的所有者和权限；数据文件夹受保护、拒绝被他人预先创建；重启前可取消，失败会完整回滚。界面只在执行时经 UAC 启动隐藏的管理员进程。命令行新增 `system status|install|restore|forget|clear-cache|cancel`。
- 自定义字体包加入 name ID 10 标记，系统字体页不会把装过的「微软字体改的自定义包」误认成原版；识别原版时也会看商标、厂商记录和 OS/2 厂商 ID。
- hinted / unhinted 默认按屏幕自动判定：任一屏幕缩放低于 150% 或关闭字体平滑时用 hinted，全部 150% 及以上用 unhinted，检测不到时用 hinted；理由显示在界面上，可手动改。命令行新增 `-hint auto|hinted|unhinted` 与 `display` 子命令。自定义字体缺少 hinting 时，按同样的判断决定是提示还是警告。
- Windows 构建：`build.cmd`（免执行策略）/ `build.ps1`（兼容 Windows PowerShell 5.1，检查 Go 版本与每一步退出码，可选 `-Test`、`-Arch arm64`）。
- 修正：图形界面退出时关闭 gui.log（Windows 上被占用的文件无法删除或覆盖）。

---

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
