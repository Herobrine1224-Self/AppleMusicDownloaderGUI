# Apple Music 下载器 Windows GUI

项目基于[apple-music-downloader](https://github.com/zhaarey/apple-music-downloader)改造，提供了一键环境配置及GUI支持。

## 下载
前往[releases](https://github.com/Herobrine1224-Self/AppleMusicDownloaderGUI/releases)界面下载zip压缩包。

## 使用

保持发布目录完整，双击 `AppleMusicDownloader.exe`：

1. 首次打开时点击“一键部署”。
2. Windows 组件缺失时确认一次 UAC；如果提示重启，重启后再次打开程序。
3. 输入 Apple ID、密码以及需要的六位验证码。
4. 在“下载”页粘贴 Apple Music 专辑、单曲或艺人链接，选择音质类型和保存目录，然后开始下载。
5. 若下载失败，重新尝试下载，可能会因为网络波动或网速过慢导致下载失败，在下载路径不变的情况下会跳过重复下载，仅重新下载失败的歌曲。
6. 关于退出，程序退出后后台会留有200MB内存占用的"VmmemWSL"进程，方便下次快速启动，可任务管理器手动结束进程或使用"wsl --shutdown"指令结束。

GUI、`runtime`、`downloader`、`tools` 必须放在同一发布目录中。不要只移动 EXE。

## WSL 数据边界

* 程序只创建随机命名的 `AppleMusic-Runtime-\*` 专用发行版，不修改现有 Ubuntu、Debian 或默认发行版。
* 专用发行版可能仍会出现在 Windows 的 WSL/资源管理器入口中；Windows 没有可靠的每应用隐藏机制。
* 默认用户不可登录，互操作与磁盘自动挂载被关闭，降低误操作概率。
* “备份并移除环境”会先导出并校验完整 tar，成功后才注销专用发行版。默认备份位于 `文档\\AppleMusicDownloader Backups`。
* 应用设置和下载记录位于 `%LOCALAPPDATA%\\AppleMusicDownloader`；下载的音乐位于 GUI 选择的目录，不存放在 WSL 虚拟磁盘中。

## 凭据

Apple ID、密码和验证码通过匿名标准输入传给 WSL 引导器，不进入 Windows 命令行，不写入 GUI 设置或任务日志。引导器只在 Linux `/run` 中暂存登录输入。

