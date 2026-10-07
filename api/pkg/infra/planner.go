package infra

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"regexp"
	"slices"

	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	infrav1 "github.com/team-loco/loco/gen/go/loco/infra/v1"
	"google.golang.org/protobuf/proto"
)

var imageDigestPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._:/-]*@sha256:[a-f0-9]{64}$`)

type CurrentService struct {
	Manifest  *infrav1.ServiceManifest
	Runtime   map[string]*deploymentv1.ServiceDeploymentSpec
	Variables map[string]string
}

func Digest(message proto.Message) (string, error) {
	data, err := (proto.MarshalOptions{Deterministic: true}).Marshal(message)
	if err != nil {
		return "", fmt.Errorf("encode infrastructure: %w", err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func ValidateImage(image string) error {
	if !imageDigestPattern.MatchString(image) {
		return fmt.Errorf("deployment image must be pinned to a sha256 digest")
	}
	return nil
}

func Operations(
	desired *infrav1.StackManifest,
	current map[string]CurrentService,
	variables map[string]map[string]string,
	selector string,
) ([]*infrav1.Operation, error) {
	var operations []*infrav1.Operation
	seen := make(map[string]bool, len(desired.GetServices()))
	for _, service := range desired.GetServices() {
		if selector != "" && selector != service.GetKey() {
			continue
		}
		seen[service.GetKey()] = true
		previous, exists := current[service.GetKey()]
		operation := &infrav1.Operation{ServiceKey: service.GetKey(), ImageDigest: service.GetResolvedImage()}
		if !exists {
			operation.Type = infrav1.OperationType_OPERATION_TYPE_CREATE
			operation.ChangedFields = []string{"resource"}
		} else {
			operation.Type = infrav1.OperationType_OPERATION_TYPE_UPDATE
			compareService(operation, service, previous, variables[service.GetKey()])
		}
		for region := range service.GetSpec().GetService().GetRegions() {
			live := previous.Runtime[region]
			image := service.GetResolvedImage()
			if image == "" && live != nil {
				image = live.GetBuild().GetImage()
			}
			if image == "" {
				continue
			}
			if err := ValidateImage(image); err != nil {
				return nil, err
			}
			deployment, err := RuntimeSpec(service, region, image, variables[service.GetKey()])
			if err != nil {
				return nil, err
			}
			if live == nil || !proto.Equal(deployment, live) {
				operation.Deploy = true
			}
		}
		if operation.Deploy {
			operation.ChangedFields = append(operation.ChangedFields, "deployment")
		}
		if len(operation.ChangedFields) > 0 {
			operations = append(operations, operation)
		}
	}
	if selector != "" {
		if !seen[selector] {
			return nil, fmt.Errorf("service key %q is not declared", selector)
		}
	} else {
		for key := range current {
			if !seen[key] {
				operations = append(operations, &infrav1.Operation{
					Type: infrav1.OperationType_OPERATION_TYPE_DELETE, ServiceKey: key,
					Destructive: true, ChangedFields: []string{"resource"},
				})
			}
		}
	}
	slices.SortFunc(operations, func(a, b *infrav1.Operation) int {
		return compareKeys(a.GetServiceKey(), b.GetServiceKey())
	})
	return operations, nil
}

func compareKeys(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func compareService(
	operation *infrav1.Operation,
	desired *infrav1.ServiceManifest,
	previous CurrentService,
	variables map[string]string,
) {
	live := previous.Manifest
	if desired.GetName() != live.GetName() || desired.GetDescription() != live.GetDescription() {
		operation.ChangedFields = append(operation.ChangedFields, "metadata")
	}
	if !proto.Equal(desired.GetSpec(), live.GetSpec()) {
		operation.ChangedFields = append(operation.ChangedFields, "spec")
		operation.Deploy = len(previous.Runtime) > 0
	}
	if desired.GetHostname() != live.GetHostname() {
		operation.ChangedFields = append(operation.ChangedFields, "domain")
		operation.Deploy = len(previous.Runtime) > 0
		operation.Destructive = live.GetHostname() != ""
	}
	if !proto.Equal(desired.GetDocker(), live.GetDocker()) || desired.GetImage() != live.GetImage() {
		operation.ChangedFields = append(operation.ChangedFields, "source")
	}
	if !maps.Equal(variables, previous.Variables) || !maps.Equal(variableRecipes(desired), variableRecipes(live)) {
		operation.ChangedFields = append(operation.ChangedFields, "variables")
	}
	for key := range previous.Variables {
		if _, exists := variables[key]; !exists {
			operation.Destructive = true
		}
	}
	for region := range live.GetSpec().GetService().GetRegions() {
		if _, exists := desired.GetSpec().GetService().GetRegions()[region]; !exists {
			operation.Destructive = true
		}
	}
}

func RuntimeSpec(
	service *infrav1.ServiceManifest,
	region string,
	image string,
	variables map[string]string,
) (*deploymentv1.ServiceDeploymentSpec, error) {
	spec := service.GetSpec().GetService()
	resources, ok := spec.GetRegions()[region]
	if !ok {
		return nil, fmt.Errorf("region %q is not declared", region)
	}
	return &deploymentv1.ServiceDeploymentSpec{
		Build:       &deploymentv1.BuildSource{Type: "image", Image: image},
		HealthCheck: spec.GetHealthCheck(), Port: spec.GetRouting().GetPort(),
		Cpu: proto.String(resources.GetCpu()), Memory: proto.String(resources.GetMemory()),
		MinReplicas: proto.Int32(resources.GetMinReplicas()), MaxReplicas: proto.Int32(resources.GetMaxReplicas()),
		Scalers: resources.GetScalers(), Env: variables,
	}, nil
}

func variableRecipes(service *infrav1.ServiceManifest) map[string]string {
	recipes := make(map[string]string, len(service.GetVariables()))
	for name, variable := range service.GetVariables() {
		switch expression := variable.GetExpression().(type) {
		case *infrav1.Variable_Secret:
			recipes[name] = "secret:" + expression.Secret
		case *infrav1.Variable_Literal:
			recipes[name] = "value"
		case *infrav1.Variable_Preserve:
			recipes[name] = "value"
		}
	}
	return recipes
}
