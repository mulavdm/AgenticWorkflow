//go:build windows

package sys

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Job wraps a Windows Job Object to supervise child process lifecycles.
type Job struct {
	handle windows.Handle
}

// CreateJob creates a new anonymous Job Object configured to automatically
// kill all associated child processes when the job object handle is closed
// (e.g., when the parent process exits, crashes, or is terminated).
func CreateJob() (*Job, error) {
	handle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create job object: %w", err)
	}

	// Configure the job to kill all child processes when the job handle is closed
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}

	_, err = windows.SetInformationJobObject(
		handle,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	if err != nil {
		windows.CloseHandle(handle)
		return nil, fmt.Errorf("failed to set job object limit: %w", err)
	}

	return &Job{handle: handle}, nil
}

// AssignProcess associates a child process PID with this Job Object.
func (j *Job) AssignProcess(pid int) error {
	pHandle, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(pid),
	)
	if err != nil {
		return fmt.Errorf("failed to open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(pHandle)

	err = windows.AssignProcessToJobObject(j.handle, pHandle)
	if err != nil {
		return fmt.Errorf("failed to assign process %d to job object: %w", pid, err)
	}

	return nil
}

// Close closes the Job Object handle, which will immediately terminate
// all supervised child processes.
func (j *Job) Close() error {
	return windows.CloseHandle(j.handle)
}
