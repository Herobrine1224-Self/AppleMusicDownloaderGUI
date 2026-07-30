//go:build windows

package main

import (
	"applemusic/gui/internal/app"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

const appTitle = "Apple Music 下载器"

const (
	pageChecking = iota
	pageDeploy
	pageDeploying
	pageReboot
	pageLogin
	pageTwoFactor
	pageError
)

var (
	colorHeader = walk.RGB(31, 33, 38)
	colorAccent = walk.RGB(218, 38, 87)
	colorText   = walk.RGB(32, 34, 39)
	colorMuted  = walk.RGB(98, 102, 110)
	colorPage   = walk.RGB(250, 250, 251)
	colorOK     = walk.RGB(28, 132, 85)
	previewMode string
)

type gui struct {
	mw *walk.MainWindow

	bundle    app.Bundle
	bundleErr error
	store     app.Store
	settings  app.Settings
	history   []app.HistoryEntry
	model     *historyModel

	pages    []*walk.Composite
	mainTabs *walk.TabWidget

	headerStatus *walk.Label
	statusBar    *walk.StatusBarItem

	checkingTitle  *walk.Label
	checkingDetail *walk.Label
	checkingBar    *walk.ProgressBar

	deployButton *walk.PushButton
	deployNote   *walk.Label

	loginAppleID  *walk.LineEdit
	loginPassword *walk.LineEdit
	loginError    *walk.Label
	loginButton   *walk.PushButton
	loginProgress *walk.ProgressBar

	codeEdit     *walk.LineEdit
	codeError    *walk.Label
	codeButton   *walk.PushButton
	codeProgress *walk.ProgressBar

	errorTitle  *walk.Label
	errorDetail *walk.TextEdit
	retryButton *walk.PushButton
	retryAction func()

	linkEdit            *walk.LineEdit
	qualityCombo        *walk.ComboBox
	outputEdit          *walk.LineEdit
	downloadButton      *walk.PushButton
	cancelButton        *walk.PushButton
	downloadRetryButton *walk.PushButton
	taskPanel           *walk.Composite
	taskTitle           *walk.Label
	taskDetail          *walk.Label
	taskStats           *walk.Label
	taskProgress        *walk.ProgressBar
	taskLog             *walk.TextEdit

	historyTable        *walk.TableView
	historyOpenButton   *walk.PushButton
	historyDeleteButton *walk.PushButton
	historyClearButton  *walk.PushButton

	envDistro    *walk.Label
	envState     *walk.Label
	envLocation  *walk.Label
	envRecovery  *walk.Label
	stopButton   *walk.PushButton
	removeButton *walk.PushButton

	lastStatus      app.BootstrapStatus
	busy            bool
	opKind          string
	cancel          context.CancelFunc
	loginPending    bool
	retryRequest    *downloadRequest
	retryAfterLogin bool
	progressStats   downloadProgressStats
	closed          atomic.Bool
}

type downloadRequest struct {
	link      app.LinkInfo
	outputDir string
	quality   string
}

func main() {
	defer func() {
		if recovered := recover(); recovered != nil {
			startupErr := fmt.Errorf("unexpected GUI panic: %v", recovered)
			fmt.Fprintln(os.Stderr, startupErr)
			if store, err := app.DefaultStore(); err == nil {
				writeStartupError(store, startupErr)
			}
			walk.MsgBox(nil, appTitle, "程序启动失败，诊断信息已写入 gui-startup.log。", walk.MsgBoxOK|walk.MsgBoxIconError)
		}
	}()
	runtime.LockOSThread()
	walk.App().SetOrganizationName("AppleMusicDownloader")
	walk.App().SetProductName("AppleMusicDownloader")

	bundle, bundleErr := app.DiscoverBundle()
	store, storeErr := app.DefaultStore()
	settings := app.DefaultSettings()
	var history []app.HistoryEntry
	if storeErr == nil {
		if loaded, err := store.LoadSettings(); err == nil {
			settings = loaded
		}
		history, _ = store.LoadHistory()
	}

	g := &gui{
		bundle: bundle, bundleErr: bundleErr, store: store,
		settings: settings, history: history, model: &historyModel{},
		pages: make([]*walk.Composite, 7),
	}
	g.model.Set(history)
	if err := g.build(); err != nil {
		fmt.Fprintln(os.Stderr, "GUI startup failed:", err)
		writeStartupError(store, err)
		walk.MsgBox(nil, appTitle, err.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
		return
	}
	if storeErr != nil {
		g.bundleErr = storeErr
	}
	g.mw.Starting().Attach(g.start)
	g.mw.Closing().Attach(g.onClosing)
	g.mw.Run()
}

func writeStartupError(store app.Store, startupErr error) {
	if store.Dir == "" || startupErr == nil {
		return
	}
	if err := os.MkdirAll(store.Dir, 0700); err != nil {
		return
	}
	message := time.Now().Format(time.RFC3339) + " GUI startup failed: " + startupErr.Error() + "\r\n"
	_ = os.WriteFile(filepath.Join(store.Dir, "gui-startup.log"), []byte(message), 0600)
}

func (g *gui) build() error {
	qualityIndex := 0
	if g.settings.Quality == app.QualityAtmos {
		qualityIndex = 1
	}

	window := MainWindow{
		AssignTo:   &g.mw,
		Title:      appTitle,
		Size:       Size{Width: 1000, Height: 650},
		MinSize:    Size{Width: 900, Height: 650},
		Font:       Font{Family: "Segoe UI", PointSize: 9},
		Background: SolidColorBrush{Color: colorPage},
		Layout:     VBox{MarginsZero: true, SpacingZero: true},
		StatusBarItems: []StatusBarItem{
			{AssignTo: &g.statusBar, Text: "正在检查运行环境", Width: 520},
		},
		Children: []Widget{
			Composite{
				Background: SolidColorBrush{Color: colorHeader},
				MinSize:    Size{Height: 50},
				MaxSize:    Size{Height: 50},
				Layout:     HBox{Margins: Margins{Left: 22, Top: 10, Right: 22, Bottom: 10}, Spacing: 12},
				Children: []Widget{
					Label{Text: "Apple Music 下载器", TextColor: walk.RGB(255, 255, 255), Font: Font{Family: "Segoe UI", PointSize: 13, Bold: true}},
					Label{AssignTo: &g.headerStatus, Text: "检测中", TextColor: walk.RGB(216, 218, 223), TextAlignment: AlignFar, MinSize: Size{Width: 120}},
					HSpacer{},
				},
			},
			Composite{
				Background: SolidColorBrush{Color: colorPage},
				Layout:     VBox{MarginsZero: true, SpacingZero: true},
				Children: []Widget{
					g.checkingPage(),
					g.deployPage(),
					g.deployingPage(),
					g.rebootPage(),
					g.loginPage(),
					g.twoFactorPage(),
					g.errorPage(),
					g.readyTabs(qualityIndex),
				},
			},
		},
	}
	if err := window.Create(); err != nil {
		return err
	}
	g.updateHistoryActions()
	return nil
}

func (g *gui) start() {
	switch previewMode {
	case "deploy":
		g.showOnboarding(pageDeploy)
		g.headerStatus.SetText("尚未部署")
	case "login":
		g.showLogin("")
	case "two-factor":
		g.showTwoFactor()
	case "home", "home-task":
		g.showReady(app.BootstrapStatus{
			Installed: true, Owned: true, Running: true, Healthy: true,
			DistroName: "AppleMusic-Runtime-a65c1c13",
			InstallDir: filepath.Join(g.store.Dir, "wsl", "AppleMusic-Runtime-a65c1c13"),
		})
		if previewMode == "home-task" {
			g.taskPanel.SetVisible(true)
			g.taskTitle.SetText("正在下载示例曲目")
			g.taskDetail.SetText("曲目 1 / 12 · 示例艺人")
			g.taskStats.SetText("当前文件大小：42.8 MiB · 下载速度：6.4 MiB/s")
			_ = g.taskProgress.SetMarqueeMode(false)
			g.taskProgress.SetValue(42)
			g.taskLog.SetText("正在下载媒体数据\r\n")
			g.downloadButton.SetEnabled(false)
			g.cancelButton.SetVisible(true)
		}
	default:
		g.startInitialCheck()
	}
}

func (g *gui) checkingPage() Widget {
	return Composite{
		AssignTo: &g.pages[pageChecking],
		Visible:  true,
		Layout:   VBox{Margins: Margins{Left: 48, Top: 42, Right: 48, Bottom: 38}, Spacing: 14},
		Children: []Widget{
			VSpacer{},
			Label{AssignTo: &g.checkingTitle, Text: "正在检查运行环境", TextColor: colorText, Font: Font{Family: "Segoe UI", PointSize: 18, Bold: true}, MinSize: Size{Height: 32}},
			Label{AssignTo: &g.checkingDetail, Text: "请稍候", TextColor: colorMuted, MinSize: Size{Height: 24}},
			ProgressBar{AssignTo: &g.checkingBar, MarqueeMode: true, MinSize: Size{Height: 8}, MaxSize: Size{Height: 8}},
			VSpacer{},
		},
	}
}

func (g *gui) deployPage() Widget {
	return Composite{
		AssignTo: &g.pages[pageDeploy], Visible: false,
		Layout: VBox{Margins: Margins{Left: 48, Top: 38, Right: 48, Bottom: 38}, Spacing: 12},
		Children: []Widget{
			Label{Text: "部署运行环境", TextColor: colorText, Font: Font{Family: "Segoe UI", PointSize: 18, Bold: true}, MinSize: Size{Height: 32}},
			Label{AssignTo: &g.deployNote, Text: "首次使用需要创建一个专用 WSL2 环境。", TextColor: colorMuted, MinSize: Size{Height: 24}},
			VSpacer{Size: 8},
			Label{Text: "仅创建 AppleMusic-Runtime 专用发行版", TextColor: colorText, MinSize: Size{Height: 23}},
			Label{Text: "不会修改或删除现有的 Ubuntu、Debian 等发行版", TextColor: colorText, MinSize: Size{Height: 23}},
			Label{Text: "Windows 组件缺失时会显示一次管理员授权窗口", TextColor: colorText, MinSize: Size{Height: 23}},
			VSpacer{Size: 12},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				PushButton{AssignTo: &g.deployButton, Text: "一键部署", MinSize: Size{Width: 132, Height: 38}, OnClicked: g.startInstall},
				HSpacer{},
			}},
			VSpacer{},
		},
	}
}

