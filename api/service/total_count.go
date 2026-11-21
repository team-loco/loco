package service

import "context"

// totalCountCap is the most rows a total_count reports exactly; totalCountCap+1 means more.
const totalCountCap = 10_000

// totalCount returns count's result when include is set, passing it the row limit that caps it.
// count must count at most limit rows, as SELECT count(*) FROM (... LIMIT limit) does, so a
// large table costs one bounded index scan.
func totalCount(
	ctx context.Context,
	include bool,
	count func(ctx context.Context, limit int32) (int64, error),
) (int32, error) {
	if !include {
		return 0, nil
	}
	n, err := count(ctx, totalCountCap+1)
	if err != nil {
		return 0, err
	}
	return int32(min(n, totalCountCap+1)), nil
}
