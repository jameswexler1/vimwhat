package whatsapp

import "google.golang.org/protobuf/proto"

// DeliveryError means a send was attempted but its acknowledgement is unknown.
// Retrying must use the same delivery ID, never silently create a fresh send.
type DeliveryError struct{ Err error }

func (e *DeliveryError) Error() string { return e.Err.Error() }
func (e *DeliveryError) Unwrap() error { return e.Err }

func TextPayload(request TextSendRequest) ([]byte, error) {
	return proto.Marshal((*Client)(nil).textMessage(request.Body, request))
}
