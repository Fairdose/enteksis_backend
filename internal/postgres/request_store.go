package postgres

import (
	"context"

	"github.com/Fairdose/enteksis_backend/internal/service"
	"github.com/jackc/pgx/v5/pgxpool"
)

type RequestStore struct {
	pool *pgxpool.Pool
}

func NewRequestStore(pool *pgxpool.Pool) *RequestStore {
	return &RequestStore{pool: pool}
}

func (store *RequestStore) CreateRequest(
	ctx context.Context,
	request service.Request,
) (service.CreatedRequest, error) {
	const query = `
		INSERT INTO service_requests (name, email, service_type, description)
		VALUES ($1, $2, $3, $4)
		RETURNING id::text, created_at`

	var created service.CreatedRequest
	err := store.pool.QueryRow(
		ctx,
		query,
		request.Name,
		request.Email,
		request.ServiceType,
		request.Description,
	).Scan(&created.ID, &created.CreatedAt)
	return created, err
}
