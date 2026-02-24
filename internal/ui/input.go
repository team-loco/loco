package ui

import (
	"fmt"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

type inputModel struct {
	err       error
	textInput textinput.Model
}

func NewInputModel(prompt string) inputModel {
	ti := textinput.New()
	ti.Prompt = prompt
	ti.Focus()
	return inputModel{
		textInput: ti,
	}
}

func (m inputModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m inputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "enter", "ctrl+c", "esc":
			return m, tea.Quit
		}
	}

	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

func (m inputModel) View() tea.View {
	return tea.NewView(fmt.Sprintf(
		"%s\n\n%s",
		m.textInput.View(),
		"(esc to quit)",
	))
}

func AskForString(prompt string) (string, error) {
	p := tea.NewProgram(NewInputModel(prompt))
	model, err := p.Run()
	if err != nil {
		return "", err
	}

	m, ok := model.(inputModel)
	if !ok {
		return "", fmt.Errorf("internal error: unexpected model type")
	}
	return m.textInput.Value(), m.err
}
