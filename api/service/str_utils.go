package service

// derefString safely dereferences a string pointer, returning an empty string if nil.
func derefString(s *string) string {
	if s != nil {
		return *s
	}
	return ""
}
