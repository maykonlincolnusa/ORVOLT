package contract

import (
	"errors"
	"fmt"
)

// PermanentError marks a payload that will never become valid. Redelivering it
// only burns broker and database capacity, so the ingest loop routes these to
// the dead-letter stream instead of retrying forever.
type PermanentError struct {
	Reason string
}

func (err *PermanentError) Error() string { return err.Reason }

func permanent(format string, arguments ...any) error {
	return &PermanentError{Reason: fmt.Sprintf(format, arguments...)}
}

// IsPermanent reports whether an error came from contract validation rather
// than from a transient dependency such as the database.
func IsPermanent(err error) bool {
	var target *PermanentError
	return errors.As(err, &target)
}
