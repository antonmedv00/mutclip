package fail

import (
	"fmt"

	"github.com/charmbracelet/log"
)

type wrappedError struct {
	self    error
	child   error
	private bool
	code    uint32
}

func (e wrappedError) Error() string {
	log.Error(e.buildString())

	code, message := e.buildPublicString()
	return fmt.Sprintf("E%06v %v", code, message)
}

func (e wrappedError) buildString() string {
	if e.child == nil {
		return e.self.Error()
	}

	child, ok := e.child.(*wrappedError)
	if ok {
		return fmt.Sprintf("%v: %v", e.self.Error(), child.buildString())
	}

	return fmt.Sprintf("%v: %v", e.self.Error(), e.child.Error())
}

func (e wrappedError) buildPublicString() (uint32, string) {
	if e.child == nil {
		if e.private {
			return 0, "Something went wrong"
		}

		return e.code, e.self.Error()
	}

	if e.private {
		return e.code, fmt.Sprintf("%v", e.self.Error())
	}

	child, ok := e.child.(*wrappedError)
	if ok {
		code, message := child.buildPublicString()
		return code, fmt.Sprintf("%v: %v", e.self.Error(), message)
	}

	return 0, fmt.Sprintf("%v: %v", e.self.Error(), e.child.Error())
}

func Fail(code uint32, format string, a ...any) error {
	return &wrappedError{self: fmt.Errorf(format, a...), child: nil, private: false, code: code}
}

func SomethingWentWrong(format string, a ...any) error {
	return &wrappedError{self: fmt.Errorf(format, a...), child: nil, private: true, code: 0}
}

func Scope(child error, format string, a ...any) error {
	return &wrappedError{self: fmt.Errorf(format, a...), child: child, private: false, code: 0}
}

func Wrap(child error, code uint32, format string, a ...any) error {
	return &wrappedError{self: fmt.Errorf(format, a...), child: child, private: true, code: code}
}
