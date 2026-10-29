package localipc

import (
	"errors"
	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
)

// RuntimeError preserves the policy code and recovery action across local IPC.
func RuntimeError(err error) *Error {
	if policy, ok := errors.AsType[*runtimepolicy.Error](err); ok {
		result := NewError(policy.Code, "unsupported")
		result.Message = policy.Action
		return result
	}
	return NewError("runtime_contract_changed", "unsupported")
}
