package infra

import (
	"errors"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	infrav1 "github.com/team-loco/loco/gen/go/loco/infra/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	loco "github.com/team-loco/loco/sdk/go"
)

func WriteAuthoring(projectRoot string, manifest *loco.Manifest, force bool) error {
	if err := ValidateManifest(manifest); err != nil {
		return err
	}
	path := filepath.Join(projectRoot, ".loco", "main.go")
	if _, err := os.Stat(path); err == nil && !force {
		return errors.New("go definition already exists; use --force to overwrite")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	literal, err := authorValue(reflect.ValueOf(manifest.GetStack()))
	if err != nil {
		return err
	}
	code := "package main\n\nimport loco \"github.com/team-loco/loco/sdk/go\"\n\n" +
		"func main() { loco.Run(func(_ *loco.Context) *loco.Stack { return " + literal + " }) }\n"
	data, err := format.Source([]byte(code))
	if err != nil {
		return fmt.Errorf("format Go definition: %w", err)
	}
	if mkdirAllErr := os.MkdirAll(filepath.Dir(path), 0o755); mkdirAllErr != nil {
		return mkdirAllErr
	}
	module := filepath.Join(filepath.Dir(path), "go.mod")
	if _, statErr := os.Stat(module); errors.Is(statErr, os.ErrNotExist) {
		contents := "module " + manifest.Stack.Name + "-infra\n\ngo 1.27.0\n\n" +
			"require github.com/team-loco/loco/sdk/go " + SDKVersion + "\n"
		if writeFileErr := os.WriteFile(module, []byte(contents), 0o644); writeFileErr != nil {
			return writeFileErr
		}
	} else if statErr != nil {
		return statErr
	}
	return os.WriteFile(path, data, 0o644)
}

func authorValue(value reflect.Value) (string, error) {
	if code, handled, err := authorHelper(value); handled {
		return code, err
	}

	switch value.Kind() {
	case reflect.Interface:
		return authorValue(value.Elem())
	case reflect.Pointer:
		if value.IsNil() {
			return "nil", nil
		}
		inner, err := authorValue(value.Elem())
		if err != nil {
			return "", err
		}
		if value.Elem().Kind() == reflect.Struct {
			return "&" + inner, nil
		}
		return "loco.Value[" + authorType(value.Elem().Type()) + "](" + inner + ")", nil
	case reflect.Struct:
		fields := make([]string, 0, value.NumField())
		for i := 0; i < value.NumField(); i++ {
			if value.Type().Field(i).PkgPath != "" || value.Field(i).IsZero() {
				continue
			}
			field, err := authorValue(value.Field(i))
			if err != nil {
				return "", err
			}
			fields = append(fields, value.Type().Field(i).Name+": "+field)
		}
		if len(fields) == 0 {
			return authorType(value.Type()) + "{}", nil
		}
		return authorType(value.Type()) + "{\n" + strings.Join(fields, ",\n") + ",\n}", nil
	case reflect.Slice:
		items := make([]string, 0, value.Len())
		for i := 0; i < value.Len(); i++ {
			item, err := authorValue(value.Index(i))
			if err != nil {
				return "", err
			}
			items = append(items, item)
		}
		if len(items) == 0 {
			return authorType(value.Type()) + "{}", nil
		}
		return authorType(value.Type()) + "{\n" + strings.Join(items, ",\n") + ",\n}", nil
	case reflect.Map:
		keys := value.MapKeys()
		slices.SortFunc(keys, func(a, b reflect.Value) int { return strings.Compare(a.String(), b.String()) })
		items := make([]string, 0, len(keys))
		for _, key := range keys {
			item, err := authorValue(value.MapIndex(key))
			if err != nil {
				return "", err
			}
			items = append(items, strconv.Quote(key.String())+": "+item)
		}
		if len(items) == 0 {
			return authorType(value.Type()) + "{}", nil
		}
		return authorType(value.Type()) + "{\n" + strings.Join(items, ",\n") + ",\n}", nil
	case reflect.String:
		return strconv.Quote(value.String()), nil
	case reflect.Bool:
		return strconv.FormatBool(value.Bool()), nil
	case reflect.Int:
		return strconv.FormatInt(value.Int(), 10), nil
	case reflect.Uint32:
		return strconv.FormatUint(value.Uint(), 10), nil
	case reflect.Int32:
		return strconv.FormatInt(value.Int(), 10), nil
	case reflect.Float64:
		return strconv.FormatFloat(value.Float(), 'g', -1, 64), nil
	default:
		return "", fmt.Errorf("unsupported authoring type %s", value.Type())
	}
}

func authorType(value reflect.Type) string {
	aliases := map[reflect.Type]string{
		reflect.TypeFor[loco.Stack]():         "loco.Stack",
		reflect.TypeFor[loco.Service]():       "loco.Service",
		reflect.TypeFor[loco.ServiceSpec]():   "loco.ServiceSpec",
		reflect.TypeFor[loco.DockerBuild]():   "loco.DockerBuild",
		reflect.TypeFor[loco.Routing]():       "loco.Routing",
		reflect.TypeFor[loco.Region]():        "loco.Region",
		reflect.TypeFor[loco.Autoscaling]():   "loco.Autoscaling",
		reflect.TypeFor[loco.Health]():        "loco.Health",
		reflect.TypeFor[loco.Observability](): "loco.Observability",
		reflect.TypeFor[loco.Logging]():       "loco.Logging",
		reflect.TypeFor[loco.Metrics]():       "loco.Metrics",
		reflect.TypeFor[loco.Tracing]():       "loco.Tracing",
		reflect.TypeFor[loco.Variable]():      "loco.Variable",
	}
	if alias, ok := aliases[value]; ok {
		return alias
	}
	switch value.Kind() {
	case reflect.Pointer:
		return "*" + authorType(value.Elem())
	case reflect.Slice:
		return "[]" + authorType(value.Elem())
	case reflect.Map:
		return "map[" + authorType(value.Key()) + "]" + authorType(value.Elem())
	default:
		return value.String()
	}
}

func authorHelper(value reflect.Value) (string, bool, error) {
	if value.Kind() == reflect.Pointer && value.IsNil() {
		return "nil", true, nil
	}
	if variable, ok := value.Interface().(*loco.Variable); ok {
		switch variable.GetExpression().(type) {
		case *infrav1.Variable_Literal:
			return "loco.Literal(" + strconv.Quote(variable.GetLiteral()) + ")", true, nil
		case *infrav1.Variable_Secret:
			return "loco.SecretRef(" + strconv.Quote(variable.GetSecret()) + ")", true, nil
		case *infrav1.Variable_Preserve:
			return "loco.Preserve()", true, nil
		}
	}
	if source, ok := value.Interface().(*infrav1.ServiceManifest_Docker); ok {
		return "loco.Docker(" + strconv.Quote(
			source.Docker.GetContext(),
		) + ", " + strconv.Quote(
			source.Docker.GetDockerfile(),
		) + ")", true, nil
	}
	if source, ok := value.Interface().(*infrav1.ServiceManifest_Image); ok {
		return "loco.Image(" + strconv.Quote(source.Image) + ")", true, nil
	}
	if spec, ok := value.Interface().(*resourcev1.ResourceSpec); ok {
		inner, err := authorValue(reflect.ValueOf(spec.GetService()))
		return "loco.Config(" + inner + ")", true, err
	}

	return "", false, nil
}
