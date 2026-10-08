package webhooks_test

import (
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/team-loco/loco/api/webhooks"
)

const shortKeyBytes = 16

func installConfig(entries ...string) string {
	joined := strings.Join(entries, ",")
	return "[" + joined + "]"
}

func TestParseInstallWebhooks(t *testing.T) {
	secret, err := webhooks.NewSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}
	shortKey := make([]byte, shortKeyBytes)
	shortSecret := "whsec_" + base64.StdEncoding.EncodeToString(shortKey)
	entry := func(url, secret string) string {
		return fmt.Sprintf(`{"url":%q,"secret":%q}`, url, secret)
	}

	for _, raw := range []string{"", "  "} {
		hooks, parseErr := webhooks.ParseInstallWebhooks(raw)
		if parseErr != nil || len(hooks) != 0 {
			t.Fatalf("empty %q = %+v, %v", raw, hooks, parseErr)
		}
	}

	valid := installConfig(
		fmt.Sprintf(`{"url":"https://hooks.example.test/loco","secret":%q,"eventTypes":["user.created"]}`, secret),
		entry("http://receiver.internal:8080/events", secret),
	)
	hooks, err := webhooks.ParseInstallWebhooks(valid)
	if err != nil {
		t.Fatalf("valid: %v", err)
	}
	if len(hooks) != 2 || hooks[0].URL != "https://hooks.example.test/loco" || hooks[0].Secret != secret ||
		!slices.Equal(hooks[0].EventTypes, []string{"user.created"}) {
		t.Fatalf("hooks = %+v", hooks)
	}
	if hooks[1].EventTypes == nil || len(hooks[1].EventTypes) != 0 {
		t.Fatalf("omitted eventTypes = %#v, want empty", hooks[1].EventTypes)
	}

	for name, tc := range map[string]struct {
		raw  string
		want error
	}{
		"unknown key": {
			raw: installConfig(fmt.Sprintf(`{"url":"https://a.test","secret":%q,"events":["x"]}`, secret)),
		},
		"not an array":   {raw: `{"url":"https://a.test"}`},
		"ftp url":        {raw: installConfig(entry("ftp://a.test/x", secret)), want: webhooks.ErrInstallWebhookURL},
		"no host":        {raw: installConfig(entry("https:///path", secret)), want: webhooks.ErrInstallWebhookURL},
		"relative url":   {raw: installConfig(entry("/hooks", secret)), want: webhooks.ErrInstallWebhookURL},
		"userinfo":       {raw: installConfig(entry("https://user:pw@a.test", secret)), want: webhooks.ErrInstallWebhookURL},
		"missing secret": {raw: installConfig(`{"url":"https://a.test"}`), want: webhooks.ErrInvalidSecret},
		"no prefix": {
			raw:  installConfig(entry("https://a.test", secret[len("whsec_"):])),
			want: webhooks.ErrInvalidSecret,
		},
		"not base64":   {raw: installConfig(entry("https://a.test", "whsec_not base64")), want: webhooks.ErrInvalidSecret},
		"short secret": {raw: installConfig(entry("https://a.test", shortSecret)), want: webhooks.ErrInvalidSecret},
		"duplicate url": {
			raw:  installConfig(entry("https://a.test/x", secret), entry("https://a.test/x", secret)),
			want: webhooks.ErrDuplicateInstallWebhook,
		},
	} {
		got, parseErr := webhooks.ParseInstallWebhooks(tc.raw)
		if parseErr == nil {
			t.Errorf("%s: accepted as %+v", name, got)
			continue
		}
		if tc.want != nil && !errors.Is(parseErr, tc.want) {
			t.Errorf("%s: error %v, want %v", name, parseErr, tc.want)
		}
	}
}
