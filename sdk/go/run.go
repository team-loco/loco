package loco

import (
	"fmt"
	"io"
	"os"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	maxJSONBytes     = 5 << 20
	maxJSONReadBytes = maxJSONBytes + 1
	maxJSONDepth     = 100
)

func Run(define func(*Context) *Stack) {
	if err := evaluate(os.Stdin, os.Stdout, define); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func evaluate(input io.Reader, output io.Writer, define func(*Context) *Stack) error {
	if define == nil {
		return errDefinitionRequired
	}
	var ctx Context
	if err := decode(input, &ctx); err != nil {
		return fmt.Errorf("read evaluation context: %w", err)
	}
	if ctx.GetEnvironment() == "" || ctx.GetProjectRoot() == "" {
		return errContextRequired
	}
	stack := define(&ctx)
	if stack == nil {
		return errStackRequired
	}
	if stack.GetVersion() == 0 {
		stack.Version = ProtocolVersion
	}
	manifest := &Manifest{Version: ProtocolVersion, Stack: stack}
	data, err := protojson.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("encode infrastructure manifest: %w", err)
	}
	if _, err := output.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write infrastructure manifest: %w", err)
	}
	return nil
}

func decode(input io.Reader, message proto.Message) error {
	data, err := io.ReadAll(io.LimitReader(input, maxJSONReadBytes))
	if err != nil {
		return err
	}
	if len(data) > maxJSONBytes {
		return errJSONTooLarge
	}
	return (protojson.UnmarshalOptions{RecursionLimit: maxJSONDepth}).Unmarshal(data, message)
}
