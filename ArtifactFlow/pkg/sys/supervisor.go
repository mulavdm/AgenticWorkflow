package sys

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/user/CentralLogger"
)

// SidecarConfig defines the parameters for launching and supervising a sidecar process.
type SidecarConfig struct {
	Name      string
	BinPath   string
	Args      []string
	Dir       string
	HealthURL string
}

// Supervisor manages the lifecycles of child sidecar processes and groups them under Job Objects for crash safety.
type Supervisor struct {
	mu        sync.Mutex
	commands  map[string]*exec.Cmd
	jobs      map[string]*Job
	logFiles  map[string]*os.File
	logPaths  map[string]string
	args      map[string][]string
	healthURL map[string]string
}

type SidecarStatus struct {
	Name      string   `json:"name"`
	Running   bool     `json:"running"`
	PID       int      `json:"pid,omitempty"`
	LogPath   string   `json:"log_path,omitempty"`
	HealthURL string   `json:"health_url,omitempty"`
	Args      []string `json:"args,omitempty"`
	ExitCode  int      `json:"exit_code,omitempty"`
}

// NewSupervisor creates a new centralized sidecar Supervisor.
func NewSupervisor() *Supervisor {
	return &Supervisor{
		commands:  make(map[string]*exec.Cmd),
		jobs:      make(map[string]*Job),
		logFiles:  make(map[string]*os.File),
		logPaths:  make(map[string]string),
		args:      make(map[string][]string),
		healthURL: make(map[string]string),
	}
}

// StartSidecar spawns a sidecar process, registers it under a Job Object, and waits for it to become healthy.
func (s *Supervisor) StartSidecar(ctx context.Context, cfg SidecarConfig) error {
	s.mu.Lock()

	if _, exists := s.commands[cfg.Name]; exists {
		s.mu.Unlock()
		return fmt.Errorf("sidecar %s is already running", cfg.Name)
	}

	// Verify binary exists
	if _, err := os.Stat(cfg.BinPath); os.IsNotExist(err) {
		s.mu.Unlock()
		return fmt.Errorf("sidecar binary not found at %s", cfg.BinPath)
	}

	// Resolve working directory
	dir := cfg.Dir
	if dir == "" {
		dir = filepath.Dir(cfg.BinPath)
	}

	// 1. Create Windows Job Object for lifecycle supervision
	job, err := CreateJob()
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("failed to create supervisor job for %s: %w", cfg.Name, err)
	}

	// 2. Build and start command
	cmd := exec.CommandContext(context.Background(), cfg.BinPath, cfg.Args...)
	cmd.Dir = dir

	var stdoutWriter io.Writer = os.Stdout
	var stderrWriter io.Writer = os.Stderr

	// Redirect output to dedicated sidecar log file if log dir is set
	logDir := logger.GetLogDir()
	if logDir != "" {
		cleanName := strings.ReplaceAll(cfg.Name, ":", "_")
		cleanName = strings.ReplaceAll(cleanName, "/", "_")
		cleanName = strings.ReplaceAll(cleanName, "\\", "_")
		logPath := filepath.Join(logDir, fmt.Sprintf("sidecar_%s.log", cleanName))
		_ = os.MkdirAll(logDir, 0755)
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err == nil {
			stdoutWriter = f
			stderrWriter = f
			s.logFiles[cfg.Name] = f
			s.logPaths[cfg.Name] = logPath
		} else {
			logger.Error("Supervisor", fmt.Sprintf("Failed to create sidecar log file %s: %v", logPath, err))
		}
	}

	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter

	if err := cmd.Start(); err != nil {
		if logFile, ok := s.logFiles[cfg.Name]; ok && logFile != nil {
			logFile.Close()
			delete(s.logFiles, cfg.Name)
		}
		job.Close()
		s.mu.Unlock()
		return fmt.Errorf("failed to start sidecar %s: %w", cfg.Name, err)
	}

	// Start a background goroutine to wait on the process so its ProcessState is populated if it exits.
	go func() {
		_ = cmd.Wait()
	}()

	// 3. Assign child process to Job Object to guarantee lifecycle binding
	if err := job.AssignProcess(cmd.Process.Pid); err != nil {
		cmd.Process.Kill()
		if logFile, ok := s.logFiles[cfg.Name]; ok && logFile != nil {
			logFile.Close()
			delete(s.logFiles, cfg.Name)
		}
		job.Close()
		s.mu.Unlock()
		return fmt.Errorf("failed to assign sidecar %s to job supervisor: %w", cfg.Name, err)
	}

	s.commands[cfg.Name] = cmd
	s.jobs[cfg.Name] = job
	s.args[cfg.Name] = append([]string(nil), cfg.Args...)
	s.healthURL[cfg.Name] = cfg.HealthURL

	// Releasing lock before calling pollHealth, which is a slow network operation
	s.mu.Unlock()

	// 4. Poll Health Endpoint if specified
	if cfg.HealthURL != "" {
		if err := s.pollHealth(ctx, cfg.Name, cfg.HealthURL); err != nil {
			// Clean up on health check failure (requires acquiring lock again)
			s.mu.Lock()
			cmd.Process.Kill()
			if logFile, ok := s.logFiles[cfg.Name]; ok && logFile != nil {
				logFile.Close()
				delete(s.logFiles, cfg.Name)
			}
			job.Close()
			delete(s.commands, cfg.Name)
			delete(s.jobs, cfg.Name)
			delete(s.logPaths, cfg.Name)
			delete(s.args, cfg.Name)
			delete(s.healthURL, cfg.Name)
			s.mu.Unlock()
			return fmt.Errorf("health check failed for sidecar %s: %w", cfg.Name, err)
		}
	}

	return nil
}

