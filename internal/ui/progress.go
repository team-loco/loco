package ui

import (
	"fmt"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type StepStatus int

const (
	Pending StepStatus = iota
	Running
	Success
	Error
)

type Step struct {
	Run     func(logf func(string)) error
	Bar     progress.Model
	Spinner spinner.Model
	Status  StepStatus
	Title   string
	Err     error
}

type model struct {
	program *tea.Program
	sync.Mutex
	steps       []Step
	logs        map[int][]string
	activeIndex int
	quitting    bool
	hasError    bool
	error       error
}

type stepDoneMsg struct {
	err   error
	index int
}

type logMsg struct {
	line  string
	index int
}

// Styling
var (
	stylePending = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#FFA500", Dark: "#FFA500"}) // orange
	styleRunning = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#0074D9", Dark: "#00BFFF"}) // blue
	styleSuccess = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#2ECC40", Dark: "#32CD32"}) // green
	styleError   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#FF4136", Dark: "#FF4500"}) // red
	styleLog     = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#666666", Dark: "#999999"}).Italic(true)
)

var LocoSpinner = spinner.Spinner{
	Frames: []string{
		"🚄",
		"🚂",
		"🚃",
		"🚋",
	},
	FPS: time.Second / 2,
}

func NewModel(steps []Step) *model {
	for i := range steps {
		steps[i].Spinner = spinner.New(spinner.WithSpinner(LocoSpinner))
		steps[i].Bar = progress.New(progress.WithGradient("#00BFFF", "#32CD32"))
	}
	return &model{
		steps: steps,
		logs:  make(map[int][]string),
	}
}

func (m *model) Init() tea.Cmd {
	if len(m.steps) == 0 {
		return tea.Quit
	}
	m.steps[0].Status = Running
	return tea.Batch(
		m.steps[0].Spinner.Tick,
		m.runStep(m.activeIndex, m.steps[m.activeIndex].Run),
	)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.quitting = true
			return m, tea.Quit
		}

	case spinner.TickMsg:
		if m.activeIndex < len(m.steps) && m.steps[m.activeIndex].Status == Running {
			step := &m.steps[m.activeIndex]
			var cmd tea.Cmd
			step.Spinner, cmd = step.Spinner.Update(msg)
			return m, cmd
		}

	case logMsg:
		m.Lock()
		m.logs[msg.index] = append(m.logs[msg.index], msg.line)
		m.Unlock()
		return m, nil

	case stepDoneMsg:
		step := &m.steps[msg.index]
		if msg.err != nil {
			step.Status = Error
			step.Err = msg.err
			m.hasError = true
			m.error = msg.err
			return m, tea.Quit
		}
		step.Status = Success
		m.activeIndex++
		if m.activeIndex >= len(m.steps) {
			return m, tea.Quit
		}
		m.steps[m.activeIndex].Status = Running
		return m, tea.Batch(
			m.steps[m.activeIndex].Spinner.Tick,
			m.runStep(m.activeIndex, m.steps[m.activeIndex].Run),
		)
	}

	return m, nil
}

func (m *model) View() string {
	s := "\n"
	indent := "  "

	for i, step := range m.steps {
		var icon string
		switch step.Status {
		case Pending:
			icon = stylePending.Render("○")
		case Running:
			icon = styleRunning.Render(step.Spinner.View())
		case Success:
			icon = styleSuccess.Render("✔")
		case Error:
			icon = styleError.Render("✖")
		}

		var connector string
		if i == len(m.steps)-1 {
			connector = "└─"
		} else {
			connector = "├─"
		}

		s += fmt.Sprintf("%s%s %s %s\n", indent, connector, icon, step.Title)

		m.Lock()
		logs := make([]string, len(m.logs[i]))
		copy(logs, m.logs[i])
		m.Unlock()

		for _, line := range logs {
			s += indent + "│   " + styleLog.Render("→ "+line) + "\n"
		}

		if i < len(m.steps)-1 {
			s += indent + "│\n"
		}
	}

	if m.quitting {
		s += "\n" + styleError.Render("Aborted.") + "\n"
	}

	return s
}

// Move runStep to be a method of model so it can access the program
func (m *model) runStep(index int, fn func(logf func(string)) error) tea.Cmd {
	return func() tea.Msg {
		logChan := make(chan string, 100)
		var wg sync.WaitGroup
		wg.Add(1)

		var err error
		go func() {
			defer wg.Done()
			defer close(logChan)

			err = fn(func(line string) {
				select {
				case logChan <- line:
				default:
					// chan is full, skip this log message
				}
			})
		}()

		go func() {
			for line := range logChan {
				if m.program != nil {
					m.program.Send(logMsg{index: index, line: line})
				}
			}
		}()

		wg.Wait()
		return stepDoneMsg{index: index, err: err}
	}
}

func RunSteps(steps []Step) error {
	m := NewModel(steps)
	p := tea.NewProgram(m)
	m.program = p // Set the program reference

	_, err := p.Run()
	if m.hasError {
		return m.error
	}

	if m.quitting {
		return fmt.Errorf("deployment was cancelled")
	}

	return err
}
