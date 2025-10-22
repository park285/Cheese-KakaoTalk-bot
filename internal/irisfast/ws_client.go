package irisfast

import "context"

type MessageCallback func(message *Message)

type StateCallback func(state WebSocketState)

type WSClient interface {
	Connect(ctx context.Context) error
	OnMessage(cb MessageCallback) int
	RemoveMessageCallback(id int)
	OnStateChange(cb StateCallback) int
	RemoveStateCallback(id int)
	Close(ctx context.Context) error
}
