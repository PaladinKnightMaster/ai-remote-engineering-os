module aireos.local/tests/e2e

go 1.23

require (
	aireos.local/packages/go/auth v0.0.0
	github.com/coder/websocket v1.8.12
	github.com/jackc/pgx/v5 v5.7.1
	golang.org/x/crypto v0.33.0
)

require (
	aidanwoods.dev/go-paseto v1.5.4 // indirect
	aidanwoods.dev/go-result v0.3.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/sync v0.11.0 // indirect
	golang.org/x/sys v0.30.0 // indirect
	golang.org/x/text v0.22.0 // indirect
)

// auth transitively imports packages/go/middleware; replaces in a dependency
// module are ignored, so the local path must be declared here too.
require aireos.local/packages/go/middleware v0.0.0 // indirect

replace (
	aireos.local/packages/go/auth => ../../packages/go/auth
	aireos.local/packages/go/middleware => ../../packages/go/middleware
)