func (g *gui) deployingPage() Widget {
	return Composite{
		AssignTo: &g.pages[pageDeploying], Visible: false,
		Layout: VBox{Margins: Margins{Left: 48, Top: 42, Right: 48, Bottom: 38}, Spacing: 14},
		Children: []Widget{
			VSpacer{},
			Label{Text: "正在部署运行环境", TextColor: colorText, Font: Font{Family: "Segoe UI", PointSize: 18, Bold: true}, MinSize: Size{Height: 32}},
			Label{Text: "下载并校验 Ubuntu Base，然后导入专用 WSL2 发行版。", TextColor: colorMuted, MinSize: Size{Height: 24}},
			ProgressBar{MarqueeMode: true, MinSize: Size{Height: 8}, MaxSize: Size{Height: 8}},
			Label{Text: "期间请保持网络连接，不要关闭程序。", TextColor: colorMuted, MinSize: Size{Height: 23}},
			VSpacer{},
		},
	}
}

func (g *gui) rebootPage() Widget {
	return Composite{
		AssignTo: &g.pages[pageReboot], Visible: false,
		Layout: VBox{Margins: Margins{Left: 48, Top: 42, Right: 48, Bottom: 38}, Spacing: 14},
		Children: []Widget{
			VSpacer{},
			Label{Text: "需要重新启动 Windows", TextColor: colorText, Font: Font{Family: "Segoe UI", PointSize: 18, Bold: true}, MinSize: Size{Height: 32}},
			Label{Text: "WSL2 系统组件已经启用。重启后再次打开本程序即可继续部署。", TextColor: colorMuted, MinSize: Size{Height: 24}},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				PushButton{Text: "立即重新启动", MinSize: Size{Width: 132, Height: 38}, OnClicked: g.restartWindows},
				HSpacer{},
			}},
			VSpacer{},
		},
	}
}

func (g *gui) loginPage() Widget {
	return Composite{
		AssignTo: &g.pages[pageLogin], Visible: false,
		Layout: VBox{Margins: Margins{Left: 48, Top: 28, Right: 48, Bottom: 24}, Spacing: 8},
		Children: []Widget{
			Label{Text: "登录 Apple Music", TextColor: colorText, Font: Font{Family: "Segoe UI", PointSize: 18, Bold: true}, MinSize: Size{Height: 32}},
			Label{Text: "Apple ID", TextColor: colorText, MinSize: Size{Height: 22}},
			LineEdit{AssignTo: &g.loginAppleID, CueBanner: "name@example.com", MaxLength: 254, MinSize: Size{Height: 34}},
			Label{Text: "密码", TextColor: colorText, MinSize: Size{Height: 22}},
			LineEdit{AssignTo: &g.loginPassword, PasswordMode: true, MaxLength: 512, MinSize: Size{Height: 34}},
			Label{AssignTo: &g.loginError, Text: "", TextColor: colorAccent, MinSize: Size{Height: 24}},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				PushButton{AssignTo: &g.loginButton, Text: "登录", MinSize: Size{Width: 112, Height: 36}, OnClicked: g.startLogin},
				ProgressBar{AssignTo: &g.loginProgress, Visible: false, MarqueeMode: true, MinSize: Size{Width: 180, Height: 8}, MaxSize: Size{Height: 8}},
				HSpacer{},
			}},
			Label{Text: "登录凭据只通过标准输入传给专用环境，不写入 Windows 配置或日志。", TextColor: colorMuted, MinSize: Size{Height: 23}},
			VSpacer{},
		},
	}
}

