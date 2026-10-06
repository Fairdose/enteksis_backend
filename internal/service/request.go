package service

import (
	"context"
	"time"
)

type Request struct {
	Name        string
	Email       string
	ServiceType string
	Description string
}

type CreatedRequest struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
}

type RequestStore interface {
	CreateRequest(ctx context.Context, request Request) (CreatedRequest, error)
}
