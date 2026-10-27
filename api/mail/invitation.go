package mail

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

const invitationBody = "%s invited you to join %s on Loco.\n\n" +
	"Sign in with this email address to join:\n%s\n\n" +
	"This invitation expires on %s.\n"

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

func NewInvitationMessage(ev InvitationEvent) Message {
	who := ev.Inviter.Email
	if ev.Inviter.Name != "" {
		who = fmt.Sprintf("%s (%s)", ev.Inviter.Name, ev.Inviter.Email)
	}
	body := fmt.Sprintf(
		invitationBody,
		who, ev.Org.Name, ev.Link, ev.Invitation.ExpiresAt.UTC().Format(time.DateOnly),
	)
	return Message{
		To:      ev.Invitation.Email,
		ReplyTo: ev.Inviter.Email,
		Subject: fmt.Sprintf("You have been invited to join %s on Loco", ev.Org.Name),
		Body:    body,
		Event:   ev,
	}
}