func (g *gui) twoFactorPage() Widget {
	return Composite{
		AssignTo: &g.pages[pageTwoFactor], Visible: false,
		Layout: VBox{Margins: Margins{Left: 48, Top: 38, Right: 48, Bottom: 38}, Spacing: 12},
		Children: []Widget{
			VSpacer{},
			Label{Text: "输入验证码", TextColor: colorText, Font: Font{Family: "Segoe UI", PointSize: 18, Bold: true}, MinSize: Size{Height: 32}},
			Label{Text: "请输入受信任设备上显示的六位数字。", TextColor: colorMuted, MinSize: Size{Height: 24}},
			LineEdit{AssignTo: &g.codeEdit, CueBanner: "000000", MaxLength: 6, MinSize: Size{Width: 220, Height: 36}, MaxSize: Size{Width: 220}},
			Label{AssignTo: &g.codeError, Text: "", TextColor: colorAccent, MinSize: Size{Height: 24}},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				PushButton{AssignTo: &g.codeButton, Text: "验证", MinSize: Size{Width: 112, Height: 36}, OnClicked: g.startTwoFactor},
				PushButton{Text: "重新登录", MinSize: Size{Width: 112, Height: 36}, OnClicked: g.showLoginAgain},
				ProgressBar{AssignTo: &g.codeProgress, Visible: false, MarqueeMode: true, MinSize: Size{Width: 180, Height: 8}, MaxSize: Size{Height: 8}},
				HSpacer{},
			}},
			VSpacer{},
		},
	}
}

func (g *gui) errorPage() Widget {
	return Composite{
		AssignTo: &g.pages[pageError], Visible: false,
		Layout: VBox{Margins: Margins{Left: 48, Top: 38, Right: 48, Bottom: 38}, Spacing: 12},
		Children: []Widget{
			VSpacer{},
			Label{AssignTo: &g.errorTitle, Text: "无法继续", TextColor: colorText, Font: Font{Family: "Segoe UI", PointSize: 18, Bold: true}, MinSize: Size{Height: 32}},
			TextEdit{AssignTo: &g.errorDetail, ReadOnly: true, VScroll: true, MinSize: Size{Height: 110}, MaxSize: Size{Height: 150}, TextColor: colorText},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				PushButton{AssignTo: &g.retryButton, Text: "重试", MinSize: Size{Width: 112, Height: 36}, OnClicked: func() {
					if g.retryAction != nil {
						g.retryAction()
					}
				}},
				HSpacer{},
			}},
			VSpacer{},
		},
	}
}

func (g *gui) readyTabs(qualityIndex int) Widget {
	return TabWidget{
		AssignTo: &g.mainTabs, Visible: false,
		ContentMargins: Margins{Left: 20, Top: 18, Right: 20, Bottom: 18},
		Pages: []TabPage{
			{Title: "下载", Layout: VBox{Margins: Margins{Left: 20, Top: 18, Right: 20, Bottom: 18}, Spacing: 10}, Children: g.downloadTab(qualityIndex)},
			{Title: "下载记录", Layout: VBox{Margins: Margins{Left: 20, Top: 18, Right: 20, Bottom: 18}, Spacing: 10}, Children: g.historyTab()},
			{Title: "运行环境", Layout: VBox{Margins: Margins{Left: 20, Top: 18, Right: 20, Bottom: 18}, Spacing: 12}, Children: g.environmentTab()},
		},
	}
}

func (g *gui) downloadTab(qualityIndex int) []Widget {
	return []Widget{
		LineEdit{AssignTo: &g.linkEdit, CueBanner: "https://music.apple.com/...", MinSize: Size{Height: 36}},
		Composite{Layout: HBox{MarginsZero: true, Spacing: 10}, Children: []Widget{
			Label{Text: "音质", TextColor: colorText, MinSize: Size{Width: 36}},
			ComboBox{AssignTo: &g.qualityCombo, Model: []string{"无损 ALAC", "杜比全景声"}, CurrentIndex: qualityIndex, MinSize: Size{Width: 120, Height: 34}, MaxSize: Size{Width: 140}},
			Label{Text: "保存到", TextColor: colorText, MinSize: Size{Width: 48}},
			LineEdit{AssignTo: &g.outputEdit, Text: g.settings.OutputDir, ReadOnly: true, MinSize: Size{Width: 180, Height: 34}, MaxSize: Size{Width: 210}, StretchFactor: 1},
			PushButton{Text: "选择...", MinSize: Size{Width: 86, Height: 34}, OnClicked: g.chooseOutputDir},
			HSpacer{},
		}},
		Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
			PushButton{AssignTo: &g.downloadButton, Text: "开始下载", MinSize: Size{Width: 122, Height: 36}, OnClicked: g.startDownload},
			PushButton{AssignTo: &g.cancelButton, Text: "取消", Visible: false, MinSize: Size{Width: 88, Height: 36}, OnClicked: g.cancelCurrentDownload},
			PushButton{AssignTo: &g.downloadRetryButton, Text: "重试下载", Visible: false, MinSize: Size{Width: 100, Height: 36}, OnClicked: g.retryFailedDownload},
			PushButton{Text: "打开下载目录", MinSize: Size{Width: 122, Height: 36}, OnClicked: g.openOutputDir},
			HSpacer{},
		}},
		Composite{AssignTo: &g.taskPanel, Visible: false, Layout: VBox{MarginsZero: true, Spacing: 8}, Children: []Widget{
			Label{AssignTo: &g.taskTitle, Text: "准备下载", TextColor: colorText, Font: Font{Family: "Segoe UI", PointSize: 11, Bold: true}, MinSize: Size{Height: 24}},
			Label{AssignTo: &g.taskDetail, Text: "", TextColor: colorMuted, MinSize: Size{Height: 23}},
			Label{AssignTo: &g.taskStats, Text: "当前文件大小：-- · 下载速度：--", TextColor: colorMuted, MinSize: Size{Height: 23}},
			ProgressBar{AssignTo: &g.taskProgress, MarqueeMode: true, MinValue: 0, MaxValue: 100, MinSize: Size{Height: 8}, MaxSize: Size{Height: 8}},
		}},
		Label{Text: "任务日志", TextColor: colorText, Font: Font{Family: "Segoe UI", PointSize: 10, Bold: true}, MinSize: Size{Height: 23}},
		TextEdit{AssignTo: &g.taskLog, ReadOnly: true, VScroll: true, TextColor: colorText, MinSize: Size{Height: 48}, StretchFactor: 1},
	}
}

func (g *gui) historyTab() []Widget {
	return []Widget{
		Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
			Label{Text: "下载记录", TextColor: colorText, Font: Font{Family: "Segoe UI", PointSize: 16, Bold: true}, MinSize: Size{Height: 28}},
			HSpacer{},
			PushButton{AssignTo: &g.historyOpenButton, Text: "打开所在位置", Enabled: false, MinSize: Size{Width: 112, Height: 34}, OnClicked: g.openSelectedHistory},
			PushButton{AssignTo: &g.historyDeleteButton, Text: "删除记录", Enabled: false, MinSize: Size{Width: 96, Height: 34}, OnClicked: g.deleteSelectedHistory},
			PushButton{AssignTo: &g.historyClearButton, Text: "清空全部", Enabled: false, MinSize: Size{Width: 96, Height: 34}, OnClicked: g.clearHistory},
		}},
		TableView{
			AssignTo: &g.historyTable, Model: g.model, AlternatingRowBG: true,
			ColumnsSizable: true, LastColumnStretched: true, CustomRowHeight: 28,
			OnCurrentIndexChanged: g.updateHistoryActions,
			OnItemActivated:       g.openSelectedHistory,
			Columns: []TableViewColumn{
				{Title: "名称", DataMember: "Title", Width: 230},
				{Title: "艺人", DataMember: "Artist", Width: 160},
				{Title: "专辑", DataMember: "Album", Width: 210},
				{Title: "完成时间", DataMember: "Completed", Width: 140},
				{Title: "文件", DataMember: "Path", Width: 220},
			},
		},
	}
}

