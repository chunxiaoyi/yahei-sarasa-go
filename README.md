# 雅黑更纱 yahei-sarasa-go

把别的字体伪装成微软雅黑，生成可以直接替换 Windows 11 系统字体的文件。默认使用 **Sarasa Gothic（更纱黑体）**，也可以用你自己的字体。

替换后的文件保留 Windows 11 原版字体的名称、字重与 fsSelection（取自原版字体的金标准记录，见 [GOLDEN.md](GOLDEN.md)），并补上真正的 **SemiBold 600**，解决 WinUI 3 中文标题被错误匹配到 Bold 700 的问题；Segoe UI 只接管原版覆盖的英文、数字等字符，中英文混排风格统一而中文回退不受影响。

## 使用

从 Releases 下载后双击 **`YaHeiSarasa.exe`** 打开图形界面（Windows 10/11，用系统自带的 Edge 显示窗口，无需安装任何东西）。

| 页面 | 做什么 |
| --- | --- |
| Sarasa 预设 | 两个按钮，一键下载 Sarasa 并生成、校验、打包 |
| 自定义字体 | 拖入自己的字体，按预设同样的规则生成 |
| 系统字体 | 查看 `C:\Windows\Fonts` 中这 6 个文件的现状，一键备份并校验 |
| 设置 | Sarasa 来源（下载 / 本地文件夹或 zip）、版本、hinted、输出位置、代理与镜像 |

### 两个预设

两个按钮就是原来的两个分支，逻辑原样保留，生成的文件与原分支**逐字节一致**（见下文“验证”）：

| Windows 看到的字体 | SemiBold 版（原 main） | 标准版（原 regular） |
| --- | --- | --- |
| 微软雅黑 Light 300 | Light | Light |
| 微软雅黑 Regular 400 | **SemiBold** | **Regular** |
| 微软雅黑 SemiBold 600 | SemiBold | SemiBold |
| 微软雅黑 Bold 700 | Bold | Bold |
| 宋体 / 新宋体 / SimSun-ExtB | SemiBold | SemiBold |
| Segoe UI Regular | **SemiBold** | **Regular** |

下载只通过 HTTP Range 取压缩包里需要的 4 个 `SarasaUiSC-*.ttf`（约 55 MiB，而不是 700 MiB 的整包），并缓存起来，第二个按钮不再联网。GitHub API 被限流时自动改走网页地址；可以设置代理（默认读取系统代理）或下载镜像前缀。

### 自定义字体

支持 TTF、OTF（CFF）、TTC/OTC、WOFF，可以一次放入多个字重或整套字体集合。

1. **自动分配**：按两个预设同样的规则，把最接近的字重放到各个位置。“标准”风格正文 400 用最接近 400 的字重；“SemiBold”风格正文 400 用最接近 600 的字重（同 SemiBold 版）。宋体始终取 600 那一档，Segoe UI 跟随正文。泛 CJK 字体集合（如 Noto Sans CJK 的 JP/KR/SC/TC/HK）优先选简体中文，等宽版本靠后。每个位置都可以手动改。
2. **体检**：GB2312 一/二级汉字、Big5 常用字、中文标点、ASCII 覆盖率，Segoe 白名单内能保留多少码位，行高（usWin 度量）与 Sarasa 的差别，有多少常用字会在 GDI 下被裁切，斜体，嵌入限制（fsType），没有 hinting 等。
3. **拦截**：不是中文字体（GB2312 一级汉字覆盖不到一半）却用于雅黑/宋体、Segoe UI 缺 ASCII、符号字体、可变 CFF2、仅位图字体、WOFF2（会给出转换命令）。被拦截的只是依赖它的输出文件，其余可以单独勾选生成。
4. **自动修正**（只作用于自定义字体，预设不受影响）：可变 TrueType 固定为默认实例并移除 fvar/gvar/STAT 等表；移除失效的 DSIG 和会干扰 DirectWrite 归类的 STAT；只有 Mac cmap 的补上 Windows Unicode 记录；OS/2 v2/v3 升到 v4（golden 的 WWS 位需要）；按实际覆盖补上 936/1252 代码页位；macStyle 与 golden fsSelection 对齐。
5. **身份**：与预设完全相同的 Windows 11 金标准记录；只有 name ID 0（版权）保留源字体自己的声明，并带上源字体的授权说明（ID 13/14）。字形、度量、OpenType 布局表逐字节不变。

