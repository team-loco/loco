package mail

import "context"

type none struct{}

func (none) Send(context.Context, InvitationEvent) error {
	return ErrNoTransport
}
