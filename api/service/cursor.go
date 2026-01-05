package service

import (
	"encoding/base64"
	"fmt"
	"strconv"
)

// encodeCursor encodes an ID as a base64 cursor token
func encodeCursor(id int64) string {
	return base64.URLEncoding.EncodeToString([]byte(fmt.Sprintf("%d", id)))
}

// decodeCursor decodes a base64 cursor token to an ID
func decodeCursor(token string) (int64, error) {
	if token == "" {
		return 0, nil
	}
	decoded, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return 0, fmt.Errorf("invalid cursor token: %w", err)
	}
	id, err := strconv.ParseInt(string(decoded), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid cursor value: %w", err)
	}
	return id, nil
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
