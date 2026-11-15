package infra

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/ui"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/schemas"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"sigs.k8s.io/yaml"
)

func buildValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate [path]",
		Short: "Validate a loco.yaml file",
		Long: `Check a loco.yaml file without calling the API.

The file must parse, pass the CLI's structural checks and match the loco.yaml JSON Schema
this CLI embeds, including its CPU, memory and replica limits. The plan checks what needs
the API: regions, domains and the API's defaults.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			path := locofile.FileName
			if len(args) == 1 {
				path = args[0]
			}
			return validateCmdFunc(path)
		},
	}
}

func validateCmdFunc(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	file, err := locofile.Parse(data)
	if err != nil {
		return formatValidationError(path, data, err)
	}
	if err := validateAgainstSchema(data); err != nil {
		return fmt.Errorf("invalid %s:\n%s", path, indent(err.Error()))
	}

	style := lipgloss.NewStyle().Foreground(ui.Ok).Bold(true)
	fmt.Printf("%s %s is valid\n", style.Render("✓"), path)
	fmt.Printf("Partial: %s\n", file.Partial)
	fmt.Printf("Services: %s\n", strings.Join(serviceNames(file), ", "))
	return nil
}

// validateAgainstSchema reports each schema violation on its own line, prefixed with the
// field path.
func validateAgainstSchema(data []byte) error {
	jsonData, err := yaml.YAMLToJSON(data)
	if err != nil {
		return fmt.Errorf("convert to JSON: %w", err)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(jsonData))
	if err != nil {
		return fmt.Errorf("decode JSON: %w", err)
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemas.LocoV1))
	if err != nil {
		return fmt.Errorf("decode embedded schema: %w", err)
	}
	root, isObject := document.(map[string]any)
	id, hasID := root["$id"].(string)
	if !isObject || !hasID {
		return errors.New("embedded schema has no $id")
	}
	compiler := jsonschema.NewCompiler()
	if err = compiler.AddResource(id, document); err != nil {
		return fmt.Errorf("load embedded schema: %w", err)
	}
	schema, err := compiler.Compile(id)
	if err != nil {
		return fmt.Errorf("compile embedded schema: %w", err)
	}

	err = schema.Validate(instance)
	if err == nil {
		return nil
	}
	validationErr, ok := errors.AsType[*jsonschema.ValidationError](err)
	if !ok {
		return fmt.Errorf("validate against schema: %w", err)
	}
	printer := message.NewPrinter(language.English)
	var lines []string
	for _, leaf := range schemaLeaves(validationErr) {
		var line string
		if line, err = describeSchemaError(compiler, printer, leaf); err != nil {
			return err
		}
		lines = append(lines, line)
	}
	slices.Sort(lines)
	return errors.New(strings.Join(lines, "\n"))
}

// schemaLeaves returns the innermost errors, taking only the deepest branch of an anyOf.
func schemaLeaves(err *jsonschema.ValidationError) []*jsonschema.ValidationError {
	if len(err.Causes) == 0 {
		return []*jsonschema.ValidationError{err}
	}
	causes := err.Causes
	if _, ok := err.ErrorKind.(*kind.AnyOf); ok {
		causes = []*jsonschema.ValidationError{slices.MaxFunc(causes, func(a, b *jsonschema.ValidationError) int {
			return schemaDepth(a) - schemaDepth(b)
		})}
	}
	var leaves []*jsonschema.ValidationError
	for _, cause := range causes {
		leaves = append(leaves, schemaLeaves(cause)...)
	}
	return leaves
}

func schemaDepth(err *jsonschema.ValidationError) int {
	depth := len(err.InstanceLocation)
	for _, cause := range err.Causes {
		depth = max(depth, schemaDepth(cause))
	}
	return depth
}

// describeSchemaError reports a pattern mismatch with the field's description.
func describeSchemaError(
	compiler *jsonschema.Compiler,
	printer *message.Printer,
	leaf *jsonschema.ValidationError,
) (string, error) {
	text := leaf.ErrorKind.LocalizedString(printer)
	if pattern, ok := leaf.ErrorKind.(*kind.Pattern); ok {
		property, err := compiler.Compile(leaf.SchemaURL)
		if err != nil {
			return "", fmt.Errorf("compile %s: %w", leaf.SchemaURL, err)
		}
		if property.Description != "" {
			text = fmt.Sprintf("%q is not accepted. %s", pattern.Got, strings.TrimSuffix(property.Description, "."))
		}
	}
	location := strings.Join(leaf.InstanceLocation, ".")
	if location == "" {
		return text, nil
	}
	return location + ": " + text, nil
}

func formatValidationError(path string, data []byte, err error) error {
	parseErr, ok := errors.AsType[*locofile.ParseError](err)
	if !ok || parseErr.Line == 0 {
		return fmt.Errorf("invalid %s:\n%s", path, indent(err.Error()))
	}
	lines := strings.Split(string(data), "\n")
	if parseErr.Line > len(lines) {
		return fmt.Errorf("invalid %s at line %d: %w", path, parseErr.Line, err)
	}
	source := lines[parseErr.Line-1]
	return fmt.Errorf("invalid %s at line %d: %w\n  %d | %s", path, parseErr.Line, err, parseErr.Line, source)
}

func indent(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = "  " + line
	}
	return strings.Join(lines, "\n")
}

func serviceNames(file *locofile.File) []string {
	names := maps.Keys(file.Services)
	return slices.Sorted(names)
}
