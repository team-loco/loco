// Package buildsource names and deletes the uploaded source archives of builds.
package buildsource

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	genDb "github.com/team-loco/loco/api/gen/db"
)

const (
	// UploadURLTTL is how long a presigned upload URL stays valid.
	UploadURLTTL = 15 * time.Minute
	// KeyPrefix starts the bucket key of every build source.
	KeyPrefix = "sources/"
)

type Deleter interface {
	Delete(ctx context.Context, key string) error
}

func Key(buildID uuid.UUID) string {
	return KeyPrefix + buildID.String() + ".tar.gz"
}

// Delete removes the source from the bucket first, so a failure leaves the row unmarked and a sweep retries it.
func Delete(ctx context.Context, queries genDb.Querier, bucket Deleter, buildID uuid.UUID, key string) error {
	if err := bucket.Delete(ctx, key); err != nil {
		return fmt.Errorf("delete source: %w", err)
	}
	if err := queries.MarkBuildSourceDeleted(ctx, buildID); err != nil {
		return fmt.Errorf("mark source deleted: %w", err)
	}
	return nil
}
