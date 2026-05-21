module aireos.local/services/audit-service

go 1.23

require (
	aireos.local/packages/go/middleware v0.0.0
	aireos.local/packages/go/migrate v0.0.0
	github.com/go-chi/chi/v5 v5.1.0
	github.com/golang-migrate/migrate/v4 v4.18.1
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.7.1
)

replace (
	aireos.local/packages/go/middleware => ../../packages/go/middleware
	aireos.local/packages/go/migrate => ../../packages/go/migrate
)
