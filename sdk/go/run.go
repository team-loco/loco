package loco

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

func Run(define func(Context) Stack) {
	if err := Evaluate(os.Stdin, os.Stdout, define); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func Evaluate(input io.Reader, output io.Writer, define func(Context) Stack) error {
	if define == nil {
		return errors.New("infrastructure definition is required")
	}
	var ctx Context
	if err := Decode(input, &ctx); err != nil {
		return fmt.Errorf("read evaluation context: %w", err)
	}
	if ctx.Environment == "" || ctx.ProjectRoot == "" {
		return errors.New("evaluation context requires environment and projectRoot")
	}
	manifest := Manifest{Version: ProtocolVersion, Stack: define(ctx)}
	if err := Normalize(&manifest); err != nil {
		return fmt.Errorf("invalid infrastructure definition: %w", err)
	}
	if err := json.NewEncoder(output).Encode(manifest); err != nil {
		return fmt.Errorf("write infrastructure manifest: %w", err)
	}
	return nil
}

func Decode(input io.Reader, value any) error {
	data, err := io.ReadAll(io.LimitReader(input, (5<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 5<<20 {
		return errors.New("JSON document exceeds 5 MiB")
	}
	if uniqueJSONErr := uniqueJSON(json.NewDecoder(bytes.NewReader(data)), 0); uniqueJSONErr != nil {
		return uniqueJSONErr
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decodeErr := decoder.Decode(value); decodeErr != nil {
		return decodeErr
	}
	var trailing any
	if decodeErr2 := decoder.Decode(&trailing); !errors.Is(decodeErr2, io.EOF) {
		return errors.New("expected exactly one JSON document")
	}
	return nil
}

func uniqueJSON(decoder *json.Decoder, depth int) error {
	if depth > 100 {
		return errors.New("JSON document exceeds maximum nesting")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]bool)
		for decoder.More() {
			token, tokenErr := decoder.Token()
			if tokenErr != nil {
				return tokenErr
			}
			key, ok := token.(string)
			if !ok || seen[key] {
				return errors.New("JSON document contains an invalid or duplicate key")
			}
			seen[key] = true
			if uniqueJSONErr2 := uniqueJSON(decoder, depth+1); uniqueJSONErr2 != nil {
				return uniqueJSONErr2
			}
		}
	case '[':
		for decoder.More() {
			if uniqueJSONErr3 := uniqueJSON(decoder, depth+1); uniqueJSONErr3 != nil {
				return uniqueJSONErr3
			}
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	_, err = decoder.Token()
	return err
}
