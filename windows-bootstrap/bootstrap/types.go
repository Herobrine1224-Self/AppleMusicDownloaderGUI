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

	UbuntuBaseURL    = "https://cdimage.ubuntu.com/ubuntu-base/releases/24.04.4/release/ubuntu-base-24.04.4-base-amd64.tar.gz"
	UbuntuBaseSHA256 = "c1e67ef7b17a6300e136118bd1dc04725009cb376c1aad10abcf8cd453628d58"
	PayloadSHA256    = "5dbc716180ca3df310f040b62f338d09f717390e8e1fe5687475a7af16f5113b"
)

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
	AppDataDir      string
	PayloadDir      string
	UbuntuBasePath  string
	UbuntuBaseURL   string
	UbuntuBaseHash  string
	PayloadHash     string
	RuntimeVersion  string
	DownloadTimeout time.Duration
	CommandTimeout  time.Duration
	StartupTimeout  time.Duration
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
