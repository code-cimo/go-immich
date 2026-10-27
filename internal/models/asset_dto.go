package models

import (
	"encoding/base64"
	"time"
)

// VisibilityStatus mirrors the Immich VisibilityStatus enum.
type VisibilityStatus string

const (
	VisibilityHidden  VisibilityStatus = "hidden"
	VisibilityVisible VisibilityStatus = "visible"
	VisibilityArchive VisibilityStatus = "archive"
	VisibilityLocked  VisibilityStatus = "locked"
)

// ---------- asset wire DTOs (OpenAPI: AssetResponseDto / ExifResponseDto) ----------

// ExifResponseDto must contain ALL fields of the official schema; the Flutter
// client's generated parser uses required non-null fields and will throw a
// FormatException if any of them is missing from the JSON object.
type ExifResponseDto struct {
	Make                   *string  `json:"make,omitempty"`
	Model                  *string  `json:"Model,omitempty"`
	Model2                 *string  `json:"model,omitempty"`
	MountMode              *string  `json:"mountMode,omitempty"`
	Lens                   *string  `json:"lens,omitempty"`
	SidecarPath            *string  `json:"sidecarPath,omitempty"`
	Archived               bool     `json:"archived"`
	Orientation            *string  `json:"orientation,omitempty"`
	DatetimeOriginal       *string  `json:"dateTimeOriginal,omitempty"`
	ModifyDate             *string  `json:"modifyDate,omitempty"`
	TimeZone               *string  `json:"timeZone,omitempty"`
	MediaType              *string  `json:"mediaType,omitempty"`
	CreateDate             *string  `json:"createDate,omitempty"`
	GPSLatitude            *float64 `json:"latitude,omitempty"`
	GPSLongitude           *float64 `json:"longitude,omitempty"`
	Rating                 *int32   `json:"rating,omitempty"`
	FaultTolerance         *int32   `json:"faultTolerance,omitempty"`
	ISO                    *uint32  `json:"iso,omitempty"`
	FocalLength          *float64 `json:"focalLength,omitempty"`
	FNumber              *float64 `json:"fNumber,omitempty"`
	ExposureTime         *float64 `json:"exposureTime,omitempty"`
	ImageWidth           *int32   `json:"exifImageWidth,omitempty"`
	ImageHeight          *int32   `json:"exifImageHeight,omitempty"`
	FileSizeInByte       *int64   `json:"fileSizeInByte,omitempty"`
	BitsPerSample        *int32   `json:"bitsPerSample,omitempty"`
	ColorSpace           *int32   `json:"colorSpace,omitempty"`
	FileCreatedAt        *string  `json:"fileCreatedAt,omitempty"`
	FileModifiedAt       *string  `json:"fileModifiedAt,omitempty"`
	City                 *string  `json:"city,omitempty"`
	State                *string  `json:"state,omitempty"`
	Country              *string  `json:"country,omitempty"`
	CityID               *string  `json:"cityID,omitempty"`
	StateID              *string  `json:"stateID,omitempty"`
	ProjectionType       *string  `json:"projectionType,omitempty"`
	Description          *string  `json:"description,omitempty"`
	ProfileDescription   *string  `json:"profileDescription,omitempty"`
	Rank                 *int32   `json:"rank,omitempty"`
}

// AssetJobStatusDto reports thumbnail/preview/transcode availability. The
// mobile app only proceeds with playback/download when these say "success".
type AssetJobStatusDto struct {
	EncodedVideo string `json:"encodedVideo"`
	GeneratedAt  string `json:"generatedAt,omitempty"`
	LivePhotoVideo string `json:"livePhotoVideo,omitempty"`
	MetaData     string `json:"metadata"`
	Thumbnail    string `json:"thumbnail"`
	IsExternal   bool   `json:"isExternal"`
}

// AssetRelationDto describes stacks/people for an asset (all empty here).
type AssetRelationDto struct {
	Assets []map[string]any `json:"assets"`
	Bubbles []map[string]any `json:"bubbles"`
	Images []map[string]any `json:"images"`
}

// AssetResponseDto is the canonical asset payload returned by every endpoint
// that hands assets to clients.
type AssetResponseDto struct {
	ID              string            `json:"id"`
	Checksum        string            `json:"checksum"`
	DeviceAssetID   string            `json:"deviceAssetId"`
	OwnerID         string            `json:"ownerId"`
	UserID          string            `json:"userId"`
	DeviceID        string            `json:"deviceId"`
	Type            AssetType         `json:"type"`
	CreationTime    time.Time         `json:"creationTime"`
	Name            string            `json:"name"`
	FileCreatedAt   time.Time         `json:"fileCreatedAt"`
	FileModifiedAt  time.Time         `json:"fileModifiedAt"`
	UpdatedAt       time.Time         `json:"updatedAt"`
	IsFavorite      bool              `json:"isFavorite"`
	IsArchived      bool              `json:"isArchived"`
	IsTrashed       bool              `json:"isTrashed"`
	IsOffline       bool              `json:"isOffline"`
	IsShared        bool              `json:"isShared"`
	Visibility      VisibilityStatus  `json:"visibility"`
	Latitude        *float64          `json:"latitude"`
	Longitude       *float64          `json:"longitude"`
	Tags            []SearchResponseDtoTermsItem `json:"tags"`
	Duration        string            `json:"duration"`
	LivePhotoVideo  map[string]any    `json:"livePhotoVideo,omitempty"`
	StackCount      int               `json:"stackCount,omitempty"`
	StackID         string            `json:"stackId,omitempty"`
	ProjectTags     []map[string]any  `json:"projectTags,omitempty"`
	ExifInfo        ExifResponseDto   `json:"exifInfo"`
	Thumbhash       string            `json:"thumbhash"`
	AssetURL        string            `json:"assetUrl,omitempty"`
	OriginalPath    string            `json:"originalPath,omitempty"`
	EncodedVideoURL string            `json:"encodedVideoUrl,omitempty"`
	JOBStatus       AssetJobStatusDto `json:"jobStatus"`
	Relations       AssetRelationDto  `json:"relations"`
	LibraryID       string            `json:"libraryId,omitempty"`
}

