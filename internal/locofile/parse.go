package locofile

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"sigs.k8s.io/yaml"
)

const (
	keyVersion      = "version"
	keyPartial      = "partial"
	keyServices     = "services"
	keyEnvironments = "environments"
	keyEnabled      = "enabled"
)

var (
	reservedTopLevelKeys = []string{"databases", "caches", "queues", "buckets"}
	reservedServiceKeys  = []string{"observability"}

	errUnknownKey = errors.New("unknown key")

	yamlLinePattern = regexp.MustCompile(`line (\d+):`)
)

// ParseError is a YAML error. Line is 1-based and 0 when the YAML library gave none.
type ParseError struct {
	Line int
	Err  error
}

func (e *ParseError) Error() string {
	return e.Err.Error()
}

func (e *ParseError) Unwrap() error {
	return e.Err
}

// FieldError is a validation error at one path in the file, such as services.web.port.
type FieldError struct {
	Path string
	Err  error
}

func (e *FieldError) Error() string {
	return e.Path + ": " + e.Err.Error()
}

func (e *FieldError) Unwrap() error {
	return e.Err
}

// Load reads and parses the file at path.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return Parse(data)
}

// Parse decodes a loco.yaml document, rejects unknown and reserved keys, and validates it.
func Parse(data []byte) (*File, error) {
	jsonData, err := yaml.YAMLToJSONStrict(data)
	if err != nil {
		return nil, newParseError(err)
	}

	var raw map[string]any
	if err := json.Unmarshal(jsonData, &raw); err != nil {
		return nil, &ParseError{Err: ErrNotAnObject}
	}
	if err := rejectReservedKeys(raw); err != nil {
		return nil, err
	}

	var file File
	if err := json.Unmarshal(jsonData, &file, json.RejectUnknownMembers(true)); err != nil {
		return nil, newDecodeError(err)
	}
	file.raw = raw

	if err := Validate(&file); err != nil {
		return nil, err
	}
	return &file, nil
}

func newParseError(err error) error {
	match := yamlLinePattern.FindStringSubmatch(err.Error())
	if match == nil {
		return &ParseError{Err: err}
	}
	line, convErr := strconv.Atoi(match[1])
	if convErr != nil {
		return &ParseError{Err: err}
	}
	return &ParseError{Line: line, Err: err}
}

func newDecodeError(err error) error {
	semanticErr, ok := errors.AsType[*json.SemanticError](err)
	if !ok || !errors.Is(err, json.ErrUnknownName) {
		return &ParseError{Err: err}
	}
	tokens := slices.Collect(semanticErr.JSONPointer.Tokens())
	return &ParseError{Err: fmt.Errorf("%w: %s", errUnknownKey, strings.Join(tokens, "."))}
}

func rejectReservedKeys(raw map[string]any) error {
	for _, key := range reservedTopLevelKeys {
		if _, found := raw[key]; found {
			return &FieldError{Path: key, Err: ErrNotSupportedYet}
		}
	}
	services, found := raw[keyServices].(map[string]any)
	if !found {
		return nil
	}
	for name, service := range services {
		fields, ok := service.(map[string]any)
		if !ok {
			continue
		}
		for _, key := range reservedServiceKeys {
			if _, found := fields[key]; found {
				return &FieldError{Path: servicePath(name, key), Err: ErrNotSupportedYet}
			}
		}
		environments, ok := fields[keyEnvironments].(map[string]any)
		if !ok {
			continue
		}
		for env, override := range environments {
			overrideFields, ok := override.(map[string]any)
			if !ok {
				continue
			}
			if _, found := overrideFields[keyEnvironments]; found {
				path := servicePath(name, keyEnvironments, env, keyEnvironments)
				return &FieldError{Path: path, Err: ErrEnvironmentsInOverride}
			}
		}
	}
	return nil
}

func servicePath(name string, keys ...string) string {
	parts := append([]string{keyServices, name}, keys...)
	return strings.Join(parts, ".")
}
