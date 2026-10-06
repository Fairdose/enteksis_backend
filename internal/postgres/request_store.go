package postgres

import (
	"context"
	"errors"

	"github.com/Fairdose/enteksis_backend/internal/service"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
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

func (store *RequestStore) ListRequests(ctx context.Context) ([]service.ServiceRequest, error) {
	const query = `
		SELECT
			request.id::text,
			request.name,
			request.email,
			request.service_type,
			request.description,
			request.created_at
		FROM service_requests request
		ORDER BY request.created_at DESC
		LIMIT 200`

	rows, err := store.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	requests := make([]service.ServiceRequest, 0)
	for rows.Next() {
		var request service.ServiceRequest
		if err := rows.Scan(
			&request.ID,
			&request.Name,
			&request.Email,
			&request.ServiceType,
			&request.Description,
			&request.CreatedAt,
		); err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	return requests, rows.Err()
}

func (store *RequestStore) GetRequest(ctx context.Context, id string) (service.ServiceRequest, error) {
	var requestID pgtype.UUID
	if err := requestID.Scan(id); err != nil || !requestID.Valid {
		return service.ServiceRequest{}, service.ErrRequestNotFound
	}

	const requestQuery = `
		SELECT
			request.id::text,
			request.name,
			request.email,
			request.service_type,
			request.description,
			request.created_at
		FROM service_requests request
		WHERE request.id = $1`

	var request service.ServiceRequest
	err := store.pool.QueryRow(ctx, requestQuery, requestID).Scan(
		&request.ID,
		&request.Name,
		&request.Email,
		&request.ServiceType,
		&request.Description,
		&request.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return service.ServiceRequest{}, service.ErrRequestNotFound
	}
	if err != nil {
		return service.ServiceRequest{}, err
	}
	return request, nil
}
