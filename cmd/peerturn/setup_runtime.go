package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/temirlankasen/peerturn/internal/localipc"
	"github.com/temirlankasen/peerturn/internal/runtimepolicy"
)

func setupExecutableInfo(binary string) (os.FileInfo, error) {
	directory, err := os.OpenRoot(filepath.Dir(binary))
	if err != nil {
		return nil, err
	}
	info, err := directory.Stat(filepath.Base(binary))
	return info, errors.Join(err, directory.Close())
}

func setupRuntimeFailure(host, detected string) *localipc.Error {
	d := runtimepolicy.Evaluate(host, detected)
	problem := localipc.RuntimeError(runtimepolicy.Check(host, detected))
	version := d.Version
	if version == "" {
		version = "unknown"
	}
	problem.Message = fmt.Sprintf("%s runtime minimum: %s; detected: %s (%s). %s", host, d.Minimum, version, d.Status, problem.Message)
	return problem
}

func inspectSetupRuntime(ctx context.Context, host string, invoke setupInvoke) (runtimepolicy.Decision, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var output setupOutput
	detected := "unknown"
	if err := invoke(ctx, host, []string{"--version"}, &output); err == nil {
		detected = output.String()
	} else if localipc.PublicError(err).Code == "runtime_contract_changed" {
		return runtimepolicy.Evaluate(host, ""), err
	}
	d := runtimepolicy.Evaluate(host, detected)
	if !d.Allowed() {
		return d, setupRuntimeFailure(host, detected)
	}
	return d, nil
}