func (g *gui) environmentTab() []Widget {
	return []Widget{
		Label{Text: "运行环境", TextColor: colorText, Font: Font{Family: "Segoe UI", PointSize: 16, Bold: true}, MinSize: Size{Height: 28}},
		Composite{Layout: Grid{Columns: 2, MarginsZero: true, Spacing: 12}, Children: []Widget{
			Label{Text: "专用发行版", TextColor: colorMuted, MinSize: Size{Width: 100, Height: 24}},
			Label{AssignTo: &g.envDistro, Text: "-", TextColor: colorText, EllipsisMode: EllipsisEnd, MinSize: Size{Height: 24}},
			Label{Text: "状态", TextColor: colorMuted, MinSize: Size{Width: 100, Height: 24}},
			Label{AssignTo: &g.envState, Text: "-", TextColor: colorText, MinSize: Size{Height: 24}},
			Label{Text: "存储位置", TextColor: colorMuted, MinSize: Size{Width: 100, Height: 24}},
			Label{AssignTo: &g.envLocation, Text: "-", TextColor: colorText, EllipsisMode: EllipsisPath, MinSize: Size{Height: 24}},
			Label{Text: "恢复数据", TextColor: colorMuted, MinSize: Size{Width: 100, Height: 24}},
			Label{AssignTo: &g.envRecovery, Text: "无", TextColor: colorText, EllipsisMode: EllipsisPath, MinSize: Size{Height: 24}},
		}},
		VSpacer{Size: 8},
		Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
			PushButton{Text: "重新检测", MinSize: Size{Width: 100, Height: 34}, OnClicked: g.startInitialCheck},
			PushButton{AssignTo: &g.stopButton, Text: "停止环境", MinSize: Size{Width: 100, Height: 34}, OnClicked: g.stopRuntime},
			PushButton{Text: "查看运行日志", MinSize: Size{Width: 112, Height: 34}, OnClicked: g.openRuntimeLog},
			HSpacer{},
		}},
		VSpacer{},
		Label{Text: "移除前会将专用发行版完整导出到“文档\\AppleMusicDownloader Backups”。", TextColor: colorMuted, MinSize: Size{Height: 24}},
		Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
			PushButton{AssignTo: &g.removeButton, Text: "备份并移除环境...", MinSize: Size{Width: 152, Height: 34}, OnClicked: g.removeRuntime},
			HSpacer{},
		}},
	}
}

func (g *gui) showOnboarding(index int) {
	for i, page := range g.pages {
		if page != nil {
			page.SetVisible(i == index)
		}
	}
	if g.mainTabs != nil {
		g.mainTabs.SetVisible(false)
	}
}

func (g *gui) showReady(status app.BootstrapStatus) {
	for _, page := range g.pages {
		if page != nil {
			page.SetVisible(false)
		}
	}
	g.loginPending = false
	g.mainTabs.SetVisible(true)
	g.lastStatus = status
	g.updateEnvironment(status)
	g.headerStatus.SetText("环境就绪")
	g.headerStatus.SetTextColor(colorOK)
	g.statusBar.SetText("专用运行环境已就绪")
	if g.retryAfterLogin && g.retryRequest != nil {
		g.retryAfterLogin = false
		g.taskPanel.SetVisible(true)
		g.downloadRetryButton.SetEnabled(true)
		g.downloadRetryButton.SetVisible(true)
		g.taskTitle.SetText("登录完成，可重试下载")
		g.taskDetail.SetText("原下载任务尚未完成")
		g.updateTaskStats("")
		g.statusBar.SetText("登录完成，可重试下载")
	}
}

func (g *gui) updateEnvironment(status app.BootstrapStatus) {
	g.lastStatus = status
	g.envDistro.SetText(valueOrDash(status.DistroName))
	state := "未运行"
	if status.Healthy {
		state = "运行正常"
	} else if status.Running {
		state = "正在运行，但服务未就绪"
	} else if !status.Installed {
		state = "未部署"
	}
	g.envState.SetText(state)
	g.envLocation.SetText(valueOrDash(status.InstallDir))
	if len(status.RecoveryPaths) == 0 {
		g.envRecovery.SetText("无")
	} else {
		g.envRecovery.SetText(strings.Join(status.RecoveryPaths, "; "))
	}
	g.stopButton.SetEnabled(status.Installed && status.Running && !g.busy)
	g.removeButton.SetEnabled(status.Installed && !g.busy)
}

func valueOrDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func (g *gui) beginOperation(kind string, timeout time.Duration) context.Context {
	g.busy = true
	g.opKind = kind
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	g.cancel = cancel
	return ctx
}

func (g *gui) endOperation() {
	if g.cancel != nil {
		g.cancel()
	}
	g.cancel = nil
	g.busy = false
	g.opKind = ""
}

func (g *gui) sync(callback func()) {
	if g.closed.Load() || g.mw == nil {
		return
	}
	g.mw.Synchronize(func() {
		if !g.closed.Load() {
			callback()
		}
	})
}

func (g *gui) startInitialCheck() {
	if g.busy {
		return
	}
	g.showOnboarding(pageChecking)
	g.checkingTitle.SetText("正在检查运行环境")
	g.checkingDetail.SetText("正在读取专用 WSL2 环境状态")
	_ = g.checkingBar.SetMarqueeMode(true)
	g.headerStatus.SetText("检测中")
	g.headerStatus.SetTextColor(walk.RGB(216, 218, 223))
	g.statusBar.SetText("正在检查运行环境")
	if g.bundleErr != nil {
		g.showError("安装包不完整", g.bundleErr, nil)
		return
	}
	ctx := g.beginOperation("check", 3*time.Minute)
	go func() {
		client := app.BootstrapClient{Bundle: g.bundle}
		response, err := client.Invoke(ctx, "status", nil)
		if err == nil && response.Status.Installed && !response.Status.Healthy {
			g.sync(func() {
				g.checkingTitle.SetText("正在启动运行环境")
				g.checkingDetail.SetText("首次启动可能需要一些时间")
			})
			response, err = client.Invoke(ctx, "start", nil)
		}
		g.sync(func() {
			g.endOperation()
			if err != nil {
				if operationCode(err) == "login_required" {
					g.lastStatus = response.Status
					g.showLogin("")
					return
				}
				g.showError("运行环境检查失败", err, g.startInitialCheck)
				return
			}
			if !response.Status.Installed {
				g.deployNote.SetText("首次使用需要创建一个专用 WSL2 环境。")
				g.showOnboarding(pageDeploy)
				g.headerStatus.SetText("尚未部署")
				g.statusBar.SetText("等待部署专用运行环境")
				return
			}
			if response.Status.Healthy {
				g.showReady(response.Status)
				return
			}
			g.showError("运行环境尚未就绪", errors.New(response.Status.Detail), g.startInitialCheck)
		})
	}()
}

