package mail

import (
	"time"

	"github.com/google/uuid"
)

type InvitationInfo struct {
	ID        uuid.UUID `json:"id"`
	OrgID     uuid.UUID `json:"org_id"`
	Email     string    `json:"email"`
	ExpiresAt time.Time `json:"expires_at"`
}

type OrgInfo struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

type InviterInfo struct {
	ID    uuid.UUID `json:"id"`
	Email string    `json:"email"`
	Name  string    `json:"name"`
}

type InvitationEvent struct {
	Invitation InvitationInfo `json:"invitation"`
	Org        OrgInfo        `json:"org"`
	Inviter    InviterInfo    `json:"inviter"`
	Link       string         `json:"link"`
}
