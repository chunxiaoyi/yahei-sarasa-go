这是一个基于 **Sarasa Gothic（更纱黑体）** 制作的 Windows 系统字体替换项目，主要用于替换微软雅黑、宋体及 Segoe UI 的部分字形，并针对 Windows 11 / WinUI 3 的字体匹配进行了适配。

项目保留 Windows 原字体的名称与字重信息，并补充了真实的 **SemiBold 600** 字重，解决 WinUI 3 中中文标题被错误匹配到 Bold 700 的问题。同时对 Segoe UI 的英文和数字进行替换，使中英文混排风格更加统一。

项目提供两个分支：

- **main**：使用 Sarasa UI SC **SemiBold** 替换 Microsoft YaHei Regular，整体显示更粗、更醒目。
- **regular**：使用 Sarasa UI SC **Regular** 替换 Microsoft YaHei Regular，显示效果更接近标准 Regular 字重。

两者其余处理逻辑保持一致。