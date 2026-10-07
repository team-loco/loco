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

	loco "github.com/team-loco/loco/sdk/go"
)

func WriteAuthoring(projectRoot string, manifest *loco.Manifest, force bool) error {
	if err := loco.Normalize(manifest); err != nil {
		return err
	}
	path := filepath.Join(projectRoot, ".loco", "main.go")
	if _, err := os.Stat(path); err == nil && !force {
		return errors.New("go definition already exists; use --force to overwrite")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	literal, err := authorValue(reflect.ValueOf(manifest.Stack))
	if err != nil {
		return err
	}
	code := "package main\n\nimport loco \"github.com/team-loco/loco/sdk/go\"\n\n" +
		"func main() { loco.Run(func(_ loco.Context) loco.Stack { return " + literal + " }) }\n"
	data, err := format.Source([]byte(code))
	if err != nil {
		return fmt.Errorf("format Go definition: %w", err)
	}
	if mkdirAllErr := os.MkdirAll(filepath.Dir(path), 0o755); mkdirAllErr != nil {
		return mkdirAllErr
	}
	module := filepath.Join(filepath.Dir(path), "go.mod")
	if _, statErr := os.Stat(module); errors.Is(statErr, os.ErrNotExist) {
		contents := "module " + manifest.Stack.Name + "-infra\n\ngo 1.24.0\n\n" +
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
	if variable, ok := value.Interface().(loco.Variable); ok {
		switch variable.Kind {
		case loco.VariableLiteral:
			return "loco.Literal(" + strconv.Quote(variable.Value) + ")", nil
		case loco.VariableSecret:
			return "loco.SecretRef(" + strconv.Quote(variable.Name) + ")", nil
		case loco.VariablePreserve:
			return "loco.Preserve()", nil
		}
	}
	switch value.Kind() {
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
		return "loco.Value[" + value.Elem().Type().String() + "](" + inner + ")", nil
	case reflect.Struct:
		fields := make([]string, 0, value.NumField())
		for i := 0; i < value.NumField(); i++ {
			if value.Field(i).IsZero() {
				continue
			}
			field, err := authorValue(value.Field(i))
			if err != nil {
				return "", err
			}
			fields = append(fields, value.Type().Field(i).Name+": "+field)
		}
		if len(fields) == 0 {
			return value.Type().String() + "{}", nil
		}
		return value.Type().String() + "{\n" + strings.Join(fields, ",\n") + ",\n}", nil
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
			return value.Type().String() + "{}", nil
		}
		return value.Type().String() + "{\n" + strings.Join(items, ",\n") + ",\n}", nil
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
			return value.Type().String() + "{}", nil
		}
		return value.Type().String() + "{\n" + strings.Join(items, ",\n") + ",\n}", nil
	case reflect.String:
		return strconv.Quote(value.String()), nil
	case reflect.Bool:
		return strconv.FormatBool(value.Bool()), nil
	case reflect.Int:
		return strconv.FormatInt(value.Int(), 10), nil
	case reflect.Int32:
		return strconv.FormatInt(value.Int(), 10), nil
	case reflect.Float64:
		return strconv.FormatFloat(value.Float(), 'g', -1, 64), nil
	default:
		return "", fmt.Errorf("unsupported authoring type %s", value.Type())
	}
}
