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
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

const appTitle = "Apple Music 下载器"

const runtimeShutdownTimeout = 30 * time.Second

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

	loginAppleID      *walk.LineEdit
	loginPassword     *walk.LineEdit
	loginError        *walk.Label
	loginButton       *walk.PushButton
	loginProgress     *walk.ProgressBar
	loginRemoveButton *walk.PushButton

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
	songFileFormatCombo *walk.ComboBox
	outputEdit          *walk.LineEdit
	fetchListButton     *walk.PushButton
	downloadButton      *walk.PushButton
	cancelButton        *walk.PushButton
	downloadRetryButton *walk.PushButton
	taskPanel           *walk.Composite
	taskTitle           *walk.Label
	taskDetail          *walk.Label
	taskStats           *walk.Label
	taskProgress        *walk.ProgressBar

	trackTable     *walk.TableView
	trackModel     *trackTableModel
	trackChecker   *trackChecker
	trackSummary   *walk.Label
	trackSelectAll *walk.PushButton
	trackClearAll  *walk.PushButton
	openLogButton  *walk.PushButton
	logPath        string
	logFile        *os.File
	logMu          sync.Mutex
	loadedLink     string

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
	logoutButton *walk.PushButton

	lastStatus        app.BootstrapStatus
	busy              bool
	opKind            string
	cancel            context.CancelFunc
	loginPending      bool
	retryRequest      *downloadRequest
	retryAfterLogin   bool
	progressStats     downloadProgressStats
	closed            atomic.Bool
	shutdownRequested bool
	shutdownPending   bool
	closeAllowed      bool
	runtimeStopped    bool
	detachedStop      bool
	downloadComplete  bool
}

type downloadRequest struct {
	link           app.LinkInfo
	outputDir      string
	quality        string
	songFileFormat string
	selectIndexes  string
}

type songFileFormatOption struct {
	Label    string
	Template string
}

var songFileFormats = []songFileFormatOption{
	{"标题", "{SongName}"},
	{"艺术家 - 标题", "{ArtistName} - {SongName}"},
	{"标题 (音轨艺术家)", "{SongName} ({ArtistName})"},
	{"音轨号. 标题", "{TrackNumber}. {SongName}"},
	{"音轨号. 艺术家 - 标题", "{TrackNumber}. {ArtistName} - {SongName}"},
	{"音轨号. 标题 (音轨艺术家)", "{TrackNumber}. {SongName} ({ArtistName})"},
	{"碟片号.音轨号.标题", "{DiscNumber}.{TrackNumber}.{SongName}"},
	{"碟片号.音轨号.艺术家 - 标题", "{DiscNumber}.{TrackNumber}.{ArtistName} - {SongName}"},
	{"碟片号.音轨号.标题 (音轨艺术家)", "{DiscNumber}.{TrackNumber}.{SongName} ({ArtistName})"},
	{"碟片号/音轨号. 标题", "{DiscNumber}/{TrackNumber}. {SongName}"},
	{"碟片号/音轨号. 艺术家 - 标题", "{DiscNumber}/{TrackNumber}. {ArtistName} - {SongName}"},
	{"碟片号/音轨号. 标题 (音轨艺术家)", "{DiscNumber}/{TrackNumber}. {SongName} ({ArtistName})"},
}

func songFileFormatLabels() []string {
	labels := make([]string, len(songFileFormats))
	for i, option := range songFileFormats {
		labels[i] = option.Label
	}
	return labels
}

func songFileFormatIndex(template string) int {
	for i, option := range songFileFormats {
		if option.Template == template {
			return i
		}
	}
	return 0
}

