// go-immich: a resource-efficient Go reimplementation of the Immich server API,
// targeted at ARM64 single-board computers (e.g. ODROID-C2, 2GB RAM).
module go-immich

go 1.22

require (
	github.com/disintegration/imaging v1.6.2
	github.com/gin-gonic/gin v1.9.1
	github.com/golang-jwt/jwt/v5 v5.2.1
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.5.5
	github.com/rwcarlsen/goexif v0.0.0-20190401172101-9ab8dea44aca
	golang.org/x/crypto v0.21.0
	modernc.org/sqlite v1.29.5
)
