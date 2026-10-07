package infra

import (
	"encoding/json/v2"
	"io"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	maxJSONDepth     = 100
	maxManifestBytes = 5 << 20
)

func DecodeJSON(input io.Reader, value any) error {
	data, err := io.ReadAll(io.LimitReader(input, maxManifestBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxManifestBytes {
		return errJSONTooLarge
	}
	if message, ok := value.(proto.Message); ok {
		return (protojson.UnmarshalOptions{RecursionLimit: maxJSONDepth}).Unmarshal(data, message)
	}
	return json.Unmarshal(data, value, json.RejectUnknownMembers(true))
}
