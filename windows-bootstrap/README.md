# AppleMusic 专用 WSL 引导器

这是 Windows GUI 后端可直接复用的 WSL2 管理层。它只创建和管理一个带随机实例后缀的专用发行版，例如：

```text
AppleMusic-Runtime-2f8a34c1
```

它不会启动、终止、导出、修改或注销用户已有的 `Ubuntu`、`Debian` 等发行版，也不会调用会影响全部 WSL 的 `wsl --shutdown`。

## 已实现

- 部署前检查 WSL 平台是否可用；不可用时**不自动安装 WSL**，直接返回明确的错误码和提示，由用户自行安装 WSL2（管理员运行 `wsl --install` 并重启）。
- 下载固定版本的 Ubuntu WSL 镜像（24.04.4，gzip tar 根文件系统）并验证固定 SHA-256；优先从中国科学技术大学（USTC）镜像下载，失败时自动切换到阿里云、华为云、官方源等备用镜像。
- 验证本地 wrapper payload 的整体 SHA-256，拒绝缺失或被修改的文件。
- 合成独立 runtime tar，再通过 `wsl --import ... --version 2` 导入。
- 在发行版内写入安装实例所有权标记，所有停止和删除操作前都重新校验。
- 禁用专用发行版的 Windows 程序互操作和磁盘自动挂载。
- 将默认用户设置为不可登录的 `applemusic-runtime`，降低用户误入后保存数据的概率。
- wrapper 入口使用 Linux 内核 `flock`，并发启动不会产生两个服务实例。
- wrapper 始终以 `root` 和固定入口运行，满足 `chroot`、`mknod` 以及工作目录要求。
- 中断导入留下的旧目录只会被原子移动到 `recovery` 保存，程序不会递归删除它。
- `remove` 校验完整 tar 和实例标记、先提交删除事务状态，成功后才执行 `wsl --unregister`。

## 构建

在 PowerShell 中运行：

```powershell
cd windows-bootstrap
.\build.ps1
```

输出目录：

```text
dist\AppleMusicWSL\
├── AppleMusicWSL.exe
├── README.md
└── payload\
    ├── wrapper
    └── rootfs\
```

`payload` 必须和 EXE 一起分发。最终安装器可以压缩这部分文件，但首次运行前必须还原成上述目录结构。

构建脚本使用全新 staging 目录，运行新 EXE 的包内 `verify` 后才替换旧发布目录；旧版本遗留的文件不会混入新包。

## 命令

全自动安装或续装：

```powershell
.\AppleMusicWSL.exe install
```

查询结构化状态：

```powershell
.\AppleMusicWSL.exe status --json
```

仅校验随包 payload，不修改 WSL：

```powershell
.\AppleMusicWSL.exe verify --json
```

首次登录（密码在控制台中不回显）：

```powershell
.\AppleMusicWSL.exe login
```

如果返回 `two_factor_required`，请在 60 秒内执行：

```powershell
.\AppleMusicWSL.exe submit-code
```

登录成功后启动 wrapper：

```powershell
.\AppleMusicWSL.exe start
```

停止专用发行版：

```powershell
.\AppleMusicWSL.exe stop
```

备份后删除专用发行版：

```powershell
.\AppleMusicWSL.exe remove
```

默认备份写入：

```text
%USERPROFILE%\Documents\AppleMusicDownloader Backups\
```

也可以指定不存在的目标文件：

```powershell
.\AppleMusicWSL.exe remove --backup "D:\Backups\AppleMusic-Runtime.tar"
```

如果注销失败或进程在注销前后中断，再次执行 `remove` 会重新校验并复用已提交的备份。备份目标必须位于应用数据和 WSL VHDX 目录之外；路径检查使用 Windows 最终物理路径，且不会覆盖并发出现的同名文件。

## 安装行为

首次执行 `install` 时按以下顺序工作：

