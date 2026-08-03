package bootstrap

import (
	"context"
	"io"
	"time"
)

const (
	ProductID                 = "io.applemusic-downloader.private-wsl"
	RuntimeVersion            = "1"
	DistroPrefix              = "AppleMusic-Runtime-"
	RuntimeLinuxDir           = "/opt/applemusic-wrapper"
	MarkerLinuxPath           = "/etc/applemusic-runtime.json"
	LoginDataLinuxDir         = RuntimeLinuxDir + "/rootfs/data/data/com.apple.android.music/files"
	LoginCredentialsLinuxPath = "/run/applemusic-login.credentials"
	LoginPendingLinuxPath     = "/run/applemusic-login.pending"
	TwoFactorLinuxPath        = LoginDataLinuxDir + "/2fa.txt"

	// UbuntuBaseURL 优先使用国内镜像，避免从国外服务器下载过慢或失败。文件
	// 是 Ubuntu 官方发布的 WSL 镜像（gzip tar 根文件系统），内容与官方
	// releases 完全一致（固定 SHA-256 校验），下载失败时依次尝试
	// UbuntuBaseMirrors 中的备用镜像。
	UbuntuBaseURL    = "https://mirrors.ustc.edu.cn/ubuntu-releases/24.04.3/ubuntu-24.04.4-wsl-amd64.wsl"
	UbuntuBaseSHA256 = "9b2f7730dc68227dd04a9f3e5eab86ad85caf556b8606ad94f1f29ff5c4fd3f5"
	PayloadSHA256    = "5dbc716180ca3df310f040b62f338d09f717390e8e1fe5687475a7af16f5113b"
)

// UbuntuBaseMirrors 是 UbuntuBaseURL 下载失败后的备用镜像，全部托管与官方
// releases 相同的固定版本文件。
var UbuntuBaseMirrors = []string{
	"https://mirrors.aliyun.com/ubuntu-releases/24.04.4/ubuntu-24.04.4-wsl-amd64.wsl",
	"https://mirrors.huaweicloud.com/ubuntu-releases/24.04.4/ubuntu-24.04.4-wsl-amd64.wsl",
	"https://releases.ubuntu.com/24.04.4/ubuntu-24.04.4-wsl-amd64.wsl",
}

type Stage string

const (
	StagePrepared         Stage = "prepared"
	StagePlatformPending  Stage = "platform_pending"
	StageRuntimeBuilt     Stage = "runtime_built"
	StageDistroRegistered Stage = "distro_registered"
	StageInstalled        Stage = "installed"
	StageRemovalPrepared  Stage = "removal_prepared"
	StageRemoved          Stage = "removed"
)

type State struct {
	SchemaVersion    int       `json:"schema_version"`
	ProductID        string    `json:"product_id"`
	OwnerSID         string    `json:"owner_sid"`
	InstanceID       string    `json:"instance_id"`
	DistroName       string    `json:"distro_name"`
	InstallDir       string    `json:"install_dir"`
	Stage            Stage     `json:"stage"`
	RuntimeVersion   string    `json:"runtime_version"`
	PayloadSHA256    string    `json:"payload_sha256"`
	UbuntuBaseSHA256 string    `json:"ubuntu_base_sha256"`
	RuntimeTarSHA256 string    `json:"runtime_tar_sha256,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	InstalledAt      time.Time `json:"installed_at,omitempty"`
	RemovedAt        time.Time `json:"removed_at,omitempty"`
	LastBackupPath   string    `json:"last_backup_path,omitempty"`
	LastBackupSHA256 string    `json:"last_backup_sha256,omitempty"`
	LastBackupSize   int64     `json:"last_backup_size,omitempty"`
	RecoveryPaths    []string  `json:"recovery_paths,omitempty"`
}

type RuntimeMarker struct {
	ProductID        string `json:"product_id"`
	InstanceID       string `json:"instance_id"`
	RuntimeVersion   string `json:"runtime_version"`
	PayloadSHA256    string `json:"payload_sha256"`
	UbuntuBaseSHA256 string `json:"ubuntu_base_sha256"`
}

type Status struct {
	Installed      bool     `json:"installed"`
	Owned          bool     `json:"owned"`
	Running        bool     `json:"running"`
	Healthy        bool     `json:"healthy"`
	Stage          Stage    `json:"stage,omitempty"`
	InstanceID     string   `json:"instance_id,omitempty"`
	DistroName     string   `json:"distro_name,omitempty"`
	InstallDir     string   `json:"install_dir,omitempty"`
	RuntimeVersion string   `json:"runtime_version,omitempty"`
	LogPath        string   `json:"log_path,omitempty"`
	Detail         string   `json:"detail,omitempty"`
	RecoveryPaths  []string `json:"recovery_paths,omitempty"`
}

type Config struct {
	AppDataDir        string
	PayloadDir        string
	UbuntuBasePath    string
	UbuntuBaseURL     string
	UbuntuBaseMirrors []string
	UbuntuBaseHash    string
	PayloadHash       string
	RuntimeVersion    string
	DownloadTimeout   time.Duration
	CommandTimeout    time.Duration
	StartupTimeout    time.Duration
}

type Command struct {
	Path    string
	Args    []string
	Dir     string
	Stdin   []byte
	Timeout time.Duration
}

type CommandResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

type Runner interface {
	Run(ctx context.Context, command Command) (CommandResult, error)
	Start(command Command, stdout, stderr io.Writer) (Process, error)
}

type Process interface {
	PID() int
	Release() error
}

type Locker interface {
	Lock(ctx context.Context) (unlock func(), err error)
}
