package service

import (
	"errors"
	"slices"

	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

const (
	updateNameField        = "name"
	updateDescriptionField = "description"
	updateTypeField        = "type"
)

var errUpdateMaskField = errors.New("update mask contains an unsupported field")

func updateMaskPaths(mask *fieldmaskpb.FieldMask, allowed, provided []string) ([]string, error) {
	paths := mask.GetPaths()
	if len(paths) == 0 {
		return provided, nil
	}
	if len(paths) == 1 && paths[0] == "*" {
		return allowed, nil
	}
	for _, path := range paths {
		if !slices.Contains(allowed, path) {
			return nil, errUpdateMaskField
		}
	}
	return paths, nil
}
