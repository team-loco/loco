package converter

import (
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"

	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

func DeserializeStoredServiceSpec(data []byte) (*resourcev1.ServiceSpec, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("decode stored service fields: %w", err)
	}
	if raw, ok := fields["observability"]; ok {
		var observability map[string]json.RawMessage
		if err := json.Unmarshal(raw, &observability); err != nil {
			return nil, fmt.Errorf("decode stored observability: %w", err)
		}
		if _, legacy := observability["tracing"]; legacy {
			delete(observability, "tracing")
			encoded, err := json.Marshal(observability)
			if err != nil {
				return nil, fmt.Errorf("encode stored observability: %w", err)
			}
			fields["observability"] = encoded
			data, err = json.Marshal(fields)
			if err != nil {
				return nil, fmt.Errorf("encode stored service fields: %w", err)
			}
		}
	}
	var spec resourcev1.ServiceSpec
	if err := protojson.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("decode stored service spec: %w", err)
	}
	return &spec, nil
}