> 请确认字体授权允许修改。可变字体只能用默认实例；需要多字重时先实例化：`fonttools varLib.instancer 字体.ttf wght=600 --static -o 字体-600.ttf`。

### 输出

```text
output/
  YaHei-Sarasa-Regular-1.0.42-hinted/
    fonts/            msyh.ttc msyhbd.ttc msyhl.ttc simsun.ttc simsunb.ttf segoeui.ttf
    manifest.json     来源与哈希、每个 face 的身份与字形来源
    SHA256SUMS.txt
    安装说明.txt
  YaHei-Sarasa-Regular-1.0.42-hinted.zip
```

把 `fonts` 中的文件覆盖到 `C:\Windows\Fonts` 的同名文件即可（系统字体受保护且被占用，通常在 WinPE/恢复环境中复制）。替换前先在“系统字体”页备份原文件。

## 命令行

`yahei-sarasa-cli.exe`（或任何平台上 `go run .`）：

```powershell
yahei-sarasa-cli build -preset semibold -source-dir D:\Sarasa      # 与旧版用法一致，写入 .\result
yahei-sarasa-cli build -preset regular -out .\output -zip -cache auto
yahei-sarasa-cli custom -style regular A-Light.ttf A-Regular.ttf A-Medium.ttf A-Bold.ttf -out .\output -zip
yahei-sarasa-cli custom -font Set.ttc -assign segoe=Latin.ttf -outputs msyh.ttc,msyhbd.ttc,segoeui.ttf
yahei-sarasa-cli inspect C:\Windows\Fonts\msyh.ttc -json
```

不带参数运行即打开图形界面。旧用法（只给 `-source-dir`、`-result`、`-hinted` 等参数）保持不变，`-preset` 默认 `semibold`。

## 构建

只用 Go 标准库（Go 1.22+），无 Python / FontForge / 7z 运行时依赖。

```powershell
.\build.ps1          # Windows：生成 dist\YaHeiSarasa.exe（窗口程序）与 dist\yahei-sarasa-cli.exe
```

```sh
./build.sh           # 任意系统交叉编译 Windows amd64/arm64
```

## 验证

```sh
go test ./...
go vet ./...
YSG_SARASA_DIR=/path/to/sarasa go test ./internal/recipe -run RealSarasa -v
python tools/validate_fonts.py output/<包名>      # 可选：用 fontTools 独立复核
```

- `internal/recipe/testdata/regression-sarasa.json` 记录了原 main（b9aa06c）与原 regular（ee9e874）程序对 Sarasa 1.0.42 hinted/unhinted 及一组合成字体的输出哈希。两个预设必须逐字节复现；合成字体那组每次 `go test` 都会跑。
- `tools/validate_fonts.py` 用 fontTools 重新读一遍生成的文件，逐个 face 对照金标准记录、字重、fsSelection、ClearType 位与 Segoe 白名单。

## 安全

图形界面是内置在程序里的本地网页：只监听 `127.0.0.1` 的随机端口，每次启动生成随机令牌，并校验 Host 头；窗口关闭后程序自动退出。程序只读取系统字体（用于状态与备份），不会修改 `C:\Windows`。

## 结构

```text
main.go                 入口：图形界面 / build / custom / inspect
internal/config         Windows 11 金标准身份（name/weight/fsSelection/Segoe 白名单）
internal/otfont         sfnt/TTC 读写、name 表、cmap 白名单、体检与规范化
internal/recipe         六个输出文件的结构与两个预设（逐字节回归测试在这里）
internal/custom         自定义字体：体检、自动分配、评估
internal/sarasa         Sarasa 获取：本地 / 缓存 / Range 下载
internal/fetch          GitHub 发布查询、代理
internal/remotezip      HTTP Range 读取 zip
internal/pack           打包：manifest、校验和、说明、zip
internal/sysfonts       系统字体状态与备份
internal/gui            本地服务与界面（web/ 内嵌）
internal/platform       Windows 原生对话框、Edge 窗口、系统代理
```

历史实验记录（D2/D4 等）见 [changelog.md](changelog.md)。
