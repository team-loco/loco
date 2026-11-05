package clickhouse

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	partPositionParts       = 3
	partPositionIntegerBits = 64
	partPositionColumns     = "(Timestamp, _block_number, _block_offset)"
)

var errInvalidPartPosition = errors.New("invalid part position")

type partPosition struct {
	timestamp   time.Time
	blockNumber uint64
	blockOffset uint64
	partitionID string
}

func (position partPosition) encode() string {
	timestamp := position.timestamp.UTC().Format(time.RFC3339Nano)
	value := fmt.Sprintf("%s/%d/%d", timestamp, position.blockNumber, position.blockOffset)
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodePartPosition(value string) (partPosition, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return partPosition{}, errInvalidPartPosition
	}
	parts := strings.Split(string(decoded), "/")
	if len(parts) != partPositionParts {
		return partPosition{}, errInvalidPartPosition
	}
	timestamp, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil || !time.Unix(0, timestamp.UnixNano()).Equal(timestamp) {
		return partPosition{}, errInvalidPartPosition
	}
	blockNumber, err := strconv.ParseUint(parts[1], 10, partPositionIntegerBits)
	if err != nil {
		return partPosition{}, errInvalidPartPosition
	}
	blockOffset, err := strconv.ParseUint(parts[2], 10, partPositionIntegerBits)
	if err != nil {
		return partPosition{}, errInvalidPartPosition
	}
	return partPosition{timestamp: timestamp, blockNumber: blockNumber, blockOffset: blockOffset}, nil
}