func main() {
	defer func() {
		if recovered := recover(); recovered != nil {
			startupErr := fmt.Errorf("unexpected GUI panic: %v", recovered)
			fmt.Fprintln(os.Stderr, startupErr)
			if dir := startupLogsDir(); dir != "" {
				writeStartupError(dir, startupErr)
			}
			walk.MsgBox(nil, appTitle, "程序启动失败，诊断信息已写入 gui-startup.log。", walk.MsgBoxOK|walk.MsgBoxIconError)
		}
	}()
	runtime.LockOSThread()
	walk.App().SetOrganizationName("AppleMusicDownloader")
	walk.App().SetProductName("AppleMusicDownloader")
	releaseInstance, acquired, instanceErr := app.AcquireSingleInstance()
	if instanceErr != nil {
		walk.MsgBox(nil, appTitle, "无法创建程序实例锁："+instanceErr.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
		return
	}
	if !acquired {
		walk.MsgBox(nil, appTitle, "程序已在运行。", walk.MsgBoxOK|walk.MsgBoxIconInformation)
		return
	}
	defer releaseInstance()

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
		trackModel:   newTrackTableModel(),
		trackChecker: newTrackChecker(),
		pages:        make([]*walk.Composite, 7),
	}
	g.trackModel.checker = g.trackChecker
	g.model.Set(history)
	defer g.stopManagedRuntimeAfterWindow()
	if err := g.build(); err != nil {
		fmt.Fprintln(os.Stderr, "GUI startup failed:", err)
		writeStartupError(g.logsDir(), err)
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

// programRoot returns the directory containing the program, honoring the
// APPLEMUSIC_BUNDLE_ROOT override used by tests and development.
func programRoot() string {
	root := os.Getenv("APPLEMUSIC_BUNDLE_ROOT")
	if root == "" {
		executable, err := os.Executable()
		if err != nil {
			return ""
		}
		root = filepath.Dir(executable)
	}
	return root
}

// startupLogsDir returns the logs directory next to the program, used when the
// GUI fails before the bundle is fully discovered.
func startupLogsDir() string {
	if root := programRoot(); root != "" {
		return filepath.Join(root, "logs")
	}
	return ""
}

func writeStartupError(logDir string, startupErr error) {
	if logDir == "" || startupErr == nil {
		return
	}
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return
	}
	message := time.Now().Format(time.RFC3339) + " GUI startup failed: " + startupErr.Error() + "\r\n"
	message += string(debug.Stack()) + "\r\n"
	_ = os.WriteFile(filepath.Join(logDir, "gui-startup.log"), []byte(message), 0600)
}

func writeShutdownError(logDir string, shutdownErr error) {
	if logDir == "" || shutdownErr == nil {
		return
	}
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return
	}
	file, err := os.OpenFile(filepath.Join(logDir, "gui-shutdown.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = fmt.Fprintf(file, "%s failed to stop the managed WSL runtime: %v\r\n", time.Now().Format(time.RFC3339), shutdownErr)
}

func (g *gui) build() error {
	qualityIndex := 0
	if g.settings.Quality == app.QualityAtmos {
		qualityIndex = 1
	}
	fileFormatIndex := songFileFormatIndex(g.settings.SongFileFormat)

	var appIcon *walk.Icon
	if g.bundle.Root != "" {
		if loaded, err := walk.NewIconFromFile(filepath.Join(g.bundle.Root, "app.ico")); err == nil {
			appIcon = loaded
		}
	}

	window := MainWindow{
		AssignTo:   &g.mw,
		Icon:       appIcon,
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
					g.readyTabs(qualityIndex, fileFormatIndex),
				},
			},
		},
	}
	if err := window.Create(); err != nil {
		return err
	}
	installTrackLVHooks(g)
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
			g.trackModel.Set([]app.TrackGroup{{
				URL: "https://music.apple.com/cn/album/example/1", Title: "示例专辑", Artist: "示例艺人",
				Tracks: []app.TrackItem{
					{Index: 1, Name: "示例曲目 1", Artist: "示例艺人", Album: "示例专辑"},
					{Index: 2, Name: "示例曲目 2", Artist: "示例艺人", Album: "示例专辑"},
				},
			}})
			g.trackChecker.Reset(2)
			g.updateTrackSummary()
			g.trackSelectAll.SetEnabled(true)
			g.trackClearAll.SetEnabled(true)
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
			Label{AssignTo: &g.checkingDetail, Text: "请稍候", Visible: false, TextColor: colorMuted, MinSize: Size{Height: 24}},
			ProgressBar{AssignTo: &g.checkingBar, MarqueeMode: true, MinSize: Size{Height: 8}, MaxSize: Size{Height: 8}},
			VSpacer{},
		},
	}
}

