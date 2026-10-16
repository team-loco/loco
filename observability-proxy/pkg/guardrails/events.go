package guardrails

import (
	"errors"
	"strings"
	"unicode/utf8"

	"buf.build/go/protovalidate"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/observability-proxy/pkg/config"
)

var (
	errEventRegionRequired = errors.New("one exact region is required")
	errInvalidEventSearch  = errors.New("search must be valid UTF-8")
)

func ValidateEventsRequest(req *observabilityv1.QueryEventsRequest, cfg *config.Config) error {
	if err := protovalidate.Validate(req); err != nil {
		return err
	}
	if strings.TrimSpace(req.GetRegion()) == "" {
		return errEventRegionRequired
	}
	if !utf8.ValidString(req.GetSearch()) {
		return errInvalidEventSearch
	}
	return validateQueryTimeRange(req.GetStartTime(), req.GetEndTime(), cfg.MaxTimeRange)
}
