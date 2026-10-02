package service

import (
	"encoding/base64"
	"fmt"

	"github.com/google/uuid"
)

// encodeCursor encodes an ID as a base64 cursor token
func encodeCursor(id string) string {
	return base64.URLEncoding.EncodeToString([]byte(id))
}

// decodeCursor decodes a base64 cursor token to an ID
func decodeCursor(token string) (string, error) {
	if token == "" {
		return "", nil
	}
	decoded, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return "", fmt.Errorf("invalid cursor token: %w", err)
	}
	id, err := uuid.ParseBytes(decoded)
	if err != nil {
		return "", fmt.Errorf("invalid cursor token: %w", err)
	}
	return id.String(), nil
}

// normalizePageSize ensures page_size is within bounds (default: 50, max: 200)
func normalizePageSize(pageSize int32) int32 {
	if pageSize == 0 {
		return 50
	}
	if pageSize > 200 {
		return 200
	}
	return pageSize
}
