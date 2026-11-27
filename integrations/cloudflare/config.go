package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/google/uuid"
)

const defaultPollSeconds = 5
const defaultAuditSeconds = 86400
const defaultBatchSize = 3
const defaultRequestSeconds = 10

var zoneIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var hostnamePattern = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

type config struct {
	APIURL         string    `json:"apiURL"`
	PollSeconds    int       `json:"pollSeconds"`
	AuditSeconds   int       `json:"auditSeconds"`
	BatchSize      int       `json:"batchSize"`
	RequestSeconds int       `json:"requestSeconds"`
	Bindings       []binding `json:"bindings"`
}
type binding struct {
	AppDomain           string        `json:"appDomain"`
	PublisherID         string        `json:"publisherID"`
	ZoneID              string        `json:"zoneID"`
	LocoTokenFile       string        `json:"locoTokenFile"`
	CloudflareTokenFile string        `json:"cloudflareTokenFile"`
	Destinations        []destination `json:"destinations"`
}
type destination struct {
	ClusterIDs []string `json:"clusterIDs"`
	Hostname   string   `json:"hostname"`
}

func readConfig(path string) (config, error) {
	c := config{
		PollSeconds:    defaultPollSeconds,
		AuditSeconds:   defaultAuditSeconds,
		BatchSize:      defaultBatchSize,
		RequestSeconds: defaultRequestSeconds,
	}
	f, err := os.Open(path)
	if err != nil {
		return c, fmt.Errorf("open publisher configuration: %w", err)
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err = dec.Decode(&c); err != nil {
		return c, fmt.Errorf("decode publisher configuration: %w", err)
	}
	var extra any
	if err = dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return c, errors.New("publisher configuration must contain one JSON object")
	}
	return c, c.validate()
}
func (c config) validate() error {
	endpoint, err := url.Parse(c.APIURL)
	if err != nil {
		return errors.New("apiURL must be a valid HTTPS URL")
	}
	validHTTPS := endpoint.Scheme == "https" && endpoint.Host != ""
	if !validHTTPS {
		return errors.New("apiURL must be an HTTPS URL with a host")
	}
	hasURLCredentials := endpoint.User != nil
	hasURLParameters := endpoint.RawQuery != "" || endpoint.Fragment != ""
	if hasURLCredentials || hasURLParameters {
		return errors.New("apiURL must not contain credentials, query or fragment")
	}
	if c.PollSeconds <= 0 || c.AuditSeconds < c.PollSeconds {
		return errors.New("poll must be positive and audit must be at least one poll")
	}
	if c.BatchSize <= 0 || c.RequestSeconds <= 0 {
		return errors.New("batch and request limits must be positive")
	}
	if len(c.Bindings) == 0 {
		return errors.New("at least one binding is required")
	}
	publishers := map[string]bool{}
	domains := map[string]bool{}
	for _, b := range c.Bindings {
		if !hostnamePattern.MatchString(b.AppDomain) || len(b.AppDomain) > 253 {
			return errors.New("binding appDomain must be a lowercase hostname")
		}
		if _, err = uuid.Parse(b.PublisherID); err != nil {
			return errors.New("binding publisherID must be a UUID")
		}
		if publishers[b.PublisherID] || domains[b.AppDomain] {
			return errors.New("bindings must have distinct publishers and app domains")
		}
		publishers[b.PublisherID] = true
		domains[b.AppDomain] = true
		if !zoneIDPattern.MatchString(b.ZoneID) {
			return errors.New("binding zoneID must be a 32-character lowercase hexadecimal ID")
		}
		if b.LocoTokenFile == "" || b.CloudflareTokenFile == "" {
			return errors.New("binding token file paths are required")
		}
		sets := map[string]bool{}
		if len(b.Destinations) == 0 {
			return errors.New("binding destinations are required")
		}
		for _, d := range b.Destinations {
			key, keyErr := clusterSet(d.ClusterIDs)
			if keyErr != nil {
				return keyErr
			}
			if sets[key] {
				return errors.New("duplicate destination cluster set")
			}
			sets[key] = true
			if !hostnamePattern.MatchString(d.Hostname) || len(d.Hostname) > 253 {
				return errors.New("destination must be a lowercase hostname")
			}
			if d.Hostname == b.AppDomain {
				return errors.New("destination must not equal the app domain")
			}
		}
	}
	return nil
}
func clusterSet(ids []string) (string, error) {
	if len(ids) == 0 {
		return "", errors.New("destination cluster set must not be empty")
	}
	canonical := make([]string, len(ids))
	for i, id := range ids {
		parsed, err := uuid.Parse(id)
		if err != nil {
			return "", errors.New("cluster ID must be a UUID")
		}
		canonical[i] = parsed.String()
	}
	slices.Sort(canonical)
	if len(slices.Compact(slices.Clone(canonical))) != len(canonical) {
		return "", errors.New("destination cluster set contains a duplicate")
	}
	return strings.Join(canonical, ","), nil
}
func (b binding) target(ids []string) (string, error) {
	key, err := clusterSet(ids)
	if err != nil {
		return "", err
	}
	for _, d := range b.Destinations {
		configured, configErr := clusterSet(d.ClusterIDs)
		if configErr != nil {
			return "", configErr
		}
		if configured == key {
			return d.Hostname, nil
		}
	}
	return "", errors.New("no shared destination is configured for this exact cluster set")
}
func readToken(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read mounted token: %w", err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", errors.New("mounted token is empty")
	}
	return token, nil
}