func (g *gui) startInstall() {
	if g.busy || g.bundleErr != nil {
		return
	}
	g.showOnboarding(pageDeploying)
	g.headerStatus.SetText("部署中")
	g.statusBar.SetText("正在部署专用 WSL2 环境")
	ctx := g.beginOperation("install", 45*time.Minute)
	go func() {
		response, err := (app.BootstrapClient{Bundle: g.bundle}).Invoke(ctx, "install", nil)
		g.sync(func() {
			g.endOperation()
			if err != nil {
				if operationCode(err) == "reboot_required" {
					g.showOnboarding(pageReboot)
					g.headerStatus.SetText("需要重启")
					g.statusBar.SetText("重启 Windows 后可继续部署")
					return
				}
				g.showError("部署失败", err, g.startInstall)
				return
			}
			g.lastStatus = response.Status
			g.showLogin("")
		})
	}()
}

func (g *gui) showLogin(message string) {
	g.showOnboarding(pageLogin)
	g.loginError.SetText(message)
	g.loginButton.SetEnabled(true)
	g.loginProgress.SetVisible(false)
	g.headerStatus.SetText("需要登录")
	g.headerStatus.SetTextColor(walk.RGB(242, 184, 72))
	g.statusBar.SetText("等待 Apple Music 登录")
	_ = g.loginAppleID.SetFocus()
}

func (g *gui) startLogin() {
	if g.busy {
		return
	}
	appleID := strings.TrimSpace(g.loginAppleID.Text())
	password := g.loginPassword.Text()
	if appleID == "" || password == "" {
		g.loginError.SetText("Apple ID 和密码不能为空")
		return
	}
	if strings.ContainsAny(appleID, ":\r\n\x00") || strings.ContainsAny(password, ":\r\n\x00") {
		g.loginError.SetText("当前登录协议不支持冒号或换行符")
		return
	}
	stdin := []byte(appleID + "\n" + password + "\n")
	password = ""
	g.loginPassword.SetText("")
	g.loginError.SetText("")
	g.loginButton.SetEnabled(false)
	g.loginProgress.SetVisible(true)
	g.statusBar.SetText("正在登录 Apple Music")
	ctx := g.beginOperation("login", 3*time.Minute)
	go func() {
		defer clearBytes(stdin)
		response, err := (app.BootstrapClient{Bundle: g.bundle}).Invoke(ctx, "login", stdin)
		var cleanupErr error
		if err != nil && operationCode(err) != "two_factor_required" {
			cleanupErr = g.stopLoginRuntime()
		}
		g.sync(func() {
			g.endOperation()
			g.loginButton.SetEnabled(true)
			g.loginProgress.SetVisible(false)
			if err != nil {
				if operationCode(err) == "two_factor_required" {
					g.loginPending = true
					g.lastStatus = response.Status
					g.showTwoFactor()
					return
				}
				g.loginPending = cleanupErr != nil
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					message := "登录已取消"
					if errors.Is(err, context.DeadlineExceeded) {
						message = "登录已超时，请重试"
					}
					g.showLogin(withCleanupError(message, cleanupErr))
					return
				}
				g.showLogin(withCleanupError(friendlyMessage(err), cleanupErr))
				return
			}
			g.showReady(response.Status)
		})
	}()
}

func clearBytes(data []byte) {
	for i := range data {
		data[i] = 0
	}
}

func (g *gui) showTwoFactor() {
	g.showOnboarding(pageTwoFactor)
	g.codeEdit.SetText("")
	g.codeError.SetText("")
	g.codeButton.SetEnabled(true)
	g.codeProgress.SetVisible(false)
	g.headerStatus.SetText("等待验证码")
	g.statusBar.SetText("等待六位验证码")
	_ = g.codeEdit.SetFocus()
}

func (g *gui) startTwoFactor() {
	if g.busy {
		return
	}
	code := strings.TrimSpace(g.codeEdit.Text())
	if len(code) != 6 || strings.Trim(code, "0123456789") != "" {
		g.codeError.SetText("请输入六位数字验证码")
		return
	}
	stdin := []byte(code + "\n")
	code = ""
	g.codeEdit.SetText("")
	g.codeError.SetText("")
	g.codeButton.SetEnabled(false)
	g.codeProgress.SetVisible(true)
	g.statusBar.SetText("正在验证")
	ctx := g.beginOperation("two_factor", 3*time.Minute)
	go func() {
		defer clearBytes(stdin)
		response, err := (app.BootstrapClient{Bundle: g.bundle}).Invoke(ctx, "submit-code", stdin)
		var cleanupErr error
		if err != nil && operationCode(err) != "two_factor_required" {
			cleanupErr = g.stopLoginRuntime()
		}
		g.sync(func() {
			g.endOperation()
			g.codeButton.SetEnabled(true)
			g.codeProgress.SetVisible(false)
			if err != nil {
				if operationCode(err) == "two_factor_required" {
					g.loginPending = true
					g.showTwoFactor()
					g.codeError.SetText("验证码未完成验证，请重新输入")
					return
				}
				g.loginPending = cleanupErr != nil
				message := "验证码未通过，请重新登录"
				if errors.Is(err, context.Canceled) {
					message = "验证已取消，请重新登录"
				} else if errors.Is(err, context.DeadlineExceeded) {
					message = "验证已超时，请重新登录"
				}
				g.showLogin(withCleanupError(message, cleanupErr))
				return
			}
			g.showReady(response.Status)
		})
	}()
}

func (g *gui) stopLoginRuntime() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := (app.BootstrapClient{Bundle: g.bundle}).Invoke(ctx, "stop", nil)
	return err
}

func withCleanupError(message string, cleanupErr error) string {
	if cleanupErr == nil {
		return message
	}
	return message + "；专用环境未能停止，请稍后重试"
}

func (g *gui) showLoginAgain() {
	if g.busy {
		return
	}
	g.showLogin("")
}

