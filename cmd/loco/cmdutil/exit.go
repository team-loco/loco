package cmdutil

type ExitError struct {
	Code int
}

func (*ExitError) Error() string {
	return "command finished with changes"
}
