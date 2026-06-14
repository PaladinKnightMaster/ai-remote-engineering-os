module aireos.local/services/mcp-gateway

go 1.23

require (
	aireos.local/packages/go/auth v0.0.0
	aireos.local/packages/go/middleware v0.0.0
	github.com/go-chi/chi/v5 v5.1.0
)

require (
	aidanwoods.dev/go-paseto v1.5.4 // indirect
	aidanwoods.dev/go-result v0.3.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	golang.org/x/crypto v0.33.0 // indirect
	golang.org/x/sys v0.30.0 // indirect
)

replace (
	aireos.local/packages/go/auth => ../../packages/go/auth
	aireos.local/packages/go/middleware => ../../packages/go/middleware
)
