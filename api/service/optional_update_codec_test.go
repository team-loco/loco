package service

import (
	"strings"
	"testing"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

func TestOptionalUpdateJSONPresenceAndValidation(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	for _, tc := range []struct {
		name, body     string
		message        proto.Message
		field          protoreflect.Name
		present, valid bool
	}{
		{
			name:    "description omitted",
			body:    `{"environmentId":"` + id + `"}`,
			message: &environmentv1.UpdateEnvironmentRequest{},
			field:   updateDescriptionField,
			present: false,
			valid:   true,
		},
		{
			name:    "description empty",
			body:    `{"environmentId":"` + id + `","description":""}`,
			message: &environmentv1.UpdateEnvironmentRequest{},
			field:   updateDescriptionField,
			present: true,
			valid:   true,
		},
		{
			name:    "description null",
			body:    `{"environmentId":"` + id + `","description":null}`,
			message: &environmentv1.UpdateEnvironmentRequest{},
			field:   updateDescriptionField,
			present: false,
			valid:   true,
		},
		{
			name:    "environment name empty",
			body:    `{"environmentId":"` + id + `","name":""}`,
			message: &environmentv1.UpdateEnvironmentRequest{},
			field:   updateNameField,
			present: true,
			valid:   false,
		},
		{
			name:    "environment type zero",
			body:    `{"environmentId":"` + id + `","type":0}`,
			message: &environmentv1.UpdateEnvironmentRequest{},
			field:   "type",
			present: true,
			valid:   false,
		},
		{
			name:    "resource name empty",
			body:    `{"resourceId":"` + id + `","name":""}`,
			message: &resourcev1.UpdateResourceRequest{},
			field:   updateNameField,
			present: true,
			valid:   false,
		},
		{
			name:    "workspace omitted",
			body:    `{"workspaceId":"` + id + `"}`,
			message: &workspacev1.UpdateWorkspaceRequest{},
			field:   updateNameField,
			present: false,
			valid:   true,
		},
		{
			name:    "workspace name empty",
			body:    `{"workspaceId":"` + id + `","name":""}`,
			message: &workspacev1.UpdateWorkspaceRequest{},
			field:   updateNameField,
			present: true,
			valid:   false,
		},
		{
			name:    "org omitted",
			body:    `{"orgId":"` + id + `"}`,
			message: &orgv1.UpdateOrgRequest{},
			field:   updateNameField,
			present: false,
			valid:   true,
		},
		{
			name:    "org name empty",
			body:    `{"orgId":"` + id + `","name":""}`,
			message: &orgv1.UpdateOrgRequest{},
			field:   updateNameField,
			present: true,
			valid:   false,
		},
		{
			name:    "avatar omitted",
			body:    `{"userId":"` + id + `"}`,
			message: &userv1.UpdateUserRequest{},
			field:   updateAvatarField,
			present: false,
			valid:   true,
		},
		{
			name:    "avatar null",
			body:    `{"userId":"` + id + `","avatarUrl":null}`,
			message: &userv1.UpdateUserRequest{},
			field:   updateAvatarField,
			present: false,
			valid:   true,
		},
		{
			name:    "avatar empty",
			body:    `{"userId":"` + id + `","avatarUrl":""}`,
			message: &userv1.UpdateUserRequest{},
			field:   updateAvatarField,
			present: true,
			valid:   true,
		},
		{
			name:    "avatar URI",
			body:    `{"userId":"` + id + `","avatarUrl":"https://example.invalid/avatar.png"}`,
			message: &userv1.UpdateUserRequest{},
			field:   updateAvatarField,
			present: true,
			valid:   true,
		},
		{
			name:    "avatar invalid",
			body:    `{"userId":"` + id + `","avatarUrl":"bad-uri","name":"rejected"}`,
			message: &userv1.UpdateUserRequest{},
			field:   updateAvatarField,
			present: true,
			valid:   false,
		},
		{
			name:    "profile name empty",
			body:    `{"userId":"` + id + `","name":""}`,
			message: &userv1.UpdateUserRequest{},
			field:   updateNameField,
			present: true,
			valid:   true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			codec := connectproto.NewJSONCodec()
			if err := codec.UnmarshalRead(t.Context(), strings.NewReader(tc.body), tc.message); err != nil {
				t.Fatal(err)
			}
			message := tc.message.ProtoReflect()
			field := message.Descriptor().Fields().ByName(tc.field)
			if got := message.Has(field); got != tc.present {
				t.Fatalf("presence=%v, want %v", got, tc.present)
			}
			if err := protovalidate.Validate(tc.message); (err == nil) != tc.valid {
				t.Fatalf("validation=%v, want valid=%v", err, tc.valid)
			}
		})
	}
}
