module aireos.local/packages/go/auth

go 1.23

require (
	aidanwoods.dev/go-paseto v1.5.4
	aireos.local/packages/go/middleware v0.0.0
	github.com/google/uuid v1.6.0
)

require (
	aidanwoods.dev/go-result v0.3.1 // indirect
	golang.org/x/crypto v0.33.0 // indirect
	golang.org/x/sys v0.30.0 // indirect
)

replace aireos.local/packages/go/middleware => ../middleware
