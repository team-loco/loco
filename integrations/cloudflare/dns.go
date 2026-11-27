package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const cloudflareAPI = "https://api.cloudflare.com/client/v4"
const dnsPageSize = 100
const maxResponseBytes = 2 << 20

type dnsClient struct {
	client                 *http.Client
	baseURL, token, zoneID string
}
type record struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	TTL     int    `json:"ttl"`
	Comment string `json:"comment"`
}
type providerError struct {
	status  int
	retryAt time.Time
	cause   error
	message string
}

func (e *providerError) Unwrap() error { return e.cause }
func (e *providerError) Error() string {
	if e.message != "" {
		return e.message
	}
	if e.status == http.StatusTooManyRequests {
		return "routing provider rate limit reached"
	}
	return "routing provider request failed"
}
func (d *dnsClient) request(ctx context.Context, method, path string, body any, result any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode DNS request: %w", err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, d.baseURL+"/zones/"+d.zoneID+"/dns_records"+path, reader)
	if err != nil {
		return errors.New("invalid routing provider request")
	}
	req.Header.Set("Authorization", "Bearer "+d.token)
	req.Header.Set("Content-Type", "application/json")
	response, err := d.client.Do(req)
	if err != nil {
		return &providerError{message: "routing provider temporarily unavailable", cause: err}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		retryAt := time.Time{}
		if seconds, parseErr := strconv.Atoi(response.Header.Get("Retry-After")); parseErr == nil && seconds > 0 {
			retryAt = time.Now().Add(time.Duration(seconds) * time.Second)
		} else if at, parseErr := http.ParseTime(response.Header.Get("Retry-After")); parseErr == nil {
			retryAt = at
		}
		return &providerError{status: response.StatusCode, retryAt: retryAt}
	}
	envelope := struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
	}{}
	if err = json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes)).Decode(&envelope); err != nil {
		return &providerError{message: "routing provider returned an invalid response", cause: err}
	}
	if !envelope.Success {
		return errors.New("routing provider rejected the request")
	}
	if result != nil {
		if err = json.Unmarshal(envelope.Result, result); err != nil {
			return &providerError{message: "routing provider returned invalid DNS data", cause: err}
		}
	}
	return nil
}
func (d *dnsClient) find(ctx context.Context, hostname string) ([]record, error) {
	// An exact-name lookup includes every record type so unowned A/AAAA records conflict too.
	var records []record
	for page := 1; ; page++ {
		query := url.Values{
			"name.exact": {hostname},
			"page":       {strconv.Itoa(page)},
			"per_page":   {strconv.Itoa(dnsPageSize)},
		}
		var batch []record
		if err := d.request(ctx, http.MethodGet, "?"+query.Encode(), nil, &batch); err != nil {
			return nil, err
		}
		records = append(records, batch...)
		if len(batch) < dnsPageSize {
			return records, nil
		}
	}
}
func (d *dnsClient) reconcile(ctx context.Context, hostname, target, publicationID string, withdrawing bool) error {
	owner := "loco-managed:" + publicationID
	records, err := d.find(ctx, hostname)
	if err != nil {
		return err
	}
	for _, r := range records {
		if r.Comment != owner {
			return errors.New("hostname has a DNS record owned by another writer")
		}
	}
	if len(records) > 1 {
		return errors.New("hostname has multiple owned DNS records; operator repair required")
	}
	if withdrawing {
		if len(records) == 0 {
			return nil
		}
		if err = d.request(ctx, http.MethodDelete, "/"+url.PathEscape(records[0].ID), nil, nil); err != nil {
			return err
		}
		remaining, readErr := d.find(ctx, hostname)
		if readErr != nil {
			return readErr
		}
		if len(remaining) != 0 {
			return errors.New("routing provider has not confirmed DNS withdrawal")
		}
		return nil
	}
	desired := record{Type: "CNAME", Name: hostname, Content: target, Proxied: true, TTL: 1, Comment: owner}
	matches := func(r record) bool {
		return r.Type == desired.Type && r.Name == desired.Name &&
			strings.TrimSuffix(r.Content, ".") == desired.Content &&
			r.Proxied &&
			r.TTL == 1 &&
			r.Comment == owner
	}
	if len(records) == 1 && matches(records[0]) {
		return nil
	}
	method, path := http.MethodPost, ""
	if len(records) == 1 {
		method = http.MethodPatch
		path = "/" + url.PathEscape(records[0].ID)
	}
	if err = d.request(ctx, method, path, desired, nil); err != nil {
		return err
	}
	actual, err := d.find(ctx, hostname)
	if err != nil {
		return err
	}
	if len(actual) != 1 || !matches(actual[0]) {
		return errors.New("routing provider has not confirmed the desired DNS record")
	}
	return nil
}
