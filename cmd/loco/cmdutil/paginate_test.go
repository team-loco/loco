package cmdutil

import (
	"errors"
	"slices"
	"testing"
)

func TestPaginateCollectsEveryPage(t *testing.T) {
	pages := map[string]struct {
		items []string
		next  string
	}{
		"":   {[]string{"a", "b"}, "p2"},
		"p2": {[]string{"c"}, ""},
	}
	got, err := Paginate(func(token string) ([]string, string, error) {
		page := pages[token]
		return page.items, page.next, nil
	})
	if err != nil {
		t.Fatalf("Paginate: %v", err)
	}
	if want := []string{"a", "b", "c"}; !slices.Equal(got, want) {
		t.Fatalf("items = %v, want %v", got, want)
	}
}

func TestPaginateRejectsRepeatedToken(t *testing.T) {
	_, err := Paginate(func(string) ([]string, string, error) {
		return []string{"a"}, "same", nil
	})
	if !errors.Is(err, ErrRepeatedPageToken) {
		t.Fatalf("error = %v, want ErrRepeatedPageToken", err)
	}
}

func TestPaginateReturnsFetchError(t *testing.T) {
	fetchErr := errors.New("boom")
	_, err := Paginate(func(string) ([]string, string, error) {
		return nil, "", fetchErr
	})
	if !errors.Is(err, fetchErr) {
		t.Fatalf("error = %v, want %v", err, fetchErr)
	}
}
