package main

import (
	"fmt"
	"strconv"
	"testing"

	"buf.build/go/protovalidate"
	"github.com/team-loco/loco/api/limits"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
	"google.golang.org/protobuf/proto"
)

func TestSecretRequestCountLimits(t *testing.T) {
	maximum := int(limits.SecretsPerResource.Value(limits.Defaults()))
	for _, count := range []int{0, 1, maximum, maximum + 1} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			names := make([]string, count)
			values := make(map[string]string, count)
			for i := range names {
				names[i] = fmt.Sprintf("SECRET_%d", i)
				values[names[i]] = "value"
			}
			const id = "12345678-1234-4234-8234-123456789012"
			for _, request := range []proto.Message{
				&secretv1.DeleteSecretsRequest{ResourceId: id, EnvironmentId: id, Names: names},
				&secretv1.SetSecretsRequest{ResourceId: id, EnvironmentId: id, Values: values},
			} {
				err := protovalidate.Validate(request)
				wantValid := count > 0 && count <= maximum
				if (err == nil) != wantValid {
					t.Errorf("%T with %d entries: error = %v, want valid = %v", request, count, err, wantValid)
				}
			}
		})
	}
}
