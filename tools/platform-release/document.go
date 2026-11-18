package main

import "fmt"

// fields accesses maps constructed by the bundle generator, whose shapes are invariant.
func fields(item object, keys ...string) object {
	for _, key := range keys {
		nested, ok := item[key].(map[string]any)
		if !ok {
			panic(fmt.Sprintf("invalid generated document field %s", key))
		}
		item = nested
	}
	return item
}

func documentName(item object) string {
	name, ok := fields(item, fieldMetadata)[fieldName].(string)
	if !ok {
		panic("generated document has no name")
	}
	return name
}

func documentKind(item object) string {
	kind, ok := item[fieldKind].(string)
	if !ok {
		panic("generated document has no kind")
	}
	return kind
}

const (
	fieldAPIVersion = "apiVersion"
	fieldKind       = "kind"
	fieldMetadata   = "metadata"
	fieldName       = "name"
)
