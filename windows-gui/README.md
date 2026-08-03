# Apple Music 下载器 Windows GUI

项目基于[apple-music-downloader](https://github.com/zhaarey/apple-music-downloader)改造，提供了环境配置及GUI支持。

## 下载
前往[Releases](https://github.com/Herobrine1224-Self/AppleMusicDownloaderGUI/releases)界面下载最新zip压缩包程序。

## 使用

保持发布目录完整，双击 `AppleMusicDownloader.exe`：

1. 请确保系统已安装 WSL2。首次打开时程序会检测 WSL；未检测到时会提示自行安装，程序不会自动安装 WSL。
2. 检测到 WSL2 后，点击“一键部署”创建专用 WSL2 环境。
3. 输入 Apple ID、密码以及需要的六位验证码。
4. 在“下载”页粘贴 Apple Music 专辑、单曲或艺人链接，选择音质类型和保存目录，点击“获取列表”加载歌曲；单曲链接同样会显示列表。
5. 单击列表中的歌曲行即可选中或取消（被选中的歌曲以“✓”和底色标记），再点击“开始下载”下载所选歌曲。
6. 若下载失败，请重新尝试下载，可能会因为网络波动或网速过慢导致下载失败。
7. 在“运行环境”页可以“退出 Apple ID”清除登录状态；退出后下次下载前需要重新登录。
8. 正常关闭程序时会自动停止专用 WSL 发行版并在1分钟后释放其后台资源；再次下载时会自动启动。

GUI、`runtime`、`downloader`、`tools` 必须放在同一发布目录中。不要只移动 EXE。

## 删除
不要只删除程序文件夹，先在程序内移除专属WSL后再删除程序文件夹。

## WSL 数据边界

* 程序只创建随机命名的 `AppleMusic-Runtime-\*` 专用发行版，不修改现有 Ubuntu、Debian 或默认发行版。
* 同一 Windows 用户只运行一个 GUI 实例，避免某个窗口退出时中断另一个窗口的下载或登录任务。
* 退出时只停止本程序的专用发行版，不会关闭其他程序正在使用的 WSL 发行版；若仍有其他 WSL 工作负载，`VmmemWSL` 会继续运行。
* 专用发行版可能仍会出现在 Windows 的 WSL/资源管理器入口中；Windows 没有可靠的每应用隐藏机制。
* 默认用户不可登录，互操作与磁盘自动挂载被关闭，降低误操作概率。
* “备份并移除环境”会先导出并校验完整 tar，成功后才注销专用发行版。默认备份位于 `文档\\AppleMusicDownloader Backups`。
* 应用设置和下载记录位于 `%LOCALAPPDATA%\\AppleMusicDownloader`；下载的音乐位于 GUI 选择的目录，不存放在 WSL 虚拟磁盘中。
* 运行日志（`wrapper.log`、下载任务日志、`gui-startup.log`）保存在程序所在目录的 `logs` 文件夹中。

## 凭据

Apple ID、密码和验证码通过匿名标准输入传给 WSL 引导器，不进入 Windows 命令行，不写入 GUI 设置或任务日志。引导器只在 Linux `/run` 中暂存登录输入。
