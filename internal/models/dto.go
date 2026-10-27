package models

import "time"

// ---------- enums ----------

// AssetType mirrors the Immich AssetType enum ("IMAGE" | "VIDEO" | "AUDIO").
type AssetType string

const (
	AssetTypeImage AssetType = "IMAGE"
	AssetTypeVideo AssetType = "VIDEO"
	AssetTypeAudio AssetType = "AUDIO"
)

// ---------- persistence rows ----------

// User is a row of the users table.
type User struct {
	ID               string    `json:"id"`
	Email            string    `json:"email"`
	PasswordHash     string    `json:"-"` // never leaves the server
	Name             string    `json:"name"`
	AvatarPath       string    `json:"-"`
	IsAdmin          bool      `json:"isAdmin"`
	IsActive         bool      `json:"isActive"`
	QuotaSizeInBytes uint64    `json:"quotaSizeInBytes"` // 0 == unlimited
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

// Asset is a row of the assets table. Several fields are internal-only and
// therefore excluded from JSON; the wire shape is rebuilt by AssetResponse.
type Asset struct {
	ID              string
	DeviceAssetID   string
	OwnerID         string
	OriginalPath    string
	ThumbPath       string
	EncodedPath     string
	Type            AssetType
	MimeType        string
	FileName        string
	FileCreatedAt   time.Time
	FileModifiedAt  time.Time
	ChecksumSHA256  string
	IsVisible       bool
	IsFavorite      bool
	IsArchived      bool
	IsTrashed       bool
	IsResized       bool
	ThumbGenerated  bool
	FileSize        int64
	Width           int
	Height          int
	DateTimeOrig    time.Time
	LocalDate       time.Time
	City            string
	State           string
	Country         string
	Lat             float64
	Lon             float64
	Make            string
	Model           string
	LensModel       string
	ImageDescription string
	ProjectionType  string
	FPS             float64
	Timezone        string
	Rating          int
	CreatedAt       time.Time
	UpdatedAt       time.Time
	DeletedAt       *time.Time
}

// Album is a row of the albums table.
type Album struct {
	ID           string    `json:"id"`
	AlbumName    string    `json:"albumName"`
	Description  string    `json:"description"`
	Order        int32     `json:"order"`
	OwnerID      string    `json:"ownerId"`
	ThumbAssetID string    `json:"thumbAssetId"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// ---------- auth DTOs ----------

// LoginCredentialDTO is the body accepted by POST /api/auth/login.
type LoginCredentialDTO struct {
	Email                 string `json:"email" binding:"required"`
	Password              string `json:"password" binding:"required"`
	ForceChangingPassword bool   `json:"forceChangingPassword,omitempty"`
}

// LoginResponseDTO matches the official Immich login response. The mobile app
// reads accessToken and userId; extra keys are ignored harmlessly.
type LoginResponseDTO struct {
	AccessToken        string `json:"accessToken"`
	UserID             string `json:"userId"`
	UserName           string `json:"userName"`
	UserEmail          string `json:"userEmail"`
	IsAdmin            bool   `json:"isAdmin"`
	ProfileImagePath   string `json:"profile_image"`
	OauthConfigExpired *bool  `json:"oauthConfigExpiresAt"`
}

// SessionInfoDto is returned by GET /api/auth/.info (used by web clients).
type SessionInfoDto struct {
	HasAuthenticatedSession bool `json:"hasAuthenticatedSession"`
}

// AdminOnboardingCheckDto: we always report "already onboarded".
type AdminOnboardingCheckDto struct {
	ShouldChangeAdminPassword bool `json:"shouldChangeAdminPassword"`
	ShouldSetupAdmin          bool `json:"shouldSetupAdmin"`
}

// ChangePasswordDTO for PUT /api/user-me/change-password.
type ChangePasswordDTO struct {
	Password    string `json:"password" binding:"required"`
	NewPassword string `json:"newPassword" binding:"required"`
}

// ---------- user DTOs (wire format) ----------

// UserResponseDto is the canonical user payload used by /user/me, /user and
// the nested "owner" object inside asset responses.
type UserResponseDto struct {
	ID               string    `json:"id"`
	Email            string    `json:"email"`
	Name             string    `json:"name"`
	AvatarColor      string    `json:"avatarColor"`
	HasProfileImage  bool      `json:"hasProfileImage"`
	ProfileImagePath string    `json:"profile_image"`
	IsAdmin          bool      `json:"isAdmin"`
	IsActive         bool      `json:"isActive"`
	QuotaSizeInBytes uint64    `json:"quotaSizeInBytes"`
	StorageLabelUsed uint64    `json:"-"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
	DeleteAfter      *int      `json:"deleteAfter"`
}

// UserInfoUpdateDto is the PATCH body of /api/user-me.
type UserInfoUpdateDto struct {
	Username    *string `json:"username,omitempty"`
	StorageLabel *string `json:"storageLabel,omitempty"`
}

// ---------- server-info DTOs ----------

// ServerVersionDto: GET /api/server-info/version.
type ServerVersionDto struct {
	Major           int    `json:"major"`
	Minor           int    `json:"minor"`
	Patch           int    `json:"patch"`
	Source          string `json:"source"`
	Version         string `json:"version"`
	BuildDate       string `json:"buildDate"`
	BuildAt         string `json:"buildAt"`
	UpdateAvailable bool   `json:"updateAvailable"`
}

// ServerInfoDto: GET /api/server-info (statistics + version block).
type ServerInfoDto struct {
	ServerVersion ServerVersionDto `json:"serverVersion"`
	ExifTool      ExifToolInfoDto  `json:"exiftoolVersion"`
	Imagemagick   string           `json:"imagemagickVersion"`
	FFmpeg        string           `json:"ffmpegVersion"`
	Versions      VersionsInfoDto  `json:"versions"`
	BuildImageInfo map[string]any  `json:"buildImageInfo"`
}

// ExifToolInfoDto nests the exiftool version field.
type ExifToolInfoDto struct {
	VersionString string `json:"versionString"`
}

// VersionsInfoDto lists API versions advertised to clients.
type VersionsInfoDto struct {
	API   string `json:"apiVersion"`
	Mobile string `json:"mobileVersion"`
}

// OnThisServerDto is the nested "onThisServer" block of config responses.
type OnThisServerDto struct {
	Approved bool `json:"approved"`
	Enabled  bool `json:"enabled"`
}

// SmartSearchConfigDto: CLIP/search stay disabled on this hardware.
type SmartSearchConfigDto struct {
	Clip   OnThisServerDto `json:"clip"`
	Tags   OnThisServerDto `json:"tags"`
	Search OnThisServerDto `json:"search"`
}

// FacialRecognitionConfigDto: clustering stays disabled.
type FacialRecognitionConfigDto struct {
	Enabled  bool `json:"enabled"`
	AutoSave bool `json:"autoSave"`
}

// StorageOptionsConfigDto advertises the upload ceiling to mobile clients.
type StorageOptionsConfigDto struct {
	External              bool   `json:"external"`
	Removable             bool   `json:"removable"`
	MountPoint            string `json:"mountPoint"`
	MaxFileUploadFileSize string `json:"maxFileUploadFileSize"`
}

// MapConfigDto: reverse geocoding & map tiles disabled per spec.
type MapConfigDto struct {
	Enabled             bool `json:"enabled"`
	LightTheme          string `json:"lightTheme"`
	DarkTheme           string `json:"darkTheme"`
	StyleURL            string `json:"styleUrl"`
	CountryCode         string `json:"countryCode"`
	ReverseGeocoding    bool `json:"reverseGeocoding"`
}

// ImageCompressionDto mirrors the official imageCompression config block.
type ImageCompressionDto struct {
	Quality      int    `json:"quality"`
	TargetSize   int    `json:"targetSize"`
	Format       string `json:"format"`
	PreferJPEG   bool   `json:"preferJpeg"`
	InputCount   int    `json:"-"`
}

// VideoTranscodingDto: transcoding disabled (no ffmpeg on target board).
type VideoTranscodingDto struct {
	Enabled                bool     `json:"enabled"`
	AcceptHeader           []string `json:"acceptHeader"`
	TargetResolution       int      `json:"targetResolution"`
	PreferredCodec         string   `json:"preferredCodec"`
	MaxBitrate             int      `json:"maxBitrate"`
	TwoPass                bool     `json:"twoPass"`
	VP9Threshold           int      `json:"vp9Threshold"`
	MonoEncoder            bool     `json:"monoEncoder"`
	ThreadsConfig          int      `json:"threadsConfig"`
	HardwareAcceleration   string   `json:"hardwareAcceleration"`
	InitTimeout            int      `json:"initTimeout"`
}

// DuplicateDetectionDto: disabled.
type DuplicateDetectionDto struct {
	Enabled bool `json:"enabled"`
}

// BackgroundTaskDto mirrors backgroundTask config.
type BackgroundTaskDto struct {
	Concurrency int64 `json:"concurrency"`
}

// AuditLogsDto mirrors auditLogs config.
type AuditLogsDto struct {
	Enabled bool `json:"enabled"`
}

// LibraryOptionsDto mirrors library config (external scanning off).
type LibraryOptionsDto struct {
	Enabled                       bool `json:"enabled"`
	WatchPaths                    []string `json:"watch.paths"`
	ScanEnabled                   bool `json:"scan.enabled"`
	ScanInterval                  string `json:"scan.interval"`
	ScanAsync                     bool `json:"scan.async"`
	DeleteSyncClip                bool `json:"deleteSyncClip"`
}

// ServerLimitsDto mirrors serverLimits config.
type ServerLimitsDto struct {
	ConcurrentStorageCheck int64 `json:"concurrentStorageCheck"`
	ConcurrentJobs         int64 `json:"concurrentJobCount"`
	ConcurrentThumbnailGeneration int64 `json:"concurrentThumbnailGenerationCount"`
}

// MachineLearningDto: ML disabled everywhere.
type MachineLearningDto struct {
	URL                      string `json:"url"`
	CLIP                     MLModelDto `json:"clip"`
	FaceDetection            MLModelDto `json:"faceDetection"`
	FaceRecognition          MLModelDto `json:"facialRecognition"`
	SmartSearch              MLModelDto `json:"smartSearch"`
}

// MLModelDto nests one machine-learning model toggle.
type MLModelDto struct {
	Enabled bool `json:"enabled"`
}

// LicenseInfoDto: no license on self-hosted clone.
type LicenseInfoDto struct {
	Key        any    `json:"key"`
	LicenseKey string `json:"licenseKey"`
	DeviceType string `json:"deviceType"`
	ExpiryTime string `json:"expiryTime"`
}

// ExtensionAuthDto / ExternalAccessDto: minimal stubs kept for schema parity.
type ExtensionAuthDto struct {
	Enabled bool `json:"enabled"`
}

// ExternalAccessDto stub.
type ExternalAccessDto struct {
	Enabled bool `json:"enabled"`
}

// BinariesConfigDto points at helper binaries that we do not ship; empty
// strings make clients fall back to their defaults.
type BinariesConfigDto struct {
	FFmpegPath    string `json:"ffmpeg"`
	FFprobePath   string `json:"ffprobe"`
	ExiftoolPath  string `json:"exiftool"`
	SendmailPath  string `json:"sendmailBinary"`
}

// ServerFeaturesDto toggles every client-side feature switch. Anything not
// implemented MUST be false or the Flutter app will call missing endpoints.
// Field set follows the v1.1xx series of the official OpenAPI spec.
type ServerFeaturesDto struct {
	Canvas                       bool `json:"canvas"`
	DragAndDrop                  bool `json:"dragAndDrop"`
	ImmichSmartSearch            bool `json:"immichSmartSearch"`
	ML                           bool `json:"ml"`
	FaceRecognition              bool `json:"faceRecognition"`
	LivePhoto                    bool `json:"livePhoto"`
	Clipboard                    bool `json:"clipboard"`
	DuplicateDeprecatedEndpoints bool `json:"duplicateDeprecatedEndpoints"`
	BuiltThumbnails              bool `json:"builtThumbnails"`
	ReverseGeocoding             bool `json:"reverseGeocoding"`
	Sidecar                      bool `json:"sidecar"`
	SearchByCaption              bool `json:"searchByCaption"`
	TLSEnabled                   bool `json:"tlsEnabled"`
	IsBinaryDataCheckEnabled     bool `json:"isBinaryDataCheckEnabled"`
	EnableTempApiKeyFeatures     bool `json:"enableTempApiKeyFeatures"`
	AutoFill                     bool `json:"autoFill"`
	ConfigFile                   bool `json:"configFile"`
	UserDeleteCheck              bool `json:"userDeleteCheck"`
	DownloadArchives             bool `json:"downloadArchives"`
	SharedLinks                  bool `json:"sharedLinks"`
	UserWebSiteIntegration       bool `json:"userWebSiteIntegration"`
	Upload                       bool `json:"upload"`
	Search                       bool `json:"search"`
	Trash                        bool `json:"trash"`
	EmailNotification            bool `json:"emailNotification"`
	Slideshow                    bool `json:"slideshow"`
	PushNotification             bool `json:"pushNotification"`
	DuplicateDetection           bool `json:"duplicateDetection"`
	Map                          bool `json:"map"`
	Timeline                     bool `json:"timeline"`
	Faces                        bool `json:"faces"`
	Places                       bool `json:"places"`
	Tags                         bool `json:"tags"`
}

// ServerConfigDto is the full payload of GET /api/server-info/config.
type ServerConfigDto struct {
	LogLevel               string                  `json:"logLevel"`
	Theme                  string                  `json:"theme"`
	FeatureFlags           ServerFeaturesDto       `json:"featureFlags"`
	ServerName             string                  `json:"serverName"`
	Binaries               BinariesConfigDto       `json:"binaries"`
	NewlyOnboarded         bool                    `json:"newlyOnboarded"`
	WebpageURL             string                  `json:"webpageUrl"`
	Name                   string                  `json:"name"`
	ExtensionAuth          ExtensionAuthDto        `json:"extensionAuth"`
	ExternalAccess         ExternalAccessDto       `json:"externalAccess"`
	StorageOptions         StorageOptionsConfigDto `json:"storageOptions"`
	MachineLearning        MachineLearningDto      `json:"machineLearning"`
	LicenseInfo            LicenseInfoDto          `json:"licenseInfo"`
	Exif                   map[string]any          `json:"exif"`
	SmartSearch            SmartSearchConfigDto    `json:"smartSearch"`
	StackedTasks           []string                `json:"stackedTasks"`
	TimestampsUpdatedAfter *string                 `json:"timestampsUpdatedAfter"`
	Map                    MapConfigDto            `json:"map"`
	FacialRecognition      FacialRecognitionConfigDto `json:"facialRecognition"`
	ImageCompression       ImageCompressionDto     `json:"imageCompression"`
	VideoTranscoding       VideoTranscodingDto     `json:"videoTranscoding"`
	Email                  map[string]any          `json:"email"`
	StorageQuotaEnabled    bool                    `json:"storageQuotaEnabled"`
	DuplicateDetection     DuplicateDetectionDto   `json:"duplicateDetection"`
	BackgroundTask         BackgroundTaskDto       `json:"backgroundTask"`
	AuditLogs              AuditLogsDto            `json:"auditLogs"`
	Library                LibraryOptionsDto       `json:"library"`
	ServerLimits           ServerLimitsDto         `json:"serverLimits"`
}
