package service

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
)

const sizeQuery = `SELECT cpu_millicores($1), memory_bytes($1)`

func TestSQLQuantityFunctionsMatchKubernetesParsing(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	valid := []string{
		"0", "1", "0.25", ".5", "0.5", "5.", "1e3", "1E3", "1e-3", "12e6", "1500m",
		"256Mi", "1024Mi", "1.5Gi", "64Ki", "1Ti", "1Pi", "1Ei", "1k", "1M", "1G", "1T", "1P", "1E",
		"1e9", "129e6", "1.5e3", "2n", "5u", "+1", "100",
	}
	for _, value := range valid {
		parsed, err := resource.ParseQuantity(value)
		if err != nil {
			t.Fatalf("%q: not a Kubernetes quantity: %v", value, err)
		}
		var cpu, memory int64
		if err = f.pool.QueryRow(t.Context(), sizeQuery, value).Scan(&cpu, &memory); err != nil {
			t.Fatalf("%q: %v", value, err)
		}
		// MilliValue overflows int64 above 9.2e15 base units; CPU quantities that large are not meaningful.
		if want := parsed.MilliValue(); parsed.Value() < 1e15 && cpu != want {
			t.Errorf("cpu_millicores(%q) = %d, want %d", value, cpu, want)
		}
		if want := parsed.Value(); memory != want {
			t.Errorf("memory_bytes(%q) = %d, want %d", value, memory, want)
		}
	}
	for _, value := range []string{"", "abc", "1Zi", "1.2.3", "Mi", "1e", "1ki", "-1", "-1e30", "-.5", "-1Gi"} {
		var cpu, memory int64
		if err := f.pool.QueryRow(t.Context(), sizeQuery, value).Scan(&cpu, &memory); err != nil {
			t.Fatalf("%q: %v", value, err)
		}
		if cpu != 0 || memory != 0 {
			t.Errorf("%q = %d, %d, want 0, 0", value, cpu, memory)
		}
	}
	for _, value := range []string{"1e99", "99E", "99Ei"} {
		var cpu, memory int64
		if err := f.pool.QueryRow(t.Context(), sizeQuery, value).Scan(&cpu, &memory); err != nil {
			t.Fatalf("%q: %v", value, err)
		}
		if cpu != 9e18 || memory != 9e18 {
			t.Errorf("%q = %d, %d, want the 9e18 cap", value, cpu, memory)
		}
	}
}
