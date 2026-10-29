//go:build !darwin && !linux

package localipc

func socketUID(_ int) (uint32, error) { return 0, NewError("unsupported", "unavailable") }

func socketPID(_ int) (int, error) { return 0, NewError("unsupported", "unavailable") }
