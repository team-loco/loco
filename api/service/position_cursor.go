package service

import (
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var errPositionCursor = errors.New("invalid page cursor")

type positionCursor struct {
	ID        *string
	CreatedAt *time.Time
}

func encodePositionCursor(id uuid.UUID, createdAt time.Time) string {
	position := "v1|" + createdAt.UTC().Format(time.RFC3339Nano) + "|" + id.String()
	return base64.URLEncoding.EncodeToString([]byte(position))
}

func decodePositionCursor(token string) (positionCursor, error) {
	if token == "" {
		return positionCursor{}, nil
	}
	decoded, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return positionCursor{}, errPositionCursor
	}
	parts := strings.Split(string(decoded), "|")
	if len(parts) == 1 {
		id, parseErr := uuid.ParseBytes(decoded)
		if parseErr != nil {
			return positionCursor{}, errPositionCursor
		}
		return positionCursor{ID: new(id.String())}, nil
	}
	if len(parts) != 3 || parts[0] != "v1" {
		return positionCursor{}, errPositionCursor
	}
	createdAt, err := time.Parse(time.RFC3339Nano, parts[1])
	if err != nil || createdAt.Year() < 1 {
		return positionCursor{}, errPositionCursor
	}
	id, err := uuid.Parse(parts[2])
	if err != nil {
		return positionCursor{}, errPositionCursor
	}
	return positionCursor{ID: new(id.String()), CreatedAt: new(createdAt)}, nil
}