func (g *gui) setCheckingDetail(text string) {
	if g.checkingDetail == nil {
		return
	}
	g.checkingDetail.SetText(text)
	g.checkingDetail.SetVisible(text != "")
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
			Label{Text: "需要 Windows 已安装 WSL2（程序不会自动安装 WSL）", TextColor: colorText, MinSize: Size{Height: 23}},
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
			Label{Text: "下载并校验 Ubuntu WSL 镜像，然后导入专用 WSL2 发行版。", TextColor: colorMuted, MinSize: Size{Height: 24}},
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
			LineEdit{AssignTo: &g.loginAppleID, CueBanner: "name@example.com", MaxLength: 254, Font: Font{Family: "Segoe UI", PointSize: 12}, MinSize: Size{Height: 34}},
			Label{Text: "密码", TextColor: colorText, MinSize: Size{Height: 22}},
			LineEdit{AssignTo: &g.loginPassword, PasswordMode: true, MaxLength: 512, Font: Font{Family: "Segoe UI", PointSize: 12}, MinSize: Size{Height: 34}},
			Label{AssignTo: &g.loginError, Text: "", TextColor: colorAccent, MinSize: Size{Height: 24}},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				PushButton{AssignTo: &g.loginButton, Text: "登录", MinSize: Size{Width: 112, Height: 36}, OnClicked: g.startLogin},
				ProgressBar{AssignTo: &g.loginProgress, Visible: false, MarqueeMode: true, MinSize: Size{Width: 180, Height: 8}, MaxSize: Size{Height: 8}},
				HSpacer{},
			}},
			Label{Text: "登录凭据只通过标准输入传给专用环境，不写入 Windows 配置或日志。", TextColor: colorMuted, MinSize: Size{Height: 23}},
			Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
				PushButton{AssignTo: &g.loginRemoveButton, Text: "备份并移除环境...", MinSize: Size{Width: 152, Height: 34}, OnClicked: g.removeRuntime},
				HSpacer{},
			}},
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
			LineEdit{AssignTo: &g.codeEdit, CueBanner: "000000", MaxLength: 6, Font: Font{Family: "Segoe UI", PointSize: 12}, MinSize: Size{Width: 220, Height: 36}, MaxSize: Size{Width: 220}},
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

func (g *gui) readyTabs(qualityIndex, fileFormatIndex int) Widget {
	return TabWidget{
		AssignTo: &g.mainTabs, Visible: false,
		ContentMargins: Margins{Left: 20, Top: 18, Right: 20, Bottom: 18},
		Pages: []TabPage{
			{Title: "下载", Layout: VBox{Margins: Margins{Left: 20, Top: 18, Right: 20, Bottom: 18}, Spacing: 10}, Children: g.downloadTab(qualityIndex, fileFormatIndex)},
			{Title: "下载记录", Layout: VBox{Margins: Margins{Left: 20, Top: 18, Right: 20, Bottom: 18}, Spacing: 10}, Children: g.historyTab()},
			{Title: "运行环境", Layout: VBox{Margins: Margins{Left: 20, Top: 18, Right: 20, Bottom: 18}, Spacing: 12}, Children: g.environmentTab()},
		},
	}
}

