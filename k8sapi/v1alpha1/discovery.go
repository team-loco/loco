package v1alpha1

import (
	"regexp"

	"k8s.io/apimachinery/pkg/util/validation"
)

var internalServiceNamePattern = regexp.MustCompile(
	`^resource-[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`,
)

func PrivateServiceName(name string) string {
	if len(validation.IsDNS1123Label(name)) != 0 || name == "localhost" ||
		internalServiceNamePattern.MatchString(name) {
		return ""
	}
	return name
}
