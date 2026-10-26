package advisorylock

import "testing"

func TestKeysAreDistinct(t *testing.T) {
	keys := map[string]int64{
		"SourceSweep":        SourceSweep,
		"ImageSweep":         ImageSweep,
		"InstallWebhookSync": InstallWebhookSync,
	}
	owners := map[int64]string{}
	for name, key := range keys {
		if owner, taken := owners[key]; taken {
			t.Errorf("%s and %s share lock key %#x", owner, name, key)
		}
		owners[key] = name
	}
}
