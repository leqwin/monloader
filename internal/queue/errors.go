package queue

// CodedError pairs a failure message with a stable code from the error-code
// vocabulary in this package. The gallery-dl wrapper and the monbooru client
// both return it; the pipeline pulls the code with errors.As to set the
// item/job outcome.
type CodedError struct {
	Code string
	Msg  string
}

func (e *CodedError) Error() string { return e.Code + ": " + e.Msg }