1. 校验 Windows/CPU 架构和 wrapper payload。
2. 创建每用户安装实例状态，不修改 WSL 全局默认发行版。
3. 检查 WSL 平台；缺失时不自动安装，返回 `wsl_platform_unavailable` 错误并提示用户自行安装 WSL2。
4. 下载并校验 Ubuntu WSL 镜像。
5. 生成带所有权标记的 runtime tar。
6. 导入随机命名的专用 WSL2 发行版。
7. 创建不可登录默认用户并执行 wrapper `1.2.0` 冒烟检查。
8. 只终止该专用发行版，使私有 `wsl.conf` 生效。

安装状态位于：

```text
%LOCALAPPDATA%\AppleMusicDownloader\bootstrap-state.json
```

若一次 `wsl --import` 中断且留下未注册目录，引导器会先等待两分钟排除 WSL 服务仍在后台工作，再将残留原样移动到：

```text
%LOCALAPPDATA%\AppleMusicDownloader\recovery\
```

随后用新的随机实例继续安装。`status --json` 的 `recovery_paths` 会列出这些保留目录；卸载器不得自动删除它们。

## 安全边界

- 当前 runtime 只支持 AMD64 Windows。现有 wrapper payload 是 x86_64 ELF，ARM64 Windows 会被明确拒绝。
- 系统基线为 Windows 10 2004（内部版本 19041）或更高版本，以及 Windows 11。
- WSL2 平台和内核仍由所有发行版共享；“专用”指独立注册、独立 VHDX 和独立文件系统，不是另一套 WSL 引擎。
- WSL 没有受支持的“对同一 Windows 用户隐藏/禁止启动某个发行版”权限模型。该发行版仍可能出现在 `wsl --list` 和资源管理器 WSL 入口中，同一用户的其他进程也能显式启动它。随机名称、不可登录默认用户以及禁用 automount/interop 只能减少误操作，不能构成安全边界。
- wrapper 的账户接口只绑定 `127.0.0.1`，不会主动暴露到局域网，但当前端口没有本地客户端认证；本机其他进程仍可能访问 token 接口。正式 GUI 发布前应增加带认证的命名管道 broker 或修改 wrapper 协议，不能把 loopback 当作安全边界。
- 日志目录权限按当前用户创建，但上层 GUI 仍应对 token 相关行脱敏并限制保留时间。
- `login` 从控制台或 GUI stdin 读取 Apple ID 与密码，不提供密码命令行参数。凭据通过 stdin 写入专用发行版 `/run` 下的 `0600` 临时文件，Linux 启动脚本读取后立即删除；Windows `wsl.exe` 参数不包含凭据。底层 Android 进程仍使用 `username:password` 协议，因此当前不支持本身含冒号的账号或密码。
- `submit-code` 只接受六位数字，并通过 stdin 写入一次性文件；wrapper 读取后会删除它。
- `start` 会先检查登录状态，未登录时立即返回 `login_required`，不会等待启动超时。

正式卸载器必须先调用 `remove` 并确认返回成功，再删除程序文件。默认备份和 `recovery` 目录应由用户明确确认后另行清理，不能随主程序静默删除。

## 稳定退出码

| 退出码 | 含义 |
|---:|---|
| 0 | 成功 |
| 20 | 需要重启 Windows 后续装 |
| 21 | 不支持的系统或 CPU 架构 |
| 22 | 镜像或 payload 完整性校验失败 |
| 23 | 发行版所有权不匹配 |
| 24 | 检测到需人工修复的残留状态 |
| 25 | wrapper 未就绪或端口冲突 |
| 26 | 尚未完成 Apple Music 登录 |
| 27 | 登录正在等待六位 2FA 验证码 |
| 28 | 登录凭据、验证码或登录流程失败 |

其他非零值表示平台命令、网络下载或文件系统操作失败。使用 `--json` 可以获得稳定的字符串错误码和详细消息。
