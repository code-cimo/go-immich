package repository

import "go-immich/internal/models"

// ResolvedAsset is a tiny carrier used by handlers that need the row plus
// derived file paths; kept in the repository package to avoid an import cycle.
type ResolvedAsset struct {
	*models.Asset
}