// SearchResponseDtoTermsItem keeps tags as objects like the official schema.
type SearchResponseDtoTermsItem struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// Mapping constants used by the repository layer to hydrate rows.

// ToResponse converts a persisted Asset into its wire representation. Paths
// are intentionally omitted: they would leak the server filesystem layout and
// the official API never sends them either.
func (a *Asset) ToResponse() AssetResponseDto {
	resp := AssetResponseDto{
		ID:             a.ID,
		Checksum:       base64.StdEncoding.EncodeToString(hexToBytes(a.ChecksumSHA256)),
		DeviceAssetID:  a.DeviceAssetID,
		OwnerID:        a.OwnerID,
		UserID:         a.OwnerID, // clients read both keys
		Type:           a.Type,
		Name:           a.FileName,
		CreationTime:   pickTime(a.DateTimeOrig, pickTime(a.FileCreatedAt, a.CreatedAt)),
		FileCreatedAt:  pickTime(a.FileCreatedAt, a.CreatedAt),
		FileModifiedAt: pickTime(a.FileModifiedAt, a.CreatedAt),
		UpdatedAt:      a.UpdatedAt,
		IsFavorite:     a.IsFavorite,
		IsArchived:     a.IsArchived,
		IsTrashed:      a.IsTrashed,
		Visibility:     visibilityOf(a.IsVisible),
		Tags:           []SearchResponseDtoTermsItem{},
		Duration:       "",
		Thumbhash:      DefaultThumbhash,
		JOBStatus: AssetJobStatusDto{
			EncodedVideo: jobStatusFor(a.Type == AssetTypeVideo && a.EncodedPath != ""),
			Thumbnail:    jobStatusFor(a.ThumbGenerated),
			MetaData:     "success",
		},
		Relations: AssetRelationDto{
			Assets:  []map[string]any{},
			Bubbles: []map[string]any{},
			Images:  []map[string]any{},
		},
	}

	exif := &resp.ExifInfo
	strp := func(s string) *string { if s == "" { return nil }; return &s }
	f64p := func(f float64) *float64 { if f == 0 { return nil }; return &f }
	i32p := func(i int) *int32 { if i == 0 { return nil }; v := int32(i); return &v }

	exif.Make = strp(a.Make)
	exif.Model = strp(a.Model)
	exif.Lens = strp(a.LensModel)
	exif.Description = strp(a.ImageDescription)
	exif.City = strp(a.City)
	exif.State = strp(a.State)
	exif.Country = strp(a.Country)
	exif.TimeZone = strp(a.Timezone)
	exif.MediaType = strp(a.MimeType)
	exif.ProjectionType = strp(a.ProjectionType)
	exif.GPSLatitude = f64p(a.Lat)
	exif.GPSLongitude = f64p(a.Lon)
	exif.ImageWidth = i32p(a.Width)
	exif.ImageHeight = i32p(a.Height)
	exif.Rating = i32p(a.Rating)
	if a.FileSize > 0 { fs := a.FileSize; exif.FileSizeInByte = &fs }
	if !a.DateTimeOrig.IsZero() {
		s := a.DateTimeOrig.Format(time.RFC3339Nano)
		exif.DatetimeOriginal = &s
	}
	if !a.FileCreatedAt.IsZero() {
		s := a.FileCreatedAt.UTC().Format("2006-01-02T15:04:05.000Z")
		exif.FileCreatedAt = &s
	}
	if !a.FileModifiedAt.IsZero() {
		s := a.FileModifiedAt.UTC().Format("2006-01-02T15:04:05.000Z")
		exif.FileModifiedAt = &s
	}
	return resp
}

// DefaultThumbhash is a neutral 64x64 blurred placeholder (RGBA packed form
// used by Immich: first byte = width, second = height, then 8x8 RGB pixels).
// Real thumbhash generation is out of scope; clients render it as grey blur.
var DefaultThumbhash = base64.StdEncoding.EncodeToString(
	append([]byte{64, 64, 16}, make([]byte, 8*8*3)...))

const (
	statusSuccess = "success"
	statusPending = "pending"
)

func jobStatusFor(done bool) string {
	if done {
		return statusSuccess
	}
	return statusPending
}

func visibilityOf(visible bool) VisibilityStatus {
	if visible {
		return VisibilityVisible
	}
	return VisibilityHidden
}

func pickTime(primary, fallback time.Time) time.Time {
	if primary.IsZero() {
		return fallback
	}
	return primary
}

// hexToBytes decodes a hex checksum back into raw bytes (Immich transmits the
// checksum base64-encoded). Returns nil on malformed input instead of panicking.
func hexToBytes(h string) []byte {
	if len(h)%2 != 0 {
		return nil
	}
	out := make([]byte, len(h)/2)
	for i := 0; i < len(out); i++ {
		var v uint32
		for j := 0; j < 2; j++ {
			c := h[i*2+j]
			switch {
			case c >= '0' && c <= '9':
				v = v<<4 | uint32(c-'0')
			case c >= 'a' && c <= 'f':
				v = v<<4 | uint32(c-'a'+10)
			case c >= 'A' && c <= 'F':
				v = v<<4 | uint32(c-'A'+10)
			default:
				return nil
			}
		}
		out[i] = byte(v)
	}
	return out
}
