//go:build !windows

package sys

import "context"

// Job wraps a stub Job Object for non-Windows systems.
type Job struct{}

// CreateJob creates a stub Job Object.
func CreateJob() (*Job, error) {
	return &Job{}, nil
}

// AssignProcess is a no-op stub.
func (j *Job) AssignProcess(pid int) error {
	return nil
}

// Close is a no-op stub.
func (j *Job) Close() error {
	return nil
}