func (g *gui) chooseOutputDir() {
	if g.busy {
		return
	}
	dialog := newOutputDirDialog()
	accepted, err := dialog.ShowBrowseFolder(g.mw)
	if err != nil {
		walk.MsgBox(g.mw, appTitle, err.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
		return
	}
	if accepted {
		g.outputEdit.SetText(dialog.FilePath)
		g.settings.OutputDir = dialog.FilePath
		_ = g.store.SaveSettings(g.settings)
	}
}

func newOutputDirDialog() walk.FileDialog {
	// Walk maps InitialDirPath to the dialog root, which prevents navigating
	// to the current directory's parent, sibling directories, or other drives.
	return walk.FileDialog{Title: "选择下载目录"}
}

func (g *gui) startDownload() {
	if g.busy {
		return
	}
	link, err := app.ValidateAppleMusicLink(g.linkEdit.Text())
	if err != nil {
		walk.MsgBox(g.mw, "链接无效", err.Error(), walk.MsgBoxOK|walk.MsgBoxIconWarning)
		return
	}
	outputDir := strings.TrimSpace(g.outputEdit.Text())
	quality := app.QualityLossless
	if g.qualityCombo.CurrentIndex() == 1 {
		quality = app.QualityAtmos
	}
	g.startDownloadRequest(downloadRequest{link: link, outputDir: outputDir, quality: quality})
}

func (g *gui) startDownloadRequest(request downloadRequest) {
	if g.busy {
		return
	}
	request.outputDir = strings.TrimSpace(request.outputDir)
	if err := os.MkdirAll(request.outputDir, 0755); err != nil {
		walk.MsgBox(g.mw, "无法使用下载目录", err.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
		return
	}
	g.settings = app.Settings{OutputDir: request.outputDir, Quality: request.quality}
	_ = g.store.SaveSettings(g.settings)
	g.retryRequest = &request
	g.retryAfterLogin = false

	g.downloadButton.SetEnabled(false)
	g.cancelButton.SetVisible(true)
	g.cancelButton.SetEnabled(true)
	g.downloadRetryButton.SetVisible(false)
	g.downloadRetryButton.SetEnabled(false)
	g.taskPanel.SetVisible(true)
	g.taskTitle.SetText("正在准备下载")
	g.taskDetail.SetText("正在确认运行环境")
	g.progressStats.Reset()
	g.updateTaskStats("")
	_ = g.taskProgress.SetMarqueeMode(true)
	g.taskLog.SetText("")
	g.statusBar.SetText("下载任务正在运行")
	ctx := g.beginOperation("download", 12*time.Hour)
	go g.runDownload(ctx, request)
}

func (g *gui) retryFailedDownload() {
	if g.busy || g.retryRequest == nil {
		return
	}
	request := *g.retryRequest
	g.linkEdit.SetText(request.link.URL)
	g.outputEdit.SetText(request.outputDir)
	qualityIndex := 0
	if request.quality == app.QualityAtmos {
		qualityIndex = 1
	}
	_ = g.qualityCombo.SetCurrentIndex(qualityIndex)
	g.startDownloadRequest(request)
}

func (g *gui) runDownload(ctx context.Context, request downloadRequest) {
	client := app.BootstrapClient{Bundle: g.bundle}
	statusResponse, err := client.Invoke(ctx, "start", nil)
	if err != nil {
		g.finishDownload(request, nil, err, statusResponse.Status)
		return
	}

	var tracks []app.DownloadedTrack
	taskID := newTaskID()
	err = app.RunDownload(ctx, g.bundle, app.DownloadOptions{
		Link: request.link, OutputDir: request.outputDir, Quality: request.quality, TaskID: taskID,
		OnEvent: func(event app.DownloadEvent) {
			if event.Event == "summary" && len(event.Tracks) > 0 {
				tracks = append([]app.DownloadedTrack(nil), event.Tracks...)
			}
			receivedAt := time.Now()
			g.sync(func() { g.handleDownloadEvent(event, receivedAt) })
		},
		OnLog: func(line string) { g.sync(func() { g.appendTaskLog(line) }) },
	})
	g.finishDownload(request, tracks, err, statusResponse.Status)
}

func newTaskID() string {
	var bytes [8]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes[:])
}

func (g *gui) handleDownloadEvent(event app.DownloadEvent, receivedAt time.Time) {
	switch event.Event {
	case "queue":
		g.progressStats.Reset()
		g.updateTaskStats("")
		g.taskTitle.SetText(fmt.Sprintf("队列 %d / %d", event.Current, event.Total))
	case "track":
		g.progressStats.Reset()
		g.updateTaskStats("")
		g.taskTitle.SetText(event.Song)
		g.taskDetail.SetText(fmt.Sprintf("曲目 %d / %d · %s", event.Current, event.Total, event.Artist))
		_ = g.taskProgress.SetMarqueeMode(true)
	case "phase":
		g.taskDetail.SetText(phaseText(event.Phase))
		g.updateTaskStats(event.Phase)
	case "progress":
		if event.Phase == downloadCompletePhase {
			g.progressStats.CompleteDownload(event.Current, receivedAt)
			g.updateTaskStats(downloadProgressPhase)
			_ = g.taskProgress.SetMarqueeMode(false)
			g.taskProgress.SetValue(100)
			g.taskDetail.SetText("下载完成")
			return
		}
		if event.Phase == "decrypting" && g.progressStats.DownloadInProgress() {
			return
		}
		g.progressStats.Observe(event.Phase, event.Current, event.Total, receivedAt)
		g.updateTaskStats(event.Phase)
		if event.Total > 0 {
			_ = g.taskProgress.SetMarqueeMode(false)
			g.taskProgress.SetRange(0, 100)
			percent := int(event.Current * 100 / event.Total)
			if percent < 0 {
				percent = 0
			}
			if percent > 100 {
				percent = 100
			}
			g.taskProgress.SetValue(percent)
			g.taskDetail.SetText(fmt.Sprintf("%s · %d%%", phaseText(event.Phase), percent))
		} else {
			_ = g.taskProgress.SetMarqueeMode(true)
			g.taskDetail.SetText(phaseText(event.Phase))
		}
	case "track_complete":
		g.updateTaskStats("")
		g.appendTaskLog("完成：" + event.Song)
	case "warning":
		g.appendTaskLog("警告：" + event.Message + formatDetail(event.Detail))
	case "error":
		g.appendTaskLog("错误：" + event.Message + formatDetail(event.Detail))
	case "summary":
		g.progressStats.Reset()
		g.updateTaskStats("")
		g.taskDetail.SetText(fmt.Sprintf("完成 %d / %d，警告 %d，错误 %d", event.Success, event.Total, event.Warnings, event.Errors))
	}
}

func (g *gui) updateTaskStats(activePhase string) {
	g.taskStats.SetText(fmt.Sprintf(
		"当前文件大小：%s · 下载速度：%s",
		g.progressStats.SizeText(),
		g.progressStats.SpeedText(activePhase),
	))
}

func phaseText(phase string) string {
	switch phase {
	case "preparing":
		return "正在准备媒体流"
	case "downloading":
		return "正在下载"
	case "decrypting":
		return "正在解密"
	case "tagging":
		return "正在写入封面和标签"
	default:
		return "正在处理"
	}
}

func formatDetail(detail string) string {
	if detail == "" {
		return ""
	}
	return "：" + detail
}

func (g *gui) appendTaskLog(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	current := g.taskLog.Text()
	if len(current) > 50000 {
		current = current[len(current)-40000:]
		g.taskLog.SetText(current)
	}
	g.taskLog.AppendText(line + "\r\n")
}

func (g *gui) finishDownload(request downloadRequest, tracks []app.DownloadedTrack, err error, status app.BootstrapStatus) {
	g.sync(func() {
		g.endOperation()
		g.downloadButton.SetEnabled(true)
		g.cancelButton.SetVisible(false)
		g.lastStatus = status
		if err != nil {
			if operationCode(err) == "login_required" {
				g.retryRequest = &request
				g.retryAfterLogin = true
				g.downloadRetryButton.SetVisible(false)
				g.showLogin("登录状态已失效，请重新登录")
				return
			}
			if errors.Is(err, context.Canceled) {
				g.retryRequest = nil
				g.retryAfterLogin = false
				g.downloadRetryButton.SetVisible(false)
				g.taskTitle.SetText("任务已取消")
				g.taskDetail.SetText("未完成的临时文件已清理")
				g.updateTaskStats("")
				g.statusBar.SetText("下载已取消")
				return
			}
			g.retryRequest = &request
			g.retryAfterLogin = false
			g.downloadRetryButton.SetEnabled(true)
			g.downloadRetryButton.SetVisible(true)
			g.taskTitle.SetText("下载失败")
			message := downloadFailureMessage(err)
			g.taskDetail.SetText(message)
			g.updateTaskStats("")
			g.appendTaskLog(message)
			g.statusBar.SetText("下载失败")
			return
		}

		g.retryRequest = nil
		g.retryAfterLogin = false
		g.downloadRetryButton.SetVisible(false)
		g.progressStats.Reset()
		now := time.Now()
		newEntries := make([]app.HistoryEntry, 0, len(tracks))
		for _, track := range tracks {
			newEntries = append(newEntries, app.HistoryEntry{
				CompletedAt: now, URL: request.link.URL, Path: track.Path,
				Artist: track.Artist, Album: track.Album, Song: track.Song,
			})
		}
		g.history = append(newEntries, g.history...)
		if len(g.history) > 100 {
			g.history = g.history[:100]
		}
		_ = g.store.SaveHistory(g.history)
		g.model.Set(g.history)
		g.updateHistoryActions()
		g.taskTitle.SetText("下载完成")
		g.taskDetail.SetText(fmt.Sprintf("已保存 %d 个曲目", len(tracks)))
		g.updateTaskStats("")
		_ = g.taskProgress.SetMarqueeMode(false)
		g.taskProgress.SetValue(100)
		g.statusBar.SetText("下载完成")
	})
}

func downloadFailureMessage(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "下载超时，请重试"
	}
	return friendlyMessage(err)
}

