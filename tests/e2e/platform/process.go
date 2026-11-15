package platform

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const processStopTimeout = 15 * time.Second

// process is a host process of the run, logging to a file under the run's log directory.
type process struct {
	name string
	cmd  *exec.Cmd
	done chan struct{}
	// exit is how the process ended; its status stays in cmd.ProcessState.
	exit error
}

// processes are the host processes the run owns.
type processes struct {
	mu      sync.Mutex
	running map[string]*process
}

// startProcess starts binary with env added to the run's environment, appending its output to
// <log dir>/<name>.log.
func (p *Platform) startProcess(name string, env []string, binary string, args ...string) error {
	logFile, err := os.OpenFile(p.logPath(name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	// The process outlives the step that starts it, so only the run's end stops it.
	cmd := exec.CommandContext(p.runCtx, binary, args...)
	cmd.Env = append(append([]string{}, p.env...), env...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return errors.Join(fmt.Errorf("start %s: %w", name, err), logFile.Close())
	}
	proc := &process{name: name, cmd: cmd, done: make(chan struct{})}
	go func() {
		proc.exit = cmd.Wait()
		if err := logFile.Close(); err != nil {
			logf("close the %s log: %v", name, err)
		}
		close(proc.done)
	}()
	p.processes.mu.Lock()
	defer p.processes.mu.Unlock()
	if p.processes.running == nil {
		p.processes.running = map[string]*process{}
	}
	p.processes.running[name] = proc
	return nil
}

// stopProcess stops the named process, with SIGKILL after processStopTimeout.
func (p *Platform) stopProcess(name string) {
	p.processes.mu.Lock()
	proc := p.processes.running[name]
	delete(p.processes.running, name)
	p.processes.mu.Unlock()
	if proc == nil {
		return
	}
	select {
	case <-proc.done:
		return
	default:
	}
	signalGroup(proc, syscall.SIGTERM)
	select {
	case <-proc.done:
	case <-time.After(processStopTimeout):
		signalGroup(proc, syscall.SIGKILL)
		<-proc.done
	}
}

// signalGroup signals the process group of proc, which exits on its own between the checks.
func signalGroup(proc *process, signal syscall.Signal) {
	if err := syscall.Kill(-proc.cmd.Process.Pid, signal); err != nil && !errors.Is(err, syscall.ESRCH) {
		logf("signal %s: %v", proc.name, err)
	}
}

func (p *Platform) stopProcesses() {
	p.processes.mu.Lock()
	names := make([]string, 0, len(p.processes.running))
	for name := range p.processes.running {
		names = append(names, name)
	}
	p.processes.mu.Unlock()
	for _, name := range names {
		p.stopProcess(name)
	}
}

func (p *Platform) logPath(name string) string {
	return filepath.Join(p.logDir, name+".log")
}

// LogLines returns the lines of a host process's log that contain substring.
func (p *Platform) LogLines(name, substring string) ([]string, error) {
	file, err := os.Open(p.logPath(name))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var lines []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), substring) {
			lines = append(lines, scanner.Text())
		}
	}
	return lines, scanner.Err()
}

// agentLog collects the ERROR entries tests expect, so the run fails on any other.
type agentLog struct {
	path     string
	mu       sync.Mutex
	expected map[string]bool
}

// ExpectAgentErrors records agent log lines a test caused on purpose.
func (p *Platform) ExpectAgentErrors(lines ...string) {
	p.agentLog.mu.Lock()
	defer p.agentLog.mu.Unlock()
	for _, line := range lines {
		p.agentLog.expected[line] = true
	}
}

var errAgentErrors = errors.New("the agent logged ERROR entries during the run")

func (l *agentLog) check() error {
	data, err := os.ReadFile(l.path)
	if err != nil {
		return fmt.Errorf("read the agent log: %w", err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var unexpected []string
	for line := range strings.Lines(string(data)) {
		line = strings.TrimRight(line, "\n")
		if strings.Contains(line, `"level":"ERROR"`) && !l.expected[line] {
			unexpected = append(unexpected, line)
		}
	}
	if len(unexpected) > 0 {
		return fmt.Errorf("%w:\n%s", errAgentErrors, strings.Join(unexpected, "\n"))
	}
	return nil
}

// LogLineNumber returns the number of the first line of a host process's log that contains
// substring, or 0 without one.
func (p *Platform) LogLineNumber(name, substring string) (int, error) {
	file, err := os.Open(p.logPath(name))
	if err != nil {
		return 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for number := 1; scanner.Scan(); number++ {
		if strings.Contains(scanner.Text(), substring) {
			return number, nil
		}
	}
	return 0, scanner.Err()
}
