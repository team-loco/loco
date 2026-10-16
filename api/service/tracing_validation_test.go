package service

import (
	"math"
	"testing"

	"buf.build/go/protovalidate"

	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

func TestTracingSampleRateWireValidation(t *testing.T) {
	for _, rate := range []float64{-0.1, 0, 0.25, 1, 1.1, math.NaN(), math.Inf(-1), math.Inf(1)} {
		config := &resourcev1.TracingConfig{Enabled: true, SampleRate: rate}
		valid := !math.IsNaN(rate) && rate >= 0 && rate <= 1
		if err := protovalidate.Validate(config); (err == nil) != valid {
			t.Errorf("sample rate %v: validation = %v, want valid=%v", rate, err, valid)
		}
	}
}