func (g *gui) cancelCurrentDownload() {
	if !g.busy || g.opKind != "download" || g.cancel == nil {
		return
	}
	g.cancelButton.SetEnabled(false)
	g.taskDetail.SetText("正在取消并清理临时文件")
	g.cancel()
}

func (g *gui) openOutputDir() {
	path := strings.TrimSpace(g.outputEdit.Text())
	if path == "" {
		return
	}
	_ = os.MkdirAll(path, 0755)
	_ = exec.Command("explorer.exe", path).Start()
}

func (g *gui) selectedHistoryIndex() (int, bool) {
	if g.historyTable == nil {
		return -1, false
	}
	index := g.historyTable.CurrentIndex()
	return index, index >= 0 && index < len(g.history)
}

func (g *gui) updateHistoryActions() {
	_, selected := g.selectedHistoryIndex()
	if g.historyOpenButton != nil {
		g.historyOpenButton.SetEnabled(selected)
	}
	if g.historyDeleteButton != nil {
		g.historyDeleteButton.SetEnabled(selected)
	}
	if g.historyClearButton != nil {
		g.historyClearButton.SetEnabled(len(g.history) > 0)
	}
}

func (g *gui) commitHistory(entries []app.HistoryEntry) error {
	if len(entries) > 100 {
		entries = entries[:100]
	}
	next := append([]app.HistoryEntry{}, entries...)
	if err := g.store.SaveHistory(next); err != nil {
		return err
	}
	g.history = next
	g.model.Set(next)
	g.updateHistoryActions()
	return nil
}

