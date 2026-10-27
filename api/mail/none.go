package mail

import "context"

type none struct{}

func (none) Send(context.Context, Message) error {
	return ErrNoTransport
}
