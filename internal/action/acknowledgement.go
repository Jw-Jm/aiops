package action

import "fmt"

var (
	ErrAckRequired        = fmt.Errorf("%w: required", ErrAcknowledgement)
	ErrAckExpired         = fmt.Errorf("%w: expired", ErrAcknowledgement)
	ErrAckScopeMismatch   = fmt.Errorf("%w: scope mismatch", ErrAcknowledgement)
	ErrAckCommandMismatch = fmt.Errorf("%w: command mismatch", ErrAcknowledgement)
)