func (g *gui) deleteSelectedHistory() {
	index, ok := g.selectedHistoryIndex()
	if !ok {
		return
	}
	name := strings.TrimSpace(g.history[index].Song)
	if name == "" {
		name = strings.TrimSpace(g.history[index].Album)
	}
	if name == "" {
		name = filepath.Base(g.history[index].Path)
	}
	if name == "" || name == "." {
		name = "所选记录"
	}
	answer := walk.MsgBox(g.mw, "删除下载记录",
		fmt.Sprintf("确定删除“%s”这条下载记录吗？\r\n\r\n此操作不会删除已下载的文件。", name),
		walk.MsgBoxYesNo|walk.MsgBoxIconWarning|walk.MsgBoxDefButton2)
	if answer != walk.DlgCmdYes {
		return
	}
	next, ok := historyWithoutIndex(g.history, index)
	if !ok {
		return
	}
	if err := g.commitHistory(next); err != nil {
		walk.MsgBox(g.mw, "删除记录失败", "无法保存下载记录。\r\n\r\n"+err.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
		return
	}
	nextIndex := index
	if nextIndex >= len(g.history) {
		nextIndex = len(g.history) - 1
	}
	if nextIndex >= 0 {
		_ = g.historyTable.SetCurrentIndex(nextIndex)
	}
	g.updateHistoryActions()
	g.statusBar.SetText("已删除下载记录")
}

func (g *gui) clearHistory() {
	if len(g.history) == 0 {
		return
	}
	answer := walk.MsgBox(g.mw, "清空下载记录",
		fmt.Sprintf("确定删除全部 %d 条下载记录吗？\r\n\r\n此操作不会删除已下载的文件，且无法撤销。", len(g.history)),
		walk.MsgBoxYesNo|walk.MsgBoxIconWarning|walk.MsgBoxDefButton2)
	if answer != walk.DlgCmdYes {
		return
	}
	if err := g.commitHistory(make([]app.HistoryEntry, 0)); err != nil {
		walk.MsgBox(g.mw, "清空记录失败", "无法保存下载记录。\r\n\r\n"+err.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
		return
	}
	_ = g.historyTable.SetCurrentIndex(-1)
	g.updateHistoryActions()
	g.statusBar.SetText("已清空下载记录")
}

func (g *gui) openSelectedHistory() {
	index, ok := g.selectedHistoryIndex()
	if !ok {
		return
	}
	path, err := existingHistoryPath(g.history[index].Path)
	if err != nil {
		style := walk.MsgBoxOK | walk.MsgBoxIconError
		if errors.Is(err, os.ErrNotExist) {
			style = walk.MsgBoxOK | walk.MsgBoxIconWarning
		}
		walk.MsgBox(g.mw, "无法打开所在位置", historyPathErrorMessage(err), style)
		return
	}
	if err := exec.Command("explorer.exe", "/select,", path).Start(); err != nil {
		walk.MsgBox(g.mw, "无法打开所在位置", "无法启动文件资源管理器。\r\n\r\n"+err.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
	}
}

func (g *gui) stopRuntime() {
	if g.busy {
		return
	}
	g.stopButton.SetEnabled(false)
	g.statusBar.SetText("正在停止专用运行环境")
	ctx := g.beginOperation("stop", 2*time.Minute)
	go func() {
		_, err := (app.BootstrapClient{Bundle: g.bundle}).Invoke(ctx, "stop", nil)
		g.sync(func() {
			g.endOperation()
			if err != nil {
				walk.MsgBox(g.mw, "停止失败", friendlyMessage(err), walk.MsgBoxOK|walk.MsgBoxIconError)
				g.stopButton.SetEnabled(true)
				return
			}
			g.loginPending = false
			g.lastStatus.Running = false
			g.lastStatus.Healthy = false
			g.updateEnvironment(g.lastStatus)
			g.headerStatus.SetText("环境已停止")
			g.headerStatus.SetTextColor(walk.RGB(216, 218, 223))
			g.statusBar.SetText("专用运行环境已停止，下载时会自动启动")
		})
	}()
}

func (g *gui) removeRuntime() {
	if g.busy {
		return
	}
	answer := walk.MsgBox(g.mw, "备份并移除运行环境",
		"程序会先导出并校验完整备份，然后只注销 AppleMusic-Runtime 专用发行版。现有 Ubuntu 不会受到影响。\r\n\r\n是否继续？",
		walk.MsgBoxYesNo|walk.MsgBoxIconWarning|walk.MsgBoxDefButton2)
	if answer != walk.DlgCmdYes {
		return
	}
	g.showOnboarding(pageChecking)
	g.checkingTitle.SetText("正在备份并移除运行环境")
	g.checkingDetail.SetText("备份完成并通过校验前，不会注销专用发行版")
	g.statusBar.SetText("正在导出专用运行环境备份")
	ctx := g.beginOperation("remove", 2*time.Hour)
	go func() {
		response, err := (app.BootstrapClient{Bundle: g.bundle}).Invoke(ctx, "remove", nil)
		g.sync(func() {
			g.endOperation()
			if err != nil {
				g.showError("移除运行环境失败", err, g.startInitialCheck)
				return
			}
			g.loginPending = false
			g.deployNote.SetText("专用运行环境已移除。再次下载前需要重新部署。")
			g.showOnboarding(pageDeploy)
			g.headerStatus.SetText("尚未部署")
			g.statusBar.SetText("运行环境已安全移除")
			if response.BackupPath != "" {
				walk.MsgBox(g.mw, "备份完成", "完整备份已保存到：\r\n"+response.BackupPath, walk.MsgBoxOK|walk.MsgBoxIconInformation)
			}
		})
	}()
}

func (g *gui) openRuntimeLog() {
	path := g.lastStatus.LogPath
	if path == "" {
		path = filepath.Join(g.store.Dir, "logs", "wrapper.log")
	}
	if _, err := os.Stat(path); err != nil {
		walk.MsgBox(g.mw, "没有可用日志", "运行日志尚未生成。", walk.MsgBoxOK|walk.MsgBoxIconInformation)
		return
	}
	_ = exec.Command("notepad.exe", path).Start()
}

func (g *gui) showError(title string, err error, retry func()) {
	g.endOperation()
	g.showOnboarding(pageError)
	g.errorTitle.SetText(title)
	g.errorDetail.SetText(friendlyMessage(err))
	g.retryAction = retry
	g.retryButton.SetEnabled(retry != nil)
	g.retryButton.SetVisible(retry != nil)
	g.headerStatus.SetText("需要处理")
	g.headerStatus.SetTextColor(colorAccent)
	g.statusBar.SetText(title)
}

func operationCode(err error) string {
	var operationErr *app.OperationError
	if errors.As(err, &operationErr) {
		return operationErr.Code
	}
	return ""
}

func friendlyMessage(err error) string {
	if err == nil {
		return "发生未知错误"
	}
	var operationErr *app.OperationError
	if errors.As(err, &operationErr) {
		switch operationErr.Code {
		case "unsupported_platform":
			return "当前 Windows 或处理器架构不受支持。需要 64 位 Windows 10/11 和 AMD64 处理器。"
		case "integrity_check_failed":
			return "安装包完整性校验失败。请重新获取完整安装包，不要单独移动 payload 文件。\r\n\r\n" + operationErr.Message
		case "wsl_platform_unavailable":
			return "WSL2 系统组件无法启用。请检查 Windows 更新和虚拟化设置。\r\n\r\n" + operationErr.Message
		case "ownership_check_failed", "distro_name_conflict":
			return "专用发行版的所有权校验失败。程序不会操作来源不明的发行版。\r\n\r\n" + operationErr.Message
		case "repair_required":
			return "运行环境状态需要修复。保留的数据不会被自动删除。\r\n\r\n" + operationErr.Message
		case "port_conflict":
			return "本机端口 10020、20020 或 30020 已被其他程序占用。关闭占用程序后重试。\r\n\r\n" + operationErr.Message
		case "runtime_not_ready":
			return "专用环境已安装，但服务未能及时启动。可在“运行环境”中查看日志。\r\n\r\n" + operationErr.Message
		case "login_failed":
			return "Apple Music 登录未完成。请检查账号、密码和验证码后重试。"
		case "another_install_is_running":
			return "另一个部署或管理操作正在进行，请稍后重试。"
		}
		if operationErr.Message != "" {
			return operationErr.Message
		}
	}
	return err.Error()
}

func (g *gui) restartWindows() {
	answer := walk.MsgBox(g.mw, "重新启动 Windows", "Windows 将立即重新启动。是否继续？", walk.MsgBoxYesNo|walk.MsgBoxIconWarning|walk.MsgBoxDefButton2)
	if answer == walk.DlgCmdYes {
		_ = exec.Command("shutdown.exe", "/r", "/t", "0").Start()
	}
}

func (g *gui) cleanupPendingLogin() {
	g.statusBar.SetText("正在停止并清理登录会话")
	ctx := g.beginOperation("login_cleanup", 30*time.Second)
	go func() {
		_, err := (app.BootstrapClient{Bundle: g.bundle}).Invoke(ctx, "stop", nil)
		g.sync(func() {
			g.endOperation()
			if err != nil {
				g.statusBar.SetText("登录会话清理失败，请重试")
				walk.MsgBox(g.mw, "无法清理登录会话", friendlyMessage(err), walk.MsgBoxOK|walk.MsgBoxIconError)
				return
			}
			g.loginPending = false
			g.lastStatus.Running = false
			g.lastStatus.Healthy = false
			g.statusBar.SetText("登录会话已清理，可以关闭程序")
		})
	}()
}

func (g *gui) onClosing(canceled *bool, reason walk.CloseReason) {
	if !g.busy {
		if g.loginPending {
			*canceled = true
			g.cleanupPendingLogin()
			return
		}
		g.closed.Store(true)
		return
	}
	if g.opKind == "login_cleanup" {
		*canceled = true
		g.statusBar.SetText("正在清理登录会话，请稍候")
		return
	}
	answer := walk.MsgBox(g.mw, "操作正在进行", "是否取消当前操作？清理完成后请再次关闭程序。", walk.MsgBoxYesNo|walk.MsgBoxIconWarning|walk.MsgBoxDefButton2)
	*canceled = true
	if answer == walk.DlgCmdYes && g.cancel != nil {
		g.statusBar.SetText("正在取消当前操作")
		g.cancel()
	}
}
