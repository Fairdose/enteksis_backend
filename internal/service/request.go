package service

import (
	"context"
	"errors"
	"time"
)

var ErrRequestNotFound = errors.New("service request not found")

type RequestStatus string

const (
	RequestStatusNew     RequestStatus = "new"
	RequestStatusRead    RequestStatus = "read"
	RequestStatusReplied RequestStatus = "replied"
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

type ServiceRequest struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Email       string        `json:"email"`
	ServiceType string        `json:"serviceType"`
	Description string        `json:"description"`
	Status      RequestStatus `json:"status"`
	CreatedAt   time.Time     `json:"createdAt"`
	UpdatedAt   time.Time     `json:"updatedAt"`
	RepliedAt   *time.Time    `json:"repliedAt"`
}

type RequestStore interface {
	CreateRequest(ctx context.Context, request Request) (CreatedRequest, error)
}

type AdminRequestStore interface {
	ListRequests(ctx context.Context) ([]ServiceRequest, error)
	GetRequest(ctx context.Context, id string) (ServiceRequest, error)
	UpdateRequestStatus(ctx context.Context, id string, status RequestStatus) (ServiceRequest, error)
	DeleteRequest(ctx context.Context, id string) error
}