func (s *Supervisor) Status(name string) (SidecarStatus, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cmd, ok := s.commands[name]
	if !ok {
		return SidecarStatus{Name: name}, false
	}
	status := SidecarStatus{
		Name:      name,
		LogPath:   s.logPaths[name],
		HealthURL: s.healthURL[name],
		Args:      append([]string(nil), s.args[name]...),
	}
	if cmd.Process != nil {
		status.PID = cmd.Process.Pid
	}
	if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
		status.Running = false
		status.ExitCode = cmd.ProcessState.ExitCode()
	} else {
		status.Running = true
	}
	return status, true
}

// pollHealth polls the health endpoint until it is healthy or context expires.
func (s *Supervisor) pollHealth(ctx context.Context, name string, healthURL string) error {
	client := http.Client{Timeout: 2 * time.Second}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			// Double check if process is still running
			s.mu.Lock()
			cmd, ok := s.commands[name]
			s.mu.Unlock()
			if !ok || (cmd.ProcessState != nil && cmd.ProcessState.Exited()) {
				exitCode := -1
				if cmd.ProcessState != nil {
					exitCode = cmd.ProcessState.ExitCode()
				}
				return fmt.Errorf("process terminated during health check (exit code: %d)", exitCode)
			}

			resp, err := client.Get(healthURL)
			if err == nil && resp.StatusCode == http.StatusOK {
				resp.Body.Close()
				return nil
			}
			if resp != nil {
				resp.Body.Close()
			}
		}
	}
}

// StopSidecar terminates a specific sidecar by name and closes its Job Object.
func (s *Supervisor) StopSidecar(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cmd, existsCmd := s.commands[name]
	job, existsJob := s.jobs[name]

	if !existsCmd {
		return fmt.Errorf("sidecar %s is not running", name)
	}

	var errs []error
	if cmd.Process != nil {
		if err := cmd.Process.Kill(); err != nil {
			errs = append(errs, err)
		}
	}

	if existsJob && job != nil {
		if err := job.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	// Close and release sidecar log file handle
	if logFile, ok := s.logFiles[name]; ok && logFile != nil {
		logFile.Close()
		delete(s.logFiles, name)
	}

	delete(s.commands, name)
	delete(s.jobs, name)
	delete(s.logPaths, name)
	delete(s.args, name)
	delete(s.healthURL, name)

	if len(errs) > 0 {
		return fmt.Errorf("errors occurred stopping sidecar %s: %v", name, errs)
	}
	return nil
}

// Close terminates all running sidecars and closes all associated Job Objects.
func (s *Supervisor) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for name, cmd := range s.commands {
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		if job, ok := s.jobs[name]; ok && job != nil {
			_ = job.Close()
		}
	}

	// Close all open sidecar log files
	for _, logFile := range s.logFiles {
		if logFile != nil {
			logFile.Close()
		}
	}

	s.commands = make(map[string]*exec.Cmd)
	s.jobs = make(map[string]*Job)
	s.logFiles = make(map[string]*os.File)
	s.logPaths = make(map[string]string)
	s.args = make(map[string][]string)
	s.healthURL = make(map[string]string)
}
