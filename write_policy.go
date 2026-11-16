package chameleon

import "errors"

// ErrWriteOutcomeUnknown means an attempted write did not finish its workflow
// contract. Read back the target before deciding on a separate explicit write.
var ErrWriteOutcomeUnknown = errors.New("write outcome unknown; inspect the target before another explicit write")

func finishWriteOutcome(attempted bool, err *error) string {
	if !attempted {
		return "write_not_started"
	}
	if *err == nil {
		return "completed"
	}
	*err = errors.Join(ErrWriteOutcomeUnknown, *err)
	return "write_outcome_unknown"
}