func (g *gui) downloadTab(qualityIndex, fileFormatIndex int) []Widget {
	return []Widget{
		LineEdit{AssignTo: &g.linkEdit, CueBanner: "https://music.apple.com/...", Font: Font{Family: "Segoe UI", PointSize: 12}, MinSize: Size{Height: 30}},
		Composite{Layout: HBox{MarginsZero: true, Spacing: 10}, Children: []Widget{
			Label{Text: "音质", TextColor: colorText, MinSize: Size{Width: 36}},
			ComboBox{AssignTo: &g.qualityCombo, Model: []string{"无损 ALAC", "杜比全景声"}, CurrentIndex: qualityIndex, MinSize: Size{Width: 120, Height: 34}, MaxSize: Size{Width: 140}},
			Label{Text: "保存到", TextColor: colorText, MinSize: Size{Width: 48}},
			LineEdit{AssignTo: &g.outputEdit, Text: g.settings.OutputDir, ReadOnly: true, MinSize: Size{Width: 180, Height: 34}, MaxSize: Size{Width: 210}, StretchFactor: 1},
			PushButton{Text: "选择...", MinSize: Size{Width: 86, Height: 34}, OnClicked: g.chooseOutputDir},
			HSpacer{},
		}},
		Composite{Layout: HBox{MarginsZero: true, Spacing: 10}, Children: []Widget{
			Label{Text: "文件名", TextColor: colorText, MinSize: Size{Width: 36}},
			ComboBox{AssignTo: &g.songFileFormatCombo, Model: songFileFormatLabels(), CurrentIndex: fileFormatIndex, MinSize: Size{Width: 250, Height: 34}, MaxSize: Size{Width: 340}},
			HSpacer{},
		}},
		Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
			PushButton{AssignTo: &g.fetchListButton, Text: "获取列表", MinSize: Size{Width: 100, Height: 36}, OnClicked: g.fetchTrackList},
			PushButton{AssignTo: &g.downloadButton, Text: "开始下载", Enabled: false, MinSize: Size{Width: 122, Height: 36}, OnClicked: g.startDownload},
			PushButton{AssignTo: &g.cancelButton, Text: "取消", Visible: false, MinSize: Size{Width: 88, Height: 36}, OnClicked: g.cancelCurrentDownload},
			PushButton{AssignTo: &g.downloadRetryButton, Text: "重试下载", Visible: false, MinSize: Size{Width: 100, Height: 36}, OnClicked: g.retryFailedDownload},
			PushButton{Text: "打开下载目录", MinSize: Size{Width: 122, Height: 36}, OnClicked: g.openOutputDir},
			PushButton{AssignTo: &g.openLogButton, Text: "打开日志", Enabled: false, MinSize: Size{Width: 96, Height: 36}, OnClicked: g.openTaskLog},
			HSpacer{},
		}},
		Composite{AssignTo: &g.taskPanel, Visible: false, Layout: VBox{MarginsZero: true, Spacing: 8}, Children: []Widget{
			Label{AssignTo: &g.taskTitle, Text: "准备下载", TextColor: colorText, Font: Font{Family: "Segoe UI", PointSize: 11, Bold: true}, MinSize: Size{Height: 24}},
			Label{AssignTo: &g.taskDetail, Text: "", TextColor: colorMuted, MinSize: Size{Height: 23}},
			Label{AssignTo: &g.taskStats, Text: "当前文件大小：-- · 下载速度：--", TextColor: colorMuted, MinSize: Size{Height: 23}},
			ProgressBar{AssignTo: &g.taskProgress, MarqueeMode: true, MinValue: 0, MaxValue: 100, MinSize: Size{Height: 8}, MaxSize: Size{Height: 8}},
		}},
		Composite{Layout: HBox{MarginsZero: true, Spacing: 10}, Children: []Widget{
			Label{Text: "歌曲列表", TextColor: colorText, Font: Font{Family: "Segoe UI", PointSize: 10, Bold: true}, MinSize: Size{Height: 23}},
			Label{AssignTo: &g.trackSummary, Text: "输入链接后点击“获取列表”获取歌曲列表", TextColor: colorMuted, MinSize: Size{Height: 23}},
			HSpacer{},
			PushButton{AssignTo: &g.trackSelectAll, Text: "全选", Enabled: false, MinSize: Size{Width: 72, Height: 30}, OnClicked: g.selectAllTracks},
			PushButton{AssignTo: &g.trackClearAll, Text: "全不选", Enabled: false, MinSize: Size{Width: 80, Height: 30}, OnClicked: g.clearAllTracks},
		}},
		TableView{
			AssignTo: &g.trackTable, Model: g.trackModel, AlternatingRowBG: true,
			LastColumnStretched: true, CustomRowHeight: 26,
			MinSize: Size{Height: 48}, StretchFactor: 1,
			Columns: []TableViewColumn{
				{Title: "选择", DataMember: "Selected", Width: 48},
				{Title: "#", DataMember: "No", Width: 44},
				{Title: "标题", DataMember: "Title", Width: 236},
				{Title: "艺术家", DataMember: "Artist", Width: 150},
				{Title: "专辑", DataMember: "Album", Width: 176},
			},
		},
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
			PushButton{AssignTo: &g.logoutButton, Text: "退出 Apple ID", MinSize: Size{Width: 112, Height: 34}, OnClicked: g.startLogout},
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
	if g.logoutButton != nil {
		g.logoutButton.SetEnabled(status.Installed && status.Healthy && !g.busy)
	}
	if g.loginRemoveButton != nil {
		g.loginRemoveButton.SetEnabled(status.Installed && !g.busy)
	}
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
	if g.loginRemoveButton != nil {
		g.loginRemoveButton.SetEnabled(false)
	}
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
	if g.loginRemoveButton != nil {
		g.loginRemoveButton.SetEnabled(g.lastStatus.Installed)
	}
	if g.shutdownRequested && !g.shutdownPending && !g.closeAllowed && g.mw != nil {
		go g.sync(g.stopRuntimeAndClose)
	}
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
	g.setCheckingDetail("正在读取专用 WSL2 环境状态")
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
				g.setCheckingDetail("")
			})
			response, err = client.Invoke(ctx, "start", nil)
		}
		g.sync(func() {
			g.endOperation()
			if g.shutdownRequested {
				return
			}
			if err != nil {
				if operationCode(err) == "login_required" {
					g.lastStatus = response.Status
					g.showLogin("")
					return
				}
				if operationCode(err) == "wsl_platform_unavailable" {
					// 只展示部署页并提示用户自行安装 WSL；程序不再自动安装。
					g.deployNote.SetText("未检测到 Windows Subsystem for Linux (WSL)。请自行安装 WSL2：以管理员身份运行 wsl --install 并重启电脑，然后回到本程序点击“一键部署”。")
					g.showOnboarding(pageDeploy)
					g.headerStatus.SetText("尚未部署")
					g.statusBar.SetText("等待部署专用运行环境")
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
			if g.shutdownRequested {
				return
			}
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
	if g.loginRemoveButton != nil {
		g.loginRemoveButton.SetEnabled(g.lastStatus.Installed && !g.busy)
	}
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
			if g.shutdownRequested {
				return
			}
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
			if g.shutdownRequested {
				return
			}
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
	return stopManagedRuntime(ctx, app.BootstrapClient{Bundle: g.bundle})
}

type bootstrapInvoker interface {
	Invoke(context.Context, string, []byte, ...string) (app.BootstrapResponse, error)
}

func stopManagedRuntime(ctx context.Context, client bootstrapInvoker) error {
	_, err := client.Invoke(ctx, "stop", nil)
	if operationCode(err) == "not_installed" {
		return nil
	}
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
	songFileFormat := app.SongFileFormatTitle
	if index := g.songFileFormatCombo.CurrentIndex(); index >= 0 && index < len(songFileFormats) {
		songFileFormat = songFileFormats[index].Template
	}
	if g.loadedLink != link.URL || g.trackModel.RowCount() == 0 {
		walk.MsgBox(g.mw, "尚未获取歌曲列表", "请先点击“获取列表”加载歌曲列表。", walk.MsgBoxOK|walk.MsgBoxIconWarning)
		return
	}
	indexes := g.trackChecker.SelectedIndexes()
	if len(indexes) == 0 {
		walk.MsgBox(g.mw, "未选择歌曲", "请先单击选择要下载的歌曲。", walk.MsgBoxOK|walk.MsgBoxIconWarning)
		return
	}
	g.startDownloadRequest(downloadRequest{
		link: link, outputDir: outputDir, quality: quality,
		songFileFormat: songFileFormat, selectIndexes: joinTrackIndexes(indexes),
	})
}

func (g *gui) fetchTrackList() {
	if g.busy {
		return
	}
	link, err := app.ValidateAppleMusicLink(g.linkEdit.Text())
	if err != nil {
		walk.MsgBox(g.mw, "链接无效", err.Error(), walk.MsgBoxOK|walk.MsgBoxIconWarning)
		return
	}
	g.loadTrackList(link)
}

func joinTrackIndexes(indexes []int) string {
	parts := make([]string, len(indexes))
	for i, index := range indexes {
		parts[i] = fmt.Sprintf("%d", index)
	}
	return strings.Join(parts, ",")
}

func (g *gui) loadTrackList(link app.LinkInfo) {
	g.fetchListButton.SetEnabled(false)
	g.downloadButton.SetEnabled(false)
	g.trackSummary.SetText("正在获取歌曲列表...")
	ctx := g.beginOperation("list-tracks", 10*time.Minute)
	go func() {
		client := app.BootstrapClient{Bundle: g.bundle}
		statusResponse, err := client.Invoke(ctx, "start", nil)
		if err == nil {
			var groups []app.TrackGroup
			groups, err = app.ListTracks(ctx, g.bundle, app.DownloadOptions{Link: link})
			g.sync(func() { g.finishListTracks(link, groups, err, statusResponse.Status) })
			return
		}
		g.sync(func() { g.finishListTracks(link, nil, err, statusResponse.Status) })
	}()
}

func (g *gui) finishListTracks(link app.LinkInfo, groups []app.TrackGroup, err error, status app.BootstrapStatus) {
	g.endOperation()
	g.fetchListButton.SetEnabled(true)
	g.lastStatus = status
	if err != nil {
		if operationCode(err) == "login_required" {
			g.retryAfterLogin = true
			g.showLogin("登录状态已失效，请重新登录后重试")
			return
		}
		g.downloadButton.SetEnabled(false)
		g.trackSummary.SetText("获取歌曲列表失败")
		walk.MsgBox(g.mw, "无法获取歌曲列表", err.Error(), walk.MsgBoxOK|walk.MsgBoxIconError)
		return
	}
	g.trackModel.Set(groups)
	total := g.trackModel.RowCount()
	g.trackChecker.Reset(total)
	g.loadedLink = link.URL
	g.trackSelectAll.SetEnabled(total > 0)
	g.trackClearAll.SetEnabled(total > 0)
	g.downloadButton.SetEnabled(total > 0)
	g.updateTrackSummary()
	g.statusBar.SetText(fmt.Sprintf("已加载 %d 首歌曲，单击选择后点击“开始下载”", total))
	if total == 0 {
		walk.MsgBox(g.mw, "没有歌曲", "该链接下没有可下载的歌曲。", walk.MsgBoxOK|walk.MsgBoxIconWarning)
	}
}

func (g *gui) updateTrackSummary() {
	total := g.trackModel.RowCount()
	if total == 0 {
		g.trackSummary.SetText("输入链接后点击“获取列表”获取歌曲列表")
		return
	}
	g.trackSummary.SetText(fmt.Sprintf("共 %d 首，已选 %d 首", total, g.trackChecker.SelectedCount()))
}

func (g *gui) selectAllTracks() {
	if g.trackModel.RowCount() == 0 {
		return
	}
	g.trackChecker.SetAll(true)
	g.trackModel.PublishRowsChanged(0, g.trackModel.RowCount()-1)
	g.updateTrackSummary()
}

func (g *gui) clearAllTracks() {
	if g.trackModel.RowCount() == 0 {
		return
	}
	g.trackChecker.SetAll(false)
	g.trackModel.PublishRowsChanged(0, g.trackModel.RowCount()-1)
	g.updateTrackSummary()
}

func (g *gui) openTaskLog() {
	if g.logPath == "" {
		return
	}
	if _, err := os.Stat(g.logPath); err != nil {
		walk.MsgBox(g.mw, "日志不存在", "日志文件已被移动或删除。", walk.MsgBoxOK|walk.MsgBoxIconWarning)
		return
	}
	_ = exec.Command("explorer.exe", "/select,"+g.logPath).Start()
}

func (g *gui) writeLogFile(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	g.logMu.Lock()
	defer g.logMu.Unlock()
	if g.logFile == nil {
		return
	}
	_, _ = fmt.Fprintf(g.logFile, "%s %s\n", time.Now().Format("2006-01-02 15:04:05"), line)
}

func (g *gui) logsDir() string {
	root := g.bundle.Root
	if root == "" {
		root = programRoot()
	}
	if root == "" {
		return ""
	}
	return filepath.Join(root, "logs")
}

func (g *gui) openLogFile(taskID string) *os.File {
	dir := g.logsDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil
	}
	path := filepath.Join(dir, "download-"+taskID+".log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil
	}
	g.sync(func() {
		g.logPath = path
		g.openLogButton.SetEnabled(true)
	})
	return file
}

func (g *gui) closeLogFile() {
	g.logMu.Lock()
	defer g.logMu.Unlock()
	if g.logFile != nil {
		_ = g.logFile.Close()
		g.logFile = nil
	}
}

func (g *gui) setLogFile(file *os.File) {
	g.logMu.Lock()
	defer g.logMu.Unlock()
	g.logFile = file
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
	g.settings = app.Settings{OutputDir: request.outputDir, Quality: request.quality, SongFileFormat: request.songFileFormat}
	_ = g.store.SaveSettings(g.settings)
	g.retryRequest = &request
	g.retryAfterLogin = false
	g.downloadComplete = false

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
	_ = g.songFileFormatCombo.SetCurrentIndex(songFileFormatIndex(request.songFileFormat))
	g.startDownloadRequest(request)
}

func (g *gui) runDownload(ctx context.Context, request downloadRequest) {
	client := app.BootstrapClient{Bundle: g.bundle}
	statusResponse, err := client.Invoke(ctx, "start", nil)
	if err != nil {
		g.finishDownload(request, nil, err, statusResponse.Status)
		return
	}

	taskID := newTaskID()
	logFile := g.openLogFile(taskID)
	if logFile != nil {
		g.setLogFile(logFile)
		defer g.closeLogFile()
		g.writeLogFile("下载任务开始：" + request.link.URL)
	}

	var tracks []app.DownloadedTrack
	err = app.RunDownload(ctx, g.bundle, app.DownloadOptions{
		Link: request.link, OutputDir: request.outputDir, Quality: request.quality,
		SongFileFormat: request.songFileFormat, SelectIndexes: request.selectIndexes, TaskID: taskID,
		OnEvent: func(event app.DownloadEvent) {
			if event.Event == "summary" && len(event.Tracks) > 0 {
				tracks = append([]app.DownloadedTrack(nil), event.Tracks...)
			}
			receivedAt := time.Now()
			g.sync(func() { g.handleDownloadEvent(event, receivedAt) })
		},
		OnLog: func(line string) { g.writeLogFile(line) },
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
			// CLI 已报告整个任务完成；用户此刻关闭程序不应再被当作
			// “操作进行中”而弹确认框。
			g.downloadComplete = true
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
		g.writeLogFile("完成：" + event.Song)
	case "warning":
		g.writeLogFile("警告：" + event.Message + formatDetail(event.Detail))
	case "error":
		g.writeLogFile("错误：" + event.Message + formatDetail(event.Detail))
	case "summary":
		g.downloadComplete = true
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

func (g *gui) finishDownload(request downloadRequest, tracks []app.DownloadedTrack, err error, status app.BootstrapStatus) {
	defer g.closeLogFile()
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
			g.writeLogFile("下载失败：" + message)
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
			if g.shutdownRequested {
				return
			}
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

func (g *gui) startLogout() {
	if g.busy || g.bundleErr != nil {
		return
	}
	answer := walk.MsgBox(g.mw, "退出 Apple ID",
		"将清除专用环境中的 Apple ID 登录状态，下次下载前需要重新登录。是否继续？",
		walk.MsgBoxYesNo|walk.MsgBoxIconWarning|walk.MsgBoxDefButton2)
	if answer != walk.DlgCmdYes {
		return
	}
	g.statusBar.SetText("正在退出 Apple ID")
	ctx := g.beginOperation("logout", 3*time.Minute)
	go func() {
		response, err := (app.BootstrapClient{Bundle: g.bundle}).Invoke(ctx, "logout", nil)
		g.sync(func() {
			g.endOperation()
			if g.shutdownRequested {
				return
			}
			if err != nil {
				g.showError("退出 Apple ID 失败", err, g.startLogout)
				return
			}
			g.lastStatus = response.Status
			g.showLogin("已退出 Apple ID，请重新登录")
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
	g.setCheckingDetail("备份完成并通过校验前，不会注销专用发行版")
	g.statusBar.SetText("正在导出专用运行环境备份")
	ctx := g.beginOperation("remove", 2*time.Hour)
	go func() {
		response, err := (app.BootstrapClient{Bundle: g.bundle}).Invoke(ctx, "remove", nil)
		g.sync(func() {
			g.endOperation()
			if g.shutdownRequested {
				return
			}
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
		path = filepath.Join(g.logsDir(), "wrapper.log")
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
			return "未检测到 WSL2，程序不会自动安装。请先自行安装：以管理员身份打开 PowerShell 运行 wsl --install，重启电脑后再重试。\r\n\r\n" + operationErr.Message
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

func (g *gui) stopRuntimeAndClose() {
	if g.shutdownPending || g.closeAllowed {
		return
	}
	if g.busy && g.cancel != nil {
		// 下载已结束但 CLI 进程还在收尾；直接终止它，不再询问用户。
		g.cancel()
	}
	g.shutdownRequested = true
	g.shutdownPending = true
	g.statusBar.SetText("正在停止专用运行环境并退出")
	// 停止专用发行版需要连续调用多次 wsl.exe（列出运行中发行版、校验所有权、
	// 终止发行版），在 WSL2 虚拟机运行或挂起时会花掉数秒到十几秒。交给独立
	// 进程在后台完成，主程序立即退出：窗口瞬间关闭，实例锁和程序文件随即
	// 释放，重开程序不会被“程序已在运行”或“文件正在使用”阻塞。
	g.detachedStop = startDetachedRuntimeStop(g.bundle)
	g.closeAllowed = true
	g.closed.Store(true)
	g.mw.Close()
}

const createNoWindowFlag = 0x08000000

// startDetachedRuntimeStop 启动一个独立的 AppleMusicWSL.exe stop 进程，由它
// 在后台停止专用 WSL 发行版，主程序不等待。返回 false 表示分离进程未能启动
// （例如安装包不完整），调用方应回退到进程内停止。
func startDetachedRuntimeStop(bundle app.Bundle) bool {
	if bundle.BootstrapExe == "" {
		return false
	}
	command := exec.Command(bundle.BootstrapExe, "stop", "--json")
	command.Dir = bundle.BootstrapDir
	command.Env = append(os.Environ(), "APPLEMUSIC_BUNDLE_ROOT="+bundle.Root)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindowFlag}
	if err := command.Start(); err != nil {
		return false
	}
	go func() { _ = command.Wait() }()
	return true
}

func shutdownOutcome(stopErr error, exitOnError bool) (closeAllowed, runtimeStopped bool) {
	if stopErr == nil {
		return true, true
	}
	return exitOnError, false
}

func (g *gui) stopManagedRuntimeAfterWindow() {
	g.closed.Store(true)
	if g.cancel != nil {
		g.cancel()
	}
	if g.runtimeStopped || g.detachedStop {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), runtimeShutdownTimeout)
	defer cancel()
	if err := stopManagedRuntime(ctx, app.BootstrapClient{Bundle: g.bundle}); err != nil {
		fmt.Fprintln(os.Stderr, "failed to stop managed WSL runtime during GUI shutdown:", err)
		writeShutdownError(g.logsDir(), err)
	}
}

func (g *gui) onClosing(canceled *bool, reason walk.CloseReason) {
	if g.closeAllowed {
		g.closed.Store(true)
		return
	}
	*canceled = true
	if g.shutdownPending {
		g.statusBar.SetText("正在停止专用运行环境并退出")
		return
	}
	if g.shutdownRequested {
		g.statusBar.SetText("正在取消当前操作并退出")
		return
	}
	if !g.busy || (g.opKind == "download" && g.downloadComplete) {
		g.stopRuntimeAndClose()
		return
	}
	if g.opKind == "shutdown" {
		*canceled = true
		g.statusBar.SetText("正在停止专用运行环境并退出")
		return
	}
	answer := walk.MsgBox(g.mw, "操作正在进行", "是否取消当前操作并退出程序？", walk.MsgBoxYesNo|walk.MsgBoxIconWarning|walk.MsgBoxDefButton2)
	if answer == walk.DlgCmdYes && g.cancel != nil {
		g.shutdownRequested = true
		g.statusBar.SetText("正在取消当前操作并退出")
		g.cancel()
	}
}
