package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/arpitkuriyal/business-drift/internal/platform/config"
)

const connectionTimeout = 2 * time.Second

// Resources holds connections to services used by the API.
type Resources struct {
	Postgres *pgxpool.Pool
	Redis    *redis.Client
}

func Open(ctx context.Context, cfg config.Config) (*Resources, error) {
	postgresConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	postgresConfig.MaxConns = 10
	postgresConfig.ConnConfig.ConnectTimeout = connectionTimeout

	postgres, err := pgxpool.NewWithConfig(ctx, postgresConfig)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	redisOptions, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		postgres.Close()
		return nil, fmt.Errorf("parse Redis URL: %w", err)
	}
	redisOptions.PoolSize = 10
	redisOptions.DialTimeout = connectionTimeout

	return &Resources{
		Postgres: postgres,
		Redis:    redis.NewClient(redisOptions),
	}, nil
}

func (r *Resources) Close() {
	_ = r.Redis.Close()
	r.Postgres.Close()
}

// Check reports whether PostgreSQL and Redis are reachable.
func (r *Resources) Check(ctx context.Context) map[string]error {
	checks := make(map[string]error, 2)

	postgresContext, cancelPostgres := context.WithTimeout(ctx, connectionTimeout)
	checks["postgres"] = r.Postgres.Ping(postgresContext)
	cancelPostgres()

	redisContext, cancelRedis := context.WithTimeout(ctx, connectionTimeout)
	checks["redis"] = r.Redis.Ping(redisContext).Err()
	cancelRedis()

	return checks
}
