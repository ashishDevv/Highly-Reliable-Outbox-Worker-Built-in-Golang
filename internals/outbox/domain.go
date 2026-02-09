package outbox

import "github.com/google/uuid"

type Event struct {
	ID   uuid.UUID `json:"id"`
	Type string    `json:"type"`
	Payload []byte `json:"payload"`
}