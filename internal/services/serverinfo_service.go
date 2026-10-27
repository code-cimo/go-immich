package services

import (
	"strconv"
	"strings"
	"time"

	"go-immich/config"
	"go-immich/internal/models"
)

// ServerInfoService builds the static configuration/version payloads. It is
// pure computation - no DB access - so responses are cheap even on an S905.
type ServerInfoService struct {
	cfg     *config.Config
	version models.ServerVersionDto
}

// NewServerInfoService parses config.ServerVersion ("v1.100.0") once at boot.
func NewServerInfoService(cfg *config.Config) (*ServerInfoService, error) {
	major, minor, patch, err := parseSemver(config.ServerVersion)
	if err != nil {
		return nil, err
	}
	return &ServerInfoService{
		cfg: cfg,
		version: models.ServerVersionDto{
			Major:           major,
			Minor:           minor,
			Patch:           patch,
			Version:         config.ServerVersion,
			Source:          "binary",
			BuildDate:       time.Now().UTC().Format("2006-01-02"),
			BuildAt:         time.Now().UTC().Format(time.RFC3339),
			UpdateAvailable: false, // air-gapped board: never advertise updates
		},
	}, nil
}

// Version returns the cached version block.
func (s *ServerInfoService) Version() models.ServerVersionDto { return s.version }

// Config assembles GET /api/server-info/config. Every unimplemented feature
// (ML, faces, CLIP, map, OAuth, shared links, live photos) reports disabled so
// the Flutter clients never call endpoints this server does not serve.
func (s *ServerInfoService) Config() models.ServerConfigDto {
	features := models.ServerFeaturesDto{
		Canvas:             false,
		DragAndDrop:        true,
		ImmichSmartSearch:  false,
		ML:                 false,
		FaceRecognition:    false,
		LivePhoto:          false,
		Clipboard:          true,
		BuiltThumbnails:    true,
		ReverseGeocoding:   false,
		Sidecar:            false,
		SearchByCaption:    false,
		TLSEnabled:         false,
		IsBinaryDataCheckEnabled: false,
		EnableTempApiKeyFeatures:  false,
		AutoFill:                  false,
		ConfigFile:                false,
		UserDeleteCheck:           false,
		DownloadArchives:          false,
		SharedLinks:               false,
		UserWebSiteIntegration:    false,
		Upload:                    true,
		Search:                    false,
		Trash:                     true,
		EmailNotification:         false,
		Slideshow:                 false,
		PushNotification:          false,
		DuplicateDetection:        false,
		Map:                       false,
		Timeline:                  true,
		Faces:                     false,
		Places:                    false,
		Tags:                      false,
	}

	return models.ServerConfigDto{
		LogLevel:       "log",
		Theme:          "light",
		FeatureFlags:   features,
		ServerName:     "go-immich",
		NewlyOnboarded: false,
		WebpageURL:     "",
		Name:           "go-immich",
		Binaries: models.BinariesConfigDto{
			FFmpegPath:   "",
			FFprobePath:  "",
			ExiftoolPath: "",
			SendmailPath: "",
		},
		ExtensionAuth:  models.ExtensionAuthDto{Enabled: false},
		ExternalAccess: models.ExternalAccessDto{Enabled: false},
		StorageOptions: models.StorageOptionsConfigDto{
			External:  false,
			Removable: false,
			MountPoint: s.cfg.PhotosDir,
			MaxFileUploadFileSize: "-1",
		},
		MachineLearning: models.MachineLearningDto{
			URL:             "",
			CLIP:            models.MLModelDto{Enabled: false},
			FaceDetection:   models.MLModelDto{Enabled: false},
			FaceRecognition: models.MLModelDto{Enabled: false},
			SmartSearch:     models.MLModelDto{Enabled: false},
		},
		LicenseInfo: models.LicenseInfoDto{Key: nil},
		Exif: map[string]any{
			"DateTimeOriginal": []string{"EXIF.DateTimeOriginal"},
		},
		SmartSearch: models.SmartSearchConfigDto{
			Clip:   models.OnThisServerDto{Approved: false, Enabled: false},
			Tags:   models.OnThisServerDto{Approved: false, Enabled: false},
			Search: models.OnThisServerDto{Approved: false, Enabled: false},
		},
		StackedTasks:           []string{},
		TimestampsUpdatedAfter: nil,
		Map: models.MapConfigDto{
			Enabled:          false,
			LightTheme:       "",
			DarkTheme:        "",
			StyleURL:         "",
			CountryCode:      "all",
			ReverseGeocoding: false,
		},
		FacialRecognition: models.FacialRecognitionConfigDto{Enabled: false, AutoSave: false},
		ImageCompression: models.ImageCompressionDto{
			Quality:    s.cfg.ResizeQuality,
			TargetSize: 0,
			Format:     "jpeg",
			PreferJPEG: true,
		},
		VideoTranscoding: models.VideoTranscodingDto{
			Enabled:              false,
			AcceptHeader:         []string{"video/*"},
			TargetResolution:     0,
			PreferredCodec:       "h264",
			MaxBitrate:           0,
			TwoPass:              false,
			VP9Threshold:         0,
			MonoEncoder:          false,
			ThreadsConfig:        0,
			HardwareAcceleration: "off",
			InitTimeout:          0,
		},
		Email:               map[string]any{},
		StorageQuotaEnabled: false,
		DuplicateDetection:  models.DuplicateDetectionDto{Enabled: false},
		BackgroundTask:      models.BackgroundTaskDto{Concurrency: int64(s.cfg.ResizeConcurrency)},
		AuditLogs:           models.AuditLogsDto{Enabled: false},
		Library: models.LibraryOptionsDto{
			Enabled:          false,
			WatchPaths:       []string{},
			ScanEnabled:      false,
			ScanInterval:     "0",
			ScanAsync:        true,
			DeleteSyncClip:   false,
		},
		ServerLimits: models.ServerLimitsDto{
			ConcurrentStorageCheck:        1,
			ConcurrentJobs:                int64(s.cfg.ResizeConcurrency),
			ConcurrentThumbnailGeneration: int64(s.cfg.ResizeConcurrency),
		},
	}
}

// parseSemver accepts "v1.100.0" or "1.100.0".
func parseSemver(v string) (major, minor, patch int, err error) {
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return 0, 0, 0, strconv.ErrSyntax
	}
	for i, dst := range []*int{&major, &minor, &patch} {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return 0, 0, 0, err
		}
		*dst = n
	}
	return major, minor, patch, nil
}
